package artifact

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryCatalog is a deterministic test adapter. Durable deployments use PostgreSQL.
type MemoryCatalog struct {
	mu      sync.Mutex
	records map[string]Record
	members map[Scope]bool
}

func NewMemoryCatalog() *MemoryCatalog {
	return &MemoryCatalog{records: map[string]Record{}, members: map[Scope]bool{}}
}
func (s *MemoryCatalog) Grant(ws, actor string, write bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[Scope{ws, actor}] = write
}
func (s *MemoryCatalog) WithArtifact(ctx context.Context, scope Scope, id string, create *Record, write bool, fn func(*Record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	canWrite, ok := s.members[scope]
	if !ok || (write && !canWrite) {
		return ErrForbidden
	}
	r, exists := s.records[id]
	if create != nil {
		if exists {
			return ErrConflict
		}
		r = *create
	} else if !exists {
		return ErrUnavailable
	}
	if r.Ref.WorkspaceID != scope.WorkspaceID {
		return ErrForbidden
	}
	r.References = append([]Reference(nil), r.References...)
	before := r.State
	if err := fn(&r); err != nil {
		return err
	}
	if before != Published && r.State == Published && r.Ref.Producer.Kind == "task" {
		return ErrForbidden
	}
	if write {
		s.records[id] = r
	}
	return nil
}

// Orphans is discovery only; Clean must recheck every candidate under the same lock.
func (s *MemoryCatalog) Orphans(ctx context.Context, scope Scope, before time.Time, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if write, ok := s.members[scope]; !ok || !write {
		return nil, ErrForbidden
	}
	if limit < 1 || limit > 1000 {
		return nil, ErrValidation
	}
	ids := []string{}
	for id, r := range s.records {
		if r.Ref.WorkspaceID == scope.WorkspaceID && r.State != Deleted && len(r.References) == 0 && !r.CreatedAt.After(before) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}
