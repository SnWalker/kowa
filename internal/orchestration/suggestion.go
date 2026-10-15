package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

// The auxiliary knowledge-suggestion branch. It exists exactly once per
// admitted run: right before the closure commits, the control plane freezes a
// pending attempt, and a worker later generates and publishes the suggestion
// artifact. The branch never blocks the delivery closure, never writes the
// knowledge repository and never changes a business fact.
const (
	// SuggestionArtifactKind is the artifact kind of one published suggestion.
	SuggestionArtifactKind = "knowledge_suggestion"
	// suggestionLease bounds one claim: after it expires the attempt may be
	// re-claimed by a restarted or competing worker.
	suggestionLease = 2 * time.Minute
	// suggestionScanLimit bounds how many attempts one worker pass processes.
	suggestionScanLimit = 16
	// suggestionPublishBound bounds the artifact work of one attempt: the
	// artifact store serialises same-workspace writes on its own lock, so a
	// hung store must never hold that lock longer than this bound.
	suggestionPublishBound = 5 * time.Second
	// suggestionSettleBound bounds the failure bookkeeping, which runs even
	// when the attempt's own context was cancelled by its bound.
	suggestionSettleBound = 10 * time.Second
)

// seedKnowledgeSuggestion freezes the pending attempt of the auxiliary branch.
// It runs right before the closure transaction: the pending fact is durable and
// auditable even if the process dies immediately afterwards, and the closure
// itself never waits for any artifact work.
func (s *Service) seedKnowledgeSuggestion(
	ctx context.Context,
	admission Admission,
) error {
	digest, err := s.suggestionInputDigest(ctx, admission)
	if err != nil {
		return err
	}
	if _, err := s.store.SeedSuggestion(ctx, SuggestionSeed{
		RunID: admission.RunID, WorkspaceID: admission.WorkspaceID,
		WorkItemID: admission.WorkItemID, InputDigest: digest, At: s.now().UTC(),
	}); err != nil {
		return fmt.Errorf("freeze the knowledge suggestion branch: %w", err)
	}
	return nil
}

// suggestionInputDigest freezes everything the suggestion derives from: the
// frozen definition, the admitted baselines, the confirmed knowledge input, the
// merge fact and the delivered artifacts of this run (never the suggestion
// artifacts themselves). The input may not drift between attempts.
func (s *Service) suggestionInputDigest(
	ctx context.Context,
	admission Admission,
) (string, error) {
	recommendation, err := s.store.Recommendation(ctx, admission.RunID)
	if err != nil {
		return "", err
	}
	merge, found, err := s.mergeObservation(ctx, admission)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("%w: the run has no reconciled merge fact", ErrPolicyBlocked)
	}
	deliverables, err := s.suggestionDeliverables(ctx, admission)
	if err != nil {
		return "", err
	}
	parts := []string{
		"knowledge-suggestion.v1", admission.RunID, admission.WorkspaceID, admission.WorkItemID,
		admission.DefinitionName, fmt.Sprint(admission.DefinitionVersion), admission.DefinitionDigest,
		admission.ProjectRepositoryID, admission.ProjectCommitOID,
		admission.KnowledgeRepositoryID, admission.KnowledgeCommitOID,
		recommendation.SnapshotID, recommendation.BindingEventID, recommendation.EffectiveInputID,
		merge.PullRequest.MergeCommitOID,
	}
	for _, deliverable := range deliverables {
		parts = append(parts, deliverable.ArtifactID+":"+deliverable.Digest)
	}
	return "sha256:" + commandDigest(parts...), nil
}

// suggestionDeliverables lists the run's published deliverables that form the
// suggestion input. The suggestion artifacts of the branch itself are excluded,
// so a retry can never feed its own output back into its input.
func (s *Service) suggestionDeliverables(
	ctx context.Context,
	admission Admission,
) ([]ArtifactRecord, error) {
	records, err := s.store.ArtifactsForWorkItem(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return nil, err
	}
	deliverables := []ArtifactRecord{}
	for _, record := range records {
		if record.Kind == SuggestionArtifactKind {
			continue
		}
		if string(artifact.Published) != record.State {
			continue
		}
		deliverables = append(deliverables, record)
	}
	return deliverables, nil
}

// ProcessSuggestions claims and processes the auxiliary attempts that are
// ready: pending ones, or processing ones whose lease expired (a crash or a
// restart). It returns how many attempts it considered.
func (s *Service) ProcessSuggestions(ctx context.Context) (int, error) {
	now := s.now().UTC()
	claimable, err := s.store.ClaimableSuggestions(ctx, now, suggestionScanLimit)
	if err != nil {
		return 0, err
	}
	processed := 0
	var failures []error
	for _, attempt := range claimable {
		processed++
		if err := s.processSuggestion(ctx, attempt); err != nil {
			failures = append(failures, fmt.Errorf("suggestion %s: %w", attempt.AttemptID, err))
		}
	}
	return processed, errors.Join(failures...)
}

// processSuggestion drives one claimable attempt to a settled state. The
// attempt row is the single source of truth of the outcome; the auxiliary
// verification evidence mirrors it. A late or restarted worker re-reads both
// and converges without publishing twice or overwriting a settled result.
func (s *Service) processSuggestion(ctx context.Context, attempt SuggestionAttempt) error {
	// Only a run that closed its work item with SUCCEEDED is processed, and
	// only through the closure record that names it: anything else stays
	// untouched.
	run, err := s.execution.Run(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	if run.State != execution.RunSucceeded {
		return nil
	}
	admission, err := s.store.Admission(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	closure, found, err := s.evaluation.Closure(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return err
	}
	if !found || closure.RunID != attempt.RunID {
		return nil
	}
	claimed, err := s.store.ClaimSuggestion(ctx, SuggestionClaim{
		AttemptID: attempt.AttemptID, LeaseUntil: s.now().UTC().Add(suggestionLease), At: s.now().UTC(),
	})
	if err != nil {
		// Another worker holds the lease, or the attempt already settled with
		// other content: this pass simply leaves it.
		if errorsIs(err, ErrVersionConflict) || errorsIs(err, ErrPolicyBlocked) || errorsIs(err, ErrNotFound) {
			return nil
		}
		return err
	}
	// The frozen input may not drift: an attempt whose input no longer matches
	// the seeded digest is failed, never published.
	digest, err := s.suggestionInputDigest(ctx, admission)
	if err != nil {
		return s.failSuggestion(ctx, admission, claimed, closure, "the frozen input cannot be read: "+classifyFailure(err))
	}
	if digest != claimed.InputDigest {
		return s.failSuggestion(ctx, admission, claimed, closure, "the frozen input of the run drifted")
	}
	// A previous worker of this very attempt may already have recorded the
	// outcome; the evidence row is the mirror and decides the settlement.
	recorded, err := s.suggestionEvidence(ctx, admission, claimed)
	if err != nil {
		return err
	}
	if recorded != nil {
		if recorded.Result == evaluation.ResultPassed {
			_, err := s.settleSuggestion(ctx, claimed, SuggestionOutcome{
				RunID: claimed.RunID, Attempt: claimed.Attempt, ExpectedVersion: claimed.Version,
				ArtifactID: recorded.ReportArtifactID, ArtifactDigest: recorded.ReportDigest,
				At: s.now().UTC(),
			})
			return err
		}
		return s.failSuggestion(ctx, admission, claimed, closure, "the previous attempt recorded a failure")
	}
	// Reuse the artifact a crashed worker published for this very attempt, or
	// publish it now — always under the short bound, never under the caller's
	// longer pass budget.
	publishCtx, cancel := context.WithTimeout(ctx, suggestionPublishBound)
	reference, published, err := s.publishSuggestionArtifact(publishCtx, admission, claimed)
	cancel()
	if err != nil {
		return s.failSuggestion(ctx, admission, claimed, closure, classifyFailure(err))
	}
	if !published {
		return s.failSuggestion(ctx, admission, claimed, closure, "the suggestion artifact could not be published")
	}
	if _, err := s.recordSuggestionEvidence(ctx, admission, claimed, closure, evaluation.ResultPassed, reference); err != nil {
		return err
	}
	_, err = s.settleSuggestion(ctx, claimed, SuggestionOutcome{
		RunID: claimed.RunID, Attempt: claimed.Attempt, ExpectedVersion: claimed.Version,
		ArtifactID: reference.ArtifactID, ArtifactDigest: reference.Digest, At: s.now().UTC(),
	})
	return err
}

// failSuggestion records a failed outcome: the auxiliary evidence first, the
// attempt row second, so a crash in between converges on re-processing. The
// bookkeeping survives a cancelled attempt context — the bound that cancelled
// the artifact work must not also lose the failure — and a settled FAILED
// attempt is retried only by an explicit command, never by the worker loop.
func (s *Service) failSuggestion(
	ctx context.Context,
	admission Admission,
	attempt SuggestionAttempt,
	closure evaluation.ClosureRecord,
	reason string,
) error {
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), suggestionSettleBound)
	defer cancel()
	if _, err := s.recordSuggestionEvidence(settleCtx, admission, attempt, closure, evaluation.ResultFailed, artifact.Ref{}); err != nil {
		// The failure could not be recorded; the attempt stays claimed until
		// its lease expires, so the next pass retries instead of losing it.
		return err
	}
	_, err := s.settleSuggestion(settleCtx, attempt, SuggestionOutcome{
		RunID: attempt.RunID, Attempt: attempt.Attempt, ExpectedVersion: attempt.Version,
		FailureReason: truncateReason(reason), At: s.now().UTC(),
	})
	return err
}

// settleSuggestion finalises one claimed attempt. A lease that moved on is not
// an error: the newer worker owns the settlement.
func (s *Service) settleSuggestion(
	ctx context.Context,
	attempt SuggestionAttempt,
	outcome SuggestionOutcome,
) (SuggestionAttempt, error) {
	settled, err := s.store.FinalizeSuggestion(ctx, outcome)
	if err != nil && errorsIs(err, ErrVersionConflict) {
		return SuggestionAttempt{}, nil
	}
	return settled, err
}

// suggestionEvidence reads the auxiliary evidence one attempt already recorded,
// if any. The producer identity of the evidence is the attempt identity.
func (s *Service) suggestionEvidence(
	ctx context.Context,
	admission Admission,
	attempt SuggestionAttempt,
) (*evaluation.Verification, error) {
	verifications, err := s.evaluation.Verifications(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return nil, err
	}
	for index := range verifications {
		verification := verifications[index]
		if verification.Kind == evaluation.KindKnowledgeSuggestion &&
			verification.ProducerID == attempt.AttemptID {
			return &verification, nil
		}
	}
	return nil, nil
}

// recordSuggestionEvidence mirrors one attempt outcome into the auxiliary
// verification evidence. The request key is the attempt identity, so a replay
// of the very same outcome is idempotent.
func (s *Service) recordSuggestionEvidence(
	ctx context.Context,
	admission Admission,
	attempt SuggestionAttempt,
	closure evaluation.ClosureRecord,
	result string,
	reference artifact.Ref,
) (evaluation.Verification, error) {
	command := evaluation.RecordVerificationCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID,
		RunID: admission.RunID, Kind: evaluation.KindKnowledgeSuggestion,
		SubjectKind: "commit", RepositoryID: admission.ProjectRepositoryID,
		CommitOID:            closure.MergeCommitOID,
		ConfigurationVersion: s.configVersion(admission),
		ProducerKind:         evaluation.ProducerControlPlane, ProducerID: attempt.AttemptID,
		Source: evaluation.SourceReal, Result: result,
		RequestKey: "fixedflow.knowledge-suggestion:" + attempt.AttemptID,
		ActorID:    admission.AdmittedByUserID,
	}
	if result == evaluation.ResultPassed {
		command.ReportArtifactID = reference.ArtifactID
		command.ReportDigest = reference.Digest
	}
	verification, err := s.evaluation.RecordVerification(ctx, command)
	if err != nil {
		return evaluation.Verification{}, fmt.Errorf("record the suggestion evidence: %w", err)
	}
	return verification, nil
}

// publishSuggestionArtifact reuses the artifact a crashed worker of this very
// attempt already published; otherwise it uploads and publishes the canonical
// suggestion document. The producer identity is the attempt identity, so a
// retry of the same attempt can never publish twice.
func (s *Service) publishSuggestionArtifact(
	ctx context.Context,
	admission Admission,
	attempt SuggestionAttempt,
) (artifact.Ref, bool, error) {
	records, err := s.store.ArtifactsForWorkItem(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return artifact.Ref{}, false, err
	}
	for _, record := range records {
		if record.Kind == SuggestionArtifactKind && record.ProducerID == attempt.AttemptID &&
			record.State == string(artifact.Published) {
			return artifact.Ref{
				SchemaVersion: artifact.SchemaVersion, ArtifactID: record.ArtifactID,
				WorkspaceID: admission.WorkspaceID, SubjectID: record.SubjectID,
				Kind: record.Kind, Producer: artifact.Producer{Kind: record.ProducerKind, ID: record.ProducerID},
				Digest: record.Digest, Size: record.Size, MediaType: record.MediaType,
				RequiresApproval: record.RequiresApproval,
				StorageRef:       "artifact:" + record.ArtifactID,
			}, true, nil
		}
	}
	content, err := s.suggestionContent(ctx, admission, attempt)
	if err != nil {
		return artifact.Ref{}, false, err
	}
	scope := artifact.Scope{WorkspaceID: admission.WorkspaceID, ActorID: admission.AdmittedByUserID}
	reference, err := s.artifacts.Upload(ctx, scope, artifact.Upload{
		RequiresApproval: false, SubjectID: admission.WorkItemID, Kind: SuggestionArtifactKind,
		MediaType: "application/json", Digest: artifact.Digest(content),
		Producer: artifact.Producer{Kind: "operation", ID: attempt.AttemptID},
	}, content)
	if err != nil {
		return artifact.Ref{}, false, fmt.Errorf("upload the suggestion artifact: %w", err)
	}
	if err := s.artifacts.Publish(ctx, scope, reference); err != nil {
		return artifact.Ref{}, false, fmt.Errorf("publish the suggestion artifact: %w", err)
	}
	return reference, true, nil
}

// suggestionContent assembles the canonical suggestion document from the
// frozen facts of the run. It is a review aid for people: the entries that may
// need an update, bound to the exact delivery and knowledge input they came
// from. It never writes the knowledge repository.
func (s *Service) suggestionContent(
	ctx context.Context,
	admission Admission,
	attempt SuggestionAttempt,
) ([]byte, error) {
	recommendation, err := s.store.Recommendation(ctx, admission.RunID)
	if err != nil {
		return nil, err
	}
	merge, found, err := s.mergeObservation(ctx, admission)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: the run has no reconciled merge fact", ErrPolicyBlocked)
	}
	deliverables, err := s.suggestionDeliverables(ctx, admission)
	if err != nil {
		return nil, err
	}
	type deliverableView struct {
		ArtifactID string `json:"artifactId"`
		Kind       string `json:"kind"`
		Digest     string `json:"digest"`
	}
	type entryView struct {
		Path     string `json:"path"`
		Digest   string `json:"digest"`
		Scope    string `json:"scope"`
		Required bool   `json:"required"`
	}
	document := struct {
		SchemaVersion     string `json:"schemaVersion"`
		Kind              string `json:"kind"`
		RunID             string `json:"workflowRunId"`
		WorkspaceID       string `json:"workspaceId"`
		WorkItemID        string `json:"workItemId"`
		DefinitionName    string `json:"definitionName"`
		DefinitionVersion int    `json:"definitionVersion"`
		DefinitionDigest  string `json:"definitionDigest"`
		MergeCommitOID    string `json:"mergeCommitOid"`
		Knowledge         struct {
			SnapshotID       string `json:"snapshotId"`
			BindingEventID   string `json:"bindingEventId"`
			EffectiveInputID string `json:"effectiveInputId"`
			IndexPath        string `json:"indexPath"`
			IndexDigest      string `json:"indexDigest"`
		} `json:"knowledge"`
		Deliverables []deliverableView `json:"deliverables"`
		Entries      []entryView       `json:"entries"`
		InputDigest  string            `json:"inputDigest"`
		Note         string            `json:"note"`
	}{
		SchemaVersion: SchemaVersion, Kind: "knowledgeSuggestion",
		RunID: admission.RunID, WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID,
		DefinitionName: admission.DefinitionName, DefinitionVersion: admission.DefinitionVersion,
		DefinitionDigest: admission.DefinitionDigest,
		MergeCommitOID:   merge.PullRequest.MergeCommitOID,
		Deliverables:     []deliverableView{}, Entries: []entryView{},
		InputDigest: attempt.InputDigest,
		Note: "Auxiliary review aid: the delivered artifacts and the frozen knowledge entries of this run. " +
			"Kowa never writes the knowledge repository; a person decides whether any entry needs an update.",
	}
	document.Knowledge.SnapshotID = recommendation.SnapshotID
	document.Knowledge.BindingEventID = recommendation.BindingEventID
	document.Knowledge.EffectiveInputID = recommendation.EffectiveInputID
	document.Knowledge.IndexPath = recommendation.IndexPath
	document.Knowledge.IndexDigest = recommendation.IndexDigest
	for _, deliverable := range deliverables {
		document.Deliverables = append(document.Deliverables, deliverableView{
			ArtifactID: deliverable.ArtifactID, Kind: deliverable.Kind, Digest: deliverable.Digest,
		})
	}
	for _, candidate := range recommendation.Candidates {
		document.Entries = append(document.Entries, entryView(candidate))
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode the suggestion document: %w", err)
	}
	return append(encoded, '\n'), nil
}

// classifyFailure turns an internal error into a short, sanitized reason. Raw
// internals (paths, SQL, provider text) never reach the projection.
func classifyFailure(err error) string {
	switch {
	case errorsIs(err, ErrPolicyBlocked):
		return "the run no longer allows the suggestion"
	case errorsIs(err, ErrNotFound):
		return "a frozen fact of the run is unavailable"
	case errorsIs(err, ErrResourceUnavailable), errorsIs(err, artifact.ErrUnavailable):
		return "the artifact store is unavailable"
	default:
		return "the suggestion could not be produced"
	}
}

// truncateReason bounds one failure reason to the column width.
func truncateReason(reason string) string {
	reason = strings.TrimSpace(strings.ReplaceAll(reason, "\n", " "))
	if len(reason) > 500 {
		reason = reason[:500]
	}
	if reason == "" {
		reason = "the suggestion could not be produced"
	}
	return reason
}

// RetrySuggestion opens the next auxiliary attempt after a failed one. It is
// only legal for a work item that closed through this very run, and it never
// touches the run, the item or the closure.
func (s *Service) retrySuggestion(ctx context.Context, command RecoveryCommand) (string, error) {
	run, err := s.execution.Run(ctx, command.RunID)
	if err != nil {
		return "", err
	}
	if run.State != execution.RunSucceeded {
		return "", fmt.Errorf(
			"%w: the suggestion branch of a run that is %s cannot be retried", ErrPolicyBlocked, run.State)
	}
	item, err := s.workitems.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return "", err
	}
	if item.State != workitem.StateClosed {
		return "", fmt.Errorf(
			"%w: the suggestion branch is retried after the work item closed", ErrPolicyBlocked)
	}
	closure, found, err := s.evaluation.Closure(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return "", err
	}
	if !found || closure.RunID != command.RunID {
		return "", fmt.Errorf(
			"%w: the run is not the closure of this work item", ErrPolicyBlocked)
	}
	attempt, err := s.store.RequestSuggestion(ctx, SuggestionRequest{RunID: command.RunID, At: s.now().UTC()})
	if err != nil {
		if errorsIs(err, ErrPolicyBlocked) || errorsIs(err, ErrNotFound) || errorsIs(err, ErrVersionConflict) {
			return "", err
		}
		return "", err
	}
	return fmt.Sprintf("suggestion attempt %d was requested", attempt.Attempt), nil
}
