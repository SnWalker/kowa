package execution_test

import (
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
)

func TestMemoryStoreCurrentRoundContract(t *testing.T) {
	executiontest.CurrentRound(t, func(t *testing.T) executiontest.CurrentRoundEnv {
		store := execution.NewMemoryStore()
		return executiontest.CurrentRoundEnv{
			Store: store, DefinitionDigest: memoryDigest, WorkspaceID: "round-workspace",
			Facts: func(t *testing.T, runID string) any { t.Helper(); return store.FactsForTest(runID) },
			TaskState: func(t *testing.T, taskID string) execution.TaskState {
				t.Helper()
				return store.TaskForTest(taskID).State
			},
		}
	})
}
