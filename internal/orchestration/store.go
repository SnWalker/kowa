package orchestration

import (
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
	// ActiveRuns lists the admitted runs the reconciler still has to advance;
	// the run state itself stays execution's authority.
	ActiveRuns(ctx context.Context) ([]string, error)

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
	bindings            map[string]Binding
	artifacts           []ArtifactRecord
	// artifactSource, when set, is the published-deliverable read the durable
	// adapter performs with its own SQL.
	artifactSource ArtifactSource
	recoveries     map[string]commandRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		admissions:          map[string]Admission{},
		admissionsByCommand: map[string]admissionRecord{},
		recommendation:      map[string]Recommendation{},
		bindings:            map[string]Binding{},
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
	if recommendation.HumanTaskID != "" && recommendation.HumanTaskID != humanTaskID {
		return Recommendation{}, ErrVersionConflict
	}
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

func (s *MemoryStore) BindInput(_ context.Context, binding Binding) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.bindings[binding.TaskID]; found {
		if !slices.Equal(existing.ArtifactIDs, binding.ArtifactIDs) || !slices.Equal(existing.Scopes, binding.Scopes) ||
			existing.InputDigest != binding.InputDigest || existing.NodeRunID != binding.NodeRunID ||
			existing.DeadlineAt.UTC() != binding.DeadlineAt.UTC() {
			return Binding{}, ErrVersionConflict
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
		identifiers = append(identifiers, runID)
	}
	slices.Sort(identifiers)
	return identifiers, nil
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
