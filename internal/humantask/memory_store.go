package humantask

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

// MemoryStore is the in-process HumanTask store. It applies the same rules as
// the PostgreSQL adapter: a single effective resolution per task, one response
// per task, an append-only continuation intent, and command idempotency with
// digest comparison.
type MemoryStore struct {
	mutex        sync.Mutex
	tasks        map[string]HumanTask
	continuation map[string]Continuation
	byResponse   map[string]string
	idempotency  map[string]memoryIdempotency
}

type memoryIdempotency struct {
	digest   string
	response []byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tasks:        map[string]HumanTask{},
		continuation: map[string]Continuation{},
		byResponse:   map[string]string{},
		idempotency:  map[string]memoryIdempotency{},
	}
}

func (s *MemoryStore) ReplayTask(_ context.Context, actor, commandType, targetID, requestKey, digest string) (HumanTask, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.replay(idempotencyKey(actor, commandType, targetID, requestKey), digest)
}

func (s *MemoryStore) ReplayContinuation(_ context.Context, actor, commandType, targetID, requestKey, digest string) (Continuation, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.replayContinuation(idempotencyKey(actor, commandType, targetID, requestKey), digest)
}

func (s *MemoryStore) Create(_ context.Context, mutation CreateMutation) (HumanTask, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.ActorID, "humantask.create", mutation.Task.WorkspaceID, mutation.RequestKey)
	if existing, found, err := s.replay(key, mutation.RequestDigest); err != nil || found {
		return existing, err
	}
	if _, exists := s.tasks[mutation.Task.ID]; exists {
		return HumanTask{}, fmt.Errorf("%w: human task already exists", ErrVersionConflict)
	}
	s.tasks[mutation.Task.ID] = mutation.Task
	return mutation.Task, s.record(key, mutation.RequestDigest, mutation.Task)
}

func (s *MemoryStore) Get(_ context.Context, workspaceID, taskID string) (HumanTask, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	task, found := s.tasks[taskID]
	if !found || task.WorkspaceID != workspaceID {
		return HumanTask{}, ErrNotFound
	}
	return s.withContinuation(task), nil
}

func (s *MemoryStore) List(_ context.Context, workspaceID string, openOnly bool) ([]HumanTask, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	tasks := make([]HumanTask, 0)
	for _, task := range s.tasks {
		if task.WorkspaceID != workspaceID {
			continue
		}
		if openOnly && task.State != StateOpen {
			continue
		}
		tasks = append(tasks, s.withContinuation(task))
	}
	sortTasks(tasks)
	return tasks, nil
}

func (s *MemoryStore) Answer(_ context.Context, mutation AnswerMutation) (HumanTask, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.ActorID, "humantask.answer", mutation.TaskID, mutation.RequestKey)
	if existing, found, err := s.replay(key, mutation.RequestDigest); err != nil || found {
		return existing, err
	}
	task, found := s.tasks[mutation.TaskID]
	if !found || task.WorkspaceID != mutation.WorkspaceID {
		return HumanTask{}, ErrNotFound
	}
	if task.State != StateOpen {
		return HumanTask{}, fmt.Errorf("%w: the request is %s", ErrPolicyBlocked, task.State)
	}
	if _, resolved := s.byResponse[task.ID]; resolved {
		return HumanTask{}, fmt.Errorf("%w: the request is already resolved", ErrPolicyBlocked)
	}
	response := mutation.Response
	task.State = StateResolved
	task.Version++
	task.UpdatedAt = response.CreatedAt
	resolvedAt := response.CreatedAt
	task.ResolvedAt = &resolvedAt
	task.ResolvedByUserID = response.ActorID
	task.ResolutionKind = string(ModeKowaAnswer)
	task.Response = &response
	s.byResponse[task.ID] = response.ResponseID
	continuation := mutation.Continuation
	if _, exists := s.continuation[continuation.ID]; exists {
		return HumanTask{}, fmt.Errorf("%w: continuation already exists", ErrVersionConflict)
	}
	s.continuation[continuation.ID] = continuation
	task.Continuation = &continuation
	s.tasks[task.ID] = task
	return task, s.record(key, mutation.RequestDigest, task)
}

func (s *MemoryStore) ResolveExternalFact(_ context.Context, mutation ResolveExternalFactMutation) (HumanTask, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.ActorID, "humantask.resolve_external", mutation.TaskID, mutation.RequestKey)
	if existing, found, err := s.replay(key, mutation.RequestDigest); err != nil || found {
		return existing, err
	}
	task, found := s.tasks[mutation.TaskID]
	if !found || task.WorkspaceID != mutation.WorkspaceID {
		return HumanTask{}, ErrNotFound
	}
	if task.State != StateOpen {
		return HumanTask{}, fmt.Errorf("%w: the request is %s", ErrPolicyBlocked, task.State)
	}
	task.State = StateResolved
	task.Version++
	task.UpdatedAt = mutation.ResolvedAt
	resolvedAt := mutation.ResolvedAt
	task.ResolvedAt = &resolvedAt
	task.ResolvedByUserID = mutation.ActorID
	task.ResolutionKind = string(ModeExternalFact)
	task.ExternalFactID = mutation.ObservationID
	s.tasks[task.ID] = task
	return task, s.record(key, mutation.RequestDigest, task)
}

func (s *MemoryStore) SetState(_ context.Context, mutation StateMutation) (HumanTask, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	task, found := s.tasks[mutation.TaskID]
	if !found || task.WorkspaceID != mutation.WorkspaceID {
		return HumanTask{}, ErrNotFound
	}
	if task.State != StateOpen {
		return HumanTask{}, fmt.Errorf("%w: the request is %s", ErrPolicyBlocked, task.State)
	}
	if mutation.State != StateExpired && mutation.State != StateCanceled {
		return HumanTask{}, ErrValidation
	}
	task.State = mutation.State
	task.Version++
	task.UpdatedAt = mutation.At
	task.ExpiredReason = mutation.Reason
	s.tasks[task.ID] = task
	return task, nil
}

// ExpireBoundTo implements the store half of version-scoped expiry: every OPEN
// task bound to the object at a version other than exceptVersion expires.
func (s *MemoryStore) ExpireBoundTo(
	_ context.Context,
	workspaceID, objectKind, objectID, exceptVersion, reason, _ string,
	at time.Time,
) (int, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	expired := 0
	for id, task := range s.tasks {
		if task.WorkspaceID != workspaceID || task.State != StateOpen {
			continue
		}
		if task.Request.Object.Kind != objectKind || task.Request.Object.ID != objectID ||
			task.Request.Object.Version == exceptVersion {
			continue
		}
		task.State = StateExpired
		task.Version++
		task.UpdatedAt = at
		task.ExpiredReason = reason
		s.tasks[id] = task
		expired++
	}
	return expired, nil
}

func (s *MemoryStore) PendingContinuations(_ context.Context, workspaceID, runID string) ([]Continuation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	continuations := make([]Continuation, 0)
	for _, continuation := range s.continuation {
		if continuation.WorkspaceID != workspaceID || continuation.State != ContinuationPending {
			continue
		}
		if runID != "" && continuation.RunID != runID {
			continue
		}
		continuations = append(continuations, continuation)
	}
	sortContinuations(continuations)
	return continuations, nil
}

func (s *MemoryStore) ConsumeContinuation(_ context.Context, mutation ConsumeContinuationMutation) (Continuation, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(mutation.ActorID, "humantask.consume_continuation", mutation.ContinuationID, "consume")
	digestValue := Digest(mutation.ContinuationID)
	if existing, found, err := s.replayContinuation(key, digestValue); err != nil || found {
		return existing, err
	}
	continuation, found := s.continuation[mutation.ContinuationID]
	if !found || continuation.WorkspaceID != mutation.WorkspaceID {
		return Continuation{}, ErrNotFound
	}
	if continuation.State == ContinuationConsumed {
		return continuation, nil
	}
	if continuation.State != ContinuationPending {
		return Continuation{}, fmt.Errorf("%w: continuation is %s", ErrPolicyBlocked, continuation.State)
	}
	continuation.State = ContinuationConsumed
	consumedAt := mutation.At
	continuation.ConsumedAt = &consumedAt
	continuation.ConsumedBy = mutation.ActorID
	s.continuation[continuation.ID] = continuation
	return continuation, s.recordContinuation(key, digestValue, continuation)
}

func (s *MemoryStore) withContinuation(task HumanTask) HumanTask {
	for _, continuation := range s.continuation {
		if continuation.TaskID == task.ID {
			copied := continuation
			task.Continuation = &copied
			break
		}
	}
	return task
}

func (s *MemoryStore) replay(key, digest string) (HumanTask, bool, error) {
	stored, found := s.idempotency[key]
	if !found {
		return HumanTask{}, false, nil
	}
	if stored.digest != digest {
		return HumanTask{}, false, ErrVersionConflict
	}
	var task HumanTask
	if err := json.Unmarshal(stored.response, &task); err != nil {
		return HumanTask{}, false, fmt.Errorf("decode idempotent response: %w", err)
	}
	return task, true, nil
}

func (s *MemoryStore) replayContinuation(key, digest string) (Continuation, bool, error) {
	stored, found := s.idempotency[key]
	if !found {
		return Continuation{}, false, nil
	}
	if stored.digest != digest {
		return Continuation{}, false, ErrVersionConflict
	}
	var continuation Continuation
	if err := json.Unmarshal(stored.response, &continuation); err != nil {
		return Continuation{}, false, fmt.Errorf("decode idempotent response: %w", err)
	}
	return continuation, true, nil
}

func (s *MemoryStore) record(key, digest string, task HumanTask) error {
	encoded, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	s.idempotency[key] = memoryIdempotency{digest: digest, response: encoded}
	return nil
}

func (s *MemoryStore) recordContinuation(key, digest string, continuation Continuation) error {
	encoded, err := json.Marshal(continuation)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	s.idempotency[key] = memoryIdempotency{digest: digest, response: encoded}
	return nil
}

func idempotencyKey(actor, commandType, targetID, requestKey string) string {
	return actor + "\x00" + commandType + "\x00" + targetID + "\x00" + requestKey
}

// sortTasks orders newest first with a deterministic tie-break.
func sortTasks(tasks []HumanTask) {
	slices.SortFunc(tasks, func(left, right HumanTask) int {
		return cmp.Or(right.CreatedAt.Compare(left.CreatedAt), cmp.Compare(left.ID, right.ID))
	})
}

// sortContinuations orders oldest first so a consumer drains them in order.
func sortContinuations(values []Continuation) {
	slices.SortFunc(values, func(left, right Continuation) int {
		return cmp.Or(left.CreatedAt.Compare(right.CreatedAt), cmp.Compare(left.ID, right.ID))
	})
}
