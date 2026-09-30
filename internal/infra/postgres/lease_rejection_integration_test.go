//go:build integration

package postgres_test

import (
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/infra/postgres"
)

func TestWorkflowExecutionLeaseRejectionPostgres(t *testing.T) {
	t.Run("rejections", testPostgresRejectedReportsPreserveCurrentTask)
	t.Run("expiry_and_identity", testPostgresLeaseExpiryAndReportIdentity)
}

func testPostgresRejectedReportsPreserveCurrentTask(t *testing.T) {

	for _, running := range []bool{false, true} {
		for _, kind := range []string{"lease", "fencing", "shape", "progress_lease", "progress_fencing"} {
			name := kind + "/leased"
			if running {
				name = kind + "/running"
			}
			t.Run(name, func(t *testing.T) {
				database, service, _, report := postgresLeaseRejectionFixture(t)
				if running {
					postgresReportWorking(t, service, report)
				}
				before := postgresTaskSnapshot(t, database, report.TaskID)
				viewBefore, err := service.RunView(t.Context(), t.Name()+"/run-1")
				if err != nil {
					t.Fatal(err)
				}
				wrong := report
				wantErr := execution.ErrStaleLease
				switch kind {
				case "lease", "progress_lease":
					wrong.LeaseID = "wrong-lease"
				case "fencing", "progress_fencing":
					wrong.FencingToken++
				case "shape":
					wrong.Output = nil
					wantErr = execution.ErrInvalidInput
				}
				if kind == "progress_lease" || kind == "progress_fencing" {
					err = service.ReportProgress(t.Context(), execution.ProgressReport{
						SchemaVersion: execution.SchemaVersion,
						Kind:          execution.ProgressEventKind,
						TaskID:        wrong.TaskID,
						LeaseID:       wrong.LeaseID,
						FencingToken:  wrong.FencingToken,
						Sequence:      2,
						Phase:         "working",
						Message:       "working",
						ObservedAt:    time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC),
					})
				} else {
					_, err = service.ReportResult(t.Context(), wrong)
				}
				if !errors.Is(err, wantErr) {
					t.Fatalf("rejected report error = %v, want %v", err, wantErr)
				}
				if !reflect.DeepEqual(postgresTaskSnapshot(t, database, report.TaskID), before) {
					t.Errorf("rejection changed current task: %#v -> %#v", before, postgresTaskSnapshot(t, database, report.TaskID))
				}
				viewAfter, err := service.RunView(t.Context(), t.Name()+"/run-1")
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(viewBefore, viewAfter) {
					t.Error("rejection changed run/node view")
				}
				accepted, err := service.ReportResult(t.Context(), report)
				if err != nil || !accepted.Applied || accepted.Duplicate {
					t.Fatalf("original correct report = %#v, %v", accepted, err)
				}
			})
		}
	}
}

func testPostgresLeaseExpiryAndReportIdentity(t *testing.T) {

	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			database, service, now, report := postgresLeaseRejectionFixture(t)
			*now = postgresTaskSnapshot(t, database, report.TaskID).LeaseExpiresAt.Add(offset)
			accepted, err := service.ReportResult(t.Context(), report)
			if offset < 0 {
				if err != nil || !accepted.Applied {
					t.Fatalf("before expiry: %#v, %v", accepted, err)
				}
				return
			}
			if !errors.Is(err, execution.ErrStaleLease) ||
				postgresTaskSnapshot(t, database, report.TaskID).State != execution.TaskExpired {
				t.Fatalf("at/after expiry: state=%s err=%v", postgresTaskSnapshot(t, database, report.TaskID).State, err)
			}
			if _, err := service.RetryTask(t.Context(), report.TaskID, t.Name()+"/task-2"); err != nil {
				t.Fatal(err)
			}
			lease, err := service.LeaseTask(t.Context(), execution.LeaseRequest{
				RuntimeID:     t.Name() + "/runtime",
				RuntimeEpoch:  "epoch-1",
				LeaseDuration: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			before := postgresTaskSnapshot(t, database, lease.Task.ID)
			viewBefore, err := service.RunView(t.Context(), t.Name()+"/run-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReportResult(t.Context(), report); !errors.Is(err, execution.ErrStaleLease) {
				t.Fatalf("late old report: %v", err)
			}
			viewAfter, err := service.RunView(t.Context(), t.Name()+"/run-1")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, postgresTaskSnapshot(t, database, lease.Task.ID)) ||
				!reflect.DeepEqual(viewBefore, viewAfter) {
				t.Fatal("late report changed retry")
			}
			report.TaskID, report.LeaseID, report.FencingToken = lease.Task.ID, lease.LeaseID, lease.FencingToken
			if accepted, err := service.ReportResult(t.Context(), report); err != nil || !accepted.Applied {
				t.Fatalf("retry result: %#v, %v", accepted, err)
			}
		})
	}
	t.Run("running_expiry", func(t *testing.T) {
		database, service, now, report := postgresLeaseRejectionFixture(t)
		postgresReportWorking(t, service, report)
		*now = postgresTaskSnapshot(t, database, report.TaskID).LeaseExpiresAt
		if _, err := service.ReportResult(t.Context(), report); !errors.Is(err, execution.ErrStaleLease) {
			t.Fatalf("running expiry: %v", err)
		}
		if postgresTaskSnapshot(t, database, report.TaskID).State != execution.TaskExpired {
			t.Fatal("running task not expired")
		}
	})
	t.Run("duplicate_and_conflict", func(t *testing.T) {
		database, service, now, report := postgresLeaseRejectionFixture(t)
		if _, err := service.ReportResult(t.Context(), report); err != nil {
			t.Fatal(err)
		}
		if err := postgresTerminalProgress(t, service, report); !errors.Is(err, execution.ErrStaleLease) {
			t.Fatalf("progress after completion: %v", err)
		}
		*now = now.Add(2 * time.Minute)
		before := postgresTaskSnapshot(t, database, report.TaskID)
		viewBefore, err := service.RunView(t.Context(), t.Name()+"/run-1")
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := service.ReportResult(t.Context(), report)
		if err != nil || !accepted.Duplicate || accepted.Applied {
			t.Fatalf("duplicate after expiry: %#v, %v", accepted, err)
		}
		for _, field := range []string{"lease", "fencing"} {
			wrong := report
			if field == "lease" {
				wrong.LeaseID = "wrong-lease"
			} else {
				wrong.FencingToken++
			}
			if _, err := service.ReportResult(t.Context(), wrong); !errors.Is(err, execution.ErrStaleLease) {
				t.Fatalf("wrong duplicate identity: %v", err)
			}
		}
		report.ResultDigest = integrationDigestB
		if _, err := service.ReportResult(t.Context(), report); !errors.Is(err, execution.ErrResultConflict) {
			t.Fatalf("conflict: %v", err)
		}
		viewAfter, err := service.RunView(t.Context(), t.Name()+"/run-1")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, postgresTaskSnapshot(t, database, report.TaskID)) ||
			!reflect.DeepEqual(viewBefore, viewAfter) {
			t.Fatal("duplicate/conflict changed accepted facts")
		}
	})
}

func postgresLeaseRejectionFixture(t *testing.T) (*sql.DB, *execution.Service, *time.Time, execution.ResultReport) {
	t.Helper()
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
 values('workspace-1','lease tests',1,now(),now()) on conflict do nothing
 `); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	store := postgres.NewStore(database)
	service := execution.NewService(store, func() time.Time { return now })
	runtimeID := t.Name() + "/runtime"
	if err := service.RegisterRuntime(t.Context(), execution.RuntimeRegistration{
		ID:                    runtimeID,
		Epoch:                 "epoch-1",
		GitHubUserID:          "9001",
		Provider:              execution.ProviderSelection{ID: "codex", Version: "1"},
		ProviderAuthenticated: true,
		Capabilities:          []execution.Capability{{ID: "plan.create", Version: 1}},
	}); err != nil {
		t.Fatal(err)
	}

	createPostgresRun(
		t,
		service,
		t.Name()+"/run-1",
		t.Name()+"/node-1",
		t.Name()+"/task-1",
		publishIntegrationPlan(t, store),
	)
	lease, err := service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID:     runtimeID,
		RuntimeEpoch:  "epoch-1",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Task.ID != t.Name()+"/task-1" {
		t.Fatalf("fixture claimed another task: %s", lease.Task.ID)
	}
	return database, service, &now, execution.ResultReport{
		SchemaVersion: execution.SchemaVersion,
		Kind:          execution.TaskResultKind,
		TaskID:        lease.Task.ID,
		LeaseID:       lease.LeaseID,
		FencingToken:  lease.FencingToken,
		Status:        execution.ResultCompleted,
		ResultDigest:  integrationDigestA,
		Output:        map[string]any{"verdict": "approved"},
	}
}

type persistedTaskSnapshot struct {
	Data           string
	State          execution.TaskState
	LeaseExpiresAt time.Time
}

func postgresTaskSnapshot(t *testing.T, database *sql.DB, taskID string) persistedTaskSnapshot {
	t.Helper()
	var snapshot persistedTaskSnapshot
	if err := database.QueryRowContext(t.Context(), `
 select row_to_json(t)::text,state,lease_expires_at
 from execution_task t where task_id=$1
 `, taskID).Scan(&snapshot.Data, &snapshot.State, &snapshot.LeaseExpiresAt); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func postgresReportWorking(t *testing.T, service *execution.Service, report execution.ResultReport) {
	t.Helper()
	if err := service.ReportProgress(t.Context(), execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion,
		Kind:          execution.ProgressEventKind,
		TaskID:        report.TaskID,
		LeaseID:       report.LeaseID,
		FencingToken:  report.FencingToken,
		Sequence:      1,
		Phase:         "working",
		Message:       "working",
		ObservedAt:    time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
}

func postgresTerminalProgress(t *testing.T, service *execution.Service, report execution.ResultReport) error {
	t.Helper()
	return service.ReportProgress(t.Context(), execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion,
		Kind:          execution.ProgressEventKind,
		TaskID:        report.TaskID,
		LeaseID:       report.LeaseID,
		FencingToken:  report.FencingToken,
		Sequence:      2,
		Phase:         "working",
		Message:       "working",
		ObservedAt:    time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC),
	})
}
