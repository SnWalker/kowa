package evaluation

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

// RunCompleter completes one delivery run. The execution service owns the run
// state machine; the PostgreSQL closure store folds this transition into the
// same SQL transaction as the WorkItem close.
type RunCompleter interface {
	CompleteRun(ctx context.Context, runID string, expectedVersion int64, at time.Time) (execution.Run, error)
}

// MemoryCloser is the delivery-side memory store for the closure: it closes the
// WorkItem through the WorkItem store and completes the run through the run
// completer, then records the closure.
type MemoryCloser struct {
	workItems workitem.Closer
	runs      RunCompleter
}

func NewMemoryCloser(workItems workitem.Closer, runs RunCompleter) *MemoryCloser {
	return &MemoryCloser{workItems: workItems, runs: runs}
}

// MemoryStore is the in-process verification/closure store.
type MemoryStore struct {
	mutex        sync.Mutex
	verification map[string]Verification
	closures     map[string]ClosureRecord
	idempotency  map[string]memoryIdempotency
	closer       *MemoryCloser
}

type memoryIdempotency struct {
	digest   string
	response []byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		verification: map[string]Verification{},
		closures:     map[string]ClosureRecord{},
		idempotency:  map[string]memoryIdempotency{},
	}
}

// WithCloser attaches the delivery-side close path so the memory adapter can
// apply the same cross-aggregate closure the PostgreSQL adapter applies in one
// transaction.
func (s *MemoryStore) WithCloser(closer *MemoryCloser) *MemoryStore {
	s.closer = closer
	return s
}

func (s *MemoryStore) RecordVerification(
	_ context.Context,
	verification Verification,
	requestKey, requestDigest string,
) (Verification, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(verification.ProducerID, "evaluation.record_verification", verification.WorkItemID, requestKey)
	if stored, found := s.idempotency[key]; found {
		if stored.digest != requestDigest {
			return Verification{}, ErrVersionConflict
		}
		var replayed Verification
		if err := json.Unmarshal(stored.response, &replayed); err != nil {
			return Verification{}, fmt.Errorf("decode idempotent response: %w", err)
		}
		return replayed, nil
	}
	if _, exists := s.verification[verification.VerificationID]; exists {
		return Verification{}, fmt.Errorf("%w: verification already exists", ErrVersionConflict)
	}
	s.verification[verification.VerificationID] = verification
	encoded, err := json.Marshal(verification)
	if err != nil {
		return Verification{}, fmt.Errorf("encode idempotent response: %w", err)
	}
	s.idempotency[key] = memoryIdempotency{digest: requestDigest, response: encoded}
	return verification, nil
}

func (s *MemoryStore) Verification(_ context.Context, workspaceID, verificationID string) (Verification, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	verification, found := s.verification[verificationID]
	if !found || verification.WorkspaceID != workspaceID {
		return Verification{}, false, nil
	}
	return verification, true, nil
}

func (s *MemoryStore) VerificationsFor(_ context.Context, workspaceID, workItemID string) ([]Verification, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	verifications := make([]Verification, 0)
	for _, verification := range s.verification {
		if verification.WorkspaceID == workspaceID && verification.WorkItemID == workItemID {
			verifications = append(verifications, verification)
		}
	}
	slices.SortFunc(verifications, func(left, right Verification) int {
		return cmp.Or(left.CreatedAt.Compare(right.CreatedAt), cmp.Compare(left.VerificationID, right.VerificationID))
	})
	return verifications, nil
}

func (s *MemoryStore) Closure(_ context.Context, workspaceID, workItemID string) (ClosureRecord, bool, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	record, found := s.closures[workItemID]
	if !found || record.WorkspaceID != workspaceID {
		return ClosureRecord{}, false, nil
	}
	return record, true, nil
}

func (s *MemoryStore) Close(ctx context.Context, mutation CloseMutation) (ClosureRecord, error) {
	if s.closer == nil {
		return ClosureRecord{}, fmt.Errorf("%w: no closure path is attached", ErrPolicyBlocked)
	}
	record := mutation.Record
	key := idempotencyKey(mutation.ActorID, "evaluation.close", record.WorkItemID, mutation.RequestKey)
	s.mutex.Lock()
	if stored, found := s.idempotency[key]; found {
		s.mutex.Unlock()
		if stored.digest != mutation.RequestDigest {
			return ClosureRecord{}, ErrVersionConflict
		}
		var replayed ClosureRecord
		if err := json.Unmarshal(stored.response, &replayed); err != nil {
			return ClosureRecord{}, fmt.Errorf("decode idempotent response: %w", err)
		}
		return replayed, nil
	}
	if _, exists := s.closures[record.WorkItemID]; exists {
		s.mutex.Unlock()
		return ClosureRecord{}, fmt.Errorf("%w: the work item is already closed", ErrPolicyBlocked)
	}
	s.mutex.Unlock()

	item, err := s.closer.workItems.Close(ctx, record.WorkItemID, mutation.ExpectedWorkItemVersion, mutation.ClosedAt)
	if err != nil {
		return ClosureRecord{}, err
	}
	if item.State != workitem.StateClosed {
		return ClosureRecord{}, fmt.Errorf("%w: the work item is %s", ErrPolicyBlocked, item.State)
	}
	if _, err := s.closer.runs.CompleteRun(ctx, record.RunID, mutation.ExpectedRunVersion, mutation.ClosedAt); err != nil {
		return ClosureRecord{}, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.closures[record.WorkItemID] = record
	encoded, err := json.Marshal(record)
	if err != nil {
		return ClosureRecord{}, fmt.Errorf("encode idempotent response: %w", err)
	}
	s.idempotency[key] = memoryIdempotency{digest: mutation.RequestDigest, response: encoded}
	return record, nil
}

func idempotencyKey(actor, commandType, targetID, requestKey string) string {
	return actor + "\x00" + commandType + "\x00" + targetID + "\x00" + requestKey
}
