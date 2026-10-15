package orchestration_test

import (
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/orchestration/orchestrationtest"
)

// TestMemoryStoreContract runs the shared orchestration store contract against
// the in-memory adapter; the PostgreSQL adapter runs the same contract in the
// isolation integration suite.
func TestMemoryStoreContract(t *testing.T) {
	orchestrationtest.StoreContract(t, func(t *testing.T) orchestrationtest.StoreEnv {
		store := orchestration.NewMemoryStore()
		workspaceID := "orchestration-workspace"
		workItemID := "orchestration-work-item"
		return orchestrationtest.StoreEnv{
			Store:            store,
			WorkspaceID:      workspaceID,
			WorkItemID:       workItemID,
			PrepareWorkspace: func(*testing.T, string) {},
			InputID:          func(*testing.T, orchestrationtest.StoreEnv) string { return "ei-memory" },
			SettleRun: func(t *testing.T, runID string) {
				if err := store.NoteRunState(t.Context(), runID, "CANCELED"); err != nil {
					t.Fatal(err)
				}
			},
			AdmissionOf: func(t *testing.T, env orchestrationtest.StoreEnv) orchestration.Admission {
				runID := "run-" + t.Name()
				return orchestration.Admission{
					RunID: runID, WorkspaceID: env.WorkspaceID, WorkItemID: env.WorkItemID,
					DefinitionName: "kowa-fixed-workflow", DefinitionVersion: 1,
					DefinitionDigest:      "sha256:" + repeat("e", 64),
					ProjectRepositoryID:   "9001",
					KnowledgeRepositoryID: "9002",
					ProjectCommitOID:      repeat("1", 40),
					KnowledgeCommitOID:    repeat("2", 40),
					ProjectBranchRef:      "refs/heads/main",
					WorkingBranchRef:      "refs/heads/task/kowa-" + repeat("3", 8),
					ProviderID:            "scripted", ProviderVersion: "scripted-1",
					PolicyVersion: "kowa.mvp-policy.v1", AdmittedByUserID: "7001",
					AdmittedAt: time.Now().UTC(),
				}
			},
		}
	})
}

func repeat(character string, count int) string {
	repeated := make([]byte, count)
	for index := range repeated {
		repeated[index] = character[0]
	}
	return string(repeated)
}
