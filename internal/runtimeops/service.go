package runtimeops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

const DefaultObservationTTL = 15 * time.Minute

var (
	capabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$`)
	runtimeIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// Store persists observations and execution configuration. Implementations
// must make observation reports idempotent per (runtime, epoch, observedAt) and
// enforce config idempotency and version checks atomically.
type Store interface {
	ReportObservation(context.Context, Observation) error
	LatestObservation(context.Context, string, string) (Observation, bool, error)
	ExecutionConfig(context.Context, string) (ExecutionConfig, bool, error)
	SaveExecutionConfig(context.Context, UpdateExecutionConfigCommand, string, time.Time) (ExecutionConfig, error)
}

// RuntimeSource reads the current registration generation of a runtime; the
// execution service is the authority for that fact.
type RuntimeSource interface {
	RuntimeStatus(context.Context, string) (execution.RuntimeStatus, error)
}

type Service struct {
	store  Store
	source RuntimeSource
	now    func() time.Time
	ttl    time.Duration
}

func NewService(store Store, source RuntimeSource, now func() time.Time) *Service {
	return &Service{store: store, source: source, now: now, ttl: DefaultObservationTTL}
}

// ReportObservation validates and stamps one ladder observation. The server
// watermark is authoritative for staleness, never the Runner's clock.
func (s *Service) ReportObservation(ctx context.Context, observation Observation) error {
	if !runtimeIDPattern.MatchString(observation.RuntimeID) || observation.RuntimeEpoch == "" ||
		observation.ObservedAt.IsZero() || len(observation.Detail) > 4096 ||
		len(observation.CLIVersion) > 256 {
		// The service is the single content authority: the PostgreSQL check
		// constraint (000004) and the frozen schema maxLength are backstops,
		// never a second decision. Memory and PostgreSQL therefore agree.
		return ErrValidation
	}
	observation.ObservedAt = observation.ObservedAt.UTC()
	observation.ReceivedAt = s.now().UTC()
	if observation.CLIVersion == "" {
		observation.CLIVersion = "unknown"
	}
	return s.store.ReportObservation(ctx, observation)
}

// WorkspaceRuntimes projects the runtimes an admin enabled for the workspace.
// The runtime's own declaration never grants visibility: only the frozen
// execution configuration does.
func (s *Service) WorkspaceRuntimes(ctx context.Context, workspaceID string) ([]RuntimeView, error) {
	config, exists, err := s.store.ExecutionConfig(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []RuntimeView{}, nil
	}
	views := make([]RuntimeView, 0, len(config.Runtimes))
	for _, runtimeID := range config.Runtimes {
		view, err := s.runtimeView(ctx, runtimeID)
		if err != nil {
			continue
		}
		views = append(views, view)
	}
	sort.Slice(views, func(left, right int) bool { return views[left].RuntimeID < views[right].RuntimeID })
	return views, nil
}

// WorkspaceRuntime projects one runtime only when it is authorized for the
// workspace.
func (s *Service) WorkspaceRuntime(ctx context.Context, workspaceID, runtimeID string) (RuntimeView, error) {
	config, exists, err := s.store.ExecutionConfig(ctx, workspaceID)
	if err != nil {
		return RuntimeView{}, err
	}
	if !exists {
		return RuntimeView{}, ErrForbidden
	}
	for _, authorized := range config.Runtimes {
		if authorized == runtimeID {
			return s.runtimeView(ctx, runtimeID)
		}
	}
	return RuntimeView{}, ErrForbidden
}

func (s *Service) runtimeView(ctx context.Context, runtimeID string) (RuntimeView, error) {
	status, err := s.source.RuntimeStatus(ctx, runtimeID)
	if err != nil {
		return RuntimeView{}, err
	}
	view := RuntimeView{
		RuntimeID: status.ID, GitHubUserID: status.GitHubUserID,
		ProviderID: status.Provider.ID, ProviderVersion: status.Provider.Version,
		ProviderAuthenticated: status.ProviderAuthenticated,
		Capabilities:          make([]Capability, 0, len(status.Capabilities)),
		RegisteredAt:          status.RegisteredAt.UTC(),
		Freshness:             FreshnessUnknown,
	}
	for _, capability := range status.Capabilities {
		view.Capabilities = append(view.Capabilities, Capability{ID: capability.ID, Version: capability.Version})
	}
	sort.Slice(view.Capabilities, func(left, right int) bool {
		if view.Capabilities[left].ID != view.Capabilities[right].ID {
			return view.Capabilities[left].ID < view.Capabilities[right].ID
		}
		return view.Capabilities[left].Version < view.Capabilities[right].Version
	})
	observation, found, err := s.store.LatestObservation(ctx, status.ID, status.Epoch)
	if err != nil {
		return RuntimeView{}, err
	}
	if !found {
		// An observation of a superseded generation is retained for audit but
		// must not make the current generation look fresh.
		if stale, staleFound, staleErr := s.store.LatestObservation(ctx, status.ID, ""); staleErr == nil && staleFound {
			view.Observation = &stale
			view.Freshness = FreshnessStale
			view.StaleReason = StaleSupersededEpoch
		}
		return view, nil
	}
	view.Observation = &observation
	if s.now().UTC().Sub(observation.ReceivedAt) > s.ttl {
		view.Freshness = FreshnessStale
		view.StaleReason = StaleExpired
		return view, nil
	}
	view.Freshness = FreshnessFresh
	return view, nil
}

// validWorkspaceID is deliberately shape-agnostic: the workspace ID grammar is
// owned by backend:S01 (base64url alphabet, which may start with `-` or `_`),
// and the authorization authority is the membership lookup per request. Local
// validation only rejects empty or absurdly long input.
func validWorkspaceID(workspaceID string) bool {
	return len(workspaceID) > 0 && len(workspaceID) <= 200
}

// ExecutionConfig reads the frozen configuration, or a zero version when the
// workspace has none yet.
func (s *Service) ExecutionConfig(ctx context.Context, workspaceID string) (ExecutionConfig, error) {
	if !validWorkspaceID(workspaceID) {
		return ExecutionConfig{}, ErrValidation
	}
	config, exists, err := s.store.ExecutionConfig(ctx, workspaceID)
	if err != nil {
		return ExecutionConfig{}, err
	}
	if !exists {
		return ExecutionConfig{WorkspaceID: workspaceID, Capabilities: []Capability{}, Runtimes: []string{}}, nil
	}
	return config, nil
}

// UpdateExecutionConfig applies one admin mutation: exact versions, idempotent
// per (workspace, caller, requestKey), conflicting on version drift.
func (s *Service) UpdateExecutionConfig(
	ctx context.Context,
	command UpdateExecutionConfigCommand,
) (ExecutionConfig, error) {
	if !validWorkspaceID(command.WorkspaceID) ||
		!requestKeyPattern.MatchString(command.RequestKey) ||
		command.ActorID == "" || command.ExpectedVersion < 0 {
		return ExecutionConfig{}, ErrValidation
	}
	seen := map[string]bool{}
	capabilities := make([]Capability, 0, len(command.Capabilities))
	for _, capability := range command.Capabilities {
		if !capabilityPattern.MatchString(capability.ID) || capability.Version < 1 || capability.Version > 65535 {
			return ExecutionConfig{}, ErrValidation
		}
		key := fmt.Sprintf("%s@%d", capability.ID, capability.Version)
		if seen[key] {
			return ExecutionConfig{}, ErrValidation
		}
		seen[key] = true
		capabilities = append(capabilities, capability)
	}
	runtimes := append([]string{}, command.Runtimes...)
	seenRuntimes := map[string]bool{}
	for _, runtimeID := range runtimes {
		if !runtimeIDPattern.MatchString(runtimeID) || seenRuntimes[runtimeID] {
			return ExecutionConfig{}, ErrValidation
		}
		seenRuntimes[runtimeID] = true
	}
	sort.Slice(capabilities, func(left, right int) bool {
		if capabilities[left].ID != capabilities[right].ID {
			return capabilities[left].ID < capabilities[right].ID
		}
		return capabilities[left].Version < capabilities[right].Version
	})
	sort.Strings(runtimes)
	digest := requestDigest(command)
	normalized := UpdateExecutionConfigCommand{
		WorkspaceID: command.WorkspaceID, ActorID: command.ActorID,
		RequestKey: command.RequestKey, ExpectedVersion: command.ExpectedVersion,
		Capabilities: capabilities, Runtimes: runtimes,
	}
	return s.store.SaveExecutionConfig(ctx, normalized, digest, s.now().UTC())
}

func requestDigest(command UpdateExecutionConfigCommand) string {
	content := struct {
		Capabilities []Capability `json:"capabilities"`
		Runtimes     []string     `json:"runtimes"`
	}{Capabilities: command.Capabilities, Runtimes: command.Runtimes}
	encoded, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
