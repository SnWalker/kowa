package executiontest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

// wireGitScope mirrors the frozen kowa.workflow-execution.v1 `$defs.GitScope`
// field names. It is the Runner consumer contract, deliberately declared apart
// from the Kowa-internal GitScope type so a producer regression cannot hide
// behind the same struct it is meant to check.
type wireGitScope struct {
	RepositoryID string `json:"repositoryId"`
	Role         string `json:"role"`
	Operation    string `json:"operation"`
	Ref          string `json:"ref"`
}

var wireNow = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

// wireSlug turns a test name into identifier-safe Kowa IDs: the frozen schema
// constrains IDs to [A-Za-z0-9][A-Za-z0-9_-]{0,127}, so subtest separators are
// folded to dashes.
func wireSlug(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "/", "-")
}

func wireScopes() []execution.GitScope {
	return []execution.GitScope{
		{RepositoryID: "3001", Role: "project", Operation: "push", Ref: "refs/heads/task/wire"},
		{RepositoryID: "4002", Role: "knowledge", Operation: "fetch", Ref: "refs/heads/main"},
	}
}

// GitScopeWire drives the real dispatcher ports on one adapter and checks the
// non-empty GitScope producer against the frozen v1 wire: the internal store
// round trip and the external TaskLease projection must both carry the full
// repository identity, role, operation and ref, contract violations must be
// rejected by the frozen schema, and a rejected request must leave current
// facts untouched while a following legal request still succeeds.
func GitScopeWire(t *testing.T, newEnv func(*testing.T) CurrentRoundEnv) {
	t.Helper()
	env := newEnv(t)
	name := wireSlug(t)
	service := execution.NewService(env.Store, func() time.Time { return wireNow })
	runtime := execution.RuntimeRegistration{
		ID: name + "-runtime", Epoch: "epoch-1", GitHubUserID: "9001",
		Provider:              execution.ProviderSelection{ID: "codex", Version: "wire"},
		ProviderAuthenticated: true,
		Capabilities:          []execution.Capability{{ID: "plan.create", Version: 1}},
	}
	if err := service.RegisterRuntime(t.Context(), runtime); err != nil {
		t.Fatal(err)
	}

	scopes := wireScopes()
	primary := name + "-run"
	if err := wireCreateRun(t, service, env, primary, primary+"-node", primary+"-task", scopes); err != nil {
		t.Fatal(err)
	}
	lease := wireExpectLease(t, service, runtime, primary+"-task")
	payload := wireMarshal(t, lease)
	// Drain the primary attempt so later scenarios never compete for a queued task.
	wireReport(t, service, lease, execution.ResultCompleted)

	t.Run("producer_wire_matches_frozen_schema", func(t *testing.T) {
		if !reflect.DeepEqual(lease.Task.GitScopes, scopes) {
			t.Fatalf("dispatcher projection lost scopes: %+v", lease.Task.GitScopes)
		}
		if decoded := wireStrictDecode(t, payload); !reflect.DeepEqual(decoded, wireWireScopes(scopes)) {
			t.Fatalf("strict consumer lost scope identity:\n got %+v\nwant %+v", decoded, wireWireScopes(scopes))
		}
		wireAssertSchema(t, payload)
	})

	t.Run("frozen_schema_rejects_scope_violations", func(t *testing.T) {
		// Prove the pinned validator is live before trusting the negative controls.
		fixture, err := os.ReadFile(filepath.Join(
			wireRepoRoot(t), "api", "workflow-execution", "v1", "examples", "positive-task-lease.json",
		))
		if err != nil {
			t.Fatal(err)
		}
		wireAssertSchema(t, fixture)
		for caseName, mutate := range map[string]func(map[string]any){
			"missing repositoryId": func(scope map[string]any) { delete(scope, "repositoryId") },
			"unknown repository_id": func(scope map[string]any) {
				scope["repository_id"] = scope["repositoryId"]
				delete(scope, "repositoryId")
			},
			"non-decimal repositoryId": func(scope map[string]any) { scope["repositoryId"] = "3001a" },
		} {
			t.Run(caseName, func(t *testing.T) {
				payload := wireMutateFirstScope(t, payload, mutate)
				if err := wireSchemaError(t, payload); err == nil {
					t.Fatalf("frozen schema accepted invalid scope: %s", payload)
				}
			})
		}
		// The Runner side must reject unknown fields as well.
		_, err = wireDecodeScopes(wireMutateFirstScope(t, payload, func(scope map[string]any) {
			scope["repository_id"] = scope["repositoryId"]
			delete(scope, "repositoryId")
		}))
		if err == nil {
			t.Fatal("strict Runner decoder accepted unknown repository_id")
		}
	})

	t.Run("illegal_scope_rejected_without_side_effect", func(t *testing.T) {
		target := name + "-illegal-run"
		before := env.Facts(t, target)
		illegal := []execution.GitScope{{
			RepositoryID: "3001", Role: "knowledge", Operation: "push", Ref: "refs/heads/wire",
		}}
		if err := wireCreateRun(t, service, env, target, target+"-node", target+"-task", illegal); err == nil {
			t.Fatal("illegal knowledge write scope was accepted")
		}
		if after := env.Facts(t, target); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected run creation changed persisted facts")
		}
		if err := wireCreateRun(t, service, env, target, target+"-node", target+"-task", scopes); err != nil {
			t.Fatalf("legal run after rejection = %v", err)
		}
		wireReport(t, service, wireExpectLease(t, service, runtime, target+"-task"), execution.ResultCompleted)
	})

	t.Run("recovery_paths_preserve_scopes", func(t *testing.T) {
		// Retry: a failed attempt recovers in the same node round.
		retryRun := name + "-retry-run"
		if err := wireCreateRun(t, service, env, retryRun, retryRun+"-node", retryRun+"-task", scopes); err != nil {
			t.Fatal(err)
		}
		failed := wireExpectLease(t, service, runtime, retryRun+"-task")
		wireReport(t, service, failed, execution.ResultFailed)
		retry, err := service.RetryTask(t.Context(), failed.Task.ID, retryRun+"-retry")
		if err != nil || !reflect.DeepEqual(retry.GitScopes, scopes) {
			t.Fatalf("retry scopes = %+v, %v", retry.GitScopes, err)
		}
		wireAssertLeaseScopes(t, wireExpectLease(t, service, runtime, retryRun+"-retry"), scopes)

		// Resume: an awaiting-input attempt recovers with new input.
		resumeRun := name + "-resume-run"
		if err := wireCreateRun(t, service, env, resumeRun, resumeRun+"-node", resumeRun+"-task", scopes); err != nil {
			t.Fatal(err)
		}
		waiting := wireExpectLease(t, service, runtime, resumeRun+"-task")
		wireReport(t, service, waiting, execution.ResultAwaitingInput)
		resumed, err := service.ResumeTask(
			t.Context(), waiting.Task.ID, resumeRun+"-resume", "wire-answer", resultDigestB, nil,
		)
		if err != nil || !reflect.DeepEqual(resumed.GitScopes, scopes) {
			t.Fatalf("resume scopes = %+v, %v", resumed.GitScopes, err)
		}
		wireAssertLeaseScopes(t, wireExpectLease(t, service, runtime, resumeRun+"-resume"), scopes)

		// Rerun: a new node round keeps the frozen scope.
		rerunRun := name + "-rerun-run"
		if err := wireCreateRun(t, service, env, rerunRun, rerunRun+"-node", rerunRun+"-task", scopes); err != nil {
			t.Fatal(err)
		}
		wireReport(t, service, wireExpectLease(t, service, runtime, rerunRun+"-task"), execution.ResultCompleted)
		if _, err := service.RerunNode(
			t.Context(), rerunRun, "plan", rerunRun+"-node-2", rerunRun+"-task-2", resultDigestB,
		); err != nil {
			t.Fatal(err)
		}
		wireAssertLeaseScopes(t, wireExpectLease(t, service, runtime, rerunRun+"-task-2"), scopes)
	})
}

func wireCreateRun(
	t *testing.T,
	service *execution.Service,
	env CurrentRoundEnv,
	runID string,
	nodeID string,
	taskID string,
	scopes []execution.GitScope,
) error {
	t.Helper()
	return service.CreateRun(t.Context(), execution.CreateRunCommand{
		Run: execution.Run{
			ID: runID, WorkspaceID: env.WorkspaceID, WorkItemID: runID + "-work",
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: env.DefinitionDigest, State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: nodeID, RunID: runID, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: resultDigestA, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: taskID, NodeRunID: nodeID, Attempt: 1, AttemptKind: execution.AttemptInitial,
			Capability: execution.Capability{ID: "plan.create", Version: 1},
			Provider:   execution.ProviderSelection{ID: "codex", Version: "wire"},
			GitScopes:  scopes, State: execution.TaskQueued,
			InputDigest: resultDigestA, DeadlineAt: wireNow.Add(time.Hour),
		}},
	})
}

func wireExpectLease(
	t *testing.T,
	service *execution.Service,
	runtime execution.RuntimeRegistration,
	wantTaskID string,
) execution.TaskLease {
	t.Helper()
	lease, err := service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: runtime.ID, RuntimeEpoch: runtime.Epoch, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Task.ID != wantTaskID {
		t.Fatalf("dispatcher leased %q, want %q", lease.Task.ID, wantTaskID)
	}
	return lease
}

func wireMarshal(t *testing.T, lease execution.TaskLease) []byte {
	t.Helper()
	encoded, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// wireDecodeScopes decodes the gitScopes of a real TaskLease payload strictly,
// exactly as a Runner consuming the frozen v1 field names must.
func wireDecodeScopes(payload []byte) ([]wireGitScope, error) {
	var envelope struct {
		Task struct {
			GitScopes []json.RawMessage `json:"gitScopes"`
		} `json:"task"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	scopes := make([]wireGitScope, 0, len(envelope.Task.GitScopes))
	for _, raw := range envelope.Task.GitScopes {
		var scope wireGitScope
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&scope); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func wireStrictDecode(t *testing.T, payload []byte) []wireGitScope {
	t.Helper()
	scopes, err := wireDecodeScopes(payload)
	if err != nil {
		t.Fatalf("strict consumer rejected the actual producer: %v", err)
	}
	return scopes
}

func wireWireScopes(scopes []execution.GitScope) []wireGitScope {
	expected := make([]wireGitScope, 0, len(scopes))
	for _, scope := range scopes {
		expected = append(expected, wireGitScope{
			RepositoryID: scope.RepositoryID, Role: scope.Role,
			Operation: scope.Operation, Ref: scope.Ref,
		})
	}
	return expected
}

func wireAssertLeaseScopes(t *testing.T, lease execution.TaskLease, scopes []execution.GitScope) {
	t.Helper()
	if !reflect.DeepEqual(lease.Task.GitScopes, scopes) {
		t.Fatalf("recovered task lost scopes: %+v", lease.Task.GitScopes)
	}
	if decoded := wireStrictDecode(t, wireMarshal(t, lease)); !reflect.DeepEqual(decoded, wireWireScopes(scopes)) {
		t.Fatalf("recovered task wire lost scopes: %+v", decoded)
	}
}

func wireReport(
	t *testing.T,
	service *execution.Service,
	lease execution.TaskLease,
	status execution.ResultStatus,
) {
	t.Helper()
	report := execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: status, ResultDigest: resultDigestB, Summary: "wire scenario",
	}
	switch status {
	case execution.ResultCompleted:
		report.Output = map[string]any{"verdict": "approved"}
	case execution.ResultFailed:
		report.Error = &execution.TaskError{Code: "WIRE_FAILURE", Message: "synthetic", Retryable: true}
	case execution.ResultAwaitingInput:
		report.InputRequest = map[string]any{"requestKey": "clarify"}
	}
	acceptance, err := service.ReportResult(t.Context(), report)
	if err != nil || !acceptance.Applied {
		t.Fatalf("result %s = %+v, %v", status, acceptance, err)
	}
}

// wireMutateFirstScope rewrites the first git scope of a real TaskLease payload.
func wireMutateFirstScope(t *testing.T, payload []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	task, ok := envelope["task"].(map[string]any)
	if !ok {
		t.Fatal("payload has no task object")
	}
	scopes, ok := task["gitScopes"].([]any)
	if !ok || len(scopes) == 0 {
		t.Fatal("payload has no git scopes")
	}
	scope, ok := scopes[0].(map[string]any)
	if !ok {
		t.Fatal("payload scope is not an object")
	}
	mutate(scope)
	mutated, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return mutated
}

// wireSchemaError validates one instance against the full frozen schema with the
// same pinned validator the repository contract gate uses.
func wireSchemaError(t *testing.T, instance []byte) error {
	t.Helper()
	root := wireRepoRoot(t)
	path := filepath.Join(t.TempDir(), "instance.json")
	if err := os.WriteFile(path, instance, 0o600); err != nil {
		t.Fatal(err)
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go binary not found on PATH to run the pinned schema validator: %v", err)
	}
	command := exec.Command(
		goBinary, "run", "github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0", "-f", "-c",
		filepath.Join(root, "api", "workflow-execution", "v1", "schema.json"), path,
	)
	command.Dir = root
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
}

func wireAssertSchema(t *testing.T, instance []byte) {
	t.Helper()
	if err := wireSchemaError(t, instance); err != nil {
		t.Fatalf("actual TaskLease violates the frozen kowa.workflow-execution.v1 schema: %v\n%s", err, instance)
	}
}

func wireRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "api", "workflow-execution", "v1", "schema.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root with the frozen workflow-execution schema was not found")
		}
		dir = parent
	}
}
