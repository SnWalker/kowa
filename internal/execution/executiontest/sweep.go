package executiontest

import (
	"errors"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

// SweepExpiry applies the frozen M1 lease-sweep contract to each Store adapter:
// expiry is driven by the persisted lease deadline (never by heartbeats), expiry
// never rewrites accepted facts, and a CANCELING run finalizes only after its
// last leased attempt settles. Assertions are fixture-relative because one sweep
// pass is a global control-plane operation over every run in the store.
func SweepExpiry(t *testing.T, newEnv func(*testing.T) CurrentRoundEnv) {
	t.Helper()
	t.Run("expired_lease_becomes_expired", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		lease := f.lease(t)
		before := f.env.Facts(t, f.runID)

		f.now = lease.ExpiresAt.Add(-time.Second)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatal(err)
		}
		if state := f.env.TaskState(t, lease.Task.ID); state != execution.TaskLeased {
			t.Fatalf("task before expiry = %s, want LEASED", state)
		}
		f.unchanged(t, before)

		f.now = lease.ExpiresAt
		result, err := f.service.SweepExpiredLeases(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if result.ExpiredTasks < 1 {
			t.Fatalf("sweep at expiry = %+v, want at least this fixture's expired task", result)
		}
		if state := f.env.TaskState(t, lease.Task.ID); state != execution.TaskExpired {
			t.Fatalf("task at expiry = %s, want EXPIRED", state)
		}
		// Expiry isolates the control-plane write-back but does not invent a
		// business decision: the run stays ACTIVE and the node is not decided
		// until an authorized recovery settles the round.
		view := f.view(t)
		if view.State != execution.RunActive {
			t.Fatalf("run after expiry = %+v, want ACTIVE", view)
		}
		if len(view.Nodes) != 1 || view.Nodes[0].State != execution.NodeRunning ||
			view.Nodes[0].Verdict != "" || len(view.Nodes[0].Output) != 0 {
			t.Fatalf("node after expiry = %+v, want running without decision", view.Nodes)
		}

		facts := f.env.Facts(t, f.runID)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.unchanged(t, facts)
		if state := f.env.TaskState(t, lease.Task.ID); state != execution.TaskExpired {
			t.Fatalf("task after repeated sweep = %s, want EXPIRED", state)
		}

		acceptance, err := f.service.ReportResult(t.Context(), roundResult(lease, "completed"))
		if !errors.Is(err, execution.ErrStaleLease) || acceptance.Applied || acceptance.Duplicate {
			t.Fatalf("late report after expiry = %+v, %v, want stale lease", acceptance, err)
		}
		f.unchanged(t, facts)
	})
	t.Run("queued_task_untouched", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		before := f.env.Facts(t, f.runID)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatal(err)
		}
		if state := f.env.TaskState(t, f.oldTask); state != execution.TaskQueued {
			t.Fatalf("queued task = %s, want QUEUED", state)
		}
		f.unchanged(t, before)
	})
	t.Run("accepted_result_untouched", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		lease := f.lease(t)
		f.complete(t, lease)
		before := f.env.Facts(t, f.runID)
		f.now = lease.ExpiresAt.Add(time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatal(err)
		}
		if state := f.env.TaskState(t, lease.Task.ID); state != execution.TaskCompleted {
			t.Fatalf("accepted task = %s, want COMPLETED", state)
		}
		f.unchanged(t, before)
	})
	t.Run("canceling_finalizes_only_when_idle", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		lease := f.lease(t)
		f.cancel(t, execution.RunCanceling)
		f.rejectDispatch(t)

		f.now = lease.ExpiresAt.Add(-time.Second)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatal(err)
		}
		if state := f.view(t).State; state != execution.RunCanceling {
			t.Fatalf("run while leased = %s, want CANCELING", state)
		}
		if state := f.env.TaskState(t, lease.Task.ID); state != execution.TaskLeased {
			t.Fatalf("leased task while canceling = %s, want LEASED", state)
		}

		f.now = lease.ExpiresAt
		result, err := f.service.SweepExpiredLeases(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if result.ExpiredTasks < 1 || result.FinalizedRuns < 1 {
			t.Fatalf("sweep at expiry = %+v, want expiry and finalization", result)
		}
		if state := f.env.TaskState(t, lease.Task.ID); state != execution.TaskExpired {
			t.Fatalf("task at expiry = %s, want EXPIRED", state)
		}
		view := f.view(t)
		if view.State != execution.RunCanceled {
			t.Fatalf("run after finalization = %+v, want CANCELED", view)
		}
		before := f.env.Facts(t, f.runID)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.unchanged(t, before)
	})
	t.Run("canceling_finalizes_after_settled_report", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		lease := f.lease(t)
		f.cancel(t, execution.RunCanceling)
		report := cancellationResult(lease, "failed")
		report.Error = &execution.TaskError{Code: "canceled", Message: "runner canceled the attempt", Retryable: false}
		acceptance, err := f.service.ReportResult(t.Context(), report)
		if err != nil || !acceptance.Applied || acceptance.Duplicate {
			t.Fatalf("canceled report = %+v, %v", acceptance, err)
		}
		// The cancellation intent survives the settled report; only the sweep finalizes.
		if state := f.view(t).State; state != execution.RunCanceling {
			t.Fatalf("run after canceled report = %s, want CANCELING", state)
		}
		result, err := f.service.SweepExpiredLeases(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if result.FinalizedRuns < 1 {
			t.Fatalf("sweep after settled report = %+v, want finalization", result)
		}
		if state := f.view(t).State; state != execution.RunCanceled {
			t.Fatalf("run after finalization = %s, want CANCELED", state)
		}
	})
}

// TaskStatusRead applies the frozen minimal read projection used by transport
// queries and restart investigation: identity before dispatch, live lease
// identity while in flight, and accepted-result visibility after settlement.
func TaskStatusRead(t *testing.T, newEnv func(*testing.T) CurrentRoundEnv) {
	t.Helper()
	f := newRoundFixture(t, newEnv(t))
	if _, err := f.service.TaskStatus(t.Context(), ""); !errors.Is(err, execution.ErrInvalidInput) {
		t.Fatalf("empty id = %v, want invalid input", err)
	}
	if _, err := f.service.TaskStatus(t.Context(), "missing-task"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("unknown id = %v, want not found", err)
	}
	queued, err := f.service.TaskStatus(t.Context(), f.oldTask)
	if err != nil {
		t.Fatal(err)
	}
	if queued.State != execution.TaskQueued || queued.ResultAccepted ||
		queued.LeaseID != "" || queued.RuntimeID != "" || !queued.LeaseExpiresAt.IsZero() {
		t.Fatalf("queued status = %+v", queued)
	}
	lease := f.lease(t)
	leased, err := f.service.TaskStatus(t.Context(), lease.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if leased.State != execution.TaskLeased || leased.ResultAccepted ||
		leased.RuntimeID != f.runtimeID || leased.RuntimeEpoch != "epoch-1" ||
		leased.LeaseID != lease.LeaseID || leased.FencingToken != lease.FencingToken ||
		!leased.LeaseExpiresAt.Equal(lease.ExpiresAt) {
		t.Fatalf("leased status = %+v", leased)
	}
	f.complete(t, lease)
	completed, err := f.service.TaskStatus(t.Context(), lease.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != execution.TaskCompleted || !completed.ResultAccepted {
		t.Fatalf("completed status = %+v", completed)
	}
	f.now = lease.ExpiresAt.Add(time.Hour)
	if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := f.service.TaskStatus(t.Context(), lease.Task.ID)
	if err != nil || after.State != execution.TaskCompleted || !after.ResultAccepted {
		t.Fatalf("settled status after sweep = %+v, %v", after, err)
	}
}
