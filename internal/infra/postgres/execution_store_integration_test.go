//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/workflow"
)

func TestWorkflowExecutionPostgres(t *testing.T) {
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	database, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into workspace (workspace_id, name, version, created_at, updated_at)
		values ('workspace-1', 'Execution integration', 1, now(), now())
	`); err != nil {
		t.Fatalf("insert workspace fixture error = %v", err)
	}

	now := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	store := postgres.NewStore(database)
	definitionDigest := publishIntegrationPlan(t, store)
	service := execution.NewService(store, func() time.Time { return now })
	if err := service.RegisterRuntime(ctx, execution.RuntimeRegistration{
		ID: "runtime-1", Epoch: "epoch-1", GitHubUserID: "9001",
		Provider: execution.ProviderSelection{ID: "codex", Version: "1"}, ProviderAuthenticated: true,
		Capabilities: []execution.Capability{{ID: "plan.create", Version: 1}},
	}); err != nil {
		t.Fatalf("RegisterRuntime() error = %v", err)
	}
	createPostgresRun(t, service, "run-1", "node-1", "task-1", definitionDigest)
	lease, err := service.LeaseTask(ctx, execution.LeaseRequest{
		RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("LeaseTask() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("database.Close() before recovery error = %v", err)
	}

	database, err = postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() after recovery error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database.Close() error = %v", err)
		}
	})
	service = execution.NewService(postgres.NewStore(database), func() time.Time { return now })
	if err := service.ReportProgress(ctx, execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.ProgressEventKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Sequence: 1, Phase: "working", Message: "working", ObservedAt: now,
	}); err != nil {
		t.Fatalf("ReportProgress() after recovery error = %v", err)
	}
	report := execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: "task-1", LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: integrationDigestA,
		Output: map[string]any{"verdict": "approved"},
	}
	accepted, err := service.ReportResult(ctx, report)
	if err != nil || !accepted.Applied {
		t.Fatalf("ReportResult() = %#v, %v", accepted, err)
	}
	duplicate, err := service.ReportResult(ctx, report)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("ReportResult() duplicate = %#v, %v", duplicate, err)
	}
	report.ResultDigest = integrationDigestB
	if _, err := service.ReportResult(ctx, report); !errors.Is(err, execution.ErrResultConflict) {
		t.Fatalf("ReportResult() conflict error = %v", err)
	}

	createPostgresRun(t, service, "run-2", "node-2", "task-2", definitionDigest)
	expiring, err := service.LeaseTask(ctx, execution.LeaseRequest{
		RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Second,
	})
	if err != nil {
		t.Fatalf("LeaseTask() expiring error = %v", err)
	}
	now = now.Add(2 * time.Second)
	_, err = service.ReportResult(ctx, execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: "task-2", LeaseID: expiring.LeaseID, FencingToken: expiring.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: integrationDigestA, Output: map[string]any{},
	})
	if !errors.Is(err, execution.ErrStaleLease) {
		t.Fatalf("ReportResult() expired error = %v, want ErrStaleLease", err)
	}
	view, err := service.RunView(ctx, "run-2")
	if err != nil {
		t.Fatalf("RunView() error = %v", err)
	}
	if view.Nodes[0].State == execution.NodeDecided {
		t.Fatal("expired result advanced persisted node")
	}

	createPostgresRun(t, service, "run-3", "node-3", "task-3", definitionDigest)
	createPostgresRun(t, service, "run-4", "node-4", "task-4", definitionDigest)
	testConcurrentTaskLeases(t, service)
}

func testConcurrentTaskLeases(t *testing.T, service *execution.Service) {
	t.Helper()
	type result struct {
		lease execution.TaskLease
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			lease, err := service.LeaseTask(t.Context(), execution.LeaseRequest{
				RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
			})
			results <- result{lease: lease, err: err}
		}()
	}
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent LeaseTask() errors = %v, %v", first.err, second.err)
	}
	if first.lease.Task.ID == second.lease.Task.ID {
		t.Fatalf("concurrent LeaseTask() duplicated task %q", first.lease.Task.ID)
	}
}

func publishIntegrationPlan(t *testing.T, store *postgres.Store) string {
	t.Helper()
	definition := workflow.Definition{
		SchemaVersion: workflow.SchemaVersion, Kind: workflow.DefinitionKind,
		Name: "delivery", Version: 1,
		Nodes: []workflow.Node{{
			ID: "plan", Type: workflow.NodeCapability, Mandatory: true,
			Capability: workflow.CapabilityRef{ID: "plan.create", Version: 1},
		}},
	}
	plan, err := workflow.Compile(definition, workflow.Catalog{
		{ID: "plan.create", Version: 1}: {
			Input:  workflow.Schema{Fields: map[string]workflow.ValueType{}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{"result_ref": workflow.ValueArtifactRef}},
		},
	})
	if err != nil {
		t.Fatalf("workflow.Compile() error = %v", err)
	}
	if err := store.PublishPlan(t.Context(), plan); err != nil {
		t.Fatalf("PublishPlan() error = %v", err)
	}
	return plan.Digest
}

func createPostgresRun(
	t *testing.T,
	service *execution.Service,
	runID string,
	nodeRunID string,
	taskID string,
	definitionDigest string,
) {
	t.Helper()
	err := service.CreateRun(t.Context(), execution.CreateRunCommand{
		Run: execution.Run{
			ID: runID, WorkspaceID: "workspace-1", WorkItemID: "work-" + runID,
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: definitionDigest, State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: nodeRunID, RunID: runID, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: integrationInputDigest, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: taskID, NodeRunID: nodeRunID, Attempt: 1, AttemptKind: execution.AttemptInitial,
			Capability: execution.Capability{ID: "plan.create", Version: 1},
			Provider:   execution.ProviderSelection{ID: "codex", Version: "1"},
			GitScopes:  []execution.GitScope{{RepositoryID: "3001", Role: "project", Operation: "push", Ref: "refs/heads/feature"}},
			State:      execution.TaskQueued, InputDigest: integrationInputDigest,
			DeadlineAt: time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC),
		}},
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
}

const (
	integrationDigestA     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	integrationDigestB     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	integrationInputDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)
