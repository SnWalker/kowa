package evaluation

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

// Service implements verification recording and the closure decision.
type Service struct {
	store     Store
	workitems WorkItemSource
	runs      RunSource
	facts     FactSource
	ids       IDGenerator
	now       func() time.Time
}

func NewService(
	store Store,
	workitems WorkItemSource,
	runs RunSource,
	facts FactSource,
	ids IDGenerator,
	now func() time.Time,
) *Service {
	return &Service{store: store, workitems: workitems, runs: runs, facts: facts, ids: ids, now: now}
}

// RecordVerification records one verification fact. Simulated evidence is
// stored honestly and marked, so a real gate can never be satisfied by it.
func (s *Service) RecordVerification(ctx context.Context, command RecordVerificationCommand) (Verification, error) {
	if !validRequestKey(command.RequestKey) || !validActor(command.ActorID) ||
		command.WorkspaceID == "" || command.WorkItemID == "" ||
		!KnownKind(command.Kind) || !validSubject(command) ||
		command.ConfigurationVersion == "" || len(command.ConfigurationVersion) > 200 ||
		!validProducer(command.ProducerKind) || !identifierPattern.MatchString(command.ProducerID) {
		return Verification{}, ErrValidation
	}
	if command.Result != ResultPassed && command.Result != ResultFailed {
		return Verification{}, ErrValidation
	}
	if command.Source != SourceReal && command.Source != SourceSimulated {
		return Verification{}, ErrValidation
	}
	if command.ReportDigest != "" && !digestPattern.MatchString(command.ReportDigest) {
		return Verification{}, ErrValidation
	}
	id, err := s.ids.New()
	if err != nil {
		return Verification{}, fmt.Errorf("generate verification id: %w", err)
	}
	now := s.now().UTC()
	verification := Verification{
		VerificationID: id, WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		RunID: command.RunID, NodeRunID: command.NodeRunID, Kind: command.Kind,
		SubjectKind: command.SubjectKind, RepositoryID: command.RepositoryID, CommitOID: command.CommitOID,
		ArtifactID: command.ArtifactID, ArtifactDigest: command.ArtifactDigest,
		ConfigurationVersion: command.ConfigurationVersion, ProducerKind: command.ProducerKind,
		ProducerID: command.ProducerID, Source: command.Source, Result: command.Result,
		ReportArtifactID: command.ReportArtifactID, ReportDigest: command.ReportDigest,
		ObservedAt: now, CreatedAt: now,
	}
	return s.store.RecordVerification(ctx, verification, command.RequestKey, Digest(
		"evaluation.record_verification", command.WorkspaceID, command.WorkItemID, command.Kind,
		command.SubjectKind, command.RepositoryID, command.CommitOID, command.ArtifactID,
		command.Result, command.Source, command.RequestKey))
}

// Close validates the closing checklist against the current facts and commits
// the run success, the WorkItem close and the closure record in one
// authoritative transaction. Every referenced fact is re-read here; the caller
// supplies identities, never verdicts.
func (s *Service) Close(ctx context.Context, command CloseCommand) (ClosureRecord, error) {
	if !validRequestKey(command.RequestKey) || !validActor(command.ActorID) ||
		command.WorkspaceID == "" || command.WorkItemID == "" || command.RunID == "" ||
		command.ExpectedWorkItemVersion < 1 || command.ExpectedRunVersion < 1 ||
		command.PolicyVersion == "" || len(command.PolicyVersion) > 200 ||
		!externalIDPattern.MatchString(command.PullRequestNumber) ||
		command.MergeObservationID == "" || command.ReviewObservationID == "" ||
		command.IndependentReviewID == "" || command.PostMergeVerificationID == "" ||
		len(command.RequiredTestIDs) == 0 {
		return ClosureRecord{}, ErrValidation
	}
	// Knowledge references are consumed from S03, not re-owned here: the
	// closure binds their exact identities for audit, so they must at least be
	// well formed. Their existence and confirmation stay S03's authority.
	for _, knowledgeReference := range []string{command.KnowledgeSnapshotID, command.KnowledgeBindingEventID} {
		if knowledgeReference != "" && !identifierPattern.MatchString(knowledgeReference) {
			return ClosureRecord{}, ErrValidation
		}
	}
	item, err := s.workitems.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return ClosureRecord{}, err
	}
	if item.State != workitem.StateOpen {
		return ClosureRecord{}, fmt.Errorf("%w: the work item is %s", ErrPolicyBlocked, item.State)
	}
	if item.Version != command.ExpectedWorkItemVersion {
		return ClosureRecord{}, ErrVersionConflict
	}
	if item.CurrentRunID != command.RunID {
		return ClosureRecord{}, fmt.Errorf(
			"%w: the closure must bind the current delivery run of the work item", ErrPolicyBlocked)
	}
	run, err := s.runs.Run(ctx, command.RunID)
	if err != nil {
		return ClosureRecord{}, err
	}
	if run.WorkspaceID != command.WorkspaceID || run.WorkItemID != command.WorkItemID {
		return ClosureRecord{}, fmt.Errorf("%w: the run does not belong to the work item", ErrPolicyBlocked)
	}
	if err := execution.CompletionAllowed(run); err != nil {
		return ClosureRecord{}, fmt.Errorf("%w: the delivery run cannot be completed", ErrPolicyBlocked)
	}
	if run.Version != command.ExpectedRunVersion {
		return ClosureRecord{}, ErrVersionConflict
	}
	if run.DefinitionDigest == "" || run.DefinitionName == "" || run.DefinitionVersion < 1 {
		return ClosureRecord{}, fmt.Errorf("%w: the run has no frozen definition to bind", ErrPolicyBlocked)
	}

	merge, err := s.observation(ctx, command.WorkspaceID, command.MergeObservationID)
	if err != nil {
		return ClosureRecord{}, err
	}
	if merge.Unknown || merge.SubjectKind != "pull_request" || merge.PullRequestAuthorID == "" ||
		!merge.Merged || !oidPattern.MatchString(merge.MergeCommitOID) || !oidPattern.MatchString(merge.HeadOID) ||
		merge.RepositoryID != item.TargetRepositoryID || merge.SubjectKey != command.PullRequestNumber {
		return ClosureRecord{}, fmt.Errorf(
			"%w: the merge observation does not establish the merged delivery of this work item", ErrPolicyBlocked)
	}
	review, err := s.observation(ctx, command.WorkspaceID, command.ReviewObservationID)
	if err != nil {
		return ClosureRecord{}, err
	}
	if review.Unknown || review.SubjectKind != "pull_request_review" || review.ReviewState != "APPROVED" ||
		review.ReviewID == "" || review.ReviewCommitOID != merge.HeadOID ||
		!validActor(review.ReviewerID) {
		return ClosureRecord{}, fmt.Errorf(
			"%w: the review observation is not an approval of the current head", ErrPolicyBlocked)
	}
	if review.ReviewerID == merge.PullRequestAuthorID {
		return ClosureRecord{}, fmt.Errorf(
			"%w: the pull request author cannot be its approving reviewer", ErrPolicyBlocked)
	}

	testIDs := make([]string, 0, len(command.RequiredTestIDs))
	for _, id := range command.RequiredTestIDs {
		if slices.Contains(testIDs, id) {
			return ClosureRecord{}, ErrValidation
		}
		testIDs = append(testIDs, id)
		if err := s.requireVerification(ctx, command.WorkspaceID, id, KindRequiredTests,
			item.TargetRepositoryID, merge.HeadOID); err != nil {
			return ClosureRecord{}, err
		}
	}
	if err := s.requireVerification(ctx, command.WorkspaceID, command.IndependentReviewID,
		KindIndependentReview, item.TargetRepositoryID, merge.HeadOID); err != nil {
		return ClosureRecord{}, err
	}
	if err := s.requireVerification(ctx, command.WorkspaceID, command.PostMergeVerificationID,
		KindPostMergeIntegration, item.TargetRepositoryID, merge.MergeCommitOID); err != nil {
		return ClosureRecord{}, err
	}

	id, err := s.ids.New()
	if err != nil {
		return ClosureRecord{}, fmt.Errorf("generate closure id: %w", err)
	}
	now := s.now().UTC()
	record := ClosureRecord{
		ClosureID: id, WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		RunID:          command.RunID,
		DefinitionName: run.DefinitionName, DefinitionVersion: run.DefinitionVersion,
		DefinitionDigest: run.DefinitionDigest, PolicyVersion: command.PolicyVersion,
		RequirementDigest:   item.Requirement.Digest,
		KnowledgeSnapshotID: command.KnowledgeSnapshotID, KnowledgeBindingID: command.KnowledgeBindingEventID,
		MergeRepositoryID: merge.RepositoryID, MergeCommitOID: merge.MergeCommitOID,
		PullRequestNumber: command.PullRequestNumber, MergeObservationID: command.MergeObservationID,
		ReviewObservationID: command.ReviewObservationID, ReviewHeadOID: merge.HeadOID,
		ReviewerGitHubUserID: review.ReviewerID, RequiredTestIDs: testIDs,
		IndependentReviewID:     command.IndependentReviewID,
		PostMergeVerificationID: command.PostMergeVerificationID,
		CreatedByGitHubUserID:   command.ActorID, CreatedAt: now,
	}
	record.ChecklistDigest = checklistDigest(record)
	return s.store.Close(ctx, CloseMutation{
		Record: record, ExpectedRunVersion: command.ExpectedRunVersion,
		ExpectedWorkItemVersion: command.ExpectedWorkItemVersion,
		ActorID:                 command.ActorID, RequestKey: command.RequestKey,
		RequestDigest: Digest("evaluation.close", command.WorkspaceID, command.WorkItemID, command.RunID,
			command.MergeObservationID, command.ReviewObservationID, command.IndependentReviewID,
			command.PostMergeVerificationID, strings.Join(testIDs, ","), command.RequestKey),
		ClosedAt: now,
	})
}

// Verifications returns the recorded verification facts of one WorkItem.
func (s *Service) Verifications(ctx context.Context, workspaceID, workItemID string) ([]Verification, error) {
	if workspaceID == "" || workItemID == "" {
		return nil, ErrValidation
	}
	verifications, err := s.store.VerificationsFor(ctx, workspaceID, workItemID)
	if err != nil {
		return nil, err
	}
	if verifications == nil {
		verifications = []Verification{}
	}
	return verifications, nil
}

// Verification reads one recorded verification fact of this workspace.
func (s *Service) Verification(ctx context.Context, workspaceID, verificationID string) (Verification, bool, error) {
	if workspaceID == "" || verificationID == "" {
		return Verification{}, false, ErrValidation
	}
	return s.store.Verification(ctx, workspaceID, verificationID)
}

// Closure returns the closure record of one WorkItem when it has one.
func (s *Service) Closure(ctx context.Context, workspaceID, workItemID string) (ClosureRecord, bool, error) {
	if workspaceID == "" || workItemID == "" {
		return ClosureRecord{}, false, ErrValidation
	}
	return s.store.Closure(ctx, workspaceID, workItemID)
}

func (s *Service) observation(ctx context.Context, workspaceID, observationID string) (ObservationFact, error) {
	fact, found, err := s.facts.Observation(ctx, workspaceID, observationID)
	if err != nil {
		return ObservationFact{}, err
	}
	if !found {
		return ObservationFact{}, fmt.Errorf("%w: observation %s does not exist", ErrPolicyBlocked, observationID)
	}
	return fact, nil
}

// requireVerification re-reads one evidence row and checks kind, result, source
// and the exact subject. Simulated or mismatched evidence never passes.
func (s *Service) requireVerification(
	ctx context.Context,
	workspaceID, verificationID, kind, repositoryID, commitOID string,
) error {
	verification, found, err := s.store.Verification(ctx, workspaceID, verificationID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: verification %s does not exist", ErrPolicyBlocked, verificationID)
	}
	if verification.Kind != kind || verification.Result != ResultPassed ||
		verification.Source != SourceReal || verification.SubjectKind != "commit" ||
		verification.RepositoryID != repositoryID || verification.CommitOID != commitOID {
		return fmt.Errorf(
			"%w: verification %s does not establish a passed %s on the bound commit", ErrPolicyBlocked, verificationID, kind)
	}
	return nil
}

func validSubject(command RecordVerificationCommand) bool {
	switch command.SubjectKind {
	case "commit":
		return externalIDPattern.MatchString(command.RepositoryID) && oidPattern.MatchString(command.CommitOID) &&
			command.ArtifactID == ""
	case "artifact":
		return identifierPattern.MatchString(command.ArtifactID) && digestPattern.MatchString(command.ArtifactDigest) &&
			command.CommitOID == "" && command.RepositoryID == ""
	default:
		return false
	}
}

func validProducer(kind string) bool {
	return kind == ProducerTask || kind == ProducerExternalCI || kind == ProducerControlPlane
}

func checklistDigest(record ClosureRecord) string {
	return "sha256:" + Digest("evaluation.checklist", record.WorkspaceID, record.WorkItemID, record.RunID,
		record.DefinitionName, fmt.Sprint(record.DefinitionVersion), record.DefinitionDigest,
		record.PolicyVersion, record.RequirementDigest, record.KnowledgeSnapshotID, record.KnowledgeBindingID,
		record.MergeRepositoryID, record.MergeCommitOID, record.PullRequestNumber,
		record.MergeObservationID, record.ReviewObservationID, record.ReviewHeadOID,
		record.ReviewerGitHubUserID, strings.Join(record.RequiredTestIDs, ","),
		record.IndependentReviewID, record.PostMergeVerificationID)
}
