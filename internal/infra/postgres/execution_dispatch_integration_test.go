//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/workflow"
)

// TestDispatchPortPostgres runs the shared dispatch/decision matrix against the
// PostgreSQL adapter, then adds the concurrency and restart facts only a real
// database can establish.
func TestDispatchPortPostgres(t *testing.T) {
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	insertWorkspaceFixture(t, databaseURL, "dispatch-workspace")

	newEnv := func(t *testing.T) executiontest.DispatchEnv {
		database, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			t.Fatalf("postgres.Open() error = %v", err)
		}
		t.Cleanup(func() {
			if err := database.Close(); err != nil {
				t.Errorf("database.Close() error = %v", err)
			}
		})
		store := postgres.NewStore(database)
		return executiontest.DispatchEnv{
			Store:       store,
			WorkspaceID: "dispatch-workspace",
			PublishPlan: func(t *testing.T, plan workflow.CompiledPlan) {
				t.Helper()
				if err := store.PublishPlan(t.Context(), plan); err != nil {
					t.Fatalf("publish plan: %v", err)
				}
			},
		}
	}
	executiontest.DispatchPort(t, newEnv)
	executiontest.RetryAttemptPort(t, newEnv)

	t.Run("concurrent_dispatch_creates_exactly_one_attempt", func(t *testing.T) {
		database, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			t.Fatalf("postgres.Open() error = %v", err)
		}
		defer func() { _ = database.Close() }()
		store := postgres.NewStore(database)
		plan := dispatchFixturePlan(t, store)
		service := execution.NewService(store, time.Now)
		runID := "concurrent-run"
		now := time.Now().UTC()
		if err := service.RegisterRuntime(ctx, execution.RuntimeRegistration{
			ID: "concurrent-runtime", Epoch: "epoch-1", GitHubUserID: "9001",
			Provider: execution.ProviderSelection{ID: "codex", Version: "1"}, ProviderAuthenticated: true,
			Capabilities: []execution.Capability{{ID: "fixture.prepare", Version: 1}},
		}); err != nil {
			t.Fatalf("RegisterRuntime() error = %v", err)
		}
		if err := service.CreateRun(ctx, execution.CreateRunCommand{
			Run: execution.Run{
				ID: runID, WorkspaceID: "dispatch-workspace", WorkItemID: "concurrent-work",
				StartedByGitHubUserID: "101", DefinitionName: plan.Name, DefinitionVersion: plan.Version,
				DefinitionDigest: plan.Digest, State: execution.RunActive, Version: 1,
			},
			Nodes: []execution.NodeRun{{
				ID: "concurrent-node", RunID: runID, NodeID: "prepare", Iteration: 1,
				State: execution.NodeReady, InputDigest: "sha256:" + repeatDigest("a"), Version: 1,
			}},
		}); err != nil {
			t.Fatalf("CreateRun() error = %v", err)
		}
		base := execution.DispatchNodeCommand{
			RunID: runID, NodeID: "prepare", NodeRunID: "concurrent-node",
			ExpectedRunVersion: 1, InputDigest: "sha256:" + repeatDigest("a"),
			Capability: execution.Capability{ID: "fixture.prepare", Version: 1},
			Provider:   execution.ProviderSelection{ID: "codex", Version: "1"},
			GitScopes: []execution.GitScope{{
				RepositoryID: "3001", Role: "project", Operation: "clone", Ref: repeatOID("b"),
			}},
			DeadlineAt: now.Add(time.Hour),
			Round:      execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
		}
		// Two different attempt identities race for the same round: exactly one
		// may materialise an execution.
		var wait sync.WaitGroup
		results := make([]error, 2)
		for index, taskID := range []string{"race-task-a", "race-task-b"} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				command := base
				command.TaskID = taskID
				_, results[index] = service.DispatchNode(context.Background(), command)
			}()
		}
		wait.Wait()
		accepted := 0
		for _, err := range results {
			if err == nil {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("concurrent dispatch accepted %d attempts, want exactly one (%v)", accepted, results)
		}
		lease, err := service.LeaseTask(ctx, execution.LeaseRequest{
			RuntimeID: "concurrent-runtime", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil {
			t.Fatalf("LeaseTask() error = %v", err)
		}
		if lease.Task.ID != "race-task-a" && lease.Task.ID != "race-task-b" {
			t.Fatalf("lease selected %s, want the single dispatched attempt", lease.Task.ID)
		}
	})

	t.Run("dispatch_survives_a_restart", func(t *testing.T) {
		database, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			t.Fatalf("postgres.Open() error = %v", err)
		}
		store := postgres.NewStore(database)
		plan := dispatchFixturePlan(t, store)
		service := execution.NewService(store, time.Now)
		runID := "restart-run"
		now := time.Now().UTC()
		if err := service.RegisterRuntime(ctx, execution.RuntimeRegistration{
			ID: "restart-runtime", Epoch: "epoch-1", GitHubUserID: "9001",
			Provider: execution.ProviderSelection{ID: "codex", Version: "1"}, ProviderAuthenticated: true,
			Capabilities: []execution.Capability{{ID: "fixture.execute", Version: 1}},
		}); err != nil {
			t.Fatalf("RegisterRuntime() error = %v", err)
		}
		if err := service.CreateRun(ctx, execution.CreateRunCommand{
			Run: execution.Run{
				ID: runID, WorkspaceID: "dispatch-workspace", WorkItemID: "restart-work",
				StartedByGitHubUserID: "101", DefinitionName: plan.Name, DefinitionVersion: plan.Version,
				DefinitionDigest: plan.Digest, State: execution.RunActive, Version: 1,
			},
			Nodes: []execution.NodeRun{{
				ID: "restart-node", RunID: runID, NodeID: "execute", Iteration: 1,
				State: execution.NodeReady, InputDigest: "sha256:" + repeatDigest("c"), Version: 1,
			}},
		}); err != nil {
			t.Fatalf("CreateRun() error = %v", err)
		}
		if _, err := service.DispatchNode(ctx, execution.DispatchNodeCommand{
			RunID: runID, NodeID: "execute", NodeRunID: "restart-node", TaskID: "restart-task",
			ExpectedRunVersion: 1, InputDigest: "sha256:" + repeatDigest("c"),
			Capability: execution.Capability{ID: "fixture.execute", Version: 1},
			Provider:   execution.ProviderSelection{ID: "codex", Version: "1"},
			GitScopes: []execution.GitScope{{
				RepositoryID: "3001", Role: "project", Operation: "clone", Ref: repeatOID("d"),
			}},
			DeadlineAt: now.Add(time.Hour),
			Round:      execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
		}); err != nil {
			t.Fatalf("DispatchNode() error = %v", err)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("database.Close() before restart error = %v", err)
		}
		database, err = postgres.Open(ctx, databaseURL)
		if err != nil {
			t.Fatalf("postgres.Open() after restart error = %v", err)
		}
		defer func() { _ = database.Close() }()
		restarted := execution.NewService(postgres.NewStore(database), time.Now)
		lease, err := restarted.LeaseTask(ctx, execution.LeaseRequest{
			RuntimeID: "restart-runtime", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil {
			t.Fatalf("LeaseTask() after restart error = %v", err)
		}
		if lease.Task.ID != "restart-task" || lease.Task.InputDigest != "sha256:"+repeatDigest("c") {
			t.Fatalf("restarted lease = %+v, want the frozen attempt", lease.Task)
		}
		// The replayed dispatch is still idempotent after the restart.
		replay, err := restarted.DispatchNode(ctx, execution.DispatchNodeCommand{
			RunID: runID, NodeID: "execute", NodeRunID: "restart-node", TaskID: "restart-task",
			ExpectedRunVersion: 9, InputDigest: "sha256:" + repeatDigest("c"),
			Capability: execution.Capability{ID: "fixture.execute", Version: 1},
			Provider:   execution.ProviderSelection{ID: "codex", Version: "1"},
			GitScopes: []execution.GitScope{{
				RepositoryID: "3001", Role: "project", Operation: "clone", Ref: repeatOID("d"),
			}},
			DeadlineAt: now.Add(time.Hour),
			Round:      execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
		})
		if err != nil || !replay.Duplicate || replay.Task.ID != "restart-task" {
			t.Fatalf("replayed dispatch after restart = %+v, %v", replay, err)
		}
	})
}

func insertWorkspaceFixture(t *testing.T, databaseURL, workspaceID string) {
	t.Helper()
	database, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.ExecContext(context.Background(), `
		insert into workspace (workspace_id, name, version, created_at, updated_at)
		values ($1, 'Dispatch port integration', 1, now(), now())
		on conflict (workspace_id) do nothing
	`, workspaceID); err != nil {
		t.Fatalf("insert workspace fixture error = %v", err)
	}
}

func repeatDigest(character string) string { return strings.Repeat(character, 64) }

func repeatOID(character string) string { return strings.Repeat(character, 40) }

func dispatchFixturePlan(t *testing.T, store *postgres.Store) workflow.CompiledPlan {
	t.Helper()
	plan := executiontest.DispatchPlan(t)
	if err := store.PublishPlan(context.Background(), plan); err != nil && !errors.Is(err, workflow.ErrDefinitionConflict) {
		t.Fatalf("publish dispatch fixture plan: %v", err)
	}
	return plan
}
