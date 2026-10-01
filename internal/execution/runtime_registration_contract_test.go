package execution_test

import (
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
)

const memoryDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestMemoryStoreRuntimeRegistrationContract(t *testing.T) {
	executiontest.RuntimeRegistration(t, func(t *testing.T) executiontest.Env {
		store := execution.NewMemoryStore()
		now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
		service := execution.NewService(store, func() time.Time { return now })
		return executiontest.Env{
			Service: service,
			NewRun: func(t *testing.T, name string, provider execution.ProviderSelection, frozenGitUser string) string {
				t.Helper()
				runID, nodeID, taskID := name+"/run", name+"/node", name+"/task"
				err := service.CreateRun(t.Context(), execution.CreateRunCommand{
					Run: execution.Run{
						ID: runID, WorkspaceID: "workspace-1", WorkItemID: "work-" + runID,
						StartedByGitHubUserID: "101", GitExecutionUserID: frozenGitUser,
						DefinitionName: "delivery", DefinitionVersion: 1, DefinitionDigest: memoryDigest,
						State: execution.RunActive, Version: 1,
					},
					Nodes: []execution.NodeRun{{
						ID: nodeID, RunID: runID, NodeID: "plan", Iteration: 1,
						State: execution.NodeReady, InputDigest: memoryDigest, Version: 1,
					}},
					Tasks: []execution.Task{{
						ID: taskID, NodeRunID: nodeID, Attempt: 1, AttemptKind: execution.AttemptInitial,
						Capability: execution.Capability{ID: "plan.create", Version: 1}, Provider: provider,
						GitScopes: []execution.GitScope{{RepositoryID: "3001", Role: "project", Operation: "push", Ref: "refs/heads/feature"}},
						State:     execution.TaskQueued, InputDigest: memoryDigest,
						DeadlineAt: time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC),
					}},
				})
				if err != nil {
					t.Fatalf("CreateRun() error = %v", err)
				}
				return taskID
			},
			Registration: func(t *testing.T, runtimeID string) (execution.RuntimeRegistration, bool) {
				t.Helper()
				return store.RuntimeForTest(runtimeID)
			},
			Task: func(t *testing.T, taskID string) any {
				t.Helper()
				return store.TaskForTest(taskID)
			},
		}
	})
}
