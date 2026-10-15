//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution/executiontest"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/orchestration/orchestrationtest"
	"github.com/SnWalker/kowa/internal/workflow"
)

// TestOrchestrationStorePostgres runs the shared orchestration store contract
// against the PostgreSQL adapter and proves the frozen constraints hold.
func TestOrchestrationStorePostgres(t *testing.T) {
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	insertWorkspaceFixture(t, databaseURL, "orchestration-workspace")
	// The run identity must stay unique across every subtest, so the counter
	// lives outside the per-subtest environment factory.
	runCounter := 0

	orchestrationtest.StoreContract(t, func(t *testing.T) orchestrationtest.StoreEnv {
		database, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			t.Fatalf("postgres.Open() error = %v", err)
		}
		t.Cleanup(func() { _ = database.Close() })
		store := postgres.NewStore(database)
		plan := executiontest.DispatchPlan(t)
		if err := store.PublishPlan(ctx, plan); err != nil && !errors.Is(err, workflow.ErrDefinitionConflict) {
			t.Fatalf("publish the fixture definition: %v", err)
		}
		workspaceID := "orchestration-workspace"
		workItemID := "orchestration-work-item"
		return orchestrationtest.StoreEnv{
			Store:       store,
			WorkspaceID: workspaceID,
			WorkItemID:  workItemID,
			PrepareWorkspace: func(t *testing.T, workspaceID string) {
				insertWorkspaceFixture(t, databaseURL, workspaceID)
			},
			InputID: func(t *testing.T, env orchestrationtest.StoreEnv) string {
				return insertEffectiveInputFixture(t, databaseURL, workspaceID, workItemID)
			},
			AdmissionOf: func(t *testing.T, env orchestrationtest.StoreEnv) orchestration.Admission {
				// The run identity is unique per subtest; it also has to satisfy
				// the frozen branch grammar (no underscores).
				runCounter++
				runID := fmt.Sprintf("run-admission-%d", runCounter)
				insertRunFixture(t, databaseURL, workspaceID, runID, plan)
				return orchestration.Admission{
					RunID: runID, WorkspaceID: env.WorkspaceID, WorkItemID: env.WorkItemID,
					DefinitionName: plan.Name, DefinitionVersion: plan.Version,
					DefinitionDigest:      plan.Digest,
					ProjectRepositoryID:   "9001",
					KnowledgeRepositoryID: "9002",
					ProjectCommitOID:      postgresRepeat("1", 40),
					KnowledgeCommitOID:    postgresRepeat("2", 40),
					ProjectBranchRef:      "refs/heads/main",
					WorkingBranchRef:      "refs/heads/task/kowa-" + postgresRepeat("3", 8),
					ProviderID:            "scripted", ProviderVersion: "scripted-1",
					PolicyVersion: "kowa.mvp-policy.v1", AdmittedByUserID: "7001",
					AdmittedAt: time.Now().UTC(),
				}
			},
		}
	})

	t.Run("constraints_reject_malformed_facts", func(t *testing.T) {
		database, err := postgres.Open(ctx, databaseURL)
		if err != nil {
			t.Fatalf("postgres.Open() error = %v", err)
		}
		defer func() { _ = database.Close() }()
		// A non-oid baseline, a branch-shaped write authorization that is not a
		// task branch, and a malformed digest must all be refused by the schema.
		for name, statement := range map[string]string{
			"non oid baseline":           `update run_admission set project_commit_oid = 'refs/heads/main'`,
			"unprotected working branch": `update run_admission set working_branch_ref = 'refs/heads/main'`,
			"malformed digest":           `update run_admission set definition_digest = 'sha256:zz'`,
		} {
			if _, err := database.ExecContext(ctx, statement); err == nil {
				t.Fatalf("%s was accepted by the frozen constraints", name)
			}
		}
	})
}

func postgresRepeat(character string, count int) string {
	repeated := make([]byte, count)
	for index := range repeated {
		repeated[index] = character[0]
	}
	return string(repeated)
}

// insertEffectiveInputFixture provisions the frozen knowledge rows a binding
// references, so the contract exercises the real foreign keys.
func insertEffectiveInputFixture(t *testing.T, databaseURL, workspaceID, workItemID string) string {
	t.Helper()
	database, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	defer func() { _ = database.Close() }()
	canonical := []byte(`{"schemaVersion":"kowa.knowledge-artifact.v1"}`)
	digest := "sha256:" + sha256Hex(canonical)
	if _, err := database.ExecContext(context.Background(), `
		insert into knowledge_snapshot (workspace_id, snapshot_id, repository_id, commit_oid, digest, canonical)
		values ($1, 'ks-fixture', '9002', $2, $3, $4) on conflict do nothing
	`, workspaceID, postgresRepeat("2", 40), digest, canonical); err != nil {
		t.Fatalf("insert knowledge snapshot fixture: %v", err)
	}
	if _, err := database.ExecContext(context.Background(), `
		insert into knowledge_binding_event (
			workspace_id, event_id, subject_id, snapshot_id, human_response_id, actor_id,
			version, request_digest, confirmed_at
		) values ($1, 'kb-fixture', $2, 'ks-fixture', 'hr-fixture', '7001', 1, $3, now())
		on conflict do nothing
	`, workspaceID, workItemID, "sha256:"+sha256Hex([]byte("fixture"))); err != nil {
		t.Fatalf("insert knowledge binding fixture: %v", err)
	}
	if _, err := database.ExecContext(context.Background(), `
		insert into effective_input (workspace_id, input_id, subject_id, snapshot_id, event_id, digest, canonical)
		values ($1, 'ei-fixture', $2, 'ks-fixture', 'kb-fixture', $3, $4) on conflict do nothing
	`, workspaceID, workItemID, digest, canonical); err != nil {
		t.Fatalf("insert effective input fixture: %v", err)
	}
	return "ei-fixture"
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func insertRunFixture(t *testing.T, databaseURL, workspaceID, runID string, plan workflow.CompiledPlan) {
	t.Helper()
	database, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.ExecContext(context.Background(), `
		insert into workflow_run (
			workflow_run_id, workspace_id, work_item_id, started_by_github_user_id,
			definition_name, definition_version, definition_digest, state, version, created_at, updated_at
		) values ($1, $2, $3, '7001', $4, $5, $6, 'ACTIVE', 1, now(), now())
		on conflict (workflow_run_id) do nothing
	`, runID, workspaceID, "orchestration-work-item", plan.Name, plan.Version, plan.Digest); err != nil {
		t.Fatalf("insert run fixture error = %v", err)
	}
}
