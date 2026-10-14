package githubdelivery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

// MVP protected delivery branches: the control plane never admits a pull
// request whose head is one of them, and a publish_pr base must be one of them.
const (
	protectedMain   = "refs/heads/main"
	protectedMaster = "refs/heads/master"
)

// unknownNoObject prefixes the unknown reason of an operation whose write was
// issued while a fresh, successful read found no object. Only this state may be
// settled as failed by an explicit reconciliation decision.
const unknownNoObject = "no_object_observed"

// Service implements delivery admission, reconciliation and projection.
type Service struct {
	store     Store
	facts     FactSource
	evidence  EvidenceSource
	runs      RunSource
	workitems WorkItemSource
	settler   FactSettler
	ids       IDGenerator
	now       func() time.Time
}

func NewService(
	store Store,
	facts FactSource,
	evidence EvidenceSource,
	runs RunSource,
	workitems WorkItemSource,
	settler FactSettler,
	ids IDGenerator,
	now func() time.Time,
) *Service {
	return &Service{
		store: store, facts: facts, evidence: evidence, runs: runs,
		workitems: workitems, settler: settler, ids: ids, now: now,
	}
}

// MarkAttemptedCommand records that a write was issued for one operation. The
// write belongs to the Runner's controlled capability; this call only makes the
// unknown outcome first-class so a retry cannot create a second object.
type MarkAttemptedCommand struct {
	WorkspaceID     string
	OperationID     string
	ExpectedVersion int64
	RequestKey      string
	ActorID         string
}

// ObservePullRequestCommand reconciles the facts of one pull request.
type ObservePullRequestCommand struct {
	WorkspaceID  string
	RepositoryID string
	Number       string
}

// DeliveryView is the read-only delivery projection of one WorkItem.
type DeliveryView struct {
	WorkItem     workitem.WorkItem
	Bindings     []workitem.RunBinding
	Operations   []ExternalOperation
	Facts        []Observation
	Verification []evaluation.Verification
	Closure      *evaluation.ClosureRecord
}

// AdmitOperation admits one external operation after re-reading every fact it
// gates on. Admission performs no write and starts no workflow.
func (s *Service) AdmitOperation(ctx context.Context, command AdmitOperationCommand) (ExternalOperation, error) {
	if !validRequestKey(command.RequestKey) || !validActor(command.RequestedByUserID) ||
		command.WorkspaceID == "" || command.WorkItemID == "" || command.RunID == "" ||
		(command.Kind != OperationVerifyChange && command.Kind != OperationPublishPR) ||
		!externalIDPattern.MatchString(command.RepositoryID) {
		return ExternalOperation{}, ErrValidation
	}
	item, err := s.workitems.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return ExternalOperation{}, err
	}
	if item.State != workitem.StateOpen {
		return ExternalOperation{}, fmt.Errorf("%w: the work item is %s", ErrPolicyBlocked, item.State)
	}
	if item.CurrentRunID != command.RunID {
		return ExternalOperation{}, fmt.Errorf(
			"%w: only the current delivery run of the work item may request delivery operations", ErrPolicyBlocked)
	}
	if command.RepositoryID != item.TargetRepositoryID {
		return ExternalOperation{}, fmt.Errorf(
			"%w: delivery operations are limited to the work item's single project repository", ErrPolicyBlocked)
	}
	run, err := s.runs.Run(ctx, command.RunID)
	if err != nil {
		return ExternalOperation{}, err
	}
	if run.WorkspaceID != command.WorkspaceID || run.WorkItemID != command.WorkItemID {
		return ExternalOperation{}, fmt.Errorf("%w: the run does not belong to the work item", ErrPolicyBlocked)
	}
	if run.State != execution.RunActive {
		return ExternalOperation{}, fmt.Errorf("%w: the run is %s", ErrPolicyBlocked, run.State)
	}
	if !validActor(run.GitExecutionUserID) {
		return ExternalOperation{}, fmt.Errorf(
			"%w: the run has no frozen machine GitHub identity for remote operations", ErrPolicyBlocked)
	}
	if !oidPattern.MatchString(command.CommitOID) {
		return ExternalOperation{}, ErrValidation
	}
	if !refPattern.MatchString(command.Ref) {
		return ExternalOperation{}, ErrValidation
	}
	if command.Ref == protectedMain || command.Ref == protectedMaster {
		return ExternalOperation{}, fmt.Errorf("%w: %s is a protected delivery branch", ErrPolicyBlocked, command.Ref)
	}
	switch command.Kind {
	case OperationVerifyChange:
		if command.BaseRef != "" || len(command.VerificationIDs) > 0 || len(command.IndependentReviewIDs) > 0 ||
			command.TitleArtifactID != "" {
			return ExternalOperation{}, ErrValidation
		}
	case OperationPublishPR:
		if !refPattern.MatchString(command.BaseRef) || command.BaseRef == command.Ref {
			return ExternalOperation{}, ErrValidation
		}
		if command.BaseRef != protectedMain && command.BaseRef != protectedMaster {
			return ExternalOperation{}, fmt.Errorf(
				"%w: a pull request must target the protected delivery branch", ErrPolicyBlocked)
		}
		if err := s.checkPublishGates(ctx, command); err != nil {
			return ExternalOperation{}, err
		}
	}
	if existing, found, err := s.store.OperationForRequest(
		ctx, command.WorkspaceID, command.Kind, command.RepositoryID, command.Ref, command.CommitOID,
	); err != nil {
		return ExternalOperation{}, err
	} else if found {
		switch existing.State {
		case StatePending:
			return existing, nil
		case StateFailed:
			// A settled failure proved no object was created; a new attempt is
			// legal and gets a new operation identity.
		default:
			return ExternalOperation{}, fmt.Errorf(
				"%w: operation %s for this head is %s and must be reconciled before another attempt",
				ErrPolicyBlocked, existing.OperationID, existing.State)
		}
	}
	id, err := s.ids.New()
	if err != nil {
		return ExternalOperation{}, fmt.Errorf("generate operation id: %w", err)
	}
	now := s.now().UTC()
	operation := ExternalOperation{
		OperationID: id, WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		RunID: command.RunID, NodeRunID: command.NodeRunID, TaskID: command.TaskID,
		Kind: command.Kind, State: StatePending, CreatedAt: now, UpdatedAt: now, Version: 1,
		Request: OperationRequest{
			RepositoryID: command.RepositoryID, Role: "project", Operation: string(command.Kind),
			Ref: command.Ref, BaseRef: command.BaseRef, CommitOID: command.CommitOID,
			ExpectedGitHubUserID: run.GitExecutionUserID, RequestedByGitHubUserID: command.RequestedByUserID,
			TitleArtifactID: command.TitleArtifactID,
		},
	}
	operation.RequestDigest = requestDigest(operation)
	return s.store.CreateOperation(ctx, AdmitMutation{
		Operation: operation, RequestKey: command.RequestKey,
		RequestDigest: Digest("delivery.admit", command.WorkspaceID, command.WorkItemID, command.RunID,
			string(command.Kind), command.RepositoryID, command.Ref, command.CommitOID, command.RequestKey,
			strings.Join(command.VerificationIDs, ","), strings.Join(command.IndependentReviewIDs, ",")),
	})
}

// checkPublishGates re-reads the gates a pull request admission depends on: the
// required tests, the independent review and the settled verify_change of the
// exact delivered commit. Simulated or stale evidence never passes.
func (s *Service) checkPublishGates(ctx context.Context, command AdmitOperationCommand) error {
	if len(command.VerificationIDs) == 0 || len(command.IndependentReviewIDs) == 0 {
		return fmt.Errorf(
			"%w: a pull request needs passed required tests and an independent review of the delivered commit",
			ErrPolicyBlocked)
	}
	for _, id := range command.VerificationIDs {
		if err := s.requireGate(ctx, command, id, evaluation.KindRequiredTests); err != nil {
			return err
		}
	}
	for _, id := range command.IndependentReviewIDs {
		if err := s.requireGate(ctx, command, id, evaluation.KindIndependentReview); err != nil {
			return err
		}
	}
	verified, found, err := s.store.OperationForRequest(
		ctx, command.WorkspaceID, OperationVerifyChange, command.RepositoryID, command.Ref, command.CommitOID)
	if err != nil {
		return err
	}
	if !found || verified.State != StateSucceeded {
		return fmt.Errorf(
			"%w: the delivered commit has no settled verify_change for this repository and ref", ErrPolicyBlocked)
	}
	return nil
}

func (s *Service) requireGate(
	ctx context.Context,
	command AdmitOperationCommand,
	verificationID, kind string,
) error {
	verification, found, err := s.evidence.Verification(ctx, command.WorkspaceID, verificationID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: verification %s does not exist", ErrPolicyBlocked, verificationID)
	}
	if verification.Kind != kind || verification.Result != evaluation.ResultPassed ||
		verification.Source != evaluation.SourceReal || verification.SubjectKind != "commit" ||
		verification.CommitOID != command.CommitOID || verification.RepositoryID != command.RepositoryID {
		return fmt.Errorf(
			"%w: verification %s does not establish a passed %s on this commit and repository",
			ErrPolicyBlocked, verificationID, kind)
	}
	return nil
}

// MarkAttempted records that the write for one operation was issued, so an
// unknown outcome blocks a second write until it is reconciled.
func (s *Service) MarkAttempted(ctx context.Context, command MarkAttemptedCommand) (ExternalOperation, error) {
	if command.WorkspaceID == "" || command.OperationID == "" || !validActor(command.ActorID) {
		return ExternalOperation{}, ErrValidation
	}
	operation, err := s.store.Operation(ctx, command.WorkspaceID, command.OperationID)
	if err != nil {
		return ExternalOperation{}, err
	}
	switch operation.State {
	case StateAttempted, StateUnknown:
		return operation, nil
	case StatePending:
	default:
		return ExternalOperation{}, fmt.Errorf("%w: the operation is already %s", ErrPolicyBlocked, operation.State)
	}
	if command.ExpectedVersion != 0 && command.ExpectedVersion != operation.Version {
		return ExternalOperation{}, ErrVersionConflict
	}
	return s.store.UpdateOperation(ctx, UpdateMutation{
		WorkspaceID: command.WorkspaceID, OperationID: operation.OperationID,
		ExpectedVersion: operation.Version, State: StateAttempted, At: s.now().UTC(),
	})
}

// ObserveOperation reconciles one operation against real GitHub facts. A
// terminal operation is never advanced again by a repeated observation.
func (s *Service) ObserveOperation(ctx context.Context, workspaceID, operationID string) (ExternalOperation, error) {
	if workspaceID == "" || operationID == "" {
		return ExternalOperation{}, ErrValidation
	}
	operation, err := s.store.Operation(ctx, workspaceID, operationID)
	if err != nil {
		return ExternalOperation{}, err
	}
	switch operation.State {
	case StateSucceeded, StateFailed:
		return operation, nil
	}
	switch operation.Kind {
	case OperationVerifyChange:
		return s.observeVerifyChange(ctx, operation)
	case OperationPublishPR:
		return s.observePublishPR(ctx, operation)
	default:
		return ExternalOperation{}, fmt.Errorf("%w: unknown operation kind %s", ErrValidation, operation.Kind)
	}
}

func (s *Service) observeVerifyChange(ctx context.Context, operation ExternalOperation) (ExternalOperation, error) {
	branch, ok := branchOf(operation.Request.Ref)
	if !ok {
		return ExternalOperation{}, ErrValidation
	}
	head, known, err := s.facts.BranchHead(ctx, operation.Request.RepositoryID, branch)
	if err != nil || !known {
		reason := "branch head is unreadable"
		if err != nil {
			reason = err.Error()
		}
		if _, recordErr := s.recordUnknown(ctx, operation, SubjectBranchHead, operation.Request.Ref, reason); recordErr != nil {
			return ExternalOperation{}, recordErr
		}
		return s.store.UpdateOperation(ctx, UpdateMutation{
			WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
			ExpectedVersion: operation.Version, State: StateUnknown,
			UnknownReason: "branch head could not be observed: " + reason, At: s.now().UTC(),
		})
	}
	observation, err := s.newObservation(operation, SubjectBranchHead, operation.Request.Ref)
	if err != nil {
		return ExternalOperation{}, err
	}
	observation.Ref = &RefFact{CommitOID: head}
	recorded, err := s.store.RecordObservation(ctx, RecordObservationMutation{Observation: observation})
	if err != nil {
		return ExternalOperation{}, err
	}
	if head != operation.Request.CommitOID {
		return s.store.UpdateOperation(ctx, UpdateMutation{
			WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
			ExpectedVersion: operation.Version, State: StateFailed,
			UnknownReason: fmt.Sprintf(
				"remote %s head %s does not match the delivered commit %s",
				operation.Request.Ref, head, operation.Request.CommitOID),
			At: s.now().UTC(),
		})
	}
	reconciled := operation.State == StateAttempted || operation.State == StateUnknown
	return s.store.UpdateOperation(ctx, UpdateMutation{
		WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
		ExpectedVersion: operation.Version, State: StateSucceeded,
		Result: &OperationResult{
			RepositoryID: operation.Request.RepositoryID, Ref: operation.Request.Ref,
			CommitOID: head, Reconciled: reconciled, ObservationID: recorded.ObservationID,
			ObservedAt: recorded.ObservedAt, ObservedByGitHubUser: recorded.ActorID,
		},
		At: s.now().UTC(),
	})
}

func (s *Service) observePublishPR(ctx context.Context, operation ExternalOperation) (ExternalOperation, error) {
	branch, ok := branchOf(operation.Request.Ref)
	if !ok {
		return ExternalOperation{}, ErrValidation
	}
	projections, err := s.facts.PullRequestsByHead(ctx, operation.Request.RepositoryID, branch)
	if err != nil {
		if _, recordErr := s.recordUnknown(ctx, operation, SubjectPullRequest, operation.Request.Ref, err.Error()); recordErr != nil {
			return ExternalOperation{}, recordErr
		}
		return s.store.UpdateOperation(ctx, UpdateMutation{
			WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
			ExpectedVersion: operation.Version, State: StateUnknown,
			UnknownReason: "pull request list could not be observed: " + err.Error(), At: s.now().UTC(),
		})
	}
	var match *PullRequestProjection
	for index := range projections {
		projection := projections[index]
		if projection.PullRequest.HeadOID != operation.Request.CommitOID {
			continue
		}
		if operation.Request.BaseRef != "" && projection.PullRequest.BaseRef != operation.Request.BaseRef {
			continue
		}
		match = &projection
		break
	}
	if match == nil {
		if operation.State == StateAttempted {
			// The read succeeded and no object exists, but a write was issued.
			// The outcome stays unknown until an explicit reconciliation
			// decision, so a second object cannot be created by accident.
			return s.store.UpdateOperation(ctx, UpdateMutation{
				WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
				ExpectedVersion: operation.Version, State: StateUnknown,
				UnknownReason: fmt.Sprintf(
					"%s: no pull request exists for the delivered head %s after an issued write",
					unknownNoObject, operation.Request.CommitOID),
				At: s.now().UTC(),
			})
		}
		return operation, nil
	}
	recorded, err := s.recordProjection(ctx, operation.WorkspaceID, operation.Request.RepositoryID, *match)
	if err != nil {
		return ExternalOperation{}, err
	}
	if match.PullRequest.AuthorID != operation.Request.ExpectedGitHubUserID {
		return s.store.UpdateOperation(ctx, UpdateMutation{
			WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
			ExpectedVersion: operation.Version, State: StateFailed,
			UnknownReason: fmt.Sprintf(
				"pull request %s was authored by %s, the frozen machine identity is %s",
				match.PullRequest.Number, match.PullRequest.AuthorID, operation.Request.ExpectedGitHubUserID),
			At: s.now().UTC(),
		})
	}
	reconciled := operation.State == StateAttempted || operation.State == StateUnknown
	return s.store.UpdateOperation(ctx, UpdateMutation{
		WorkspaceID: operation.WorkspaceID, OperationID: operation.OperationID,
		ExpectedVersion: operation.Version, State: StateSucceeded,
		Result: &OperationResult{
			RepositoryID: operation.Request.RepositoryID, Ref: operation.Request.Ref,
			CommitOID: match.PullRequest.HeadOID, PullRequestNumber: match.PullRequest.Number,
			PullRequestAuthorID: match.PullRequest.AuthorID, BaseRef: match.PullRequest.BaseRef,
			Reconciled: reconciled, ObservationID: recorded.pullRequest.ObservationID,
			ObservedAt: recorded.pullRequest.ObservedAt, ObservedByGitHubUser: recorded.pullRequest.ActorID,
		},
		At: s.now().UTC(),
	})
}

// AbandonOperation settles an unknown operation as failed, and only after a
// fresh reconciliation confirmed that no external object exists. The read is
// never skipped: abandoning on an unreadable fact would hide a live object.
func (s *Service) AbandonOperation(ctx context.Context, command AbandonCommand) (ExternalOperation, error) {
	if command.WorkspaceID == "" || command.OperationID == "" || !validActor(command.ActorID) ||
		!validRequestKey(command.RequestKey) || command.Reason == "" {
		return ExternalOperation{}, ErrValidation
	}
	operation, err := s.store.Operation(ctx, command.WorkspaceID, command.OperationID)
	if err != nil {
		return ExternalOperation{}, err
	}
	if command.ExpectedVersion != 0 && command.ExpectedVersion != operation.Version {
		return ExternalOperation{}, ErrVersionConflict
	}
	switch operation.State {
	case StateSucceeded, StateFailed:
		return operation, nil
	case StatePending, StateAttempted, StateUnknown:
	default:
		return ExternalOperation{}, ErrValidation
	}
	observed, err := s.ObserveOperation(ctx, command.WorkspaceID, command.OperationID)
	if err != nil {
		return ExternalOperation{}, err
	}
	switch observed.State {
	case StateSucceeded, StateFailed:
		// The reconciliation settled the operation; nothing is abandoned.
		return observed, nil
	case StateUnknown:
		if !strings.HasPrefix(observed.UnknownReason, unknownNoObject) {
			return ExternalOperation{}, fmt.Errorf(
				"%w: the external outcome is still unreadable and cannot be abandoned", ErrExternalUnknown)
		}
	}
	return s.store.UpdateOperation(ctx, UpdateMutation{
		WorkspaceID: command.WorkspaceID, OperationID: command.OperationID,
		ExpectedVersion: observed.Version, State: StateFailed,
		UnknownReason: "abandoned after reconciliation: " + command.Reason, At: s.now().UTC(),
	})
}

// ObservePullRequest records the reconciled facts of one pull request, settles
// the external-fact human tasks a matching review or merge establishes, and
// expires tasks bound to a superseded head. A failed read is recorded as an
// unknown attempt and never replaces a known fact.
func (s *Service) ObservePullRequest(ctx context.Context, command ObservePullRequestCommand) (PullRequestProjection, error) {
	if command.WorkspaceID == "" || !externalIDPattern.MatchString(command.RepositoryID) || command.Number == "" {
		return PullRequestProjection{}, ErrValidation
	}
	projection, err := s.facts.PullRequest(ctx, command.RepositoryID, command.Number)
	if err != nil || projection.Unknown {
		reason := projection.UnknownReason
		if reason == "" && err != nil {
			reason = err.Error()
		}
		if reason == "" {
			reason = "pull request could not be observed"
		}
		observation := Observation{
			WorkspaceID: command.WorkspaceID, RepositoryID: command.RepositoryID,
			SubjectKind: SubjectPullRequest, SubjectKey: command.Number,
			Unknown: true, UnknownReason: reason,
		}
		if err := s.stampObservation(&observation); err != nil {
			return PullRequestProjection{}, err
		}
		if _, err := s.store.RecordObservation(ctx, RecordObservationMutation{Observation: observation}); err != nil {
			return PullRequestProjection{}, err
		}
		projection.Unknown, projection.UnknownReason = true, reason
		return projection, nil
	}
	recorded, err := s.recordProjection(ctx, command.WorkspaceID, command.RepositoryID, projection)
	if err != nil {
		return PullRequestProjection{}, err
	}
	if err := s.settleTasks(ctx, command.WorkspaceID, projection, recorded); err != nil {
		return PullRequestProjection{}, err
	}
	return projection, nil
}

type recordedProjection struct {
	pullRequest Observation
	reviews     map[string]Observation
	checks      *Observation
}

// settleTasks turns reconciled facts into task transitions: a matching review
// or merge settles the bound external-fact task, and any task bound to a
// superseded head of the same object expires.
func (s *Service) settleTasks(
	ctx context.Context,
	workspaceID string,
	projection PullRequestProjection,
	recorded recordedProjection,
) error {
	if s.settler == nil {
		return nil
	}
	number := projection.PullRequest.Number
	head := projection.PullRequest.HeadOID
	if number == "" || head == "" {
		return nil
	}
	actor := recorded.pullRequest.ActorID
	if !validActor(actor) {
		// The task transitions need the acting identity for their audit rows.
		// Without one the facts are recorded above and the transitions are
		// skipped: an unnamed actor is never invented, and the observation
		// itself is not lost.
		return nil
	}
	for _, kind := range []string{"pull_request_review", "pull_request_merge"} {
		if _, err := s.settler.ExpireBoundTo(ctx, workspaceID, kind, number, head,
			"the pull request head is now "+head, actor); err != nil {
			return err
		}
	}
	if projection.PullRequest.Merged {
		if _, err := s.settler.ResolveForObject(ctx, workspaceID, "pull_request_merge", number, head,
			recorded.pullRequest.ObservationID, actor); err != nil {
			return err
		}
	}
	for _, review := range projection.Reviews {
		if review.State != "APPROVED" || review.CommitOID != head {
			continue
		}
		if review.ReviewerID == projection.PullRequest.AuthorID {
			// The author's own approval never satisfies the review gate.
			continue
		}
		observation, found := recorded.reviews[review.ReviewID]
		if !found {
			continue
		}
		if _, err := s.settler.ResolveForObject(ctx, workspaceID, "pull_request_review", number, head,
			observation.ObservationID, actor); err != nil {
			return err
		}
	}
	return nil
}

// recordProjection persists the observations of one projection.
func (s *Service) recordProjection(
	ctx context.Context,
	workspaceID, repositoryID string,
	projection PullRequestProjection,
) (recordedProjection, error) {
	pr := projection.PullRequest
	if pr.Number == "" || !oidPattern.MatchString(pr.HeadOID) || !validActor(pr.AuthorID) {
		return recordedProjection{}, fmt.Errorf("%w: pull request facts are incomplete", ErrValidation)
	}
	observation := Observation{
		WorkspaceID: workspaceID, RepositoryID: repositoryID,
		SubjectKind: SubjectPullRequest, SubjectKey: pr.Number, ActorID: projection.ActorID,
	}
	if err := s.stampObservation(&observation); err != nil {
		return recordedProjection{}, err
	}
	fact := pr
	observation.PullRequest = &fact
	recorded, err := s.store.RecordObservation(ctx, RecordObservationMutation{Observation: observation})
	if err != nil {
		return recordedProjection{}, err
	}
	result := recordedProjection{pullRequest: recorded, reviews: map[string]Observation{}}
	for _, review := range projection.Reviews {
		if review.ReviewID == "" || !validActor(review.ReviewerID) {
			return recordedProjection{}, fmt.Errorf("%w: review facts are incomplete", ErrValidation)
		}
		reviewObservation := Observation{
			WorkspaceID: workspaceID, RepositoryID: repositoryID,
			SubjectKind: SubjectReview, SubjectKey: pr.Number + "/" + review.ReviewID,
			ActorID: projection.ActorID,
		}
		if err := s.stampObservation(&reviewObservation); err != nil {
			return recordedProjection{}, err
		}
		reviewFact := review
		reviewObservation.Review = &reviewFact
		recordedReview, err := s.store.RecordObservation(ctx, RecordObservationMutation{Observation: reviewObservation})
		if err != nil {
			return recordedProjection{}, err
		}
		result.reviews[review.ReviewID] = recordedReview
	}
	if projection.Checks != nil {
		checks := *projection.Checks
		checksObservation := Observation{
			WorkspaceID: workspaceID, RepositoryID: repositoryID,
			SubjectKind: SubjectChecks, SubjectKey: checks.CommitOID, ActorID: projection.ActorID,
		}
		if err := s.stampObservation(&checksObservation); err != nil {
			return recordedProjection{}, err
		}
		checksObservation.Checks = &checks
		recordedChecks, err := s.store.RecordObservation(ctx, RecordObservationMutation{Observation: checksObservation})
		if err != nil {
			return recordedProjection{}, err
		}
		result.checks = &recordedChecks
	}
	return result, nil
}

func (s *Service) recordUnknown(
	ctx context.Context,
	operation ExternalOperation,
	subjectKind, subjectKey, reason string,
) (Observation, error) {
	observation := Observation{
		WorkspaceID: operation.WorkspaceID, RepositoryID: operation.Request.RepositoryID,
		SubjectKind: subjectKind, SubjectKey: subjectKey,
		Unknown: true, UnknownReason: reason,
	}
	if err := s.stampObservation(&observation); err != nil {
		return Observation{}, err
	}
	return s.store.RecordObservation(ctx, RecordObservationMutation{Observation: observation})
}

func (s *Service) newObservation(operation ExternalOperation, subjectKind, subjectKey string) (Observation, error) {
	observation := Observation{
		WorkspaceID: operation.WorkspaceID, RepositoryID: operation.Request.RepositoryID,
		SubjectKind: subjectKind, SubjectKey: subjectKey,
	}
	if err := s.stampObservation(&observation); err != nil {
		return Observation{}, err
	}
	return observation, nil
}

// stampObservation assigns the server-side identity and watermark.
func (s *Service) stampObservation(observation *Observation) error {
	id, err := s.ids.New()
	if err != nil {
		return fmt.Errorf("generate observation id: %w", err)
	}
	observation.ObservationID = id
	observation.ObservedAt = s.now().UTC()
	if observation.Unknown && observation.UnknownReason == "" {
		observation.UnknownReason = "unreadable"
	}
	return nil
}

// DeliveryView projects the delivery facts of one WorkItem. It never computes
// a business verdict: the facts and their watermarks are reported as observed.
func (s *Service) DeliveryView(ctx context.Context, workspaceID, workItemID string) (DeliveryView, error) {
	if workspaceID == "" || workItemID == "" {
		return DeliveryView{}, ErrValidation
	}
	item, err := s.workitems.Get(ctx, workspaceID, workItemID)
	if err != nil {
		return DeliveryView{}, err
	}
	bindings, err := s.workitems.RunBindings(ctx, workspaceID, workItemID)
	if err != nil {
		return DeliveryView{}, err
	}
	operations, err := s.store.Operations(ctx, workspaceID, workItemID)
	if err != nil {
		return DeliveryView{}, err
	}
	facts, err := s.store.LatestObservations(ctx, workspaceID, item.TargetRepositoryID)
	if err != nil {
		return DeliveryView{}, err
	}
	view := DeliveryView{WorkItem: item, Bindings: bindings, Operations: operations, Facts: facts}
	if s.evidence == nil {
		return view, nil
	}
	view.Verification, err = s.evidence.VerificationsFor(ctx, workspaceID, workItemID)
	if err != nil {
		return DeliveryView{}, err
	}
	closure, found, err := s.evidence.Closure(ctx, workspaceID, workItemID)
	if err != nil {
		return DeliveryView{}, err
	}
	if found {
		view.Closure = &closure
	}
	if view.Verification == nil {
		view.Verification = []evaluation.Verification{}
	}
	if view.Facts == nil {
		view.Facts = []Observation{}
	}
	if view.Operations == nil {
		view.Operations = []ExternalOperation{}
	}
	return view, nil
}

// Observation reads one recorded observation of this workspace.
func (s *Service) Observation(ctx context.Context, workspaceID, observationID string) (Observation, bool, error) {
	if workspaceID == "" || observationID == "" {
		return Observation{}, false, ErrValidation
	}
	return s.store.Observation(ctx, workspaceID, observationID)
}

// UnsettledOperations counts the operations whose outcome is not reconciled;
// the WorkItem service refuses cancellation while any of them is open.
func (s *Service) UnsettledOperations(ctx context.Context, workspaceID, workItemID string) (int, error) {
	if workspaceID == "" || workItemID == "" {
		return 0, ErrValidation
	}
	return s.store.UnsettledOperations(ctx, workspaceID, workItemID)
}

// FactSettlesObject reports whether one recorded observation establishes the
// bound object of an external-fact human task. The rule lives here; callers
// only read the observation row.
func FactSettlesObject(observation Observation, objectKind, objectID, objectVersion string) bool {
	if observation.Unknown {
		return false
	}
	switch objectKind {
	case "pull_request_review":
		return observation.SubjectKind == SubjectReview && observation.Review != nil &&
			observation.Review.State == "APPROVED" &&
			observation.Review.CommitOID == objectVersion &&
			observation.SubjectKey == objectID+"/"+observation.Review.ReviewID
	case "pull_request_merge":
		return observation.SubjectKind == SubjectPullRequest && observation.PullRequest != nil &&
			observation.PullRequest.Merged &&
			observation.PullRequest.HeadOID == objectVersion &&
			observation.PullRequest.Number == objectID
	default:
		return false
	}
}

func branchOf(ref string) (string, bool) {
	branch, found := strings.CutPrefix(ref, "refs/heads/")
	if !found || branch == "" {
		return "", false
	}
	return branch, true
}

func requestDigest(operation ExternalOperation) string {
	return "sha256:" + Digest("delivery.request", operation.Request.RepositoryID, operation.Request.Role,
		operation.Request.Operation, operation.Request.Ref, operation.Request.BaseRef,
		operation.Request.CommitOID, operation.Request.ExpectedGitHubUserID,
		operation.Request.RequestedByGitHubUserID)
}
