package orchestration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/knowledge"
	"github.com/SnWalker/kowa/internal/workflow"
)

// indexDocument is the frozen knowledge index of the MVP: the repository
// declares its entries, and the human confirms the selection at the frozen
// commit. No model and no search service is involved.
type indexDocument struct {
	SchemaVersion string      `json:"schemaVersion"`
	Entries       []Candidate `json:"entries"`
}

// recommendKnowledge reads the frozen knowledge index once per run and records
// the recommendation the human confirms. A read failure is never an empty
// recommendation: an unreadable index fails closed.
func (s *Service) recommendKnowledge(ctx context.Context, admission Admission) (Recommendation, error) {
	if existing, err := s.store.Recommendation(ctx, admission.RunID); err == nil {
		return existing, nil
	} else if !isNotFound(err) {
		return Recommendation{}, err
	}
	repository, err := s.fetch.Open(ctx, admission.KnowledgeRepositoryID, admission.KnowledgeCommitOID)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: the frozen knowledge commit cannot be materialised: %v",
			ErrResourceUnavailable, err)
	}
	defer func() { _ = repository.Close() }()
	source := repository.Source()
	if err := source.Probe(ctx, admission.KnowledgeRepositoryID, admission.KnowledgeCommitOID); err != nil {
		return Recommendation{}, fmt.Errorf("%w: the frozen knowledge commit is unavailable: %v",
			ErrResourceUnavailable, err)
	}
	raw, err := source.Read(ctx, admission.KnowledgeRepositoryID, admission.KnowledgeCommitOID, KnowledgeIndexPath)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: the knowledge index %s cannot be read: %v",
			ErrResourceUnavailable, KnowledgeIndexPath, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var index indexDocument
	if err := decoder.Decode(&index); err != nil {
		return Recommendation{}, fmt.Errorf("%w: the knowledge index is not valid: %v", ErrValidation, err)
	}
	if index.SchemaVersion != SchemaVersion {
		return Recommendation{}, fmt.Errorf("%w: unsupported knowledge index schema %q",
			ErrValidation, index.SchemaVersion)
	}
	seen := map[string]bool{}
	candidates := make([]Candidate, 0, len(index.Entries))
	for _, candidate := range index.Entries {
		if candidate.Path == "" || !artifact.ValidDigest(candidate.Digest) || candidate.Scope == "" || seen[candidate.Path] {
			return Recommendation{}, fmt.Errorf("%w: the knowledge index declares an invalid entry %q",
				ErrValidation, candidate.Path)
		}
		seen[candidate.Path] = true
		candidates = append(candidates, candidate)
	}
	recommendation := Recommendation{
		RunID: admission.RunID, WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID,
		KnowledgeRepositoryID: admission.KnowledgeRepositoryID,
		KnowledgeCommitOID:    admission.KnowledgeCommitOID,
		IndexPath:             KnowledgeIndexPath, IndexDigest: artifact.Digest(raw),
		State: StateRecommended, Candidates: candidates, RecommendedAt: s.now().UTC(),
	}
	return s.store.Recommend(ctx, recommendation)
}

// knowledgeSelection validates the answer against the frozen candidates: the
// human may drop entries and re-add any entry of the frozen index, never invent
// content outside it.
func (s *Service) knowledgeSelection(recommendation Recommendation, values map[string]string) ([]knowledge.Entry, error) {
	selected := map[string]bool{}
	for _, line := range strings.Split(values["selection"], "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		selected[path] = true
	}
	entries := make([]knowledge.Entry, 0, len(recommendation.Candidates))
	for _, candidate := range recommendation.Candidates {
		if candidate.Required && !selected[candidate.Path] {
			return nil, fmt.Errorf("%w: the required knowledge entry %q cannot be dropped",
				ErrPolicyBlocked, candidate.Path)
		}
		if !selected[candidate.Path] {
			continue
		}
		entries = append(entries, knowledge.Entry{
			Path: candidate.Path, Digest: candidate.Digest, Scope: candidate.Scope, Required: candidate.Required,
		})
	}
	if len(selected) != len(entries) {
		return nil, fmt.Errorf("%w: the answer selects an entry that is not in the frozen index", ErrPolicyBlocked)
	}
	return entries, nil
}

// confirmKnowledge consumes one accepted answer: it captures and publishes the
// frozen snapshot, records the S03 binding event and assembles the run's
// effective knowledge input. Nothing is confirmed without an accepted answer.
func (s *Service) confirmKnowledge(
	ctx context.Context,
	admission Admission,
	recommendation Recommendation,
	continuation humantask.Continuation,
) (Recommendation, error) {
	task, err := s.humans.Get(ctx, admission.WorkspaceID, continuation.TaskID)
	if err != nil {
		return Recommendation{}, err
	}
	if task.Response == nil {
		return Recommendation{}, fmt.Errorf("%w: the continuation has no accepted answer", ErrPolicyBlocked)
	}
	response := task.Response
	if response.Decision == humantask.DecisionRejected {
		return Recommendation{}, errKnowledgeRejected
	}
	if response.Decision != humantask.DecisionApproved {
		return Recommendation{}, fmt.Errorf("%w: only an approval confirms the knowledge selection", ErrPolicyBlocked)
	}
	entries, err := s.knowledgeSelection(recommendation, response.Values)
	if err != nil {
		return Recommendation{}, err
	}
	repository, err := s.fetch.Open(ctx, admission.KnowledgeRepositoryID, admission.KnowledgeCommitOID)
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: the frozen knowledge commit cannot be materialised: %v",
			ErrResourceUnavailable, err)
	}
	defer func() { _ = repository.Close() }()
	snapshot, err := knowledge.Capture(ctx, repository.Source(), knowledge.Selection{
		WorkspaceID: admission.WorkspaceID, RepositoryID: admission.KnowledgeRepositoryID,
		Commit: admission.KnowledgeCommitOID, Entries: entries,
	})
	if err != nil {
		return Recommendation{}, fmt.Errorf("%w: capturing the frozen knowledge failed: %v", ErrResourceUnavailable, err)
	}
	// The confirming actor is the member who answered, never the run initiator.
	scope := artifact.Scope{WorkspaceID: admission.WorkspaceID, ActorID: response.ActorID}
	if err := s.knowledge.PublishSnapshot(ctx, scope, snapshot); err != nil {
		return Recommendation{}, err
	}
	event, err := s.confirmWithExpectedVersion(ctx, scope, admission, response, snapshot)
	if err != nil {
		return Recommendation{}, err
	}
	inputDigest := artifact.Digest(snapshot.Canonical)
	_ = inputDigest
	request := knowledge.InputRequest{
		WorkspaceID: admission.WorkspaceID, SubjectID: admission.WorkItemID,
		RequirementVersion: "", ProjectCommit: admission.ProjectCommitOID,
		DefinitionDigest: admission.DefinitionDigest, PolicyVersion: admission.PolicyVersion,
		ConfigVersion: s.configVersion(admission),
	}
	item, err := s.workitems.Get(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return Recommendation{}, err
	}
	request.RequirementVersion = item.Requirement.Digest
	input, err := s.knowledge.AssembleInput(ctx, scope, request, event.ID)
	if err != nil {
		return Recommendation{}, fmt.Errorf("assemble the knowledge input: %w", err)
	}
	return s.store.ConfirmRecommendation(ctx, ConfirmMutation{
		RunID: admission.RunID, HumanTaskID: continuation.TaskID, SnapshotID: snapshot.ID,
		BindingEventID: event.ID, EffectiveInputID: input.ID, At: s.now().UTC(),
	})
}

// errKnowledgeRejected marks a refused knowledge confirmation; the caller opens
// a new confirmation round instead of continuing the flow.
var errKnowledgeRejected = fmt.Errorf("%w: the knowledge confirmation was refused", ErrPolicyBlocked)

// confirmWithExpectedVersion records the S03 binding event. The expected
// version is derived from this module's own confirmed admissions, and a
// conflict is retried once: the only way this module's count can lag is an
// interrupted write after S03 already recorded the event.
func (s *Service) confirmWithExpectedVersion(
	ctx context.Context,
	scope artifact.Scope,
	admission Admission,
	response *humantask.Response,
	snapshot knowledge.Snapshot,
) (knowledge.BindingEvent, error) {
	expected, err := s.store.ConfirmedKnowledgeVersion(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return knowledge.BindingEvent{}, err
	}
	command := knowledge.ConfirmCommand{
		SubjectID: admission.WorkItemID, HumanResponseID: response.ResponseID,
		ExpectedVersion: int64(expected), Snapshot: snapshot,
	}
	event, err := s.knowledge.Confirm(ctx, scope, command)
	if err == nil {
		return event, nil
	}
	if !errorsIs(err, knowledge.ErrConflict) {
		return knowledge.BindingEvent{}, err
	}
	command.ExpectedVersion++
	return s.knowledge.Confirm(ctx, scope, command)
}

// assembleInput freezes one capability node's effective input: the frozen
// project and knowledge commits, the published upstream deliverables and the
// node's control-plane fields. It is only ever called once the node's
// upstreams are decided and approved.
func (s *Service) assembleInput(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	nodeRunID string,
	upstreams []knowledge.Upstream,
	fields []knowledge.Field,
) (knowledge.EffectiveInput, error) {
	recommendation, err := s.store.Recommendation(ctx, admission.RunID)
	if err != nil {
		return knowledge.EffectiveInput{}, err
	}
	if !recommendation.Confirmed() {
		return knowledge.EffectiveInput{}, fmt.Errorf(
			"%w: the knowledge binding is not confirmed for this run", ErrPolicyBlocked)
	}
	item, err := s.workitems.Get(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return knowledge.EffectiveInput{}, err
	}
	// One upstream per producer node: a node publishes one deliverable, and the
	// input contract refuses a repeated upstream name.
	required := make([]string, 0, len(node.Inputs))
	seenRequired := map[string]bool{}
	for _, binding := range node.Inputs {
		if !binding.Required || seenRequired[binding.ProducerNodeID] {
			continue
		}
		seenRequired[binding.ProducerNodeID] = true
		required = append(required, binding.ProducerNodeID)
	}
	request := knowledge.InputRequest{
		WorkspaceID: admission.WorkspaceID, SubjectID: admission.WorkItemID,
		RequirementVersion: item.Requirement.Digest, ProjectCommit: admission.ProjectCommitOID,
		DefinitionDigest: admission.DefinitionDigest, PolicyVersion: admission.PolicyVersion,
		ConfigVersion: s.configVersion(admission), Upstream: upstreams, Fields: fields,
		RequiredUpstream: required,
	}
	scope := artifact.Scope{WorkspaceID: admission.WorkspaceID, ActorID: admission.AdmittedByUserID}
	input, err := s.knowledge.AssembleInput(ctx, scope, request, recommendation.BindingEventID)
	if err != nil {
		// The frozen input identity travels with the failure: which subject,
		// commit, definition and upstreams were refused is what a diagnosis
		// needs, and it exposes no content.
		return knowledge.EffectiveInput{}, fmt.Errorf(
			"assemble the input of node %s (subject %s, requirement %s, commit %s, definition %s, policy %s, config %s, upstreams %d): %w",
			node.ID, request.SubjectID, request.RequirementVersion, request.ProjectCommit,
			request.DefinitionDigest, request.PolicyVersion, request.ConfigVersion,
			len(request.Upstream), err)
	}
	_ = nodeRunID
	return input, nil
}

// configVersion freezes the execution configuration identity of the run.
func (s *Service) configVersion(admission Admission) string {
	return "provider=" + admission.ProviderID + "@" + admission.ProviderVersion
}
