//go:build integration

package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/githubobs"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/server"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

const (
	journeyAdminID             = "7001"
	journeyAdminLogin          = "journey-admin"
	journeyHumanID             = "7002"
	journeyHumanLogin          = "journey-reviewer"
	journeyMachineID           = "7003"
	journeyProjectRepository   = "9001"
	journeyKnowledgeRepository = "9002"
	journeyRuntimeID           = "runtime-journey"
	journeyRuntimeEpoch        = "epoch-journey"
	journeyRuntimeToken        = "journey-runtime-token-0123456789"
)

// TestFixedWorkflowJourney drives the whole fixed workflow through the real
// control plane: a real HTTPS surface and the real PostgreSQL authority, with
// only the external adapters scripted (an OAuth stub, a scripted GitHub
// observation and a scripted Runner that leases over the frozen transport).
func TestFixedWorkflowJourney(t *testing.T) {
	h := startJourney(t)

	workspaceID := h.createWorkspace(t, "journey")
	h.configureRepositories(t, workspaceID)
	h.publishExecutionConfig(t, workspaceID)
	h.registerRuntime(t)

	t.Run("knowledge_confirmation_gates_the_fixed_flow", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Add a deterministic greeting helper")
		admission := h.startRun(t, workspaceID, workItemID)
		if admission.Admission.ProjectCommitOID != h.projectHead(t) {
			t.Fatalf("the admitted baseline is %s, the frozen project head is %s",
				admission.Admission.ProjectCommitOID, h.projectHead(t))
		}
		if !strings.HasPrefix(admission.Admission.WorkingBranchRef, "refs/heads/task/") {
			t.Fatalf("the admitted working branch is %q", admission.Admission.WorkingBranchRef)
		}
		if admission.Recommendation == nil || len(admission.Recommendation.Candidates) == 0 {
			t.Fatalf("admission did not recommend knowledge candidates: %+v", admission.Recommendation)
		}
		if admission.Recommendation.HumanTaskID == "" {
			t.Fatalf("the admission carries no confirmation request: %+v", admission.Recommendation)
		}
		// No attempt may exist before the human confirmation.
		h.expectNoLease(t, "no attempt may be dispatched before the knowledge confirmation")

		// A branch that advances after admission never changes a frozen input.
		h.advanceProjectBranch(t)
		h.answerHumanTask(t, workspaceID, admission.Recommendation.HumanTaskID,
			"approved", map[string]string{"selection": h.indexSelection(t)})
		h.waitFor(t, func() bool {
			journey := h.journey(t, workspaceID, workItemID)
			return journey.Run != nil && journey.Run.DecidedNodes >= 3
		}, "the knowledge step must confirm and bind the frozen selection")

		plan := h.leaseCapability(t, "plan.create")
		if plan.Task.InputDigest == "" {
			t.Fatal("the dispatched attempt carries no frozen input digest")
		}
		h.expectFrozenScopes(t, plan, admission.Admission, false)
		h.reportResult(t, plan, "completed", digestOf("plan"), map[string]any{
			"verdict": "approved", "summary": "plan",
		})
	})

	t.Run("fixed_flow_reaches_closure_through_the_frozen_gates", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Close the greeting delivery")
		h.startRun(t, workspaceID, workItemID)
		view := h.drive(t, workspaceID, workItemID, driveOptions{})
		if view.WorkItem.State != "CLOSED" || view.Closure == nil {
			t.Fatalf("the journey did not close: %+v", view)
		}
		if view.Run != nil && view.Run.State != "SUCCEEDED" {
			t.Fatalf("the delivery run ended %s", view.Run.State)
		}
		// The delivered commit is a real commit of the local remote.
		if commit := h.machineCommit(); !isOID(commit) {
			t.Fatalf("the delivered commit is not an oid: %q", commit)
		}
	})

	t.Run("post_merge_failure_keeps_the_item_open_and_repair_takes_over", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Fail the post-merge verification")
		admission := h.startRun(t, workspaceID, workItemID)
		view := h.drive(t, workspaceID, workItemID, driveOptions{
			failIntegration: true, stopWhenDecided: "integration",
		})
		if view.WorkItem.State != "OPEN" {
			t.Fatalf("a failed post-merge verification must keep the item open: %+v", view)
		}
		if view.Closure != nil {
			t.Fatalf("a failed post-merge verification must not close the item")
		}
		// A repair run can only take over once the failed run has ended: the
		// human cancels it, then the repair continues from the merged baseline.
		blocked := h.journey(t, workspaceID, workItemID)
		if blocked.Run == nil {
			t.Fatal("the failed run is not readable in the journey")
		}
		var stopped struct {
			Applied  bool   `json:"applied"`
			RunState string `json:"state"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "cancel", "requestKey": h.nextKey("journey-stop"),
				"expectedVersion": blocked.Run.Version,
			}, &stopped); status != http.StatusOK || !stopped.Applied {
			t.Fatalf("the failed run could not be stopped: status %d, %+v", status, stopped)
		}
		h.waitFor(t, func() bool {
			current := h.journey(t, workspaceID, workItemID)
			return current.Run != nil && current.Run.State == "CANCELED"
		}, "the failed run must settle before the repair takes over")

		// The repair run takes advancement authority while the original item
		// stays incomplete, and it never inherits the old approval.
		var recovery struct {
			RunID   string `json:"workflowRunId"`
			Applied bool   `json:"applied"`
			Detail  string `json:"detail"`
		}
		status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "repair", "reason": "the merged verification failed",
				"requestKey":      h.nextKey("journey-repair"),
				"expectedVersion": h.journey(t, workspaceID, workItemID).WorkItem.Version,
			}, &recovery)
		if status != http.StatusOK || !recovery.Applied || recovery.RunID == admission.Admission.RunID {
			t.Fatalf("the repair run was not admitted: status %d, %+v", status, recovery)
		}
		after := h.journey(t, workspaceID, workItemID)
		if after.WorkItem.State != "OPEN" {
			t.Fatalf("the original work item changed state: %+v", after.WorkItem)
		}
		if after.Run == nil || after.Run.WorkflowRunID != recovery.RunID {
			t.Fatalf("the repair run does not hold advancement authority: %+v", after.Run)
		}
	})

	t.Run("cancellation_stops_new_dispatch", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Cancel the delivery")
		admission := h.startRun(t, workspaceID, workItemID)
		var recovery struct {
			Applied  bool   `json:"applied"`
			RunState string `json:"state"`
		}
		status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "cancel", "requestKey": h.nextKey("journey-cancel"), "expectedVersion": 1,
			}, &recovery)
		if status != http.StatusOK || !recovery.Applied {
			t.Fatalf("cancellation was not applied: status %d, %+v", status, recovery)
		}
		h.waitFor(t, func() bool {
			journey := h.journey(t, workspaceID, workItemID)
			return journey.Run != nil && journey.Run.State == "CANCELED"
		}, "the canceled run must settle")
		// A canceled run materialises no further node round, whatever the
		// workspace's runtimes do with other runs.
		before := h.runNodeCount(t, workspaceID, workItemID, admission.Admission.RunID)
		time.Sleep(2 * time.Second)
		after := h.runNodeCount(t, workspaceID, workItemID, admission.Admission.RunID)
		if after != before {
			t.Fatalf("a canceled run dispatched %d more rounds", after-before)
		}
	})

	t.Run("unknown_attempt_input_fails_closed", func(t *testing.T) {
		status := h.runtimeRequest(t, http.MethodGet, "/api/v1/runtime/tasks/journey-unknown-task/input", nil, nil)
		if status != http.StatusNotFound && status != http.StatusConflict {
			t.Fatalf("an unknown attempt input answered %d, want a closed refusal", status)
		}
		// The legal request after the refusal still succeeds.
		if status := h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/tasks/lease", map[string]any{
			"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch,
			"waitSeconds": 1, "leaseSeconds": 300,
		}, nil); status != http.StatusNoContent && status != http.StatusOK {
			t.Fatalf("the transport refused a legal request after the refusal: %d", status)
		}
	})
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

type journeyHarness struct {
	t               *testing.T
	baseURL         string
	origin          string
	client          *http.Client
	csrfToken       string
	observer        *scriptedObserver
	projectRemote   string
	knowledgeRemote string
	projectHeadOID  string
	materializeRoot string
	// workingBranch is the frozen write authorization of the admitted run.
	workingBranch string
	counter       int
}

// nextKey returns a unique, grammar-safe request key for one command.
func (h *journeyHarness) nextKey(prefix string) string {
	h.counter++
	return fmt.Sprintf("%s-%d", prefix, h.counter)
}

type admissionResponse struct {
	Admission struct {
		RunID                 string `json:"workflowRunId"`
		ProjectRepositoryID   string `json:"projectRepositoryId"`
		KnowledgeRepositoryID string `json:"knowledgeRepositoryId"`
		ProjectCommitOID      string `json:"projectCommitOid"`
		KnowledgeCommitOID    string `json:"knowledgeCommitOid"`
		WorkingBranchRef      string `json:"workingBranchRef"`
		ProviderID            string `json:"providerId"`
	} `json:"admission"`
	Recommendation *struct {
		HumanTaskID string `json:"humanTaskId"`
		IndexDigest string `json:"indexDigest"`
		Candidates  []struct {
			Path     string `json:"path"`
			Digest   string `json:"digest"`
			Scope    string `json:"scope"`
			Required bool   `json:"required"`
		} `json:"candidates"`
	} `json:"knowledgeRecommendation"`
}

func startJourney(t *testing.T) *journeyHarness {
	t.Helper()
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	h := &journeyHarness{t: t}
	h.projectRemote, h.projectHeadOID = buildProjectRepository(t)
	h.knowledgeRemote, _ = buildKnowledgeRepository(t)
	h.materializeRoot = t.TempDir()
	h.observer = newScriptedObserver(map[string]string{
		journeyProjectRepository:   h.projectRemote,
		journeyKnowledgeRepository: h.knowledgeRemote,
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h.origin = "https://" + listener.Addr().String()
	h.baseURL = h.origin

	stub := newJourneyOAuthStub(t)
	defer stub.Close()

	var controlPlane *http.Server
	app := server.New(
		fx.Replace(
			config.Server{Address: "127.0.0.1:0"},
			config.Identity{
				Enabled: true, DatabaseURL: databaseURL, TrustedOrigin: h.origin,
				ClientID: "journey-client", ClientSecret: "journey-secret",
				AdminIDs: []string{journeyAdminID, journeyHumanID},
			},
			config.Runtime{
				Enabled: true, DatabaseURL: databaseURL,
				Credentials:         map[string]string{journeyRuntimeID: journeyRuntimeToken},
				GitHubMachineUserID: journeyMachineID,
				MaterializeRoot:     h.materializeRoot,
				RepositoryRegistry: map[string]string{
					journeyProjectRepository:   h.projectRemote,
					journeyKnowledgeRepository: h.knowledgeRemote,
				},
				ReconcileInterval: 200 * time.Millisecond,
			},
		),
		fx.Decorate(func(identity.OAuthClient) (identity.OAuthClient, error) {
			return stub.client(h.origin), nil
		}),
		fx.Decorate(func(workspace.RepositoryVisibility) workspace.RepositoryVisibility {
			return journeyVisibility{}
		}),
		fx.Decorate(func(githubobs.Observer) githubobs.Observer { return h.observer }),
		fx.Populate(&controlPlane),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("build the control plane: %v", err)
	}
	startContext, cancel := context.WithCancel(context.Background())
	if err := app.Start(startContext); err != nil {
		t.Fatalf("start the control plane: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Stop(context.Background())
		cancel()
		_ = listener.Close()
	})

	if controlPlane == nil {
		t.Fatal("the control plane did not expose its HTTP server")
	}
	certificate := selfSigned(t, listener.Addr().String())
	webServer := &http.Server{Handler: controlPlane.Handler, ReadHeaderTimeout: 10 * time.Second}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	go func() { _ = webServer.Serve(tlsListener) }()
	t.Cleanup(func() { _ = webServer.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	h.client = &http.Client{
		Jar:       jar,
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	h.login(t)
	return h
}

type journeyVisibility struct{}

// RepositoryVisible accepts every repository except one marker id, so a real
// denial stays expressible.
func (journeyVisibility) RepositoryVisible(_ context.Context, _, repositoryID string) error {
	if repositoryID == "999999" {
		return workspace.ErrResourceUnavailable
	}
	return nil
}

func (h *journeyHarness) login(t *testing.T) {
	t.Helper()
	var login struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if status := h.do(t, http.MethodPost, "/api/v2/auth/github/login",
		map[string]string{"returnTo": "/"}, &login); status != http.StatusOK {
		t.Fatalf("begin login: status %d", status)
	}
	if login.AuthorizationURL == "" {
		t.Fatal("begin login returned no authorization url")
	}
	response, err := h.client.Get(login.AuthorizationURL)
	if err != nil {
		t.Fatalf("follow the authorization redirect: %v", err)
	}
	_ = response.Body.Close()
	var snapshot struct {
		CSRFToken string `json:"csrfToken"`
		Identity  struct {
			GitHubUserID string `json:"githubUserId"`
		} `json:"identity"`
	}
	if status := h.do(t, http.MethodPost, "/api/v2/auth/session",
		map[string]string{"requestId": "journey-session"}, &snapshot); status != http.StatusOK {
		t.Fatalf("recover the session: status %d", status)
	}
	if snapshot.CSRFToken == "" || snapshot.Identity.GitHubUserID != journeyAdminID {
		t.Fatalf("session snapshot = %+v", snapshot)
	}
	h.csrfToken = snapshot.CSRFToken
}

// doWithKey sends one command whose owning operation reads its idempotency key
// from the Idempotency-Key header.
func (h *journeyHarness) doWithKey(
	t *testing.T,
	method, path, requestKey string,
	body any, out any,
) int {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode the request body: %v", err)
	}
	request, err := http.NewRequest(method, h.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Origin", h.origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", requestKey)
	if h.csrfToken != "" {
		request.Header.Set("X-CSRF-Token", h.csrfToken)
	}
	return h.roundTrip(t, request, out)
}

func (h *journeyHarness) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Origin", h.origin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if h.csrfToken != "" && method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", h.csrfToken)
	}
	return h.roundTrip(t, request, out)
}

func (h *journeyHarness) roundTrip(t *testing.T, request *http.Request, out any) int {
	t.Helper()
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.URL.Path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		t.Logf("%s %s -> %d %s", request.Method, request.URL.Path, response.StatusCode, payload)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("%s %s: decode the response %s: %v", request.Method, request.URL.Path, payload, err)
		}
	}
	return response.StatusCode
}

func (h *journeyHarness) createWorkspace(t *testing.T, name string) string {
	t.Helper()
	var created struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if status := h.doWithKey(t, http.MethodPost, "/api/v1/workspaces", "journey-workspace",
		map[string]string{"name": name}, &created); status != http.StatusCreated {
		t.Fatalf("bootstrap the workspace: status %d", status)
	}
	return created.WorkspaceID
}

func (h *journeyHarness) configureRepositories(t *testing.T, workspaceID string) {
	t.Helper()
	body := map[string]any{
		"expectedVersion": 1,
		"project": map[string]string{
			"githubRepositoryId": journeyProjectRepository, "displayName": "journey/project", "installationId": "1",
		},
		"knowledge": map[string]string{
			"githubRepositoryId": journeyKnowledgeRepository, "displayName": "journey/knowledge", "installationId": "1",
		},
	}
	if status := h.doWithKey(t, http.MethodPut, "/api/v1/workspaces/"+workspaceID+"/repositories",
		"journey-repositories", body, nil); status != http.StatusOK {
		t.Fatalf("configure the repositories: status %d", status)
	}
}

func (h *journeyHarness) publishExecutionConfig(t *testing.T, workspaceID string) {
	t.Helper()
	capabilities := []map[string]any{}
	for _, capability := range journeyCapabilities {
		capabilities = append(capabilities, map[string]any{"id": capability, "version": 1})
	}
	body := map[string]any{
		"requestKey": "journey-execution-config", "expectedVersion": 0,
		"capabilities": capabilities, "runtimes": []string{journeyRuntimeID},
	}
	if status := h.do(t, http.MethodPut, "/api/v1/workspaces/"+workspaceID+"/execution-config", body, nil); status != http.StatusOK {
		t.Fatalf("publish the execution configuration: status %d", status)
	}
}

var journeyCapabilities = []string{
	"plan.create", "plan.review", "testcases.design", "code.implement", "test.execute",
	"code.review", "security.review", "delivery.publish_pr", "delivery.integration_verify",
}

func (h *journeyHarness) registerRuntime(t *testing.T) {
	t.Helper()
	capabilities := []map[string]any{}
	for _, capability := range journeyCapabilities {
		capabilities = append(capabilities, map[string]any{"id": capability, "version": 1})
	}
	body := map[string]any{
		"schemaVersion": "kowa.workflow-execution.v1", "kind": "runtimeRegistration",
		"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch, "gitHubUserId": journeyMachineID,
		"provider":              map[string]string{"id": "scripted", "version": "scripted-1"},
		"providerAuthenticated": true, "capabilities": capabilities,
	}
	status := h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/register", body, nil)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("register the runtime: status %d", status)
	}
}

// runtimeRequest drives the frozen Runner transport as a runtime identity.
func (h *journeyHarness) runtimeRequest(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the runtime request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build the runtime request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+journeyRuntimeToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the runtime response: %v", err)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		t.Logf("%s %s -> %d %s", request.Method, request.URL.Path, response.StatusCode, payload)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("decode the runtime response %s: %v", payload, err)
		}
	}
	return response.StatusCode
}

func (h *journeyHarness) createWorkItem(t *testing.T, workspaceID, title string) string {
	t.Helper()
	var created struct {
		WorkItem struct {
			ID string `json:"workItemId"`
		} `json:"workItem"`
	}
	body := map[string]any{
		"requestKey":  h.nextKey("journey-item"),
		"title":       title,
		"requirement": map[string]any{"version": 1, "content": "Add a helper that greets deterministically."},
		"acceptance":  map[string]any{"version": 1, "content": "The helper has a passing unit test."},
	}
	if status := h.do(t, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/work-items", body, &created); status != http.StatusCreated {
		t.Fatalf("create the work item: status %d", status)
	}
	if created.WorkItem.ID == "" {
		t.Fatal("the work item response carries no identity")
	}
	return created.WorkItem.ID
}

func (h *journeyHarness) startRun(t *testing.T, workspaceID, workItemID string) admissionResponse {
	t.Helper()
	var admission admissionResponse
	body := map[string]any{"requestKey": h.nextKey("journey-run"), "expectedVersion": 1}
	status := h.do(t, http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs", body, &admission)
	if status != http.StatusCreated {
		t.Fatalf("admit the run: status %d", status)
	}
	h.workingBranch = admission.Admission.WorkingBranchRef
	return admission
}

func (h *journeyHarness) journey(t *testing.T, workspaceID, workItemID string) journeyView {
	t.Helper()
	var view journeyView
	if status := h.do(t, http.MethodGet,
		"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/journey", nil, &view); status != http.StatusOK {
		t.Fatalf("read the journey: status %d", status)
	}
	return view
}

type journeyView struct {
	WorkItem struct {
		State   string `json:"state"`
		Version int64  `json:"version"`
	} `json:"workItem"`
	Run *struct {
		WorkflowRunID string `json:"workflowRunId"`
		State         string `json:"state"`
		Version       int64  `json:"version"`
		DecidedNodes  int    `json:"decidedNodes"`
		TotalNodes    int    `json:"totalNodes"`
	} `json:"run"`
	OpenHumanTasks []struct {
		HumanTaskID string `json:"humanTaskId"`
		Kind        string `json:"kind"`
		State       string `json:"state"`
	} `json:"openHumanTasks"`
	Closure *struct {
		ClosureID string `json:"closureId"`
	} `json:"closure"`
}

type journeyHumanTask struct {
	Request struct {
		Version int `json:"version"`
		Object  struct {
			Kind    string `json:"kind"`
			ID      string `json:"id"`
			Version string `json:"version"`
			Digest  string `json:"digest"`
		} `json:"object"`
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	} `json:"request"`
	State   string `json:"state"`
	Version int64  `json:"version"`
}

type journeyHumanTaskResponse struct {
	HumanTask journeyHumanTask `json:"humanTask"`
}

func (h *journeyHarness) answerHumanTask(
	t *testing.T,
	workspaceID, taskID, decision string,
	values map[string]string,
) {
	t.Helper()
	var response journeyHumanTaskResponse
	if status := h.do(t, http.MethodGet,
		"/api/v1/workspaces/"+workspaceID+"/human-tasks/"+taskID, nil, &response); status != http.StatusOK {
		t.Fatalf("read the human task: status %d", status)
	}
	task := response.HumanTask
	submitted := map[string]string{}
	for name, value := range values {
		submitted[name] = value
	}
	// The frozen request declares its own fields: a required decision field
	// carries the decision value, so the answer never disagrees with it.
	for _, field := range task.Request.Fields {
		if field.Name == "decision" {
			submitted["decision"] = decision
		}
	}
	body := map[string]any{
		"requestKey": h.nextKey("journey-answer"), "requestVersion": task.Request.Version,
		"object": map[string]string{
			"kind": task.Request.Object.Kind, "id": task.Request.Object.ID,
			"version": task.Request.Object.Version, "digest": task.Request.Object.Digest,
		},
		"decision": decision, "values": submitted,
	}
	if status := h.do(t, http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/human-tasks/"+taskID+"/responses", body, nil); status != http.StatusOK {
		t.Fatalf("answer the human task: status %d", status)
	}
}

func allCandidatePaths(admission *struct {
	HumanTaskID string `json:"humanTaskId"`
	IndexDigest string `json:"indexDigest"`
	Candidates  []struct {
		Path     string `json:"path"`
		Digest   string `json:"digest"`
		Scope    string `json:"scope"`
		Required bool   `json:"required"`
	} `json:"candidates"`
}) string {
	paths := make([]string, 0, len(admission.Candidates))
	for _, candidate := range admission.Candidates {
		paths = append(paths, candidate.Path)
	}
	return strings.Join(paths, "\n")
}

func (h *journeyHarness) expectNoLease(t *testing.T, message string) {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	var lease leasedTask
	status := h.leaseOnce(t, &lease)
	if status != http.StatusNoContent {
		t.Fatalf("%s (lease status %d, body %+v)", message, status, lease)
	}
}

type leasedTask struct {
	LeaseID      string `json:"leaseId"`
	FencingToken int64  `json:"fencingToken"`
	Task         struct {
		TaskID     string `json:"taskId"`
		NodeRunID  string `json:"nodeRunId"`
		Capability struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"capability"`
		InputDigest string `json:"inputDigest"`
		GitScopes   []struct {
			RepositoryID string `json:"repositoryId"`
			Role         string `json:"role"`
			Operation    string `json:"operation"`
			Ref          string `json:"ref"`
		} `json:"gitScopes"`
	} `json:"task"`
}

func (h *journeyHarness) leaseCapability(t *testing.T, capability string) leasedTask {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var lease leasedTask
		status := h.leaseOnce(t, &lease)
		if status == http.StatusNoContent {
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("lease a task: status %d", status)
		}
		if lease.Task.Capability.ID != capability {
			t.Fatalf("leased %s while waiting for %s", lease.Task.Capability.ID, capability)
		}
		return lease
	}
	t.Fatalf("no %s attempt was dispatched in time", capability)
	return leasedTask{}
}

func (h *journeyHarness) expectFrozenScopes(t *testing.T, lease leasedTask, admission struct {
	RunID                 string `json:"workflowRunId"`
	ProjectRepositoryID   string `json:"projectRepositoryId"`
	KnowledgeRepositoryID string `json:"knowledgeRepositoryId"`
	ProjectCommitOID      string `json:"projectCommitOid"`
	KnowledgeCommitOID    string `json:"knowledgeCommitOid"`
	WorkingBranchRef      string `json:"workingBranchRef"`
	ProviderID            string `json:"providerId"`
}, withWrite bool) {
	t.Helper()
	var projectRead, knowledgeRead, write bool
	for _, scope := range lease.Task.GitScopes {
		switch scope.Operation {
		case "clone":
			if scope.Role == "project" && scope.Ref == admission.ProjectCommitOID && isOID(scope.Ref) {
				projectRead = true
			}
			if scope.Role == "knowledge" && scope.Ref == admission.KnowledgeCommitOID && isOID(scope.Ref) {
				knowledgeRead = true
			}
		case "push":
			if scope.Ref == admission.WorkingBranchRef {
				write = true
			}
		}
	}
	if !projectRead || !knowledgeRead {
		t.Fatalf("read scopes are not frozen commit oids: %+v", lease.Task.GitScopes)
	}
	if write != withWrite {
		t.Fatalf("write authorization = %v, want %v: %+v", write, withWrite, lease.Task.GitScopes)
	}
}

func isOID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func (h *journeyHarness) projectHead(t *testing.T) string {
	t.Helper()
	return h.projectHeadOID
}

// advanceProjectBranch adds one commit to the delivery branch. It must never
// change a frozen input of an already admitted run.
func (h *journeyHarness) advanceProjectBranch(t *testing.T) {
	t.Helper()
	clone := t.TempDir()
	runGit(t, "", "clone", "--quiet", h.projectRemote, clone)
	writeFile(t, filepath.Join(clone, "NOTES.md"), "a later commit\n")
	runGit(t, clone, "add", "NOTES.md")
	runGit(t, clone, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "later change")
	runGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

// runNodeCount reads how many node rounds one run has materialised.
func (h *journeyHarness) runNodeCount(t *testing.T, workspaceID, workItemID, runID string) int {
	t.Helper()
	var detail struct {
		Nodes []struct {
			NodeID string `json:"nodeId"`
		} `json:"nodes"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/runs/"+runID, nil, &detail); status != http.StatusOK {
		t.Fatalf("read the run detail: status %d", status)
	}
	return len(detail.Nodes)
}

func (h *journeyHarness) waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", message)
}

// ---------------------------------------------------------------------------
// scripted external adapters
// ---------------------------------------------------------------------------

// scriptedObserver plays GitHub's read facts for the journey. Branch heads are
// read from the real local remotes over the same read-only Git protocol the
// production adapter uses; only the GitHub API facts are scripted. Nothing here
// is Kowa: it is the external platform the acceptance boundary isolates.
type scriptedObserver struct {
	mu               sync.Mutex
	pullRequest      *githubobs.PullRequestObservation
	review           *githubobs.ReviewObservation
	registry         map[string]string
	observedBranches map[string]bool
	machineCommit    string
}

func newScriptedObserver(registry map[string]string) *scriptedObserver {
	return &scriptedObserver{registry: registry, observedBranches: map[string]bool{}}
}

func (o *scriptedObserver) Identity(context.Context) (string, error) { return journeyMachineID, nil }

func (o *scriptedObserver) Repository(_ context.Context, repositoryID string) (githubobs.RepositoryObservation, error) {
	return githubobs.RepositoryObservation{
		RepositoryID: repositoryID, FullName: "journey/repo-" + repositoryID,
		DefaultBranch: "main", ActorID: journeyMachineID, ObservedAt: time.Now().UTC(),
	}, nil
}

// BranchHead reads the branch head from the real local remote over the same
// read-only Git protocol the production adapter uses; only the GitHub API facts
// are scripted.
func (o *scriptedObserver) BranchHead(_ context.Context, repositoryID, ref string) (githubobs.RefObservation, error) {
	observation := githubobs.RefObservation{
		RepositoryID: repositoryID, Ref: ref, ActorID: journeyMachineID, ObservedAt: time.Now().UTC(),
	}
	url, known := o.registry[repositoryID]
	if !known {
		observation.Unknown, observation.UnknownReason = true, githubobs.UnknownUnavailable
		return observation, githubobs.ErrUnknown
	}
	// The delivery contract addresses branches by their bare name; Git needs the
	// full reference.
	spec := ref
	if !strings.HasPrefix(spec, "refs/") {
		spec = "refs/heads/" + spec
	}
	output, err := exec.Command("git", "ls-remote", url, spec).Output()
	if err != nil {
		observation.Unknown, observation.UnknownReason = true, githubobs.UnknownUnavailable
		return observation, githubobs.ErrUnknown
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == spec && isOID(fields[0]) {
			observation.OID = fields[0]
			return observation, nil
		}
	}
	observation.Unknown, observation.UnknownReason = true, githubobs.UnknownUnavailable
	return observation, fmt.Errorf("%w: branch %s does not resolve", githubobs.ErrUnknown, ref)
}

func (o *scriptedObserver) PullRequest(_ context.Context, repositoryID, number string) (githubobs.PullRequestObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pullRequest == nil || o.pullRequest.Number != number {
		return githubobs.PullRequestObservation{
			RepositoryID: repositoryID, Number: number, ActorID: journeyMachineID,
			ObservedAt: time.Now().UTC(), Unknown: true, UnknownReason: githubobs.UnknownUnavailable,
		}, fmt.Errorf("%w: no pull request %s", githubobs.ErrUnknown, number)
	}
	observation := *o.pullRequest
	observation.ObservedAt = time.Now().UTC()
	return observation, nil
}

func (o *scriptedObserver) PullRequestsByHead(_ context.Context, repositoryID, headBranch string) ([]githubobs.PullRequestObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pullRequest == nil {
		return nil, nil
	}
	observation := *o.pullRequest
	observation.ObservedAt = time.Now().UTC()
	return []githubobs.PullRequestObservation{observation}, nil
}

func (o *scriptedObserver) Reviews(_ context.Context, repositoryID, number string) ([]githubobs.ReviewObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.review == nil || o.review.Number != number {
		return nil, nil
	}
	observation := *o.review
	observation.ObservedAt = time.Now().UTC()
	return []githubobs.ReviewObservation{observation}, nil
}

func (o *scriptedObserver) ReviewerPermission(_ context.Context, _, _, expectedUserID string) (string, error) {
	if expectedUserID == journeyHumanID {
		return "write", nil
	}
	return "", nil
}

func (o *scriptedObserver) Checks(_ context.Context, repositoryID, commitOID string) (githubobs.ChecksObservation, error) {
	return githubobs.ChecksObservation{
		RepositoryID: repositoryID, CommitOID: commitOID, State: "passed",
		Total: 1, Failed: 0, Pending: 0, ObservedAt: time.Now().UTC(),
	}, nil
}

func (o *scriptedObserver) Permission(_ context.Context, repositoryID string) (githubobs.PermissionObservation, error) {
	return githubobs.PermissionObservation{
		RepositoryID: repositoryID, Push: true, ActorID: journeyMachineID, ObservedAt: time.Now().UTC(),
	}, nil
}

// ---------------------------------------------------------------------------
// local repositories
// ---------------------------------------------------------------------------

func buildProjectRepository(t *testing.T) (string, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "project.git")
	runGit(t, "", "init", "--quiet", "--bare", "--initial-branch=main", remote)
	work := t.TempDir()
	runGit(t, "", "clone", "--quiet", remote, work)
	writeFile(t, filepath.Join(work, "README.md"), "# journey project\n")
	runGit(t, work, "add", "README.md")
	runGit(t, work, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "initial project")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	return remote, head
}

func buildKnowledgeRepository(t *testing.T) (string, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "knowledge.git")
	runGit(t, "", "init", "--quiet", "--bare", "--initial-branch=main", remote)
	work := t.TempDir()
	runGit(t, "", "clone", "--quiet", remote, work)
	design := "The project uses small deterministic helpers.\n"
	writeFile(t, filepath.Join(work, "docs", "design.md"), design)
	index := fmt.Sprintf(`{"schemaVersion":"kowa.web-application.v1","entries":[{"path":"docs/design.md","digest":"%s","scope":"design","required":true}]}`,
		digestOf(design))
	writeFile(t, filepath.Join(work, "kowa-index.json"), index)
	runGit(t, work, "add", "docs/design.md", "kowa-index.json")
	runGit(t, work, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "knowledge index")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	return remote, head
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

// leaseOnce performs one long-poll lease attempt over the frozen transport.
func (h *journeyHarness) leaseOnce(t *testing.T, out *leasedTask) int {
	t.Helper()
	return h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/tasks/lease", map[string]any{
		"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch,
		"waitSeconds": 1, "leaseSeconds": 300,
	}, out)
}
