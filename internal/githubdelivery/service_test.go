package githubdelivery_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/workitem"
)

const (
	projectRepo   = "1401511579"
	knowledgeRepo = "1413481670"
	machineUserID = "135722937"
	headOID       = "de62a4324cad57908376f8c5f3e14bd3bd3dab95"
	otherOID      = "19a68257314d32152f520c698b6b38b00cb6f17e"
	mergeOID      = "ca6c6ec8446ec687213d58064a7fd8363050321a"
)

type fakeRuns struct {
	view execution.RunView
	run  execution.Run
	err  error
}

func (f fakeRuns) RunView(context.Context, string) (execution.RunView, error) {
	return f.view, f.err
}

func (f fakeRuns) Run(context.Context, string) (execution.Run, error) { return f.run, f.err }

type fakeWorkItems struct {
	item workitem.WorkItem
}

func (f fakeWorkItems) Get(context.Context, string, string) (workitem.WorkItem, error) {
	return f.item, nil
}

func (f fakeWorkItems) RunBindings(context.Context, string, string) ([]workitem.RunBinding, error) {
	return []workitem.RunBinding{{WorkItemID: f.item.ID, RunID: f.item.CurrentRunID, Kind: workitem.BindingInitial}}, nil
}

type fakeEvidence struct {
	rows map[string]evaluation.Verification
}

func (f fakeEvidence) Verification(_ context.Context, _, verificationID string) (evaluation.Verification, bool, error) {
	row, found := f.rows[verificationID]
	return row, found, nil
}

func (f fakeEvidence) VerificationsFor(context.Context, string, string) ([]evaluation.Verification, error) {
	rows := make([]evaluation.Verification, 0, len(f.rows))
	for _, row := range f.rows {
		rows = append(rows, row)
	}
	return rows, nil
}

func (f fakeEvidence) Closure(context.Context, string, string) (evaluation.ClosureRecord, bool, error) {
	return evaluation.ClosureRecord{}, false, nil
}

type fakeSettler struct {
	settled  []string
	expired  int
	lastKind string
}

func (f *fakeSettler) ResolveForObject(_ context.Context, _, objectKind, objectID, objectVersion, observationID, _ string) (bool, error) {
	f.settled = append(f.settled, objectKind+":"+objectID+"@"+objectVersion+"#"+observationID)
	f.lastKind = objectKind
	return true, nil
}

func (f *fakeSettler) ExpireBoundTo(_ context.Context, _, objectKind, objectID, exceptVersion, reason, actorID string) (int, error) {
	f.expired++
	f.lastKind = objectKind
	return 1, nil
}

type sequenceIDs struct{ next int }

func (s *sequenceIDs) New() (string, error) {
	s.next++
	return "op_" + strconv.Itoa(s.next), nil
}

func frozenWorkItem() workitem.WorkItem {
	return workitem.WorkItem{
		ID: "wi_1", WorkspaceID: "ws_1", Title: "Fix the failing test",
		TargetRepositoryID: projectRepo, State: workitem.StateOpen, Version: 3,
		CurrentRunID: "run_1", CreatedByUserID: "101",
	}
}

func frozenRun() execution.Run {
	return execution.Run{
		ID: "run_1", WorkspaceID: "ws_1", WorkItemID: "wi_1",
		StartedByGitHubUserID: "101", GitExecutionUserID: machineUserID,
		State: execution.RunActive, Version: 4,
	}
}

func newService(
	t *testing.T,
	facts githubdelivery.FactSource,
	evidence githubdelivery.EvidenceSource,
	settler githubdelivery.FactSettler,
) *githubdelivery.Service {
	t.Helper()
	return githubdelivery.NewService(
		githubdelivery.NewMemoryStore(), facts, evidence,
		fakeRuns{view: execution.RunView{ID: "run_1", WorkItemID: "wi_1", State: execution.RunActive},
			run: frozenRun()},
		fakeWorkItems{item: frozenWorkItem()},
		settler, &sequenceIDs{},
		func() time.Time { return time.Date(2026, 10, 14, 14, 0, 0, 0, time.UTC) },
	)
}

func passedEvidence() fakeEvidence {
	return fakeEvidence{rows: map[string]evaluation.Verification{
		"ve_tests": {
			VerificationID: "ve_tests", Kind: "required_tests", SubjectKind: "commit",
			RepositoryID: projectRepo, CommitOID: headOID, Result: "passed", Source: "real",
		},
		"ve_review": {
			VerificationID: "ve_review", Kind: "independent_review", SubjectKind: "commit",
			RepositoryID: projectRepo, CommitOID: headOID, Result: "passed", Source: "real",
		},
	}}
}

func publishCommand() githubdelivery.AdmitOperationCommand {
	return githubdelivery.AdmitOperationCommand{
		WorkspaceID: "ws_1", WorkItemID: "wi_1", RunID: "run_1",
		Kind: githubdelivery.OperationPublishPR, RepositoryID: projectRepo,
		Ref: "refs/heads/task/s05-fix", BaseRef: "refs/heads/main", CommitOID: headOID,
		RequestedByUserID: "101", RequestKey: "admit-1",
		VerificationIDs: []string{"ve_tests"}, IndependentReviewIDs: []string{"ve_review"},
	}
}

type scriptedFactSource struct {
	pullRequests  []githubdelivery.PullRequestProjection
	findErr       error
	branchHeadOID string
	branchKnown   bool
	branchErr     error
}

func (s scriptedFactSource) PullRequest(context.Context, string, string) (githubdelivery.PullRequestProjection, error) {
	if s.findErr != nil {
		return githubdelivery.PullRequestProjection{}, s.findErr
	}
	if len(s.pullRequests) == 0 {
		return githubdelivery.PullRequestProjection{}, errors.New("no fact")
	}
	return s.pullRequests[0], nil
}

func (s scriptedFactSource) PullRequestsByHead(context.Context, string, string) ([]githubdelivery.PullRequestProjection, error) {
	return s.pullRequests, s.findErr
}

func (s scriptedFactSource) BranchHead(context.Context, string, string) (string, bool, error) {
	return s.branchHeadOID, s.branchKnown, s.branchErr
}

func pullRequestFact(number, author, head string, merged bool) githubdelivery.PullRequestProjection {
	fact := githubdelivery.PullRequestFact{
		Number: number, AuthorID: author, HeadOID: head, HeadRef: "task/s05-fix",
		BaseRef: "refs/heads/main", State: "open",
	}
	if merged {
		fact.State = "closed"
		fact.Merged = true
		fact.MergeCommitOID = mergeOID
		fact.MergedByID = "277460407"
		fact.MergedByType = "User"
		fact.MergedAt = time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	}
	return githubdelivery.PullRequestProjection{
		RepositoryID: projectRepo, Number: number, PullRequest: fact, ActorID: machineUserID,
	}
}

func TestVerifyChangeSettlesOnlyFromTheRemoteFact(t *testing.T) {
	service := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	command := publishCommand()
	command.Kind = githubdelivery.OperationVerifyChange
	command.Ref = "refs/heads/task/s05-fix"
	command.BaseRef = ""
	command.VerificationIDs, command.IndependentReviewIDs = nil, nil
	admitted, err := service.AdmitOperation(context.Background(), command)
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	if admitted.State != githubdelivery.StatePending {
		t.Fatalf("AdmitOperation() state = %s", admitted.State)
	}
	settled, err := service.ObserveOperation(context.Background(), "ws_1", admitted.OperationID)
	if err != nil {
		t.Fatalf("ObserveOperation() error = %v", err)
	}
	if settled.State != githubdelivery.StateSucceeded || settled.Result == nil || settled.Result.CommitOID != headOID {
		t.Fatalf("ObserveOperation() = %#v", settled)
	}

	mismatch := newService(t, scriptedFactSource{branchHeadOID: otherOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	admittedMismatch, err := mismatch.AdmitOperation(context.Background(), command)
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	failed, err := mismatch.ObserveOperation(context.Background(), "ws_1", admittedMismatch.OperationID)
	if err != nil {
		t.Fatalf("ObserveOperation() error = %v", err)
	}
	if failed.State != githubdelivery.StateFailed {
		t.Fatalf("mismatched head settled as %s, want FAILED", failed.State)
	}

	unknown := newService(t, scriptedFactSource{branchErr: errors.New("gh unavailable")}, passedEvidence(), &fakeSettler{})
	admittedUnknown, err := unknown.AdmitOperation(context.Background(), command)
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	unreadable, err := unknown.ObserveOperation(context.Background(), "ws_1", admittedUnknown.OperationID)
	if err != nil {
		t.Fatalf("ObserveOperation() error = %v", err)
	}
	if unreadable.State != githubdelivery.StateUnknown || unreadable.UnknownReason == "" {
		t.Fatalf("unreadable remote settled as %#v, want UNKNOWN with a reason", unreadable)
	}
}

// mustVerifyChange admits and settles the verify_change of the delivered commit;
// a pull request admission is gated on that settled fact.
func mustVerifyChange(t *testing.T, service *githubdelivery.Service) {
	t.Helper()
	command := publishCommand()
	command.Kind = githubdelivery.OperationVerifyChange
	command.BaseRef = ""
	command.VerificationIDs, command.IndependentReviewIDs = nil, nil
	admitted, err := service.AdmitOperation(context.Background(), command)
	if err != nil {
		t.Fatalf("verify_change admission error = %v", err)
	}
	settled, err := service.ObserveOperation(context.Background(), "ws_1", admitted.OperationID)
	if err != nil || settled.State != githubdelivery.StateSucceeded {
		t.Fatalf("verify_change observation = %#v, %v", settled, err)
	}
}

func TestPublishPRAdmissionRequiresTestsAndAnIndependentReview(t *testing.T) {
	service := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, service)
	verified := publishCommand()
	verified.IndependentReviewIDs = nil
	if _, err := service.AdmitOperation(context.Background(), verified); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("admission without an independent review = %v, want ErrPolicyBlocked", err)
	}
	withoutTests := publishCommand()
	withoutTests.VerificationIDs = nil
	if _, err := service.AdmitOperation(context.Background(), withoutTests); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("admission without required tests = %v, want ErrPolicyBlocked", err)
	}
	simulated := passedEvidence()
	row := simulated.rows["ve_tests"]
	row.Source = "simulated"
	simulated.rows["ve_tests"] = row
	simulatedService := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, simulated, &fakeSettler{})
	mustVerifyChange(t, simulatedService)
	if _, err := simulatedService.AdmitOperation(context.Background(), publishCommand()); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("admission on simulated evidence = %v, want ErrPolicyBlocked", err)
	}
	staleHead := passedEvidence()
	row = staleHead.rows["ve_review"]
	row.CommitOID = otherOID
	staleHead.rows["ve_review"] = row
	staleService := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, staleHead, &fakeSettler{})
	mustVerifyChange(t, staleService)
	if _, err := staleService.AdmitOperation(context.Background(), publishCommand()); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("admission with evidence for another head = %v, want ErrPolicyBlocked", err)
	}
	admitted, err := service.AdmitOperation(context.Background(), publishCommand())
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	if admitted.Request.ExpectedGitHubUserID != machineUserID {
		t.Fatalf("admission machine identity = %q", admitted.Request.ExpectedGitHubUserID)
	}
}

func TestLostPublishPRResponseIsReconciledAndNeverRecreated(t *testing.T) {
	facts := scriptedFactSource{pullRequests: []githubdelivery.PullRequestProjection{pullRequestFact("42", machineUserID, headOID, false)}, branchHeadOID: headOID, branchKnown: true}
	service := newService(t, facts, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, service)
	admitted, err := service.AdmitOperation(context.Background(), publishCommand())
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	attempted, err := service.MarkAttempted(context.Background(), githubdelivery.MarkAttemptedCommand{
		WorkspaceID: "ws_1", OperationID: admitted.OperationID, ExpectedVersion: admitted.Version,
		RequestKey: "attempt-1", ActorID: machineUserID,
	})
	if err != nil || attempted.State != githubdelivery.StateAttempted {
		t.Fatalf("MarkAttempted() = %#v, %v", attempted, err)
	}
	settled, err := service.ObserveOperation(context.Background(), "ws_1", admitted.OperationID)
	if err != nil {
		t.Fatalf("ObserveOperation() error = %v", err)
	}
	if settled.State != githubdelivery.StateSucceeded || settled.Result == nil ||
		settled.Result.PullRequestNumber != "42" || !settled.Result.Reconciled {
		t.Fatalf("reconciled operation = %#v", settled)
	}
	if settled.Result.PullRequestAuthorID != machineUserID {
		t.Fatalf("pull request author = %q", settled.Result.PullRequestAuthorID)
	}
	repeat, err := service.ObserveOperation(context.Background(), "ws_1", admitted.OperationID)
	if err != nil || repeat.Version != settled.Version || repeat.State != githubdelivery.StateSucceeded {
		t.Fatalf("repeated observation advanced the operation: %#v, %v", repeat, err)
	}
}

func TestPublishPRAuthorMismatchIsNotAccepted(t *testing.T) {
	facts := scriptedFactSource{pullRequests: []githubdelivery.PullRequestProjection{pullRequestFact("42", "101", headOID, false)}, branchHeadOID: headOID, branchKnown: true}
	service := newService(t, facts, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, service)
	admitted, err := service.AdmitOperation(context.Background(), publishCommand())
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	if _, err := service.MarkAttempted(context.Background(), githubdelivery.MarkAttemptedCommand{
		WorkspaceID: "ws_1", OperationID: admitted.OperationID, ExpectedVersion: admitted.Version,
		RequestKey: "attempt-1", ActorID: machineUserID,
	}); err != nil {
		t.Fatalf("MarkAttempted() error = %v", err)
	}
	settled, err := service.ObserveOperation(context.Background(), "ws_1", admitted.OperationID)
	if err != nil {
		t.Fatalf("ObserveOperation() error = %v", err)
	}
	if settled.State != githubdelivery.StateFailed {
		t.Fatalf("a web identity as pull request author settled as %s, want FAILED", settled.State)
	}
}

func TestUnknownPublishPROutcomeBlocksASecondWrite(t *testing.T) {
	unreadable := newService(t, scriptedFactSource{findErr: errors.New("gh unavailable"), branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, unreadable)
	admitted, err := unreadable.AdmitOperation(context.Background(), publishCommand())
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	if _, err := unreadable.MarkAttempted(context.Background(), githubdelivery.MarkAttemptedCommand{
		WorkspaceID: "ws_1", OperationID: admitted.OperationID, ExpectedVersion: admitted.Version,
		RequestKey: "attempt-1", ActorID: machineUserID,
	}); err != nil {
		t.Fatalf("MarkAttempted() error = %v", err)
	}
	pending, err := unreadable.ObserveOperation(context.Background(), "ws_1", admitted.OperationID)
	if err != nil || pending.State != githubdelivery.StateUnknown {
		t.Fatalf("ObserveOperation() = %#v, %v", pending, err)
	}
	second := publishCommand()
	second.RequestKey = "admit-2"
	if _, err := unreadable.AdmitOperation(context.Background(), second); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("second admission while unknown = %v, want ErrPolicyBlocked", err)
	}
	reconciled := newService(t, scriptedFactSource{pullRequests: []githubdelivery.PullRequestProjection{pullRequestFact("42", machineUserID, headOID, false)}, branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, reconciled)
	reAdmitted, err := reconciled.AdmitOperation(context.Background(), second)
	if err != nil {
		t.Fatalf("re-admission after reconciliation error = %v", err)
	}
	if reAdmitted.State != githubdelivery.StatePending {
		t.Fatalf("re-admitted operation = %#v", reAdmitted)
	}
}

func TestAdmissionRefusesAForeignRepositoryOrNonCurrentRun(t *testing.T) {
	service := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, service)
	foreign := publishCommand()
	foreign.RepositoryID = knowledgeRepo
	if _, err := service.AdmitOperation(context.Background(), foreign); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("knowledge repository admission = %v, want ErrPolicyBlocked", err)
	}
	protected := publishCommand()
	protected.Ref = "refs/heads/main"
	if _, err := service.AdmitOperation(context.Background(), protected); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("protected head admission = %v, want ErrPolicyBlocked", err)
	}
	stale := publishCommand()
	stale.RunID = "run_old"
	if _, err := service.AdmitOperation(context.Background(), stale); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("admission from a non-current run = %v, want ErrPolicyBlocked", err)
	}
	mismatch := publishCommand()
	mismatch.Ref = "not-a-branch-ref"
	if _, err := service.AdmitOperation(context.Background(), mismatch); !errors.Is(err, githubdelivery.ErrValidation) {
		t.Fatalf("malformed ref admission = %v, want ErrValidation", err)
	}
}

func TestObservePullRequestSettlesReviewAndMergeFromRealFacts(t *testing.T) {
	settler := &fakeSettler{}
	facts := scriptedFactSource{pullRequests: []githubdelivery.PullRequestProjection{func() githubdelivery.PullRequestProjection {
		projection := pullRequestFact("42", machineUserID, headOID, true)
		projection.Reviews = []githubdelivery.ReviewFact{{
			ReviewID: "5001", ReviewerID: "277460407", ReviewerLogin: "reviewer-a",
			ReviewerType: "User", ReviewerPermission: "write",
			State: "APPROVED", CommitOID: headOID,
			Submitted: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC),
		}}
		projection.Checks = &githubdelivery.ChecksFact{CommitOID: headOID, State: githubdelivery.ChecksPassed, Total: 2}
		return projection
	}()}}
	service := newService(t, facts, passedEvidence(), settler)
	projection, err := service.ObservePullRequest(context.Background(), githubdelivery.ObservePullRequestCommand{
		WorkspaceID: "ws_1", RepositoryID: projectRepo, Number: "42",
	})
	if err != nil {
		t.Fatalf("ObservePullRequest() error = %v", err)
	}
	if projection.Unknown {
		t.Fatalf("observed pull request is unknown: %#v", projection)
	}
	if len(settler.settled) != 2 {
		t.Fatalf("settled tasks = %#v, want a review and a merge settlement", settler.settled)
	}
	observations, err := service.DeliveryView(context.Background(), "ws_1", "wi_1")
	if err != nil {
		t.Fatalf("DeliveryView() error = %v", err)
	}
	if len(observations.Facts) == 0 {
		t.Fatalf("DeliveryView() has no facts: %#v", observations)
	}
}

func TestExternalFactSettlementRequiresAnAuthorizedReviewer(t *testing.T) {
	head := headOID
	base := githubdelivery.Observation{
		ObservationID: "obs_review", WorkspaceID: "ws_1", RepositoryID: projectRepo,
		SubjectKind: githubdelivery.SubjectReview, SubjectKey: "42/5001",
		ObservedAt: time.Now().UTC(),
	}
	authorized := githubdelivery.ReviewFact{
		ReviewID: "5001", ReviewerID: "277460407", ReviewerType: "User", ReviewerPermission: "write",
		State: "APPROVED", CommitOID: head,
	}
	cases := map[string]githubdelivery.ReviewFact{
		"authorized":         authorized,
		"bot account":        func() githubdelivery.ReviewFact { r := authorized; r.ReviewerType = "Bot"; return r }(),
		"read only":          func() githubdelivery.ReviewFact { r := authorized; r.ReviewerPermission = "read"; return r }(),
		"unknown permission": func() githubdelivery.ReviewFact { r := authorized; r.ReviewerPermission = ""; return r }(),
		"another commit":     func() githubdelivery.ReviewFact { r := authorized; r.CommitOID = otherOID; return r }(),
	}
	for name, review := range cases {
		t.Run(name, func(t *testing.T) {
			observation := base
			fact := review
			observation.Review = &fact
			settles := githubdelivery.FactSettlesObject(observation, "pull_request_review", "42", head)
			if name == "authorized" && !settles {
				t.Fatal("an authorized approval must settle the request")
			}
			if name != "authorized" && settles {
				t.Fatalf("an unauthorized review settled the request: %#v", review)
			}
		})
	}
}

func TestHeadDriftExpiresTasksBoundToTheOldHead(t *testing.T) {
	settler := &fakeSettler{}
	facts := scriptedFactSource{pullRequests: []githubdelivery.PullRequestProjection{pullRequestFact("42", machineUserID, headOID, false)}}
	service := newService(t, facts, passedEvidence(), settler)
	if _, err := service.ObservePullRequest(context.Background(), githubdelivery.ObservePullRequestCommand{
		WorkspaceID: "ws_1", RepositoryID: projectRepo, Number: "42",
	}); err != nil {
		t.Fatalf("ObservePullRequest() error = %v", err)
	}
	if settler.expired == 0 {
		t.Fatalf("head drift did not expire tasks bound to other versions")
	}
	if settler.lastKind != "pull_request_review" && settler.lastKind != "pull_request_merge" {
		t.Fatalf("expired object kind = %q", settler.lastKind)
	}
}

func TestAbandonOnlyAfterReconciliationConfirmsNoObject(t *testing.T) {
	unreadable := newService(t, scriptedFactSource{findErr: errors.New("gh unavailable"), branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, unreadable)
	admittedUnreadable, err := unreadable.AdmitOperation(context.Background(), publishCommand())
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	if _, err := unreadable.MarkAttempted(context.Background(), githubdelivery.MarkAttemptedCommand{
		WorkspaceID: "ws_1", OperationID: admittedUnreadable.OperationID, ExpectedVersion: admittedUnreadable.Version,
		RequestKey: "attempt-1", ActorID: machineUserID,
	}); err != nil {
		t.Fatalf("MarkAttempted() error = %v", err)
	}
	if _, err := unreadable.AbandonOperation(context.Background(), githubdelivery.AbandonCommand{
		WorkspaceID: "ws_1", OperationID: admittedUnreadable.OperationID, ExpectedVersion: 0,
		ActorID: machineUserID, RequestKey: "abandon-1", Reason: "the write never completed",
	}); !errors.Is(err, githubdelivery.ErrExternalUnknown) {
		t.Fatalf("abandon on an unreadable outcome = %v, want ErrExternalUnknown", err)
	}

	empty := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, empty)
	admittedEmpty, err := empty.AdmitOperation(context.Background(), publishCommand())
	if err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	if _, err := empty.MarkAttempted(context.Background(), githubdelivery.MarkAttemptedCommand{
		WorkspaceID: "ws_1", OperationID: admittedEmpty.OperationID, ExpectedVersion: admittedEmpty.Version,
		RequestKey: "attempt-1", ActorID: machineUserID,
	}); err != nil {
		t.Fatalf("MarkAttempted() error = %v", err)
	}
	abandoned, err := empty.AbandonOperation(context.Background(), githubdelivery.AbandonCommand{
		WorkspaceID: "ws_1", OperationID: admittedEmpty.OperationID, ExpectedVersion: 0,
		ActorID: machineUserID, RequestKey: "abandon-1", Reason: "the write never completed",
	})
	if err != nil || abandoned.State != githubdelivery.StateFailed {
		t.Fatalf("AbandonOperation() = %#v, %v", abandoned, err)
	}
	// A settled failure permits a new attempt for the same head.
	retry := publishCommand()
	retry.RequestKey = "admit-retry"
	reAdmitted, err := empty.AdmitOperation(context.Background(), retry)
	if err != nil || reAdmitted.State != githubdelivery.StatePending {
		t.Fatalf("AdmitOperation() after abandon = %#v, %v", reAdmitted, err)
	}
}

func TestUnsettledOperationsBlockWorkItemCancellation(t *testing.T) {
	service := newService(t, scriptedFactSource{branchHeadOID: headOID, branchKnown: true}, passedEvidence(), &fakeSettler{})
	mustVerifyChange(t, service)
	if _, err := service.AdmitOperation(context.Background(), publishCommand()); err != nil {
		t.Fatalf("AdmitOperation() error = %v", err)
	}
	unsettled, err := service.UnsettledOperations(context.Background(), "ws_1", "wi_1")
	if err != nil || unsettled != 1 {
		t.Fatalf("UnsettledOperations() = %d, %v", unsettled, err)
	}
}
