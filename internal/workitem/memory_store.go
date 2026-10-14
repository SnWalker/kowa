package workitem

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

// MemoryStore is the in-process WorkItem store. It applies exactly the same
// rules as the PostgreSQL adapter: version checks, idempotent commands with
// digest comparison, single effective cancellation and single current run.
type MemoryStore struct {
	mutex       sync.Mutex
	items       map[string]WorkItem
	bindings    map[string][]RunBinding
	idempotency map[string]memoryIdempotency
}

type memoryIdempotency struct {
	digest   string
	response []byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items:       map[string]WorkItem{},
		bindings:    map[string][]RunBinding{},
		idempotency: map[string]memoryIdempotency{},
	}
}

func (s *MemoryStore) Create(_ context.Context, mutation CreateMutation) (WorkItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.Actor.GitHubUserID, "workitem.create", mutation.WorkItem.WorkspaceID, mutation.RequestKey)
	if existing, found, err := s.replay(key, mutation.RequestDigest); err != nil || found {
		return existing, err
	}
	if _, exists := s.items[mutation.WorkItem.ID]; exists {
		return WorkItem{}, fmt.Errorf("%w: work item already exists", ErrVersionConflict)
	}
	s.items[mutation.WorkItem.ID] = mutation.WorkItem
	return mutation.WorkItem, s.record(key, mutation.RequestDigest, mutation.WorkItem)
}

func (s *MemoryStore) Get(_ context.Context, workspaceID, workItemID string) (WorkItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	item, found := s.items[workItemID]
	if !found || item.WorkspaceID != workspaceID {
		return WorkItem{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) List(_ context.Context, workspaceID string) ([]WorkItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	items := make([]WorkItem, 0)
	for _, item := range s.items {
		if item.WorkspaceID == workspaceID {
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(left, right WorkItem) int {
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return right.CreatedAt.Compare(left.CreatedAt)
		}
		return compareStrings(left.ID, right.ID)
	})
	return items, nil
}

func (s *MemoryStore) Cancel(_ context.Context, mutation CancelMutation) (WorkItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.Actor.GitHubUserID, "workitem.cancel", mutation.WorkItemID, mutation.RequestKey)
	if existing, found, err := s.replay(key, mutation.RequestDigest); err != nil || found {
		return existing, err
	}
	item, found := s.items[mutation.WorkItemID]
	if !found || item.WorkspaceID != mutation.WorkspaceID {
		return WorkItem{}, ErrNotFound
	}
	if item.Version != mutation.ExpectedVersion {
		return WorkItem{}, ErrVersionConflict
	}
	if item.State != StateOpen {
		return WorkItem{}, fmt.Errorf("%w: only an OPEN work item can be canceled", ErrPolicyBlocked)
	}
	item.State = StateCanceled
	item.Version++
	item.UpdatedAt = mutation.CanceledAt
	canceledAt := mutation.CanceledAt
	item.CanceledAt = &canceledAt
	s.items[item.ID] = item
	return item, s.record(key, mutation.RequestDigest, item)
}

func (s *MemoryStore) AssociateRun(_ context.Context, mutation AssociateRunMutation) (WorkItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.Binding.BoundByUserID, "workitem.associate_run", mutation.WorkItemID, mutation.RequestKey)
	if existing, found, err := s.replay(key, mutation.RequestDigest); err != nil || found {
		return existing, err
	}
	item, found := s.items[mutation.WorkItemID]
	if !found {
		return WorkItem{}, ErrNotFound
	}
	if item.Version != mutation.ExpectedVersion {
		return WorkItem{}, ErrVersionConflict
	}
	if item.State != StateOpen {
		return WorkItem{}, fmt.Errorf("%w: a %s work item cannot bind a run", ErrPolicyBlocked, item.State)
	}
	binding := mutation.Binding
	switch binding.Kind {
	case BindingInitial:
		if item.CurrentRunID != "" && item.CurrentRunID != binding.RunID {
			return WorkItem{}, fmt.Errorf("%w: the work item already has a current run", ErrPolicyBlocked)
		}
	case BindingRepair:
		if item.CurrentRunID != binding.SourceRunID {
			return WorkItem{}, fmt.Errorf("%w: a repair run must replace the current run", ErrPolicyBlocked)
		}
	default:
		return WorkItem{}, ErrValidation
	}
	for _, existing := range s.bindings[item.ID] {
		if existing.RunID == binding.RunID {
			return WorkItem{}, fmt.Errorf("%w: the run is already bound", ErrVersionConflict)
		}
	}
	item.CurrentRunID = binding.RunID
	item.Version++
	item.UpdatedAt = mutation.BoundAt
	s.items[item.ID] = item
	s.bindings[item.ID] = append(s.bindings[item.ID], binding)
	return item, s.record(key, mutation.RequestDigest, item)
}

func (s *MemoryStore) RunBindings(_ context.Context, workspaceID, workItemID string) ([]RunBinding, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	item, found := s.items[workItemID]
	if !found || item.WorkspaceID != workspaceID {
		return nil, ErrNotFound
	}
	bindings := make([]RunBinding, 0, len(s.bindings[workItemID]))
	bindings = append(bindings, s.bindings[workItemID]...)
	slices.SortFunc(bindings, func(left, right RunBinding) int {
		if !left.BoundAt.Equal(right.BoundAt) {
			return left.BoundAt.Compare(right.BoundAt)
		}
		return compareStrings(left.RunID, right.RunID)
	})
	return bindings, nil
}

// Close applies the WorkItem half of the closure transaction. The PostgreSQL
// adapter shares one transaction with the run completion and the closure
// evidence; the memory adapter shares one mutex.
func (s *MemoryStore) Close(
	_ context.Context,
	workItemID string,
	expectedVersion int64,
	closedAt time.Time,
) (WorkItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	item, found := s.items[workItemID]
	if !found {
		return WorkItem{}, ErrNotFound
	}
	if item.Version != expectedVersion {
		return WorkItem{}, ErrVersionConflict
	}
	if item.State != StateOpen {
		return WorkItem{}, fmt.Errorf("%w: a %s work item cannot be closed", ErrPolicyBlocked, item.State)
	}
	item.State = StateClosed
	item.Version++
	item.UpdatedAt = closedAt
	closed := closedAt
	item.ClosedAt = &closed
	s.items[item.ID] = item
	return item, nil
}

func (s *MemoryStore) replay(key, digest string) (WorkItem, bool, error) {
	stored, found := s.idempotency[key]
	if !found {
		return WorkItem{}, false, nil
	}
	if stored.digest != digest {
		return WorkItem{}, false, ErrVersionConflict
	}
	var item WorkItem
	if err := json.Unmarshal(stored.response, &item); err != nil {
		return WorkItem{}, false, fmt.Errorf("decode idempotent response: %w", err)
	}
	return item, true, nil
}

func (s *MemoryStore) record(key, digest string, item WorkItem) error {
	encoded, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	s.idempotency[key] = memoryIdempotency{digest: digest, response: encoded}
	return nil
}

func idempotencyKey(actor, commandType, targetID, requestKey string) string {
	return actor + "\x00" + commandType + "\x00" + targetID + "\x00" + requestKey
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
