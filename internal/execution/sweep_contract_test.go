package execution_test

import (
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
)

func memorySweepEnv(t *testing.T) executiontest.CurrentRoundEnv {
	t.Helper()
	store := execution.NewMemoryStore()
	return executiontest.CurrentRoundEnv{
		Store: store, DefinitionDigest: memoryDigest, WorkspaceID: "sweep-workspace",
		Facts: func(t *testing.T, runID string) any { t.Helper(); return store.FactsForTest(runID) },
		TaskState: func(t *testing.T, taskID string) execution.TaskState {
			t.Helper()
			return store.TaskForTest(taskID).State
		},
	}
}

func TestMemoryStoreSweepExpiryContract(t *testing.T) {
	executiontest.SweepExpiry(t, memorySweepEnv)
}

func TestMemoryStoreTaskStatusReadContract(t *testing.T) {
	executiontest.TaskStatusRead(t, memorySweepEnv)
}
