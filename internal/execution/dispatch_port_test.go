package execution_test

import (
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
	"github.com/SnWalker/kowa/internal/workflow"
)

// TestMemoryStoreDispatchPort runs the shared dispatch/decision matrix against
// the in-memory adapter; the PostgreSQL adapter runs the same matrix in the
// isolation integration suite.
func TestMemoryStoreDispatchPort(t *testing.T) {
	executiontest.DispatchPort(t, func(t *testing.T) executiontest.DispatchEnv {
		store := execution.NewMemoryStore()
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
	})
}
