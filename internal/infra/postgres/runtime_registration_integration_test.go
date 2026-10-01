//go:build integration

package postgres_test

import (
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/execution/executiontest"
	"github.com/SnWalker/kowa/internal/infra/postgres"
)

func TestWorkflowExecutionRuntimeRegistrationPostgres(t *testing.T) {
	executiontest.RuntimeRegistration(t, func(t *testing.T) executiontest.Env {
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
 values('workspace-1','runtime registration tests',1,now(),now()) on conflict do nothing
 `); err != nil {
			t.Fatal(err)
		}
		store := postgres.NewStore(database)
		now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
		service := execution.NewService(store, func() time.Time { return now })
		definitionDigest := publishIntegrationPlan(t, store)
		return executiontest.Env{
			Service: service,
			NewRun: func(t *testing.T, name string, provider execution.ProviderSelection, frozenGitUser string) string {
				t.Helper()
				runID, nodeID, taskID := name+"/run", name+"/node", name+"/task"
				err := service.CreateRun(t.Context(), execution.CreateRunCommand{
					Run: execution.Run{
						ID: runID, WorkspaceID: "workspace-1", WorkItemID: "work-" + runID,
						StartedByGitHubUserID: "101", GitExecutionUserID: frozenGitUser,
						DefinitionName: "delivery", DefinitionVersion: 1, DefinitionDigest: definitionDigest,
						State: execution.RunActive, Version: 1,
					},
					Nodes: []execution.NodeRun{{
						ID: nodeID, RunID: runID, NodeID: "plan", Iteration: 1,
						State: execution.NodeReady, InputDigest: integrationInputDigest, Version: 1,
					}},
					Tasks: []execution.Task{{
						ID: taskID, NodeRunID: nodeID, Attempt: 1, AttemptKind: execution.AttemptInitial,
						Capability: execution.Capability{ID: "plan.create", Version: 1}, Provider: provider,
						GitScopes: []execution.GitScope{{RepositoryID: "3001", Role: "project", Operation: "push", Ref: "refs/heads/feature"}},
						State:     execution.TaskQueued, InputDigest: integrationInputDigest,
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
				return persistedRegistration(t, database, runtimeID)
			},
			Task: func(t *testing.T, taskID string) any {
				t.Helper()
				return postgresTaskSnapshot(t, database, taskID)
			},
		}
	})
}

func persistedRegistration(t *testing.T, database *sql.DB, runtimeID string) (execution.RuntimeRegistration, bool) {
	t.Helper()
	registration := execution.RuntimeRegistration{ID: runtimeID}
	var providerID, providerVersion sql.NullString
	err := database.QueryRowContext(t.Context(), `
 select runtime_epoch, github_user_id, provider_id, provider_version, provider_authenticated
 from runtime_registration where runtime_id=$1
 `, runtimeID).Scan(
		&registration.Epoch, &registration.GitHubUserID, &providerID, &providerVersion,
		&registration.ProviderAuthenticated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.RuntimeRegistration{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	registration.Provider = execution.ProviderSelection{ID: providerID.String, Version: providerVersion.String}
	rows, err := database.QueryContext(t.Context(), `
 select capability_id, capability_version from runtime_capability where runtime_id=$1
 `, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var capability execution.Capability
		if err := rows.Scan(&capability.ID, &capability.Version); err != nil {
			t.Fatal(err)
		}
		registration.Capabilities = append(registration.Capabilities, capability)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return registration, true
}
