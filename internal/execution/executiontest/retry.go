package executiontest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

// RetryAttemptPort applies the shared invariants of the additive S06 retry
// operation (`RetryAttempt`) to each adapter: a settled-unusable or
// queued-but-expired attempt opens a new, leasable attempt under a fresh
// caller-frozen deadline; a live attempt is never replaced; identical retries
// replay and other content conflicts. The fixture clock is movable, so the
// deadline boundary is proven without waiting.
func RetryAttemptPort(t *testing.T, newEnv func(*testing.T) DispatchEnv) {
	t.Run("expired_attempts_open_a_leasable_new_attempt", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		// The attempt is leased, then its deadline passes: the sweep settles it.
		f.lease(t)
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		status, err := f.service.TaskStatus(t.Context(), f.taskID("prepare-task"))
		if err != nil || status.State != execution.TaskExpired {
			t.Fatalf("the leased attempt = %+v, %v, want EXPIRED", status, err)
		}
		next, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour)))
		if err != nil {
			t.Fatalf("retry attempt: %v", err)
		}
		if next.Attempt != 2 || next.AttemptKind != execution.AttemptRetry ||
			next.PreviousTaskID != f.taskID("prepare-task") || next.State != execution.TaskQueued {
			t.Fatalf("new attempt = %+v", next)
		}
		if !next.DeadlineAt.After(f.now) {
			t.Fatalf("the new deadline %s is not in the future", next.DeadlineAt)
		}
		if next.Capability.ID != "fixture.prepare" || next.Provider != f.provider ||
			next.InputDigest != dispatchDigestA || len(next.GitScopes) != 2 {
			t.Fatalf("the frozen intent was not carried over: %+v", next)
		}
		// The new attempt really is leasable.
		lease, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil || lease.Task.ID != next.ID {
			t.Fatalf("lease the retried attempt = %+v, %v", lease.Task, err)
		}
	})

	t.Run("a_queued_attempt_with_a_passed_deadline_is_replaced", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		// No runner ever leases it; the deadline passes while it is QUEUED.
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		}); err == nil {
			t.Fatal("the queued attempt with a passed deadline was leasable")
		}
		next, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour)))
		if err != nil {
			t.Fatalf("retry the expired queue entry: %v", err)
		}
		settled, err := f.service.TaskStatus(t.Context(), f.taskID("prepare-task"))
		if err != nil || settled.State != execution.TaskExpired {
			t.Fatalf("the replaced queue entry = %+v, %v, want EXPIRED", settled, err)
		}
		lease, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil || lease.Task.ID != next.ID {
			t.Fatalf("lease the replacement = %+v, %v", lease.Task, err)
		}
	})

	t.Run("live_attempts_are_never_replaced", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		// A queued attempt that is still leasable is not the recovery boundary.
		if _, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour))); !errors.Is(err, execution.ErrRetryNotAllowed) {
			t.Fatalf("retry of a leasable queue entry = %v, want retry-not-allowed", err)
		}
		// A leased attempt settles through the sweep first, never through retry.
		f.lease(t)
		if _, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour))); !errors.Is(err, execution.ErrRetryNotAllowed) {
			t.Fatalf("retry of a leased attempt = %v, want retry-not-allowed", err)
		}
		// A deadline that is not in the future is refused: a new dispatch
		// intent must be leasable.
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if _, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(-time.Minute))); !errors.Is(err, execution.ErrInvalidInput) {
			t.Fatalf("retry with a passed deadline = %v, want invalid input", err)
		}
		if _, err := f.service.TaskStatus(t.Context(), f.taskID("prepare-task-2")); !errors.Is(err, execution.ErrNotFound) {
			t.Fatalf("the refused retry still created facts: %v", err)
		}
		// The legal retry after the refusals succeeds.
		if _, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour))); err != nil {
			t.Fatalf("the legal retry after the refusals: %v", err)
		}
	})

	t.Run("identical_retries_replay_and_other_content_conflicts", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		f.lease(t)
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		command := f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour))
		first, err := f.service.RetryAttempt(t.Context(), command)
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		// The identical command replays even after the run moved on and the
		// deadline passed again.
		if _, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		}); err != nil {
			t.Fatalf("lease: %v", err)
		}
		f.now = f.now.Add(3 * time.Hour)
		replay, err := f.service.RetryAttempt(t.Context(), command)
		if err != nil || replay.ID != first.ID ||
			!execution.SameInstant(replay.DeadlineAt, first.DeadlineAt) {
			t.Fatalf("identical replay = %+v, %v (want %s)", replay, err, first.DeadlineAt)
		}
		other := command
		other.DeadlineAt = command.DeadlineAt.Add(time.Minute)
		if _, err := f.service.RetryAttempt(t.Context(), other); !errors.Is(err, execution.ErrVersionConflict) {
			t.Fatalf("other content under the same identity = %v, want version conflict", err)
		}
	})

	t.Run("concurrent_identical_commands_replay_one_attempt", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		f.lease(t)
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		command := f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour))
		type outcome struct {
			task execution.Task
			err  error
		}
		outcomes := make([]outcome, 2)
		var wait sync.WaitGroup
		for index := range outcomes {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				outcomes[index].task, outcomes[index].err = f.service.RetryAttempt(t.Context(), command)
			}(index)
		}
		wait.Wait()
		for _, candidate := range outcomes {
			if candidate.err != nil || candidate.task.ID != f.taskID("prepare-task-2") {
				t.Fatalf("concurrent retry = %+v, %v", candidate.task, candidate.err)
			}
		}
		// Exactly one attempt exists for the identity.
		if _, err := f.service.TaskStatus(t.Context(), f.taskID("prepare-task-2")); err != nil {
			t.Fatalf("the new attempt is missing: %v", err)
		}
	})

	t.Run("two_consecutive_expirations_recover_in_kind", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		f.lease(t)
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if _, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task", "prepare-task-2", f.now.Add(time.Hour))); err != nil {
			t.Fatalf("first retry: %v", err)
		}
		f.lease(t)
		f.now = f.now.Add(2 * time.Hour)
		if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		third, err := f.service.RetryAttempt(t.Context(), f.retryCommand(t, "prepare-task-2", "prepare-task-3", f.now.Add(time.Hour)))
		if err != nil {
			t.Fatalf("second retry: %v", err)
		}
		if third.Attempt != 3 {
			t.Fatalf("the second retry = attempt %d, want 3", third.Attempt)
		}
		lease, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil || lease.Task.ID != third.ID {
			t.Fatalf("lease the third attempt = %+v, %v", lease.Task, err)
		}
	})
}

// retryCommand builds the shared retry command of the fixture.
func (f *dispatchFixture) retryCommand(t *testing.T, sourceTask, newTask string, deadline time.Time) execution.RetryAttemptCommand {
	t.Helper()
	return execution.RetryAttemptCommand{
		RunID: f.runID, NodeRunID: f.nodeRunID("prepare"),
		SourceTaskID: f.taskID(sourceTask), NewTaskID: f.taskID(newTask),
		DeadlineAt: deadline,
	}
}
