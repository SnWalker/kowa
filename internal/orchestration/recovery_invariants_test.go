package orchestration_test

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/workflow"
	"github.com/SnWalker/kowa/internal/workitem"
)

// These tests pin the recovery and budget invariants the stage contract froze:
// a bounded automatic revision region that really re-reviews, a Retry that keeps
// its frozen input, a Rerun that rebuilds the affected downstream, one single
// advancing run per work item, and a reconciler that never reports a re-read as
// progress.

// TestAutomaticRevisionRerunsTheReview drives one automatic revision through to
// a second review: the flow may not stall while the reviewing node stays on its
// first, already decided round.
func TestAutomaticRevisionRerunsTheReview(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)

	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "needs_revision")
	// The revision target runs again, and the review that asked for it must be
	// materialised again instead of leaving the run stalled.
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	reviewRound := f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "approved")
	if reviewRound.iteration != 2 {
		t.Fatalf("the review ran at iteration %d, want a second review round", reviewRound.iteration)
	}
}

// TestRevisionBudgetExhaustsToHumanHandling proves the automatic budget is
// bounded and that its exhaustion hands the region to a human instead of
// stalling silently or resetting the counter.
func TestRevisionBudgetExhaustsToHumanHandling(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	// Every review asks for another revision until the automatic budget is spent.
	for round := 0; round < fixedflow.MaxAutoRevisions; round++ {
		f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "needs_revision")
		f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	}
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "needs_revision")
	f.advance(t, result.Admission.RunID, 8)

	escalation := f.openHumanTask(t, result.Admission.RunID, orchestration.HumanTaskRevisionEscalation)
	f.answer(t, escalation, "approved", map[string]string{"feedback": "grant one more round"})
	// The grant reopens the region: the target runs once more and the review
	// that asked for it reviews the new deliverable.
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "approved")
	if iteration := f.currentIteration(t, result.Admission.RunID, fixedflow.NodePlanReview); iteration <= fixedflow.MaxAutoRevisions+1 {
		t.Fatalf("the human grant did not reopen the region: plan_review iteration %d", iteration)
	}
}

// TestRetryKeepsItsFrozenBinding proves a Retry keeps the effective input: the
// new attempt is leasable *and* its frozen input read succeeds with the very
// same digest, and a second retry is a new attempt rather than a conflict.
func TestRetryKeepsItsFrozenBinding(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.advance(t, result.Admission.RunID, 4)
	plan := f.leaseCapability(t, "plan.create")
	original, err := f.store.Binding(t.Context(), plan.Task.ID)
	if err != nil {
		t.Fatalf("read the original binding: %v", err)
	}
	f.failAttempt(t, plan, result.Admission.RunID)

	first := f.recoverRetry(t, result.Admission.RunID, fixedflow.NodePlan)
	f.advance(t, result.Admission.RunID, 4)
	retried := f.leaseCapability(t, "plan.create")
	if retried.Task.ID == plan.Task.ID {
		t.Fatal("the retry reused the failed attempt identity")
	}
	binding, err := f.store.Binding(t.Context(), retried.Task.ID)
	if err != nil {
		t.Fatalf("the retried attempt has no frozen binding: %v", err)
	}
	if binding.InputDigest != original.InputDigest || binding.NodeRunID != original.NodeRunID ||
		!binding.DeadlineAt.Equal(original.DeadlineAt) {
		t.Fatalf("the retry changed the frozen input: %+v (was %+v)", binding, original)
	}
	manifest, err := f.service.TaskInput(t.Context(), retried.Task.ID)
	if err != nil {
		t.Fatalf("the retried attempt cannot read its frozen input: %v", err)
	}
	if manifest.BundleDigest != original.InputDigest {
		t.Fatalf("the served bundle digest %s differs from the frozen %s",
			manifest.BundleDigest, original.InputDigest)
	}
	_ = first

	// A second retry after another failure is a new attempt, not a conflict.
	f.failAttempt(t, retried, result.Admission.RunID)
	f.recoverRetry(t, result.Admission.RunID, fixedflow.NodePlan)
	f.advance(t, result.Admission.RunID, 4)
	second := f.leaseCapability(t, "plan.create")
	if second.Task.ID == retried.Task.ID || second.Task.Attempt <= retried.Task.Attempt {
		t.Fatalf("the second retry did not create a new attempt: %+v", second.Task)
	}
}

// TestRerunRebuildsAffectedDownstream proves a rerun rebuilds the nodes the
// rerun affects: a decision taken on the superseded round may not keep gating
// the flow, and the human gate is asked again for the new deliverable.
func TestRerunRebuildsAffectedDownstream(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "approved")
	before := f.currentIteration(t, result.Admission.RunID, fixedflow.NodePlanApproval)
	requestsBefore := f.humanRequestCount(t, result.Admission.RunID, fixedflow.HumanTaskPlanApproval)

	f.recoverRerun(t, result.Admission.RunID, fixedflow.NodePlan)
	// The rebuilt plan is reviewed again, and only then is the human gate asked
	// for the new deliverable.
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "approved")
	f.advance(t, result.Admission.RunID, 12)

	if iteration := f.currentIteration(t, result.Admission.RunID, fixedflow.NodePlanApproval); iteration <= before {
		t.Fatalf("the approval round stayed at %d after the rerun", iteration)
	}
	requestsAfter := f.humanRequestCount(t, result.Admission.RunID, fixedflow.HumanTaskPlanApproval)
	if requestsAfter <= requestsBefore {
		t.Fatalf("the superseded plan kept its approval: %d requests before and after", requestsBefore)
	}
}

// TestRefusedDuplicateAdmissionLeavesNoAdvancingRun proves admission uniqueness:
// a refused second admission may not leave behind a run the reconciler keeps
// advancing.
func TestRefusedDuplicateAdmissionLeavesNoAdvancingRun(t *testing.T) {
	f := newAdvanceFixture(t)
	item := f.createWorkItem(t)
	first, err := f.service.Admit(t.Context(), orchestration.AdmitCommand{
		WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
		RequestKey: "admit-first", ExpectedVersion: item.Version, Kind: workitem.BindingInitial,
	})
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	item, err = f.workitems.Get(t.Context(), f.workspaceID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Admit(t.Context(), orchestration.AdmitCommand{
		WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
		RequestKey: "admit-second", ExpectedVersion: item.Version, Kind: workitem.BindingInitial,
	}); err == nil {
		t.Fatal("a second admission of the same work item was accepted")
	}
	f.answerKnowledge(t, first)
	f.advance(t, first.Admission.RunID, 8)

	active, err := f.service.ActiveRunIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0] != first.Admission.RunID {
		t.Fatalf("refused admission left advancing runs behind: %v (want only %s)",
			active, first.Admission.RunID)
	}
}

// TestAdvanceOverAQueuedAttemptIsNotProgress proves a pass that only re-reads an
// existing attempt does not keep dispatching: waiting for a Runner must not
// spin the reconciler.
func TestAdvanceOverAQueuedAttemptIsNotProgress(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.advance(t, result.Admission.RunID, 8)
	f.counter.reset()
	if err := f.service.Advance(t.Context(), result.Admission.RunID); err != nil {
		t.Fatalf("advance over the queued attempt: %v", err)
	}
	if attempts := f.counter.dispatchAttempts(); attempts > 1 {
		t.Fatalf("one pass over a queued attempt made %d dispatch attempts", attempts)
	}
}

// TestRunDetailReportsAttemptsAndHistory pins the frozen read promise: the run
// detail carries every materialised round with its attempts, not only the
// current round.
func TestRunDetailReportsAttemptsAndHistory(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.create", "approved")
	f.completeCapabilityWithVerdict(t, result.Admission.RunID, "plan.review", "approved")
	item, err := f.workitems.Get(t.Context(), f.workspaceID, result.Admission.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.service.RunDetail(t.Context(), f.workspaceID, item.ID, result.Admission.RunID)
	if err != nil {
		t.Fatalf("read the run detail: %v", err)
	}
	attempts := 0
	decided := 0
	for _, node := range detail.Nodes {
		if node.Attempts == nil {
			t.Fatalf("node %s reports a null attempt list", node.NodeID)
		}
		attempts += len(node.Attempts)
		if node.State == string(execution.NodeDecided) {
			decided++
		}
	}
	if attempts < 2 || decided < 3 {
		t.Fatalf("the run detail reports %d attempts and %d decided rounds across %d nodes",
			attempts, decided, len(detail.Nodes))
	}
	for _, node := range detail.Nodes {
		if node.NodeID == "plan" {
			if node.InputDigest == "" || node.Verdict == "" {
				t.Fatalf("the plan round lost its frozen input or verdict: %+v", node)
			}
			for _, attempt := range node.Attempts {
				if attempt.InputDigest != node.InputDigest {
					t.Fatalf("attempt %s reads %s but the round is frozen on %s",
						attempt.TaskID, attempt.InputDigest, node.InputDigest)
				}
			}
		}
	}
	if detail.Watermark.ObservedAt.IsZero() {
		t.Fatal("the run detail carries no observation watermark")
	}
}

// TestRetryReplaysAndSurvivesRestartAndConcurrency proves the retry command is
// consumed exactly once: the identical command replays its recorded outcome, a
// restart keeps the persisted attempt chain, and two concurrent requests with
// different keys create exactly one new attempt.
func TestRetryReplaysAndSurvivesRestartAndConcurrency(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.advance(t, result.Admission.RunID, 4)
	plan := f.leaseCapability(t, "plan.create")
	f.failAttempt(t, plan, result.Admission.RunID)

	command := orchestration.RecoveryCommand{
		WorkspaceID: f.workspaceID, WorkItemID: result.Admission.WorkItemID,
		RunID: result.Admission.RunID, Kind: orchestration.RecoveryRetry, NodeID: fixedflow.NodePlan,
		ExpectedVersion: 1, RequestKey: "retry-replay", ActorID: f.actorID,
	}
	first := f.recover(t, command)
	// The identical command answers from its record: the same outcome, and no
	// second attempt appears behind it.
	replay := f.recover(t, command)
	if replay.Detail != first.Detail || replay.Kind != first.Kind || !replay.Applied ||
		!replay.RecordedAt.Equal(first.RecordedAt) {
		t.Fatalf("the identical retry = %+v, want the recorded outcome %+v", replay, first)
	}
	if attempts := f.roundAttempts(t, result.Admission.RunID, fixedflow.NodePlan); attempts != 2 {
		t.Fatalf("the replayed retry created attempt %d of the round", attempts)
	}

	// A restart only changes the process, never the persisted chain.
	f.restart(t)
	retried := f.leaseCapability(t, "plan.create")
	f.failAttempt(t, retried, result.Admission.RunID)
	f.recoverRetry(t, result.Admission.RunID, fixedflow.NodePlan)
	f.advance(t, result.Admission.RunID, 4)
	if attempts := f.roundAttempts(t, result.Admission.RunID, fixedflow.NodePlan); attempts != 3 {
		t.Fatalf("the retry after the restart did not open a new attempt: %d", attempts)
	}

	// Two concurrent retries with different keys consume one intent: exactly
	// one new attempt exists afterwards.
	latest := f.leaseCapability(t, "plan.create")
	f.failAttempt(t, latest, result.Admission.RunID)
	before := f.roundAttempts(t, result.Admission.RunID, fixedflow.NodePlan)
	outcomes := make([]error, 2)
	var wait sync.WaitGroup
	for index := range outcomes {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, outcomes[index] = f.service.Recover(t.Context(), orchestration.RecoveryCommand{
				WorkspaceID: f.workspaceID, WorkItemID: result.Admission.WorkItemID,
				RunID: result.Admission.RunID, Kind: orchestration.RecoveryRetry, NodeID: fixedflow.NodePlan,
				ExpectedVersion: 1, ActorID: f.actorID,
				RequestKey: "retry-concurrent-" + strconv.Itoa(index),
			})
		}(index)
	}
	wait.Wait()
	applied := 0
	for _, err := range outcomes {
		if err == nil {
			applied++
		}
	}
	if applied == 0 {
		t.Fatalf("neither concurrent retry was applied: %v", outcomes)
	}
	if attempts := f.roundAttempts(t, result.Admission.RunID, fixedflow.NodePlan); attempts != before+1 {
		t.Fatalf("the concurrent retries created %d new attempts, want exactly 1",
			attempts-before)
	}
}

// TestRetryIsRefusedUntilTheAttemptFailed proves the refusal path is accurate
// and that a legal retry still succeeds after it.
func TestRetryIsRefusedUntilTheAttemptFailed(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.advance(t, result.Admission.RunID, 4)
	plan := f.leaseCapability(t, "plan.create")
	// The attempt is leased: retrying now is refused with its true state.
	_, err := f.service.Recover(t.Context(), orchestration.RecoveryCommand{
		WorkspaceID: f.workspaceID, WorkItemID: result.Admission.WorkItemID,
		RunID: result.Admission.RunID, Kind: orchestration.RecoveryRetry, NodeID: fixedflow.NodePlan,
		ExpectedVersion: 1, RequestKey: "retry-too-early", ActorID: f.actorID,
	})
	if !errors.Is(err, orchestration.ErrPolicyBlocked) || !strings.Contains(err.Error(), string(execution.TaskLeased)) {
		t.Fatalf("the early retry = %v, want a policy refusal naming the attempt state", err)
	}
	// After the failure the same intent succeeds.
	f.failAttempt(t, plan, result.Admission.RunID)
	f.recoverRetry(t, result.Admission.RunID, fixedflow.NodePlan)
	f.advance(t, result.Admission.RunID, 4)
	f.leaseCapability(t, "plan.create")
}

// TestConcurrentAdmissionsLeaveOneAdvancingRun proves admission uniqueness
// under a real race: two different keys, exactly one advancing run; the loser
// leaves no advancing orphan and its run is withdrawn.
func TestConcurrentAdmissionsLeaveOneAdvancingRun(t *testing.T) {
	f := newAdvanceFixture(t)
	item := f.createWorkItem(t)
	type outcome struct {
		result orchestration.AdmissionResult
		err    error
	}
	outcomes := make([]outcome, 2)
	var wait sync.WaitGroup
	for index := range outcomes {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			outcomes[index].result, outcomes[index].err = f.service.Admit(t.Context(), orchestration.AdmitCommand{
				WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
				RequestKey: "admit-race-" + strconv.Itoa(index), ExpectedVersion: item.Version,
				Kind: workitem.BindingInitial,
			})
		}(index)
	}
	wait.Wait()
	winners, winner := 0, outcome{}
	for _, candidate := range outcomes {
		if candidate.err == nil {
			winners++
			winner = candidate
		}
	}
	if winners != 1 {
		t.Fatalf("the concurrent admissions produced %d winners: %+v", winners, outcomes)
	}
	active, err := f.service.ActiveRunIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("the refused admission left %d advancing runs: %v", len(active), active)
	}
	current, err := f.workitems.Get(t.Context(), f.workspaceID, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CurrentRunID != active[0] {
		t.Fatalf("the advancing run %s does not hold advancement authority (%s)", active[0], current.CurrentRunID)
	}
	// The winner is fully functional: the knowledge gate answers and the flow
	// keeps materialising its first node.
	f.answerKnowledge(t, winner.result)
	f.advance(t, winner.result.Admission.RunID, 4)
	f.leaseCapability(t, "plan.create")
}

// roundAttempts counts the persisted attempts of one node's current round.
func (f *advanceFixture) roundAttempts(t *testing.T, runID, nodeID string) int {
	t.Helper()
	view, err := f.execution.RunView(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	roundID := ""
	for _, node := range view.Nodes {
		if node.NodeID == nodeID {
			roundID = node.ID
		}
	}
	if roundID == "" {
		t.Fatalf("node %s has no round", nodeID)
	}
	attempts, err := f.execution.RunAttempts(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, attempt := range attempts {
		if attempt.NodeRunID == roundID {
			count++
		}
	}
	return count
}

// TestRefusedKnowledgeConfirmationIsAskedAgain proves a refused selection is
// re-asked on a new round (the recommendation names the pending request), and
// the confirmed selection then releases the flow.
func TestRefusedKnowledgeConfirmationIsAskedAgain(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answer(t, result.Recommendation.HumanTaskID, "rejected", nil)
	f.advance(t, result.Admission.RunID, 8)
	second := f.openHumanTask(t, result.Admission.RunID, orchestration.HumanTaskKnowledgeConfirmation)
	if second == result.Recommendation.HumanTaskID {
		t.Fatal("the refused confirmation was not asked again")
	}
	f.answer(t, second, "approved", map[string]string{"selection": knowledgeEntryPath})
	f.advance(t, result.Admission.RunID, 8)
	f.leaseCapability(t, "plan.create")
}

// ---------------------------------------------------------------------------
// fixture helpers
// ---------------------------------------------------------------------------

func (f *advanceFixture) admittedRun(t *testing.T) orchestration.AdmissionResult {
	t.Helper()
	item := f.createWorkItem(t)
	result, err := f.service.Admit(t.Context(), orchestration.AdmitCommand{
		WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
		RequestKey: "admit-" + item.ID, ExpectedVersion: item.Version, Kind: workitem.BindingInitial,
	})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	return result
}

func (f *advanceFixture) answerKnowledge(t *testing.T, result orchestration.AdmissionResult) {
	t.Helper()
	if result.Recommendation == nil || result.Recommendation.HumanTaskID == "" {
		t.Fatalf("admission produced no confirmation request: %+v", result.Recommendation)
	}
	f.answer(t, result.Recommendation.HumanTaskID, "approved", map[string]string{
		"selection": knowledgeEntryPath,
	})
}

func (f *advanceFixture) advance(t *testing.T, runID string, passes int) {
	t.Helper()
	for range passes {
		if err := f.service.Advance(t.Context(), runID); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}
}

type completedRound struct {
	iteration int
	verdict   string
}

// completeCapabilityWithVerdict leases the named capability, reports the given
// verdict, and returns the round it decided.
func (f *advanceFixture) completeCapabilityWithVerdict(
	t *testing.T,
	runID, capability, verdict string,
) completedRound {
	t.Helper()
	for pass := 0; pass < 60; pass++ {
		if err := f.service.Advance(t.Context(), runID); err != nil {
			t.Fatalf("advance: %v", err)
		}
		lease, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil {
			continue
		}
		if lease.Task.Capability.ID != capability {
			t.Fatalf("leased %s while waiting for %s", lease.Task.Capability.ID, capability)
		}
		output := map[string]any{"verdict": verdict, "summary": "scripted"}
		if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
			SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
			TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
			Status: execution.ResultCompleted, ResultDigest: artifact.Digest([]byte(capability + verdict)),
			Summary: "scripted", Output: output,
		}); err != nil {
			t.Fatalf("report the %s attempt: %v", capability, err)
		}
		if err := f.service.Advance(t.Context(), runID); err != nil {
			t.Fatalf("advance after %s: %v", capability, err)
		}
		return completedRound{iteration: f.currentIteration(t, runID, nodeOf(capability)), verdict: verdict}
	}
	t.Fatalf("%s never became leasable", capability)
	return completedRound{}
}

// leaseCapability leases the one attempt the reconciler dispatched.
func (f *advanceFixture) leaseCapability(t *testing.T, capability string) execution.TaskLease {
	t.Helper()
	lease, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("lease %s: %v", capability, err)
	}
	if lease.Task.Capability.ID != capability {
		t.Fatalf("leased %s while waiting for %s", lease.Task.Capability.ID, capability)
	}
	return lease
}

// failAttempt reports a retryable failure for one leased attempt.
func (f *advanceFixture) failAttempt(t *testing.T, lease execution.TaskLease, runID string) {
	t.Helper()
	if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultFailed, ResultDigest: artifact.Digest([]byte("failure")),
		Summary: "scripted failure",
		Error:   &execution.TaskError{Code: "PROVIDER_TIMEOUT", Message: "scripted", Retryable: true},
	}); err != nil {
		t.Fatalf("report the failure: %v", err)
	}
	f.advance(t, runID, 2)
}

func (f *advanceFixture) recoverRetry(t *testing.T, runID, nodeID string) orchestration.RunRecovery {
	t.Helper()
	// Every recovery command carries its own request key: a repeated key is a
	// replay of the recorded outcome, not a second action.
	f.recoveryCount++
	return f.recover(t, orchestration.RecoveryCommand{
		WorkspaceID: f.workspaceID, WorkItemID: f.workitemOf(t, runID), RunID: runID,
		Kind: orchestration.RecoveryRetry, NodeID: nodeID, ExpectedVersion: 1,
		RequestKey: "retry-" + nodeID + "-" + strconv.Itoa(f.recoveryCount), ActorID: f.actorID,
	})
}

func (f *advanceFixture) recoverRerun(t *testing.T, runID, nodeID string) orchestration.RunRecovery {
	t.Helper()
	f.recoveryCount++
	return f.recover(t, orchestration.RecoveryCommand{
		WorkspaceID: f.workspaceID, WorkItemID: f.workitemOf(t, runID), RunID: runID,
		Kind: orchestration.RecoveryRerun, NodeID: nodeID, ExpectedVersion: 1,
		RequestKey: "rerun-" + nodeID + "-" + strconv.Itoa(f.recoveryCount), ActorID: f.actorID,
	})
}

func (f *advanceFixture) recover(t *testing.T, command orchestration.RecoveryCommand) orchestration.RunRecovery {
	t.Helper()
	recovery, err := f.service.Recover(t.Context(), command)
	if err != nil {
		t.Fatalf("recover %s: %v", command.Kind, err)
	}
	if !recovery.Applied {
		t.Fatalf("recover %s was not applied", command.Kind)
	}
	return recovery
}

func (f *advanceFixture) workitemOf(t *testing.T, runID string) string {
	t.Helper()
	run, err := f.execution.Run(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	return run.WorkItemID
}

func (f *advanceFixture) currentIteration(t *testing.T, runID, nodeID string) int {
	t.Helper()
	view, err := f.execution.RunView(t.Context(), runID)
	if err != nil {
		t.Fatalf("run view: %v", err)
	}
	for _, node := range view.Nodes {
		if node.NodeID == nodeID {
			return node.Iteration
		}
	}
	return 0
}

func (f *advanceFixture) humanRequestCount(t *testing.T, runID, kind string) int {
	t.Helper()
	tasks, err := f.humans.List(t.Context(), f.workspaceID, false)
	if err != nil {
		t.Fatalf("list the human tasks: %v", err)
	}
	run, err := f.execution.Run(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, task := range tasks {
		if task.RunID == run.ID && task.Request.Kind == kind {
			count++
		}
	}
	return count
}

// nodeOf maps a capability back to the node that runs it.
func nodeOf(capability string) string {
	plan, err := fixedflow.Compile()
	if err != nil {
		panic(err)
	}
	for id, node := range plan.Nodes {
		if node.Type == workflow.NodeCapability && node.Capability.ID == capability {
			return id
		}
	}
	return ""
}
