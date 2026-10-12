//go:build integration

package postgres_test

import (
	"os"
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
	"github.com/SnWalker/kowa/internal/infra/postgres"
)

func TestWorkflowExecutionCancellationIntentPostgres(t *testing.T) {
	executiontest.CancellationIntent(t, func(t *testing.T) executiontest.CurrentRoundEnv {
		database, err := postgres.Open(t.Context(), os.Getenv("KOWA_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := database.Close(); err != nil {
				t.Error(err)
			}
		})
		if _, err := database.ExecContext(t.Context(), `
 insert into workspace(workspace_id,name,version,created_at,updated_at)
 values('round-workspace','current round tests',1,now(),now()) on conflict do nothing
 `); err != nil {
			t.Fatal(err)
		}
		store := postgres.NewStore(database)
		return executiontest.CurrentRoundEnv{
			Store: store, DefinitionDigest: publishIntegrationPlan(t, store), WorkspaceID: "round-workspace",
			Facts: func(t *testing.T, runID string) any { t.Helper(); return postgresRoundFacts(t, database, runID) },
			TaskState: func(t *testing.T, taskID string) execution.TaskState {
				t.Helper()
				var state execution.TaskState
				err := database.QueryRowContext(
					t.Context(), `select state from execution_task where task_id=$1`, taskID,
				).Scan(&state)
				if err != nil {
					t.Fatal(err)
				}
				return state
			},
		}
	})
}
