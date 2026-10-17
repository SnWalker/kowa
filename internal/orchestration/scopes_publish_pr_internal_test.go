package orchestration

import (
	"testing"

	"github.com/SnWalker/kowa/internal/fixedflow"
)

// TestPublishPrNodeCarriesTheWriteScope freezes the scope contract of the
// independent publish_pr step: the node authorizes exactly the frozen working
// branch for the controlled write (the Runner performs it with the machine's
// own gh identity), while every other node keeps the read-only shape. Without
// this scope the controlled channel has no write to perform and the step can
// never produce a pull request.
func TestPublishPrNodeCarriesTheWriteScope(t *testing.T) {
	service := &Service{}
	admission := Admission{
		RunID: "run-1", WorkspaceID: "ws-1", WorkItemID: "wi-1",
		ProjectRepositoryID: "1401511579", KnowledgeRepositoryID: "1413481670",
		ProjectCommitOID:   "1111111111111111111111111111111111111111",
		KnowledgeCommitOID: "2222222222222222222222222222222222222222",
		ProjectBranchRef:   "refs/heads/main",
		WorkingBranchRef:   "refs/heads/task/kowa-run-1",
	}

	assertScopes := func(t *testing.T, nodeID string, wantWrites map[string]string) {
		t.Helper()
		scopes := service.scopes(admission, nodeID, admission.ProjectCommitOID)
		writes := map[string]string{}
		reads := 0
		for _, scope := range scopes {
			switch scope.Operation {
			case "clone", "fetch", "pull":
				reads++
				if scope.Ref != admission.ProjectCommitOID && scope.Ref != admission.KnowledgeCommitOID {
					t.Fatalf("node %s read scope %s is not a frozen commit OID", nodeID, scope.Ref)
				}
			default:
				writes[scope.Operation+" "+scope.Ref] = scope.RepositoryID
			}
		}
		if reads != 2 {
			t.Fatalf("node %s has %d read scopes, want the project and knowledge clone", nodeID, reads)
		}
		if len(writes) != len(wantWrites) {
			t.Fatalf("node %s write scopes = %v, want %v", nodeID, writes, wantWrites)
		}
		for operation, repositoryID := range wantWrites {
			if writes[operation] != repositoryID {
				t.Fatalf("node %s is missing write scope %q on %s (has %v)", nodeID, operation, repositoryID, writes)
			}
		}
	}

	assertScopes(t, fixedflow.NodePublishPR, map[string]string{
		"publish_pr " + admission.WorkingBranchRef: admission.ProjectRepositoryID,
	})
	assertScopes(t, fixedflow.NodeCode, map[string]string{
		"push " + admission.WorkingBranchRef: admission.ProjectRepositoryID,
	})
	assertScopes(t, fixedflow.NodePlan, map[string]string{})
	assertScopes(t, fixedflow.NodeIntegration, map[string]string{})
}
