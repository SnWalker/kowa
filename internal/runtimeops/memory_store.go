package runtimeops

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is the in-process adapter used by unit tests and development
// servers; it enforces the same idempotency and version rules as PostgreSQL.
type MemoryStore struct {
	mu           sync.Mutex
	observations map[string]Observation
	configs      map[string]ExecutionConfig
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		observations: map[string]Observation{},
		configs:      map[string]ExecutionConfig{},
	}
}

func observationKey(runtimeID, epoch string, observedAt time.Time) string {
	return runtimeID + "\x00" + epoch + "\x00" + observedAt.UTC().Format(time.RFC3339Nano)
}

func (s *MemoryStore) ReportObservation(_ context.Context, observation Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := observationKey(observation.RuntimeID, observation.RuntimeEpoch, observation.ObservedAt)
	if existing, exists := s.observations[key]; exists {
		// Re-delivery of the identical observation is idempotent and keeps the
		// original watermark.
		if existing.CLIVersion == observation.CLIVersion && existing.Executable == observation.Executable &&
			existing.VersionCompatible == observation.VersionCompatible &&
			existing.Authenticated == observation.Authenticated &&
			existing.CanExecute == observation.CanExecute {
			return nil
		}
		return ErrRequestConflict
	}
	s.observations[key] = observation
	return nil
}

// LatestObservation returns the newest received observation; an empty epoch
// matches any generation (used to detect superseded-generation observations).
func (s *MemoryStore) LatestObservation(_ context.Context, runtimeID, epoch string) (Observation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	var latest Observation
	for _, observation := range s.observations {
		if observation.RuntimeID != runtimeID {
			continue
		}
		if epoch != "" && observation.RuntimeEpoch != epoch {
			continue
		}
		if !found || observation.ReceivedAt.After(latest.ReceivedAt) ||
			(observation.ReceivedAt.Equal(latest.ReceivedAt) && observation.ObservedAt.After(latest.ObservedAt)) {
			latest = observation
			found = true
		}
	}
	return latest, found, nil
}

func (s *MemoryStore) ExecutionConfig(_ context.Context, workspaceID string) (ExecutionConfig, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	config, exists := s.configs[workspaceID]
	return config, exists, nil
}

func (s *MemoryStore) SaveExecutionConfig(
	_ context.Context,
	command UpdateExecutionConfigCommand,
	digest string,
	now time.Time,
) (ExecutionConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, exists := s.configs[command.WorkspaceID]
	if exists && existing.RequestKey == command.RequestKey {
		if existing.RequestDigest == digest {
			return existing, nil
		}
		return ExecutionConfig{}, ErrRequestConflict
	}
	if exists {
		if existing.Version != command.ExpectedVersion {
			return ExecutionConfig{}, ErrVersionConflict
		}
	} else if command.ExpectedVersion != 0 {
		return ExecutionConfig{}, ErrVersionConflict
	}
	next := ExecutionConfig{
		WorkspaceID: command.WorkspaceID, Version: 1,
		Capabilities: command.Capabilities, Runtimes: command.Runtimes,
		RequestKey: command.RequestKey, RequestDigest: digest,
		UpdatedByGitHubUserID: command.ActorID, UpdatedAt: now.UTC(),
	}
	if exists {
		next.Version = existing.Version + 1
	}
	s.configs[command.WorkspaceID] = next
	return next, nil
}
