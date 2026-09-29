package execution

import (
	"errors"
	"testing"
	"time"
)

func TestLeaseProgressAndResultAcceptance(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	service := NewService(NewMemoryStore(), func() time.Time { return now })
	registerTestRuntime(t, service)
	createTestRun(t, service)

	lease, err := service.LeaseTask(t.Context(), LeaseRequest{
		RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("LeaseTask() error = %v", err)
	}
	if lease.Task.ID != "task-1" || lease.FencingToken != 1 {
		t.Fatalf("LeaseTask() lease = %#v", lease)
	}
	if err := service.ReportProgress(t.Context(), ProgressReport{
		SchemaVersion: SchemaVersion, Kind: ProgressEventKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: 1, Sequence: 1, Phase: "working",
		Message: "working", ObservedAt: now,
	}); err != nil {
		t.Fatalf("ReportProgress() error = %v", err)
	}
	view, err := service.RunView(t.Context(), "run-1")
	if err != nil {
		t.Fatalf("RunView() error = %v", err)
	}
	if view.Nodes[0].State != NodeRunning {
		t.Fatalf("progress advanced node state = %q, want %q", view.Nodes[0].State, NodeRunning)
	}

	result := ResultReport{
		SchemaVersion: SchemaVersion, Kind: TaskResultKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: 1,
		Status: ResultCompleted, ResultDigest: digestA, Output: map[string]any{"verdict": "approved"},
	}
	accepted, err := service.ReportResult(t.Context(), result)
	if err != nil || !accepted.Applied {
		t.Fatalf("ReportResult() = %#v, %v", accepted, err)
	}
	repeated, err := service.ReportResult(t.Context(), result)
	if err != nil || !repeated.Duplicate || repeated.Applied {
		t.Fatalf("ReportResult() duplicate = %#v, %v", repeated, err)
	}
	result.ResultDigest = digestB
	if _, err := service.ReportResult(t.Context(), result); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("ReportResult() conflict error = %v, want ErrResultConflict", err)
	}
}

func TestCreateRunRejectsKnowledgeWriteScope(t *testing.T) {
	t.Parallel()

	service := NewService(NewMemoryStore(), time.Now)
	err := service.CreateRun(t.Context(), CreateRunCommand{
		Run: Run{
			ID: "run-1", WorkspaceID: "workspace-1", WorkItemID: "work-1",
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: digestA, State: RunActive, Version: 1,
		},
		Nodes: []NodeRun{{ID: "node-1", RunID: "run-1", NodeID: "plan", Iteration: 1, State: NodeReady, InputDigest: inputDigest, Version: 1}},
		Tasks: []Task{{
			ID: "task-1", NodeRunID: "node-1", Attempt: 1, AttemptKind: AttemptInitial,
			Capability: Capability{ID: "plan.create", Version: 1},
			Provider:   ProviderSelection{ID: "codex", Version: "1"},
			GitScopes:  []GitScope{{RepositoryID: "3002", Role: "knowledge", Operation: "push", Ref: "refs/heads/main"}},
			State:      TaskQueued, InputDigest: inputDigest,
			DeadlineAt: time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC),
		}},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateRun() error = %v, want ErrInvalidInput", err)
	}
}

func TestExpiredLeaseAndLateResultDoNotAdvance(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	service := NewService(NewMemoryStore(), func() time.Time { return now })
	registerTestRuntime(t, service)
	createTestRun(t, service)
	lease, err := service.LeaseTask(t.Context(), LeaseRequest{RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Second})
	if err != nil {
		t.Fatalf("LeaseTask() error = %v", err)
	}
	now = now.Add(2 * time.Second)
	_, err = service.ReportResult(t.Context(), ResultReport{
		SchemaVersion: SchemaVersion, Kind: TaskResultKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: ResultCompleted, ResultDigest: digestA, Output: map[string]any{},
	})
	if !errors.Is(err, ErrStaleLease) {
		t.Fatalf("ReportResult() error = %v, want ErrStaleLease", err)
	}
	view, err := service.RunView(t.Context(), "run-1")
	if err != nil {
		t.Fatalf("RunView() error = %v", err)
	}
	if view.Nodes[0].State == NodeDecided {
		t.Fatal("late result advanced node")
	}
}

func TestRetryRerunAndCancel(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	service := NewService(NewMemoryStore(), func() time.Time { return now })
	registerTestRuntime(t, service)
	createTestRun(t, service)
	lease, err := service.LeaseTask(t.Context(), LeaseRequest{RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("LeaseTask() error = %v", err)
	}
	_, err = service.ReportResult(t.Context(), ResultReport{
		SchemaVersion: SchemaVersion, Kind: TaskResultKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: ResultFailed, ResultDigest: digestA,
		Error: &TaskError{Code: "provider_error", Message: "provider failed", Retryable: true},
	})
	if err != nil {
		t.Fatalf("ReportResult() error = %v", err)
	}
	retry, err := service.RetryTask(t.Context(), "task-1", "task-2")
	if err != nil {
		t.Fatalf("RetryTask() error = %v", err)
	}
	if retry.InputDigest != inputDigest || retry.PreviousTaskID != "task-1" || retry.AttemptKind != AttemptRetry {
		t.Fatalf("RetryTask() task = %#v", retry)
	}
	rerun, err := service.RerunNode(t.Context(), "run-1", "plan", "node-2", "task-3", digestB)
	if err != nil {
		t.Fatalf("RerunNode() error = %v", err)
	}
	if rerun.Iteration != 2 || rerun.ID != "node-2" {
		t.Fatalf("RerunNode() node = %#v", rerun)
	}
	view, err := service.RunView(t.Context(), "run-1")
	if err != nil {
		t.Fatalf("RunView() before cancel error = %v", err)
	}
	if err := service.CancelRun(t.Context(), "run-1", view.Version); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if err := service.CancelRun(t.Context(), "run-1", view.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("CancelRun() stale version error = %v, want ErrVersionConflict", err)
	}
}

func TestResumeAndRevisionBudget(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	service := NewService(NewMemoryStore(), func() time.Time { return now })
	registerTestRuntime(t, service)
	createTestRun(t, service)
	lease, err := service.LeaseTask(t.Context(), LeaseRequest{RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatalf("LeaseTask() error = %v", err)
	}
	_, err = service.ReportResult(t.Context(), ResultReport{
		SchemaVersion: SchemaVersion, Kind: TaskResultKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: ResultAwaitingInput, ResultDigest: digestA,
		InputRequest: map[string]any{"requestKey": "clarify"},
	})
	if err != nil {
		t.Fatalf("ReportResult() awaiting input error = %v", err)
	}
	resumed, err := service.ResumeTask(
		t.Context(),
		"task-1",
		"task-2",
		"response-1",
		digestB,
		&ResumeRef{RuntimeID: "runtime-1", ProviderID: "codex", SessionRef: "session-1"},
	)
	if err != nil {
		t.Fatalf("ResumeTask() error = %v", err)
	}
	if resumed.AttemptKind != AttemptResume || resumed.HumanResponseID != "response-1" || resumed.InputDigest != digestB {
		t.Fatalf("ResumeTask() task = %#v", resumed)
	}

	for iteration := 2; iteration <= 4; iteration++ {
		nodeID := "revision-node-" + string(rune('0'+iteration))
		taskID := "revision-task-" + string(rune('0'+iteration))
		if _, err := service.ReviseNode(t.Context(), "run-1", "plan", nodeID, taskID, digestB); err != nil {
			t.Fatalf("ReviseNode() iteration %d error = %v", iteration, err)
		}
	}
	if _, err := service.ReviseNode(t.Context(), "run-1", "plan", "revision-node-5", "revision-task-5", digestB); !errors.Is(err, ErrPolicyBlocked) {
		t.Fatalf("ReviseNode() exhausted error = %v, want ErrPolicyBlocked", err)
	}
}

func registerTestRuntime(t *testing.T, service *Service) {
	t.Helper()
	err := service.RegisterRuntime(t.Context(), RuntimeRegistration{
		ID: "runtime-1", Epoch: "epoch-1", GitHubUserID: "9001",
		Provider: ProviderSelection{ID: "codex", Version: "1"}, ProviderAuthenticated: true,
		Capabilities: []Capability{{ID: "plan.create", Version: 1}},
	})
	if err != nil {
		t.Fatalf("RegisterRuntime() error = %v", err)
	}
}

func createTestRun(t *testing.T, service *Service) {
	t.Helper()
	err := service.CreateRun(t.Context(), CreateRunCommand{
		Run:   Run{ID: "run-1", WorkspaceID: "workspace-1", WorkItemID: "work-1", StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1, DefinitionDigest: digestA, State: RunActive, Version: 1},
		Nodes: []NodeRun{{ID: "node-1", RunID: "run-1", NodeID: "plan", Iteration: 1, State: NodeReady, InputDigest: inputDigest, Version: 1}},
		Tasks: []Task{{
			ID: "task-1", NodeRunID: "node-1", Attempt: 1, AttemptKind: AttemptInitial,
			Capability: Capability{ID: "plan.create", Version: 1},
			Provider:   ProviderSelection{ID: "codex", Version: "1"},
			GitScopes:  []GitScope{{RepositoryID: "3001", Role: "project", Operation: "push", Ref: "refs/heads/feature"}},
			State:      TaskQueued, InputDigest: inputDigest,
			DeadlineAt: time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC),
		}},
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
}

const (
	digestA     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	inputDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)
