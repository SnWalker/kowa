package identity

import (
	"context"
	"errors"
	"sync"
	"time"
)

type memoryStore struct {
	mu       sync.Mutex
	flows    map[[32]byte]LoginFlow
	sessions map[[32]byte]Session
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		flows:    make(map[[32]byte]LoginFlow),
		sessions: make(map[[32]byte]Session),
	}
}

func (s *memoryStore) SaveLoginFlow(_ context.Context, flow LoginFlow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.flows[flow.StateDigest]; exists {
		return ErrConflict
	}
	s.flows[flow.StateDigest] = flow
	return nil
}

func (s *memoryStore) ConsumeLoginFlow(_ context.Context, state [32]byte, now time.Time) (LoginFlow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, exists := s.flows[state]
	if !exists || !now.Before(flow.ExpiresAt) {
		return LoginFlow{}, ErrUnauthorized
	}
	delete(s.flows, state)
	return flow, nil
}

func (s *memoryStore) CreateSession(_ context.Context, _ GitHubUser, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.TokenDigest] = session
	return nil
}

func (s *memoryStore) GetSession(_ context.Context, token [32]byte) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[token]
	if !exists {
		return Session{}, ErrUnauthorized
	}
	return session, nil
}

func (s *memoryStore) RevokeSession(_ context.Context, token [32]byte, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, exists := s.sessions[token]
	if !exists {
		return errors.New("session not found")
	}
	session.RevokedAt = &now
	s.sessions[token] = session
	return nil
}
