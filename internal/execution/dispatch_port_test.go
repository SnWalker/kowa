package execution_test

import (
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
	"github.com/SnWalker/kowa/internal/workflow"
)

// TestDispatchIntentComparisonIgnoresSubMicrosecondDrift pins the frozen
// dispatch-intent timestamp rule with fixed values: a nanosecond-bearing
// deadline equals its stored microsecond form, and a full microsecond of drift
// stays a different intent (Linux keeps nanoseconds, PostgreSQL microseconds).
func TestDispatchIntentComparisonIgnoresSubMicrosecondDrift(t *testing.T) {
	nanoseconds := time.Date(2026, 10, 15, 12, 35, 34, 123456789, time.UTC)
	microseconds := nanoseconds.Truncate(time.Microsecond)
	if !execution.SameInstant(nanoseconds, microseconds) {
		t.Fatal("a sub-microsecond drift changed the frozen dispatch intent")
	}
	if execution.SameInstant(nanoseconds, microseconds.Add(time.Microsecond)) {
		t.Fatal("a full microsecond of drift was treated as the same intent")
	}
}

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
