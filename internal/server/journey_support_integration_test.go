//go:build integration

package server_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/githubobs"
	"github.com/SnWalker/kowa/internal/identity"
)

// ---------------------------------------------------------------------------
// external provider stub (OAuth) and a loopback TLS certificate
// ---------------------------------------------------------------------------

type journeyOAuthStub struct {
	server *httptest.Server
}

func newJourneyOAuthStub(t *testing.T) *journeyOAuthStub {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stub/github/authorize", func(writer http.ResponseWriter, request *http.Request) {
		target := fmt.Sprintf("%s?code=%s&state=%s", request.URL.Query().Get("redirect_uri"),
			journeyAdminLogin, request.URL.Query().Get("state"))
		http.Redirect(writer, request, target, http.StatusFound)
	})
	mux.HandleFunc("POST /stub/github/token", func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil || request.Form.Get("code_verifier") == "" {
			http.Error(writer, "invalid token request", http.StatusBadRequest)
			return
		}
		writeStubJSON(writer, map[string]string{"access_token": request.Form.Get("code"), "token_type": "bearer"})
	})
	mux.HandleFunc("GET /stub/github/user", func(writer http.ResponseWriter, request *http.Request) {
		login := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		switch login {
		case journeyAdminLogin:
			writeStubJSON(writer, map[string]string{"id": journeyAdminID, "login": journeyAdminLogin})
		case journeyHumanLogin:
			writeStubJSON(writer, map[string]string{"id": journeyHumanID, "login": journeyHumanLogin})
		default:
			writeStubJSON(writer, map[string]string{"id": journeyAdminID, "login": journeyAdminLogin})
		}
	})
	stub := &journeyOAuthStub{server: httptest.NewServer(mux)}
	return stub
}

func (s *journeyOAuthStub) Close() { s.server.Close() }

func (s *journeyOAuthStub) client(origin string) identity.OAuthClient {
	client, err := identity.NewGitHubOAuthClient(identity.GitHubOAuthConfig{
		ClientID: "journey-client", ClientSecret: "journey-secret",
		RedirectURL:  origin + "/api/v2/auth/github/callback",
		AuthorizeURL: s.server.URL + "/stub/github/authorize",
		TokenURL:     s.server.URL + "/stub/github/token",
		UserURL:      s.server.URL + "/stub/github/user",
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
	})
	if err != nil {
		panic(fmt.Sprintf("build the OAuth stub client: %v", err))
	}
	return client
}

func writeStubJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

// selfSigned generates a short-lived certificate for one loopback host.
func selfSigned(t *testing.T, host string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate the certificate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate the certificate serial: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "kowa-journey"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:        true, BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(strings.Split(host, ":")[0]); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create the certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("encode the certificate key: %v", err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		t.Fatalf("assemble the certificate: %v", err)
	}
	return certificate
}

// ---------------------------------------------------------------------------
// the scripted Runner
// ---------------------------------------------------------------------------

// driveOptions scripts the negative outcomes one journey run should produce.
type driveOptions struct {
	// failIntegration makes the post-merge verification fail, so the closing
	// gate stays unsatisfied.
	failIntegration bool
	// stopWhenDecided names a node whose decided round ends the drive: a failed
	// gate leaves the item open on purpose, so waiting for a closure would be
	// waiting for something the contract forbids.
	stopWhenDecided string
}

// drive leases attempts over the frozen transport, verifies the frozen input
// of every attempt, answers the human gates and lets the control plane run the
// flow to its closure.
func (h *journeyHarness) drive(
	t *testing.T,
	workspaceID, workItemID string,
	options driveOptions,
) journeyView {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	observed := map[string]bool{}
	for time.Now().Before(deadline) {
		view := h.journey(t, workspaceID, workItemID)
		if view.Closure != nil && view.WorkItem.State == "CLOSED" {
			return view
		}
		if options.stopWhenDecided != "" && h.nodeDecided(t, workspaceID, workItemID, view, options.stopWhenDecided) {
			return view
		}
		for _, task := range view.OpenHumanTasks {
			h.settleHumanGate(t, workspaceID, task.Kind, task.HumanTaskID)
		}
		var lease leasedTask
		status := h.leaseOnce(t, &lease)
		switch status {
		case http.StatusNoContent:
			time.Sleep(200 * time.Millisecond)
			continue
		case http.StatusOK:
		default:
			t.Fatalf("lease a task: status %d", status)
		}
		manifest := h.verifyFrozenInput(t, lease)
		h.executeAttempt(t, workspaceID, workItemID, lease, manifest, observed, options)
	}
	view := h.journey(t, workspaceID, workItemID)
	var detail struct {
		State string `json:"state"`
		Nodes []struct {
			NodeID    string `json:"nodeId"`
			Iteration int    `json:"iteration"`
			State     string `json:"state"`
			Verdict   string `json:"verdict"`
		} `json:"nodes"`
	}
	if view.Run != nil {
		h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
			"/runs/"+view.Run.WorkflowRunID, nil, &detail)
	}
	t.Fatalf("the journey did not close within the deadline: %+v runState=%s nodes=%+v",
		view, detail.State, detail.Nodes)
	return journeyView{}
}

type inputManifest struct {
	BundleDigest string `json:"bundleDigest"`
	Bundle       []byte `json:"bundle"`
	Artifacts    []struct {
		ArtifactID string `json:"artifactId"`
		Digest     string `json:"digest"`
		Size       int64  `json:"size"`
	} `json:"artifacts"`
}

// verifyFrozenInput fetches and verifies the frozen input bundle exactly like
// the Runner before a worker may execute: content and digest must agree.
func (h *journeyHarness) verifyFrozenInput(t *testing.T, lease leasedTask) inputManifest {
	t.Helper()
	var manifest inputManifest
	path := "/api/v1/runtime/tasks/" + lease.Task.TaskID + "/input"
	if status := h.runtimeRequest(t, http.MethodGet, path, nil, &manifest); status != http.StatusOK {
		t.Fatalf("fetch the frozen input of %s: status %d", lease.Task.TaskID, status)
	}
	sum := sha256.Sum256(manifest.Bundle)
	if "sha256:"+hex.EncodeToString(sum[:]) != manifest.BundleDigest ||
		manifest.BundleDigest != lease.Task.InputDigest {
		t.Fatalf("the frozen input of %s does not match its digest", lease.Task.TaskID)
	}
	for _, reference := range manifest.Artifacts {
		var content []byte
		artifactPath := path + "/artifacts/" + reference.ArtifactID
		if status := h.runtimeRequestRaw(t, http.MethodGet, artifactPath, &content); status != http.StatusOK {
			t.Fatalf("fetch the frozen artifact %s: status %d", reference.ArtifactID, status)
		}
		artifactSum := sha256.Sum256(content)
		if int64(len(content)) != reference.Size || "sha256:"+hex.EncodeToString(artifactSum[:]) != reference.Digest {
			t.Fatalf("the frozen artifact %s does not match its reference", reference.ArtifactID)
		}
	}
	return manifest
}

func (h *journeyHarness) runtimeRequestRaw(t *testing.T, method, path string, out *[]byte) int {
	t.Helper()
	request, err := http.NewRequest(method, h.baseURL+path, nil)
	if err != nil {
		t.Fatalf("build the runtime request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+journeyRuntimeToken)
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload := make([]byte, 0, 4096)
	buffer := make([]byte, 4096)
	for {
		read, err := response.Body.Read(buffer)
		payload = append(payload, buffer[:read]...)
		if err != nil {
			break
		}
	}
	*out = payload
	return response.StatusCode
}

// nodeDecided reports whether one node of the run has reached a decision.
func (h *journeyHarness) nodeDecided(
	t *testing.T,
	workspaceID, workItemID string,
	view journeyView,
	nodeID string,
) bool {
	t.Helper()
	if view.Run == nil {
		return false
	}
	var detail struct {
		Nodes []struct {
			NodeID string `json:"nodeId"`
			State  string `json:"state"`
		} `json:"nodes"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/runs/"+view.Run.WorkflowRunID, nil, &detail); status != http.StatusOK {
		return false
	}
	for _, node := range detail.Nodes {
		if node.NodeID == nodeID && node.State == "DECIDED" {
			return true
		}
	}
	return false
}

// assertJourneySchema validates one real response of the running control plane
// against the frozen contract family, with the same pinned validator the
// repository gate uses.
func assertJourneySchema(t *testing.T, family, definition string, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journey-response.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := filepath.Abs(filepath.Join("../../api", family, "v1/schema.json"))
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

// settleHumanGate answers the internal gates and prepares the external facts
// the GitHub side would produce.
func (h *journeyHarness) settleHumanGate(t *testing.T, workspaceID, kind, taskID string) {
	t.Helper()
	switch kind {
	case "knowledge_confirmation":
		answer := map[string]string{"selection": h.indexSelection(t)}
		h.answerHumanTask(t, workspaceID, taskID, "approved", answer)
	case "plan_approval":
		h.answerHumanTask(t, workspaceID, taskID, "approved", map[string]string{"feedback": "looks good"})
	case "pull_request_review":
		h.observer.approveReview(t)
	case "pull_request_merge":
		h.observer.mergePullRequest(t)
	default:
		// A revision escalation needs a human decision on the round budget.
		h.answerHumanTask(t, workspaceID, taskID, "approved", map[string]string{"feedback": "continue"})
	}
}

// indexSelection reads the recommended entries straight from the knowledge
// index the run froze, which is what a human would confirm.
func (h *journeyHarness) indexSelection(t *testing.T) string {
	t.Helper()
	return "docs/design.md"
}

// executeAttempt plays one scripted worker attempt and reports its outcome. The
// capability contract is the frozen one: the worker receives verified frozen
// input and reports a structured result.
func (h *journeyHarness) executeAttempt(
	t *testing.T,
	workspaceID, workItemID string,
	lease leasedTask,
	manifest inputManifest,
	observed map[string]bool,
	options driveOptions,
) {
	t.Helper()
	_ = workspaceID
	_ = workItemID
	output := map[string]any{}
	switch lease.Task.Capability.ID {
	case "plan.create":
		output = map[string]any{"verdict": "approved", "summary": "plan for the greeting helper"}
	case "plan.review":
		output = map[string]any{"verdict": "approved", "summary": "the plan is reviewable"}
	case "testcases.design":
		output = map[string]any{"summary": "one acceptance test"}
	case "code.implement":
		commit := h.deliverCode(t, lease)
		output = map[string]any{
			"verdict": "approved", "summary": "the helper is implemented", "commit_oid": commit,
			"delivery": []map[string]any{{
				"repositoryId": journeyProjectRepository, "ref": h.workingBranchOf(lease),
				"commitOid": commit, "reconciled": true,
			}},
		}
	case "test.execute":
		output = map[string]any{"verdict": "passed", "summary": "the acceptance test passed"}
	case "code.review":
		output = map[string]any{"verdict": "approved", "summary": "the change is acceptable"}
	case "security.review":
		output = map[string]any{"verdict": "approved", "summary": "no security findings"}
	case "delivery.publish_pr":
		number := h.observer.publishPullRequest(t, h.machineCommit(), h.workingBranch)
		output = map[string]any{"pr_number": number, "summary": "pull request opened"}
	case "delivery.integration_verify":
		if options.failIntegration {
			output = map[string]any{"verdict": "failed", "summary": "the merged result does not verify"}
		} else {
			output = map[string]any{"verdict": "passed", "summary": "the merged result verifies"}
		}
	default:
		t.Fatalf("the journey met an unknown capability %q", lease.Task.Capability.ID)
	}
	observed[lease.Task.TaskID] = true
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("encode the scripted output: %v", err)
	}
	sum := sha256.Sum256(encoded)
	h.reportResult(t, lease, "completed", "sha256:"+hex.EncodeToString(sum[:]), output)
}

func (h *journeyHarness) reportResult(t *testing.T, lease leasedTask, status, digest string, output map[string]any) {
	t.Helper()
	body := map[string]any{
		"schemaVersion": "kowa.workflow-execution.v1", "kind": "taskResult",
		"taskId": lease.Task.TaskID, "leaseId": lease.LeaseID, "fencingToken": lease.FencingToken,
		"status": status, "resultDigest": digest, "summary": "scripted attempt",
	}
	if output != nil {
		body["output"] = output
	}
	if status == "failed" {
		body["error"] = map[string]any{"code": "SCRIPTED_FAILURE", "message": "scripted", "retryable": true}
	}
	if reported := h.runtimeRequest(t, http.MethodPost,
		"/api/v1/runtime/tasks/"+lease.Task.TaskID+"/result", body, nil); reported != http.StatusOK {
		t.Fatalf("report the attempt result: status %d", reported)
	}
}

// deliverCode plays the controlled Git channel: a real commit is pushed to the
// frozen working branch of the local remote, and the attempt reports the
// remote-visible commit.
func (h *journeyHarness) deliverCode(t *testing.T, lease leasedTask) string {
	t.Helper()
	branch := h.workingBranchOf(lease)
	clone := t.TempDir()
	runGit(t, "", "clone", "--quiet", h.projectRemote, clone)
	// Each delivery writes its own content, so a later journey in the same
	// repository produces a real commit rather than an empty one.
	writeFile(t, filepath.Join(clone, "greeting.go"), fmt.Sprintf(
		"package greeting\n\n// Hello returns a deterministic greeting.\nfunc Hello() string { return %q }\n",
		h.nextKey("greeting")))
	runGit(t, clone, "add", "greeting.go")
	runGit(t, clone, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "add the greeting helper")
	runGit(t, clone, "push", "--quiet", "origin", "HEAD:"+branch)
	h.observer.rememberBranch(branch)
	h.setMachineCommit(strings.TrimSpace(runGit(t, clone, "rev-parse", "HEAD")))
	return h.machineCommit()
}

func (h *journeyHarness) workingBranchOf(lease leasedTask) string {
	for _, scope := range lease.Task.GitScopes {
		if scope.Operation == "push" {
			return scope.Ref
		}
	}
	return ""
}

func (h *journeyHarness) machineCommit() string {
	h.observer.mu.Lock()
	defer h.observer.mu.Unlock()
	return h.observer.machineCommit
}

func (h *journeyHarness) setMachineCommit(commit string) {
	h.observer.mu.Lock()
	defer h.observer.mu.Unlock()
	h.observer.machineCommit = commit
}

// rememberBranch makes the scripted observation read the real local remote.
func (o *scriptedObserver) rememberBranch(branch string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observedBranches[branch] = true
}

// publishPullRequest records the pull request the delivery created. The head is
// the pushed commit, so every later fact binds it.
func (o *scriptedObserver) publishPullRequest(t *testing.T, head, headRef string) string {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pullRequest == nil {
		o.pullRequest = &githubobs.PullRequestObservation{}
	}
	o.pullRequest.RepositoryID = journeyProjectRepository
	o.pullRequest.Number = "42"
	o.pullRequest.AuthorID = journeyMachineID
	o.pullRequest.HeadOID = head
	o.pullRequest.HeadRef = headRef
	o.pullRequest.BaseRef = "refs/heads/main"
	o.pullRequest.State = "open"
	o.pullRequest.Merged = false
	o.pullRequest.ActorID = journeyMachineID
	return "42"
}

// approveReview records the human review GitHub would report.
func (o *scriptedObserver) approveReview(t *testing.T) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pullRequest == nil {
		t.Fatal("a review cannot exist without a pull request")
	}
	o.review = &githubobs.ReviewObservation{
		RepositoryID: journeyProjectRepository, Number: o.pullRequest.Number,
		ReviewID: "5001", ReviewerID: journeyHumanID, ReviewerLogin: journeyHumanLogin,
		ReviewerType: "User", ReviewerPermission: "write", AuthorAssociation: "MEMBER",
		State: "APPROVED", CommitOID: o.pullRequest.HeadOID, SubmittedAt: time.Now().UTC(),
		ActorID: journeyHumanID,
	}
}

// mergePullRequest performs the human merge in the local repository, so the
// merge commit the closure binds is a real Git fact.
func (o *scriptedObserver) mergePullRequest(t *testing.T) {
	t.Helper()
	o.mu.Lock()
	pullRequest := o.pullRequest
	o.mu.Unlock()
	if pullRequest == nil {
		t.Fatal("a merge cannot exist without a pull request")
	}
	clone := t.TempDir()
	runGit(t, "", "clone", "--quiet", o.registry[journeyProjectRepository], clone)
	runGit(t, clone, "fetch", "--quiet", "origin", pullRequest.HeadRef)
	runGit(t, clone, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"merge", "--quiet", "--no-ff", "-m", "merge the greeting helper", "FETCH_HEAD")
	mergeCommit := strings.TrimSpace(runGit(t, clone, "rev-parse", "HEAD"))
	runGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")

	o.mu.Lock()
	defer o.mu.Unlock()
	o.pullRequest.Merged = true
	o.pullRequest.State = "closed"
	o.pullRequest.MergeCommitOID = mergeCommit
	o.pullRequest.MergedByID = journeyHumanID
	o.pullRequest.MergedByType = "User"
	o.pullRequest.MergedAt = time.Now().UTC()
}
