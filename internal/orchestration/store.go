package orchestration

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// ArtifactRecord is the read-only projection of one published deliverable of a
// work item, used by the artifact view. The artifact module owns the content
// and its publication state; this record adds no semantics.
type ArtifactRecord struct {
	ArtifactID       string    `json:"artifactId"`
	SubjectID        string    `json:"subjectId"`
	Kind             string    `json:"kind"`
	Digest           string    `json:"digest"`
	Size             int64     `json:"size"`
	MediaType        string    `json:"mediaType"`
	ProducerKind     string    `json:"producerKind"`
	ProducerID       string    `json:"producerId"`
	RequiresApproval bool      `json:"requiresApproval"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"createdAt"`
}

// ConfirmMutation records the frozen knowledge binding of one run.
type ConfirmMutation struct {
	RunID            string
	HumanTaskID      string
	SnapshotID       string
	BindingEventID   string
	EffectiveInputID string
	At               time.Time
}

// ArtifactSource lists the published deliverables of one work item. The
// artifact module owns those rows; the durable orchestration store reads them
// from its own SQL, and the in-memory adapter delegates to this source.
type ArtifactSource interface {
	ArtifactsForWorkItem(ctx context.Context, workspaceID, workItemID string) ([]ArtifactRecord, error)
}

// Store persists the admission, the knowledge recommendation and the frozen
// task input bindings of the fixed flow. Both adapters share this contract.
type Store interface {
	// ConfirmedKnowledgeVersion is the number of confirmations this module
	// already recorded for one work item; S03 needs it as the expected version
	// of the next confirmation.
	ConfirmedKnowledgeVersion(ctx context.Context, workspaceID, workItemID string) (int, error)
	// ReplayedCommand and RecordCommand answer an identical command from the
	// recorded outcome, so a retried command never applies twice.
	ReplayedCommand(
		ctx context.Context,
		actor, commandType, targetID, requestKey, requestDigest string,
	) ([]byte, bool, error)
	RecordCommand(
		ctx context.Context,
		actor, commandType, targetID, requestKey, requestDigest string,
		response []byte,
	) error
	// AdmitOnce persists one admission together with its command idempotency
	// record in one transaction. The second return value reports a replay of an
	// identical command, whose frozen admission is then returned.
	AdmitOnce(
		ctx context.Context,
		admission Admission,
		actor, requestKey, requestDigest string,
	) (Admission, bool, error)
	Admission(ctx context.Context, runID string) (Admission, error)
	// ReplayedAdmission answers an identical admission command before any new
	// run is materialised.
	ReplayedAdmission(
		ctx context.Context,
		actor, workItemID, requestKey, requestDigest string,
	) (Admission, bool, error)

	Recommend(ctx context.Context, recommendation Recommendation) (Recommendation, error)
	Recommendation(ctx context.Context, runID string) (Recommendation, error)
	// AttachConfirmationTask records the request that will carry the decision.
	AttachConfirmationTask(ctx context.Context, runID, humanTaskID string) (Recommendation, error)
	ConfirmRecommendation(ctx context.Context, mutation ConfirmMutation) (Recommendation, error)

	// BindInput persists one frozen binding. The row is immutable: a retry of
	// the same dispatch intent returns the existing facts, never a new copy.
	BindInput(ctx context.Context, binding Binding) (Binding, error)
	Binding(ctx context.Context, taskID string) (Binding, error)

	ArtifactsForWorkItem(ctx context.Context, workspaceID, workItemID string) ([]ArtifactRecord, error)
	// ActiveRuns lists the admitted runs whose delivery run is still live; the
	// run state stays execution's authority and the advancement authority is
	// applied by the service.
	ActiveRuns(ctx context.Context) ([]string, error)
	// VoidAdmission forgets an admission whose advancement authority was
	// refused: nothing of the attempt may survive it, including its recorded
	// command, so a replay of the same request can never answer with the
	// withdrawn facts.
	VoidAdmission(ctx context.Context, runID, actorID, workItemID, requestKey string) error
	// NoteRunState records the delivery run state of one admitted run, so an
	// adapter without the run table can report the same liveness.
	NoteRunState(ctx context.Context, runID, state string) error

	// SeedSuggestion materialises the first auxiliary suggestion attempt of one
	// run. It is idempotent on the same frozen input and refuses a drifted one.
	SeedSuggestion(ctx context.Context, seed SuggestionSeed) (SuggestionAttempt, error)
	// ClaimSuggestion fences one attempt under a lease. Only a pending attempt,
	// or a processing one whose lease expired, may be claimed.
	ClaimSuggestion(ctx context.Context, claim SuggestionClaim) (SuggestionAttempt, error)
	// FinalizeSuggestion settles one claimed attempt. The expected version
	// proves the claimant still owns the lease, and a settled attempt is never
	// overwritten by a late writer.
	FinalizeSuggestion(ctx context.Context, outcome SuggestionOutcome) (SuggestionAttempt, error)
	// SuggestionAttempts lists every attempt of one run, oldest first.
	SuggestionAttempts(ctx context.Context, runID string) ([]SuggestionAttempt, error)
	// ClaimableSuggestions lists the attempts a worker may process: pending, or
	// processing with an expired lease, oldest first.
	ClaimableSuggestions(ctx context.Context, now time.Time, limit int) ([]SuggestionAttempt, error)
	// RequestSuggestion opens the next attempt after a failed one; every other
	// current state is refused.
	RequestSuggestion(ctx context.Context, request SuggestionRequest) (SuggestionAttempt, error)

	// Close releases adapter resources.
	Close() error
}

// MemoryStore is the in-process adapter of the orchestration store. It is the
// single-process test adapter; the PostgreSQL adapter is the authority.
type MemoryStore struct {
	mu                  sync.Mutex
	admissions          map[string]Admission
	admissionsByCommand map[string]admissionRecord
	recommendation      map[string]Recommendation
	// runStates mirrors the delivery run state of each admitted run so the
	// adapter's ActiveRuns matches the durable one.
	runStates map[string]string
	bindings  map[string]Binding
	artifacts []ArtifactRecord
	// suggestions holds every attempt of each run's auxiliary branch, in
	// attempt order: the newest row is the current state.
	suggestions map[string][]SuggestionAttempt
	// artifactSource, when set, is the published-deliverable read the durable
	// adapter performs with its own SQL.
	artifactSource ArtifactSource
	recoveries     map[string]commandRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		admissions:          map[string]Admission{},
		admissionsByCommand: map[string]admissionRecord{},
		runStates:           map[string]string{},
		recommendation:      map[string]Recommendation{},
		bindings:            map[string]Binding{},
		suggestions:         map[string][]SuggestionAttempt{},
		recoveries:          map[string]commandRecord{},
	}
}

func (s *MemoryStore) Close() error { return nil }

func (s *MemoryStore) AdmitOnce(
	_ context.Context,
	admission Admission,
	actor, requestKey, requestDigest string,
) (Admission, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := admissionKey(actor, admission.WorkItemID, requestKey)
	if record, found := s.admissionsByCommand[key]; found {
		if record.digest != requestDigest {
			return Admission{}, false, ErrVersionConflict
		}
		return cloneAdmission(record.admission), true, nil
	}
	s.admissions[admission.RunID] = admission
	s.admissionsByCommand[key] = admissionRecord{admission: admission, digest: requestDigest}
	return cloneAdmission(admission), false, nil
}

func (s *MemoryStore) ReplayedAdmission(
	_ context.Context,
	actor, workItemID, requestKey, requestDigest string,
) (Admission, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.admissionsByCommand[admissionKey(actor, workItemID, requestKey)]
	if !found {
		return Admission{}, false, nil
	}
	if record.digest != requestDigest {
		return Admission{}, false, ErrVersionConflict
	}
	return cloneAdmission(record.admission), true, nil
}

type admissionRecord struct {
	admission Admission
	digest    string
}

func admissionKey(actor, workItemID, requestKey string) string {
	return actor + "\x00" + workItemID + "\x00" + requestKey
}

func cloneAdmission(admission Admission) Admission { return admission }

func (s *MemoryStore) Admission(_ context.Context, runID string) (Admission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	admission, found := s.admissions[runID]
	if !found {
		return Admission{}, fmt.Errorf("%w: no admission for run %s", ErrNotFound, runID)
	}
	return admission, nil
}

func (s *MemoryStore) Recommend(_ context.Context, recommendation Recommendation) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.recommendation[recommendation.RunID]; found {
		if !sameRecommendation(existing, recommendation) {
			return Recommendation{}, ErrVersionConflict
		}
		return cloneRecommendation(existing), nil
	}
	s.recommendation[recommendation.RunID] = recommendation
	return cloneRecommendation(recommendation), nil
}

func (s *MemoryStore) Recommendation(_ context.Context, runID string) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recommendation, found := s.recommendation[runID]
	if !found {
		return Recommendation{}, fmt.Errorf("%w: no knowledge recommendation for run %s", ErrNotFound, runID)
	}
	return cloneRecommendation(recommendation), nil
}

func (s *MemoryStore) AttachConfirmationTask(_ context.Context, runID, humanTaskID string) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recommendation, found := s.recommendation[runID]
	if !found {
		return Recommendation{}, fmt.Errorf("%w: no knowledge recommendation for run %s", ErrNotFound, runID)
	}
	if recommendation.Confirmed() {
		// The confirmation binds the request that carried the decision; a later
		// request can no longer take its place.
		if recommendation.HumanTaskID != humanTaskID {
			return Recommendation{}, ErrVersionConflict
		}
		return cloneRecommendation(recommendation), nil
	}
	// While the selection is unconfirmed a re-asked confirmation replaces the
	// attached request: the recommendation always names the pending one.
	recommendation.HumanTaskID = humanTaskID
	s.recommendation[runID] = recommendation
	return cloneRecommendation(recommendation), nil
}

func (s *MemoryStore) ConfirmRecommendation(_ context.Context, mutation ConfirmMutation) (Recommendation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recommendation, found := s.recommendation[mutation.RunID]
	if !found {
		return Recommendation{}, fmt.Errorf("%w: no knowledge recommendation for run %s", ErrNotFound, mutation.RunID)
	}
	if recommendation.Confirmed() {
		if recommendation.SnapshotID == mutation.SnapshotID &&
			recommendation.BindingEventID == mutation.BindingEventID &&
			recommendation.EffectiveInputID == mutation.EffectiveInputID {
			return cloneRecommendation(recommendation), nil
		}
		return Recommendation{}, ErrVersionConflict
	}
	if recommendation.HumanTaskID != "" && mutation.HumanTaskID != "" &&
		recommendation.HumanTaskID != mutation.HumanTaskID {
		return Recommendation{}, ErrVersionConflict
	}
	confirmed := mutation.At
	recommendation.State = StateConfirmed
	recommendation.HumanTaskID = mutation.HumanTaskID
	recommendation.SnapshotID = mutation.SnapshotID
	recommendation.BindingEventID = mutation.BindingEventID
	recommendation.EffectiveInputID = mutation.EffectiveInputID
	recommendation.ConfirmedAt = &confirmed
	s.recommendation[mutation.RunID] = recommendation
	return cloneRecommendation(recommendation), nil
}

// BindingDrift reports the first frozen field that differs between a persisted
// binding and an incoming dispatch intent, or "" when the two are identical.
// A binding is immutable: drift in any identity, input, repository, capability,
// provider, scope, artifact or deadline field is a version conflict, never a
// silent reuse of the old facts. BoundAt is the write timestamp of the first
// persistence, not part of the intent, so it is not compared.
func BindingDrift(existing, binding Binding) string {
	switch {
	case existing.WorkspaceID != binding.WorkspaceID:
		return "workspace"
	case existing.RunID != binding.RunID:
		return "workflowRunId"
	case existing.NodeID != binding.NodeID:
		return "nodeId"
	case existing.NodeRunID != binding.NodeRunID:
		return "nodeRunId"
	case existing.InputID != binding.InputID:
		return "inputId"
	case existing.InputDigest != binding.InputDigest:
		return "inputDigest"
	case existing.ProjectRepositoryID != binding.ProjectRepositoryID:
		return "projectRepositoryId"
	case existing.ProjectCommitOID != binding.ProjectCommitOID:
		return "projectCommitOid"
	case existing.KnowledgeRepositoryID != binding.KnowledgeRepositoryID:
		return "knowledgeRepositoryId"
	case existing.KnowledgeCommitOID != binding.KnowledgeCommitOID:
		return "knowledgeCommitOid"
	case existing.CapabilityID != binding.CapabilityID:
		return "capabilityId"
	case existing.CapabilityVersion != binding.CapabilityVersion:
		return "capabilityVersion"
	case existing.ProviderID != binding.ProviderID:
		return "providerId"
	case existing.ProviderVersion != binding.ProviderVersion:
		return "providerVersion"
	case !slices.Equal(existing.Scopes, binding.Scopes):
		return "gitScopes"
	case !slices.Equal(existing.ArtifactIDs, binding.ArtifactIDs):
		return "artifactIds"
	case !slices.Equal(existing.CausalRefs, binding.CausalRefs):
		return "causalRefs"
	case !SameInstant(existing.DeadlineAt, binding.DeadlineAt):
		return "deadlineAt"
	default:
		return ""
	}
}

func (s *MemoryStore) BindInput(_ context.Context, binding Binding) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.bindings[binding.TaskID]; found {
		if field := BindingDrift(existing, binding); field != "" {
			return Binding{}, fmt.Errorf(
				"%w: task %s is frozen with other %s", ErrVersionConflict, binding.TaskID, field)
		}
		return cloneBinding(existing), nil
	}
	s.bindings[binding.TaskID] = binding
	return cloneBinding(binding), nil
}

func (s *MemoryStore) Binding(_ context.Context, taskID string) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, found := s.bindings[taskID]
	if !found {
		return Binding{}, fmt.Errorf("%w: no frozen input binding for task %s", ErrNotFound, taskID)
	}
	return cloneBinding(binding), nil
}

// RegisterArtifact lets an isolated journey declare the deliverables the
// artifact module published, so the artifact view can be exercised without the
// artifact store. The PostgreSQL adapter reads the artifact rows instead.
func (s *MemoryStore) RegisterArtifact(record ArtifactRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts = append(s.artifacts, record)
}

func (s *MemoryStore) ActiveRuns(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identifiers := make([]string, 0, len(s.admissions))
	for runID := range s.admissions {
		if state, known := s.runStates[runID]; known && state != "ACTIVE" {
			continue
		}
		identifiers = append(identifiers, runID)
	}
	slices.Sort(identifiers)
	return identifiers, nil
}

// NoteRunState records the delivery run state of one admitted run.
func (s *MemoryStore) NoteRunState(_ context.Context, runID, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runStates == nil {
		s.runStates = map[string]string{}
	}
	s.runStates[runID] = state
	return nil
}

// VoidAdmission drops the admission record of a withdrawn attempt together
// with the command record that could replay it.
func (s *MemoryStore) VoidAdmission(_ context.Context, runID, _, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.admissions, runID)
	for key, record := range s.admissionsByCommand {
		if record.admission.RunID == runID {
			delete(s.admissionsByCommand, key)
		}
	}
	return nil
}

// suggestionAttemptID names one attempt: the identity is stable and derived
// from the persisted run and attempt numbers, never from a caller.
func suggestionAttemptID(runID string, attempt int) string {
	return fmt.Sprintf("%s:knowledge-suggestion:%d", runID, attempt)
}

// SeedSuggestion materialises the first auxiliary suggestion attempt of one run.
func (s *MemoryStore) SeedSuggestion(_ context.Context, seed SuggestionSeed) (SuggestionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, attempt := range s.suggestions[seed.RunID] {
		if attempt.Attempt == 1 {
			if attempt.InputDigest != seed.InputDigest {
				return SuggestionAttempt{}, fmt.Errorf(
					"%w: suggestion attempt 1 of run %s is frozen on input %s",
					ErrVersionConflict, seed.RunID, attempt.InputDigest)
			}
			return cloneSuggestion(attempt), nil
		}
	}
	attempt := SuggestionAttempt{
		RunID: seed.RunID, WorkspaceID: seed.WorkspaceID, WorkItemID: seed.WorkItemID,
		Attempt: 1, AttemptID: suggestionAttemptID(seed.RunID, 1),
		State: SuggestionPending, InputDigest: seed.InputDigest,
		RequestedAt: seed.At, UpdatedAt: seed.At, Version: 1,
	}
	s.suggestions[seed.RunID] = append(s.suggestions[seed.RunID], attempt)
	return cloneSuggestion(attempt), nil
}

// ClaimSuggestion fences one attempt under a lease.
func (s *MemoryStore) ClaimSuggestion(_ context.Context, claim SuggestionClaim) (SuggestionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runID, attempt, found := s.findSuggestion(claim.AttemptID)
	if !found {
		return SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %s", ErrNotFound, claim.AttemptID)
	}
	switch attempt.State {
	case SuggestionPending:
	case SuggestionProcessing:
		if attempt.ClaimedUntil != nil && attempt.ClaimedUntil.After(claim.At) {
			return SuggestionAttempt{}, fmt.Errorf(
				"%w: suggestion attempt %s is being processed", ErrVersionConflict, claim.AttemptID)
		}
	default:
		return SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %s is %s", ErrPolicyBlocked, claim.AttemptID, attempt.State)
	}
	until := claim.LeaseUntil
	attempt.State = SuggestionProcessing
	attempt.ClaimedUntil = &until
	attempt.UpdatedAt = claim.At
	attempt.Version++
	s.suggestions[runID][suggestionIndex(s.suggestions[runID], attempt.Attempt)] = attempt
	return cloneSuggestion(attempt), nil
}

// FinalizeSuggestion settles one claimed attempt under its lease version.
func (s *MemoryStore) FinalizeSuggestion(_ context.Context, outcome SuggestionOutcome) (SuggestionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := s.suggestions[outcome.RunID]
	index := suggestionIndex(attempts, outcome.Attempt)
	if index < 0 {
		return SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %d of run %s", ErrNotFound, outcome.Attempt, outcome.RunID)
	}
	attempt := attempts[index]
	if attempt.State != SuggestionProcessing || attempt.Version != outcome.ExpectedVersion {
		return SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %s is %s at version %d",
			ErrVersionConflict, attempt.AttemptID, attempt.State, attempt.Version)
	}
	published := outcome.FailureReason == ""
	if published {
		attempt.State = SuggestionPublished
		attempt.ArtifactID = outcome.ArtifactID
		attempt.ArtifactDigest = outcome.ArtifactDigest
	} else {
		attempt.State = SuggestionFailed
		attempt.FailureReason = outcome.FailureReason
	}
	attempt.ClaimedUntil = nil
	attempt.UpdatedAt = outcome.At
	attempt.Version++
	attempts[index] = attempt
	return cloneSuggestion(attempt), nil
}

// SuggestionAttempts lists every attempt of one run, oldest first.
func (s *MemoryStore) SuggestionAttempts(_ context.Context, runID string) ([]SuggestionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := []SuggestionAttempt{}
	for _, attempt := range s.suggestions[runID] {
		attempts = append(attempts, cloneSuggestion(attempt))
	}
	return attempts, nil
}

// ClaimableSuggestions lists the attempts a worker may process, oldest first.
func (s *MemoryStore) ClaimableSuggestions(
	_ context.Context,
	now time.Time,
	limit int,
) ([]SuggestionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claimable := []SuggestionAttempt{}
	for _, attempts := range s.suggestions {
		for _, attempt := range attempts {
			switch attempt.State {
			case SuggestionPending:
			case SuggestionProcessing:
				if attempt.ClaimedUntil != nil && attempt.ClaimedUntil.After(now) {
					continue
				}
			default:
				continue
			}
			claimable = append(claimable, cloneSuggestion(attempt))
		}
	}
	slices.SortFunc(claimable, func(left, right SuggestionAttempt) int {
		return cmp.Or(
			cmp.Compare(left.RequestedAt.UTC().Format(time.RFC3339Nano), right.RequestedAt.UTC().Format(time.RFC3339Nano)),
			cmp.Compare(left.AttemptID, right.AttemptID),
		)
	})
	if limit > 0 && len(claimable) > limit {
		claimable = claimable[:limit]
	}
	return claimable, nil
}

// RequestSuggestion opens the next attempt after a failed one.
func (s *MemoryStore) RequestSuggestion(_ context.Context, request SuggestionRequest) (SuggestionAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := s.suggestions[request.RunID]
	if len(attempts) == 0 {
		return SuggestionAttempt{}, fmt.Errorf(
			"%w: run %s has no suggestion attempt", ErrNotFound, request.RunID)
	}
	newest := attempts[len(attempts)-1]
	if newest.State != SuggestionFailed {
		return SuggestionAttempt{}, fmt.Errorf(
			"%w: the newest suggestion attempt of run %s is %s",
			ErrPolicyBlocked, request.RunID, newest.State)
	}
	attempt := SuggestionAttempt{
		RunID: newest.RunID, WorkspaceID: newest.WorkspaceID, WorkItemID: newest.WorkItemID,
		Attempt: newest.Attempt + 1, AttemptID: suggestionAttemptID(request.RunID, newest.Attempt+1),
		State: SuggestionPending, InputDigest: newest.InputDigest,
		RequestedAt: request.At, UpdatedAt: request.At, Version: 1,
	}
	s.suggestions[request.RunID] = append(attempts, attempt)
	return cloneSuggestion(attempt), nil
}

// findSuggestion locates one attempt by its identity.
func (s *MemoryStore) findSuggestion(attemptID string) (string, SuggestionAttempt, bool) {
	for runID, attempts := range s.suggestions {
		for _, attempt := range attempts {
			if attempt.AttemptID == attemptID {
				return runID, attempt, true
			}
		}
	}
	return "", SuggestionAttempt{}, false
}

// suggestionIndex returns the position of one attempt number, or -1.
func suggestionIndex(attempts []SuggestionAttempt, attempt int) int {
	for index, candidate := range attempts {
		if candidate.Attempt == attempt {
			return index
		}
	}
	return -1
}

func cloneSuggestion(attempt SuggestionAttempt) SuggestionAttempt {
	if attempt.ClaimedUntil != nil {
		claimed := *attempt.ClaimedUntil
		attempt.ClaimedUntil = &claimed
	}
	return attempt
}

// UseArtifacts wires the published-deliverable source of the in-memory adapter.
func (s *MemoryStore) UseArtifacts(source ArtifactSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifactSource = source
}

func (s *MemoryStore) ArtifactsForWorkItem(ctx context.Context, workspaceID, workItemID string) ([]ArtifactRecord, error) {
	s.mu.Lock()
	source := s.artifactSource
	records := slices.Clone(s.artifacts)
	s.mu.Unlock()
	if source != nil {
		return source.ArtifactsForWorkItem(ctx, workspaceID, workItemID)
	}
	return records, nil
}

func (s *MemoryStore) ReplayedCommand(
	_ context.Context,
	actor, commandType, targetID, requestKey, digest string,
) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.recoveries[commandKey(actor, commandType, targetID, requestKey)]
	if !found {
		return nil, false, nil
	}
	if record.digest != digest {
		return nil, false, ErrVersionConflict
	}
	return slices.Clone(record.response), true, nil
}

func (s *MemoryStore) RecordCommand(
	_ context.Context,
	actor, commandType, targetID, requestKey, digest string,
	response []byte,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recoveries[commandKey(actor, commandType, targetID, requestKey)] = commandRecord{
		response: slices.Clone(response), digest: digest,
	}
	return nil
}

type commandRecord struct {
	response []byte
	digest   string
}

func commandKey(actor, commandType, targetID, requestKey string) string {
	return actor + "\x00" + commandType + "\x00" + targetID + "\x00" + requestKey
}

// ConfirmedKnowledgeVersion counts the confirmations this module recorded for
// one work item; S03 owns the binding events those confirmations produced.
func (s *MemoryStore) ConfirmedKnowledgeVersion(_ context.Context, _, workItemID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, recommendation := range s.recommendation {
		if recommendation.WorkItemID == workItemID && recommendation.Confirmed() {
			count++
		}
	}
	return count, nil
}

func sameRecommendation(left, right Recommendation) bool {
	return left.KnowledgeCommitOID == right.KnowledgeCommitOID && left.IndexDigest == right.IndexDigest &&
		slices.Equal(left.Candidates, right.Candidates)
}

func cloneRecommendation(recommendation Recommendation) Recommendation {
	cloned := recommendation
	cloned.Candidates = slices.Clone(recommendation.Candidates)
	return cloned
}

func cloneBinding(binding Binding) Binding {
	cloned := binding
	cloned.Scopes = slices.Clone(binding.Scopes)
	cloned.ArtifactIDs = slices.Clone(binding.ArtifactIDs)
	cloned.CausalRefs = slices.Clone(binding.CausalRefs)
	return cloned
}

// cloneOutput keeps stored node outputs caller-independent.
func cloneOutput(output map[string]any) map[string]any { return maps.Clone(output) }

// cloneBinding keeps a returned binding independent of the stored row.
func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode orchestration value: %v", err))
	}
	return encoded
}
