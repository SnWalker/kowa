package executiontest

import (
	"errors"
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
)

func cancellationRecovery(t *testing.T, newEnv func(*testing.T) CurrentRoundEnv) {
	t.Helper()
	for _, kind := range []string{"retry", "resume", "rerun", "revise"} {
		for _, mode := range []string{"canceling", "canceled", "normal"} {
			t.Run("recovery/"+kind+"/"+mode, func(t *testing.T) {
				f := newRoundFixture(t, newEnv(t))
				lease := f.lease(t)
				report := roundResult(lease, "failed")
				if kind == "resume" {
					report = cancellationResult(lease, "awaiting_input")
				}
				if mode == "canceling" {
					f.cancel(t, execution.RunCanceling)
				}
				accepted, err := f.service.ReportResult(t.Context(), report)
				appliedOnce := accepted.Applied && !accepted.Duplicate
				if err != nil || !appliedOnce {
					t.Fatalf("original result = %+v, %v", accepted, err)
				}
				if mode == "canceled" {
					f.cancel(t, execution.RunCanceled)
				}
				facts := f.env.Facts(t, f.runID)
				err = f.recoverCancellation(t, kind)
				if mode != "normal" {
					if !errors.Is(err, execution.ErrRetryNotAllowed) {
						t.Errorf(
							"%s after %s = %v, want retry not allowed",
							kind,
							mode,
							err,
						)
					}
					f.unchanged(t, facts)
					f.rejectDispatch(t)
					// Rejection cannot damage already accepted history.
					duplicate, duplicateErr := f.service.ReportResult(t.Context(), report)
					duplicateOnly := duplicate.Duplicate && !duplicate.Applied
					if duplicateErr != nil || !duplicateOnly {
						t.Errorf("accepted history = %+v, %v", duplicate, duplicateErr)
					}
					f.unchanged(t, facts)
					return
				}
				if err != nil {
					t.Fatalf("normal %s = %v", kind, err)
				}
				current := f.lease(t)
				if current.Task.ID != f.newTask {
					t.Fatalf("normal recovery dispatched wrong task: %+v", current)
				}
				wantInput := resultDigestB
				if kind == "retry" {
					wantInput = resultDigestA
				}
				if current.Task.InputDigest != wantInput {
					t.Errorf("recovery input = %s, want %s", current.Task.InputDigest, wantInput)
				}
				f.complete(t, current)
			})
		}
	}
}

func (f *roundFixture) recoverCancellation(t *testing.T, kind string) error {
	t.Helper()
	var err error
	switch kind {
	case "retry":
		_, err = f.service.RetryTask(t.Context(), f.oldTask, f.newTask)
	case "resume":
		_, err = f.service.ResumeTask(
			t.Context(),
			f.oldTask,
			f.newTask,
			"synthetic-answer",
			resultDigestB,
			nil,
		)
	case "rerun":
		_, err = f.rerun(t, false)
	case "revise":
		_, err = f.rerun(t, true)
	default:
		t.Fatalf("unknown recovery kind %q", kind)
	}
	return err
}
