package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/identity"

	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

const (
	webWorkspaceID = "workspace-a"
	webAdminID     = "101"
	webRunID       = "run-8f0c2a6d4b1e47c9a3f5d708b6e4c2a1"
	webWorkItemID  = "wi-4c1f0b7e9d2a44f08b1c7a0e5f3d9b21"
	webDigest      = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	webOID         = "0123456789abcdef0123456789abcdef01234567"
)

type fakeWebApplication struct {
	plan     fixedflow.View
	admitted orchestration.AdmissionResult
	// admitErr makes the next admission fail, so the refusal mapping is tested.
	admitErr error
}

func (f *fakeWebApplication) Plan() (fixedflow.View, error) { return f.plan, nil }

func (f *fakeWebApplication) Admit(
	context.Context,
	orchestration.AdmitCommand,
) (orchestration.AdmissionResult, error) {
	if f.admitErr != nil {
		return orchestration.AdmissionResult{}, f.admitErr
	}
	return f.admitted, nil
}

func (f *fakeWebApplication) RunDetail(
	context.Context, string, string, string,
) (orchestration.RunDetailView, error) {
	return orchestration.RunDetailView{
		SchemaVersion: orchestration.SchemaVersion, Kind: "runDetail", WorkItemID: webWorkItemID,
		WorkflowRunID: webRunID, State: "ACTIVE", Version: 2, DefinitionName: fixedflow.DefinitionName,
		DefinitionVersion: fixedflow.DefinitionVersion, DefinitionDigest: webDigest,
		StartedByUserID: webAdminID, MachineUserID: "9001",
		Nodes: []orchestration.NodeHistory{{
			NodeID: fixedflow.NodeKnowledgeRecommend, Type: "system", Iteration: 1,
			State: "DECIDED", Verdict: "approved", InputDigest: webDigest,
			Attempts: []orchestration.NodeAttempt{},
		}},
		RunBindings: []workitem.RunBinding{},
		Watermark:   orchestration.Watermark{ObservedAt: time.Now().UTC(), RunUpdatedAt: time.Now().UTC()},
	}, nil
}

func (f *fakeWebApplication) Recover(
	context.Context, orchestration.RecoveryCommand,
) (orchestration.RunRecovery, error) {
	return orchestration.RunRecovery{
		RunID: webRunID, Kind: orchestration.RecoveryCancel, Applied: true, RunState: "CANCELING",
		RunVersion: 2, Detail: "cancellation was requested", RecordedAt: time.Now().UTC(),
	}, nil
}

func (f *fakeWebApplication) Artifacts(
	context.Context, string, string,
) (orchestration.ArtifactListView, error) {
	return orchestration.ArtifactListView{
		SchemaVersion: orchestration.SchemaVersion, Kind: "artifactList", WorkItemID: webWorkItemID,
		Artifacts: []orchestration.ArtifactRecord{{
			ArtifactID: "9f1c7a4e2b6d80c53e9f1a4b7d0c3e6a", SubjectID: webWorkItemID,
			Kind: "node_output_plan", Digest: webDigest, Size: 12, MediaType: "application/json",
			ProducerKind: "operation", ProducerID: "fixedflow.node-output:plan:1",
			RequiresApproval: true, State: "PUBLISHED", CreatedAt: time.Now().UTC(),
		}},
		RunBindings: []workitem.RunBinding{},
	}, nil
}

func (f *fakeWebApplication) Journey(
	context.Context, string, string,
) (orchestration.JourneyView, error) {
	return orchestration.JourneyView{
		SchemaVersion: orchestration.SchemaVersion, Kind: "journey",
		WorkItem: workitem.WorkItem{
			ID: webWorkItemID, WorkspaceID: webWorkspaceID, Title: "Close the greeting delivery",
			TargetRepositoryID: "9001",
			Requirement:        workitem.VersionedContent{Version: 1, Digest: webDigest, Content: "requirement"},
			Acceptance:         workitem.VersionedContent{Version: 1, Digest: webDigest, Content: "acceptance"},
			State:              workitem.StateOpen, Version: 2, CurrentRunID: webRunID,
			CreatedByUserID: webAdminID,
		},
		DefinitionName: fixedflow.DefinitionName, DefinitionDigest: webDigest,
		Run: &orchestration.JourneyRun{
			WorkflowRunID: webRunID, State: "ACTIVE", Version: 3, DecidedNodes: 3,
			TotalNodes: len(f.plan.Nodes),
		},
		OpenHumanTasks: []orchestration.JourneyHumanTask{},
		Watermark:      orchestration.Watermark{ObservedAt: time.Now().UTC(), RunUpdatedAt: time.Now().UTC()},
	}, nil
}

type fakeWorkspaceDirectory struct{}

func (fakeWorkspaceDirectory) WorkspacesForMember(
	_ context.Context,
	githubUserID string,
) ([]workspace.Workspace, error) {
	if githubUserID != webAdminID {
		return nil, nil
	}
	return []workspace.Workspace{{
		ID: webWorkspaceID, Name: "Journey", Version: 3,
		Members: []workspace.Member{{GitHubUserID: webAdminID, Role: workspace.RoleDeveloper}},
	}}, nil
}

func newWebApplicationFixture(t *testing.T) (*httptest.Server, *fakeWebApplication) {
	t.Helper()
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile the built-in flow: %v", err)
	}
	targets := &fakeWebApplication{
		plan: fixedflow.BuildView(plan),
		admitted: orchestration.AdmissionResult{
			Admission: orchestration.Admission{
				RunID: webRunID, WorkspaceID: webWorkspaceID, WorkItemID: webWorkItemID,
				DefinitionName: fixedflow.DefinitionName, DefinitionVersion: fixedflow.DefinitionVersion,
				DefinitionDigest:    plan.Digest,
				ProjectRepositoryID: "9001", KnowledgeRepositoryID: "9002",
				ProjectCommitOID: webOID, KnowledgeCommitOID: webOID,
				ProjectBranchRef: "refs/heads/main", WorkingBranchRef: "refs/heads/task/kowa-8f0c2a6d",
				ProviderID: "scripted", ProviderVersion: "scripted-1",
				PolicyVersion: fixedflow.PolicyVersion, AdmittedByUserID: webAdminID,
				AdmittedAt: time.Now().UTC(),
			},
			WorkItemVersion: 2,
			Recommendation: &orchestration.Recommendation{
				RunID: webRunID, WorkspaceID: webWorkspaceID, WorkItemID: webWorkItemID,
				KnowledgeRepositoryID: "9002", KnowledgeCommitOID: webOID,
				IndexPath: "kowa-index.json", IndexDigest: webDigest, State: orchestration.StateRecommended,
				HumanTaskID: "ht-journey-1",
				Candidates: []orchestration.Candidate{{
					Path: "docs/design.md", Digest: webDigest, Scope: "design", Required: true,
				}},
				RecommendedAt: time.Now().UTC(),
			},
		},
	}
	handler := httpapi.NewWebApplicationHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: webAdminID, Login: "admin"}, csrfToken: "csrf"},
		&fakeWorkspaces{members: map[string]workspace.Member{
			webAdminID: {GitHubUserID: webAdminID, Role: workspace.RoleDeveloper},
		}},
		fakeWorkspaceDirectory{}, targets, "https://kowa.test", nil,
	)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, targets
}

// TestWebApplicationSurfaceMatchesTheFrozenContract drives every registered
// operation of the manifest against the real handler: a registered operation
// must exist at its declared method and path.
func TestWebApplicationSurfaceMatchesTheFrozenContract(t *testing.T) {
	manifest, err := os.ReadFile("../../../api/web-application/v1/delivery.json")
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var registered struct {
		Operations map[string]struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Layer  string `json:"layer"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(manifest, &registered); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	server, _ := newWebApplicationFixture(t)
	client := webClient(t)
	for operation, entry := range registered.Operations {
		path := entry.Path
		replacements := map[string]string{
			"{workspaceID}": webWorkspaceID, "{workItemID}": webWorkItemID, "{runID}": webRunID,
		}
		for placeholder, value := range replacements {
			path = strings.ReplaceAll(path, placeholder, value)
		}
		request, err := http.NewRequest(entry.Method, server.URL+path, nil)
		if err != nil {
			t.Fatalf("build %s: %v", operation, err)
		}
		request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
		request.Header.Set("Origin", "https://kowa.test")
		request.Header.Set("X-CSRF-Token", "csrf")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
			t.Fatalf("operation %s is not served at %s %s (status %d)",
				operation, entry.Method, entry.Path, response.StatusCode)
		}
	}
}

// TestWebApplicationResponsesMatchTheFrozenSchema validates real handler
// responses against the frozen contract.
func TestWebApplicationResponsesMatchTheFrozenSchema(t *testing.T) {
	server, _ := newWebApplicationFixture(t)
	client := webClient(t)
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"workspace list", http.MethodGet, "/api/v1/workspaces"},
		{"fixed workflow", http.MethodGet, "/api/v1/workspaces/" + webWorkspaceID + "/fixed-workflow"},
		{"run detail", http.MethodGet, "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID + "/runs/" + webRunID},
		{"artifacts", http.MethodGet, "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID + "/artifacts"},
		{"journey", http.MethodGet, "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID + "/journey"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequest(testCase.method, server.URL+testCase.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status %d", response.StatusCode)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			family := "web-application"
			if testCase.name == "fixed workflow" {
				family = "fixed-workflow"
			}
			assertResponseSchema(t, family, testCase.name, body)
		})
	}
}

// TestWebApplicationRefusesUnauthorizedAndConflictingCommands pins the negative
// paths of the six frozen operations: a missing session, a missing CSRF token,
// a non-member and a foreign workspace are refused, and a conflicting command
// answer is mapped to the frozen category.
func TestWebApplicationRefusesUnauthorizedAndConflictingCommands(t *testing.T) {
	server, targets := newWebApplicationFixture(t)
	client := webClient(t)
	cases := []struct {
		name    string
		method  string
		path    string
		session string
		csrf    string
		body    any
		want    int
	}{
		{name: "no session", method: http.MethodGet,
			path: "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID + "/journey",
			want: http.StatusUnauthorized},
		{name: "no csrf", method: http.MethodPost,
			path:    "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID + "/runs",
			session: "session-token",
			body:    map[string]any{"requestKey": "admit-1", "expectedVersion": 1},
			want:    http.StatusUnauthorized},
		{name: "unknown workspace", method: http.MethodGet,
			path:    "/api/v1/workspaces/workspace-other/work-items/" + webWorkItemID + "/journey",
			session: "session-token", want: http.StatusForbidden},
		{name: "no session on recovery", method: http.MethodPost,
			path: "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID +
				"/runs/" + webRunID + "/recovery",
			csrf: "csrf", body: map[string]any{"kind": "cancel", "requestKey": "cancel-1", "expectedVersion": 1},
			want: http.StatusUnauthorized},
		{name: "no session on artifacts", method: http.MethodGet,
			path: "/api/v1/workspaces/" + webWorkspaceID + "/work-items/" + webWorkItemID + "/artifacts",
			want: http.StatusUnauthorized},
		{name: "no session on workspace list", method: http.MethodGet,
			path: "/api/v1/workspaces", want: http.StatusUnauthorized},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var reader *strings.Reader
			if testCase.body != nil {
				encoded, err := json.Marshal(testCase.body)
				if err != nil {
					t.Fatal(err)
				}
				reader = strings.NewReader(string(encoded))
			} else {
				reader = strings.NewReader("")
			}
			request, err := http.NewRequest(testCase.method, server.URL+testCase.path, reader)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", "https://kowa.test")
			if testCase.body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			if testCase.session != "" {
				request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: testCase.session})
			}
			if testCase.csrf != "" {
				request.Header.Set("X-CSRF-Token", testCase.csrf)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != testCase.want {
				t.Fatalf("status %d, want %d", response.StatusCode, testCase.want)
			}
		})
	}

	// A conflicting admission (the work item already has its run) is answered
	// with the frozen conflict category, and the legal command after it works.
	// The service wraps its sentinels; the surface maps them, never the text.
	targets.admitErr = fmt.Errorf("%w: the work item already has a delivery run",
		orchestration.ErrPolicyBlocked)
	var refused struct {
		Error string `json:"error"`
	}
	request, err := http.NewRequest(http.MethodPost,
		server.URL+"/api/v1/workspaces/"+webWorkspaceID+"/work-items/"+webWorkItemID+"/runs",
		strings.NewReader(`{"requestKey":"admit-2","expectedVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "https://kowa.test")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "csrf")
	request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("refused admission status %d (%s) err=%v", response.StatusCode, body, targets.admitErr)
	}
	if err := json.Unmarshal(body, &refused); err != nil || refused.Error != "POLICY_BLOCKED" {
		t.Fatalf("refused admission category %s, err=%v", refused.Error, err)
	}
	targets.admitErr = nil
	legal, err := http.NewRequest(http.MethodPost,
		server.URL+"/api/v1/workspaces/"+webWorkspaceID+"/work-items/"+webWorkItemID+"/runs",
		strings.NewReader(`{"requestKey":"admit-3","expectedVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	legal.Header.Set("Origin", "https://kowa.test")
	legal.Header.Set("Content-Type", "application/json")
	legal.Header.Set("X-CSRF-Token", "csrf")
	legal.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	legalResponse, err := client.Do(legal)
	if err != nil {
		t.Fatal(err)
	}
	_ = legalResponse.Body.Close()
	if legalResponse.StatusCode != http.StatusCreated {
		t.Fatalf("legal admission after the refusal answered %d", legalResponse.StatusCode)
	}
}

// webClient returns the plain test client used against the loopback server.
func webClient(*testing.T) *http.Client { return http.DefaultClient }

// assertResponseSchema validates one real producer response against the frozen
// contract family with the same pinned validator the repository gate uses.
func assertResponseSchema(t *testing.T, family, definition string, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "actual-response.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := filepath.Abs(filepath.Join("../../../api", family, "v1/schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run",
		"github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0", "-q", "-f", "-c", schema, path)
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if err := command.Run(); err != nil {
		t.Fatalf("%s producer violates the frozen schema: %v; %s", definition, err, data)
	}
}
