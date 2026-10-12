package executiontest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
)

// CancellationIntent checks accepted reports and rejection isolation after a
// cancellation. Final cancellation cleanup belongs to the Runner lifecycle.
func CancellationIntent(t *testing.T, newEnv func(*testing.T) CurrentRoundEnv) {
	t.Helper()
	for _, running := range []bool{false, true} {
		state := "leased"
		if running {
			state = "running"
		}
		for _, kind := range []string{"failed", "completed", "awaiting_input"} {
			for _, cancel := range []bool{false, true} {
				mode := "active"
				if cancel {
					mode = "canceling"
				}
				t.Run(state+"/"+kind+"/"+mode, func(t *testing.T) {
					f := newRoundFixture(t, newEnv(t))
					lease := f.lease(t)
					if running {
						if err := f.progress(t, lease); err != nil {
							t.Fatal(err)
						}
					}
					if cancel {
						f.cancel(t, execution.RunCanceling)
						f.rejectDispatch(t)
					}
					before := f.view(t)
					report := cancellationResult(lease, kind)
					acceptance, err := f.service.ReportResult(t.Context(), report)
					appliedOnce := acceptance.Applied && !acceptance.Duplicate
					if err != nil || !appliedOnce {
						t.Fatalf("valid result = %+v, %v", acceptance, err)
					}
					after := f.view(t)
					wantState := before.State
					wantVersion := before.Version
					if !cancel && kind == "failed" {
						wantState = execution.RunPaused
						wantVersion++
					}
					if after.State != wantState || after.Version != wantVersion {
						t.Errorf(
							"run = %s v%d, want %s v%d",
							after.State,
							after.Version,
							wantState,
							wantVersion,
						)
					}
					wantTask := execution.TaskCompleted
					wantNode := execution.NodeDecided
					switch kind {
					case "failed":
						wantTask, wantNode = execution.TaskFailed, execution.NodeExecutionFailed
					case "awaiting_input":
						wantTask, wantNode = execution.TaskAwaitingInput, execution.NodeWaitingHuman
					}
					if got := f.env.TaskState(t, lease.Task.ID); got != wantTask {
						t.Errorf("task = %s, want %s", got, wantTask)
					}
					if len(after.Nodes) != 1 || after.Nodes[0].State != wantNode {
						t.Fatalf(
							"accepted node result = %+v, want %s",
							after.Nodes,
							wantNode,
						)
					}
					if cancel {
						f.rejectDispatch(t)
					}
					// Same accepted identity is idempotent even after expiry.
					facts := f.env.Facts(t, f.runID)
					f.now = lease.ExpiresAt
					duplicate, err := f.service.ReportResult(t.Context(), report)
					duplicateOnly := duplicate.Duplicate && !duplicate.Applied
					if err != nil || !duplicateOnly {
						t.Errorf("duplicate = %+v, %v", duplicate, err)
					}
					f.unchanged(t, facts)
					report.ResultDigest = resultDigestB
					if _, err := f.service.ReportResult(t.Context(), report); !errors.Is(err, execution.ErrResultConflict) {
						t.Errorf("conflicting accepted result = %v", err)
					}
					f.unchanged(t, facts)
				})
			}
		}
		for _, wrong := range []string{"lease", "fencing", "invalid_result"} {
			t.Run(state+"/rejected/"+wrong, func(t *testing.T) {
				f := newRoundFixture(t, newEnv(t))
				lease := f.lease(t)
				if running {
					if err := f.progress(t, lease); err != nil {
						t.Fatal(err)
					}
				}
				f.cancel(t, execution.RunCanceling)
				facts := f.env.Facts(t, f.runID)
				report := roundResult(lease, "failed")
				wantErr := execution.ErrStaleLease
				switch wrong {
				case "lease":
					report.LeaseID = "wrong"
				case "fencing":
					report.FencingToken++
				case "invalid_result":
					report.Error = nil
					wantErr = execution.ErrInvalidInput
				}
				acceptance, err := f.service.ReportResult(t.Context(), report)
				rejected := !acceptance.Applied && !acceptance.Duplicate
				if !errors.Is(err, wantErr) || !rejected {
					t.Errorf(
						"rejected result = %+v, %v; want %v",
						acceptance,
						err,
						wantErr,
					)
				}
				f.unchanged(t, facts)
				acceptance, err = f.service.ReportResult(t.Context(), roundResult(lease, "failed"))
				appliedOnce := acceptance.Applied && !acceptance.Duplicate
				if err != nil || !appliedOnce {
					t.Fatalf("valid result after rejection = %+v, %v", acceptance, err)
				}
				if view := f.view(t); view.State != execution.RunCanceling {
					t.Errorf("valid result erased cancellation: %+v", view)
				}
			})
		}
		t.Run(state+"/expired", func(t *testing.T) {
			f := newRoundFixture(t, newEnv(t))
			lease := f.lease(t)
			if running {
				if err := f.progress(t, lease); err != nil {
					t.Fatal(err)
				}
			}
			f.cancel(t, execution.RunCanceling)
			before := f.view(t)
			f.now = lease.ExpiresAt
			acceptance, err := f.service.ReportResult(t.Context(), roundResult(lease, "failed"))
			rejected := !acceptance.Applied && !acceptance.Duplicate
			if !errors.Is(err, execution.ErrStaleLease) || !rejected {
				t.Errorf("expired result = %+v, %v", acceptance, err)
			}
			if got := f.env.TaskState(t, lease.Task.ID); got != execution.TaskExpired {
				t.Errorf("expired task = %s", got)
			}
			if after := f.view(t); !reflect.DeepEqual(before, after) {
				t.Errorf("expiry changed run/node view: before=%+v after=%+v", before, after)
			}
			facts := f.env.Facts(t, f.runID)
			f.rejectLate(t, lease, "failed")
			f.unchanged(t, facts)
			f.rejectDispatch(t)
		})
	}
	t.Run("queued", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		f.cancel(t, execution.RunCanceled)
		if state := f.env.TaskState(t, f.oldTask); state != execution.TaskCanceled {
			t.Errorf("queued task after cancel = %s", state)
		}
		f.rejectDispatch(t)
	})
	cancellationRecovery(t, newEnv)
}

func cancellationResult(lease execution.TaskLease, kind string) execution.ResultReport {
	report := roundResult(lease, kind)
	if kind == "awaiting_input" {
		report.Status = execution.ResultAwaitingInput
		report.Output = nil
		report.InputRequest = map[string]any{"requestKey": "synthetic-input"}
	}
	return report
}

func (f *roundFixture) view(t *testing.T) execution.RunView {
	t.Helper()
	view, err := f.service.RunView(t.Context(), f.runID)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func (f *roundFixture) cancel(t *testing.T, want execution.RunState) {
	t.Helper()
	before := f.view(t)
	if err := f.service.CancelRun(t.Context(), f.runID, before.Version); err != nil {
		t.Fatal(err)
	}
	if after := f.view(t); after.State != want || after.Version != before.Version+1 {
		t.Fatalf(
			"cancel = %+v, want %s v%d",
			after,
			want,
			before.Version+1,
		)
	}
}

func (f *roundFixture) rejectDispatch(t *testing.T) {
	t.Helper()
	facts := f.env.Facts(t, f.runID)
	_, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: 1,
	})
	if !errors.Is(err, execution.ErrCapabilityUnavailable) {
		t.Errorf("dispatch after cancel = %v", err)
	}
	f.unchanged(t, facts)
}
