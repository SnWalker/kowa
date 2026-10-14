package githubdelivery

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// MemoryStore is the in-process delivery store. It applies the same rules as
// the PostgreSQL adapter: idempotent admission, versioned operation
// transitions, append-only observations and one object per request identity.
type MemoryStore struct {
	mutex        sync.Mutex
	operations   map[string]ExternalOperation
	observations map[string]Observation
	idempotency  map[string]memoryIdempotency
}

type memoryIdempotency struct {
	digest   string
	response []byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		operations:   map[string]ExternalOperation{},
		observations: map[string]Observation{},
		idempotency:  map[string]memoryIdempotency{},
	}
}

func (s *MemoryStore) CreateOperation(_ context.Context, mutation AdmitMutation) (ExternalOperation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	operation := mutation.Operation
	key := idempotencyKey(operation.Request.RequestedByGitHubUserID,
		"delivery.admit."+string(operation.Kind), operation.WorkItemID, mutation.RequestKey)
	if stored, found := s.idempotency[key]; found {
		if stored.digest != mutation.RequestDigest {
			return ExternalOperation{}, ErrVersionConflict
		}
		var replayed ExternalOperation
		if err := json.Unmarshal(stored.response, &replayed); err != nil {
			return ExternalOperation{}, fmt.Errorf("decode idempotent response: %w", err)
		}
		return replayed, nil
	}
	if _, exists := s.operations[operation.OperationID]; exists {
		return ExternalOperation{}, fmt.Errorf("%w: operation already exists", ErrVersionConflict)
	}
	s.operations[operation.OperationID] = operation
	return operation, s.record(key, mutation.RequestDigest, operation)
}

func (s *MemoryStore) Operation(_ context.Context, workspaceID, operationID string) (ExternalOperation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	operation, found := s.operations[operationID]
	if !found || operation.WorkspaceID != workspaceID {
		return ExternalOperation{}, ErrNotFound
	}
	return operation, nil
}

func (s *MemoryStore) Operations(_ context.Context, workspaceID, workItemID string) ([]ExternalOperation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	operations := make([]ExternalOperation, 0)
	for _, operation := range s.operations {
		if operation.WorkspaceID == workspaceID && operation.WorkItemID == workItemID {
			operations = append(operations, operation)
		}
	}
	slices.SortFunc(operations, func(left, right ExternalOperation) int {
		return cmp.Or(left.CreatedAt.Compare(right.CreatedAt), cmp.Compare(left.OperationID, right.OperationID))
	})
	return operations, nil
}

func (s *MemoryStore) OperationForRequest(
	_ context.Context,
	workspaceID string,
	kind OperationKind,
	repositoryID, ref, commitOID string,
) (ExternalOperation, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, operation := range s.operations {
		if operation.WorkspaceID == workspaceID && operation.Kind == kind &&
			operation.Request.RepositoryID == repositoryID && operation.Request.Ref == ref &&
			operation.Request.CommitOID == commitOID {
			return operation, true, nil
		}
	}
	return ExternalOperation{}, false, nil
}

func (s *MemoryStore) UpdateOperation(_ context.Context, mutation UpdateMutation) (ExternalOperation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	operation, found := s.operations[mutation.OperationID]
	if !found || operation.WorkspaceID != mutation.WorkspaceID {
		return ExternalOperation{}, ErrNotFound
	}
	if operation.Version != mutation.ExpectedVersion {
		return ExternalOperation{}, ErrVersionConflict
	}
	operation.State = mutation.State
	operation.Result = mutation.Result
	operation.UnknownReason = mutation.UnknownReason
	operation.Version++
	operation.UpdatedAt = mutation.At
	if mutation.State == StateSucceeded || mutation.State == StateFailed {
		settled := mutation.At
		operation.SettledAt = &settled
	}
	s.operations[operation.OperationID] = operation
	return operation, nil
}

func (s *MemoryStore) RecordObservation(_ context.Context, mutation RecordObservationMutation) (Observation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists := s.observations[mutation.Observation.ObservationID]; exists {
		return Observation{}, fmt.Errorf("%w: observation already exists", ErrVersionConflict)
	}
	s.observations[mutation.Observation.ObservationID] = mutation.Observation
	return mutation.Observation, nil
}

func (s *MemoryStore) Observation(_ context.Context, workspaceID, observationID string) (Observation, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	observation, found := s.observations[observationID]
	if !found || observation.WorkspaceID != workspaceID {
		return Observation{}, false, nil
	}
	return observation, true, nil
}

func (s *MemoryStore) LatestObservations(_ context.Context, workspaceID, repositoryID string) ([]Observation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	latest := map[string]Observation{}
	for _, observation := range s.observations {
		if observation.WorkspaceID != workspaceID || observation.RepositoryID != repositoryID {
			continue
		}
		key := observation.SubjectKind + "\x00" + observation.SubjectKey
		existing, found := latest[key]
		if !found || observation.ObservedAt.After(existing.ObservedAt) ||
			(observation.ObservedAt.Equal(existing.ObservedAt) && observation.ObservationID > existing.ObservationID) {
			latest[key] = observation
		}
	}
	observations := make([]Observation, 0, len(latest))
	for _, observation := range latest {
		observations = append(observations, observation)
	}
	slices.SortFunc(observations, func(left, right Observation) int {
		return cmp.Or(cmp.Compare(left.SubjectKind, right.SubjectKind), cmp.Compare(left.SubjectKey, right.SubjectKey))
	})
	return observations, nil
}

func (s *MemoryStore) UnsettledOperations(_ context.Context, workspaceID, workItemID string) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	count := 0
	for _, operation := range s.operations {
		if operation.WorkspaceID != workspaceID || operation.WorkItemID != workItemID {
			continue
		}
		switch operation.State {
		case StateSucceeded, StateFailed:
		default:
			count++
		}
	}
	return count, nil
}

func (s *MemoryStore) record(key, digest string, operation ExternalOperation) error {
	encoded, err := json.Marshal(operation)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	s.idempotency[key] = memoryIdempotency{digest: digest, response: encoded}
	return nil
}

func idempotencyKey(actor, commandType, targetID, requestKey string) string {
	return actor + "\x00" + commandType + "\x00" + targetID + "\x00" + requestKey
}
