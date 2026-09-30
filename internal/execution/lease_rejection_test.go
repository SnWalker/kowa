package execution

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRejectedReportsPreserveCurrentTask(t *testing.T) {
	t.Parallel()
	for _, running := range []bool{false, true} {
		for _, kind := range []string{"lease", "fencing", "shape", "progress_lease", "progress_fencing"} {
			name := kind + "/leased"
			if running {
				name = kind + "/running"
			}
			t.Run(name, func(t *testing.T) {
				store, service, _, report := leaseRejectionFixture(t)
				if running {
					reportWorking(t, service, report)
				}
				before := store.tasks[report.TaskID]
				viewBefore, err := service.RunView(t.Context(), "run-1")
				if err != nil {
					t.Fatal(err)
				}
				wrong := report
				wantErr := ErrStaleLease
				switch kind {
				case "lease", "progress_lease":
					wrong.LeaseID = "wrong-lease"
				case "fencing", "progress_fencing":
					wrong.FencingToken++
				case "shape":
					wrong.Output = nil
					wantErr = ErrInvalidInput
				}
				if kind == "progress_lease" || kind == "progress_fencing" {
					err = service.ReportProgress(t.Context(), ProgressReport{
						SchemaVersion: SchemaVersion,
						Kind:          ProgressEventKind,
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
				if !reflect.DeepEqual(store.tasks[report.TaskID], before) {
					t.Errorf("rejection changed current task: %#v -> %#v", before, store.tasks[report.TaskID])
				}
				viewAfter, err := service.RunView(t.Context(), "run-1")
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

func TestLeaseExpiryAndReportIdentity(t *testing.T) {
	t.Parallel()
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			store, service, now, report := leaseRejectionFixture(t)
			*now = store.tasks[report.TaskID].LeaseExpiresAt.Add(offset)
			accepted, err := service.ReportResult(t.Context(), report)
			if offset < 0 {
				if err != nil || !accepted.Applied {
					t.Fatalf("before expiry: %#v, %v", accepted, err)
				}
				return
			}
			if !errors.Is(err, ErrStaleLease) || store.tasks[report.TaskID].State != TaskExpired {
				t.Fatalf("at/after expiry: state=%s err=%v", store.tasks[report.TaskID].State, err)
			}
			if _, err := service.RetryTask(t.Context(), report.TaskID, "task-2"); err != nil {
				t.Fatal(err)
			}
			lease, err := service.LeaseTask(t.Context(), LeaseRequest{
				RuntimeID:     "runtime-1",
				RuntimeEpoch:  "epoch-1",
				LeaseDuration: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			before := store.tasks[lease.Task.ID]
			viewBefore, err := service.RunView(t.Context(), "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReportResult(t.Context(), report); !errors.Is(err, ErrStaleLease) {
				t.Fatalf("late old report: %v", err)
			}
			viewAfter, err := service.RunView(t.Context(), "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, store.tasks[lease.Task.ID]) || !reflect.DeepEqual(viewBefore, viewAfter) {
				t.Fatal("late report changed retry")
			}
			report.TaskID, report.LeaseID, report.FencingToken = lease.Task.ID, lease.LeaseID, lease.FencingToken
			if accepted, err := service.ReportResult(t.Context(), report); err != nil || !accepted.Applied {
				t.Fatalf("retry result: %#v, %v", accepted, err)
			}
		})
	}
	t.Run("running_expiry", func(t *testing.T) {
		store, service, now, report := leaseRejectionFixture(t)
		reportWorking(t, service, report)
		*now = store.tasks[report.TaskID].LeaseExpiresAt
		if _, err := service.ReportResult(t.Context(), report); !errors.Is(err, ErrStaleLease) {
			t.Fatalf("running expiry: %v", err)
		}
		if store.tasks[report.TaskID].State != TaskExpired {
			t.Fatal("running task not expired")
		}
	})
	t.Run("duplicate_and_conflict", func(t *testing.T) {
		store, service, now, report := leaseRejectionFixture(t)
		if _, err := service.ReportResult(t.Context(), report); err != nil {
			t.Fatal(err)
		}
		if err := terminalProgress(t, service, report); !errors.Is(err, ErrStaleLease) {
			t.Fatalf("progress after completion: %v", err)
		}
		*now = now.Add(2 * time.Minute)
		before := store.tasks[report.TaskID]
		viewBefore, err := service.RunView(t.Context(), "run-1")
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
			if _, err := service.ReportResult(t.Context(), wrong); !errors.Is(err, ErrStaleLease) {
				t.Fatalf("wrong duplicate identity: %v", err)
			}
		}
		report.ResultDigest = digestB
		if _, err := service.ReportResult(t.Context(), report); !errors.Is(err, ErrResultConflict) {
			t.Fatalf("conflict: %v", err)
		}
		viewAfter, err := service.RunView(t.Context(), "run-1")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, store.tasks[report.TaskID]) || !reflect.DeepEqual(viewBefore, viewAfter) {
			t.Fatal("duplicate/conflict changed accepted facts")
		}
	})
}

func leaseRejectionFixture(t *testing.T) (*MemoryStore, *Service, *time.Time, ResultReport) {
	t.Helper()
	now := time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := NewService(store, func() time.Time { return now })
	registerTestRuntime(t, service)
	createTestRun(t, service)
	lease, err := service.LeaseTask(t.Context(), LeaseRequest{
		RuntimeID:     "runtime-1",
		RuntimeEpoch:  "epoch-1",
		LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, service, &now, ResultReport{
		SchemaVersion: SchemaVersion,
		Kind:          TaskResultKind,
		TaskID:        lease.Task.ID,
		LeaseID:       lease.LeaseID,
		FencingToken:  lease.FencingToken,
		Status:        ResultCompleted,
		ResultDigest:  digestA,
		Output:        map[string]any{"verdict": "approved"},
	}
}

func reportWorking(t *testing.T, service *Service, report ResultReport) {
	t.Helper()
	if err := service.ReportProgress(t.Context(), ProgressReport{
		SchemaVersion: SchemaVersion,
		Kind:          ProgressEventKind,
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

func terminalProgress(t *testing.T, service *Service, report ResultReport) error {
	t.Helper()
	return service.ReportProgress(t.Context(), ProgressReport{
		SchemaVersion: SchemaVersion,
		Kind:          ProgressEventKind,
		TaskID:        report.TaskID,
		LeaseID:       report.LeaseID,
		FencingToken:  report.FencingToken,
		Sequence:      2,
		Phase:         "working",
		Message:       "working",
		ObservedAt:    time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC),
	})
}
