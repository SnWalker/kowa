package evaluation

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// MemoryStore is the in-process verification store. It deliberately does not
// implement closing: the frozen closure invariant requires one authoritative
// transaction over the WorkItem, the delivery run and the closure record, and
// only the PostgreSQL adapter provides that.
type MemoryStore struct {
	mutex        sync.Mutex
	verification map[string]Verification
	idempotency  map[string]memoryIdempotency
}

type memoryIdempotency struct {
	digest   string
	response []byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		verification: map[string]Verification{},
		idempotency:  map[string]memoryIdempotency{},
	}
}

func (s *MemoryStore) RecordVerification(
	_ context.Context,
	verification Verification,
	actorID, requestKey, requestDigest string,
) (Verification, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	key := idempotencyKey(actorID, "evaluation.record_verification", verification.WorkItemID, requestKey)
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

func idempotencyKey(actor, commandType, targetID, requestKey string) string {
	return actor + "\x00" + commandType + "\x00" + targetID + "\x00" + requestKey
}
