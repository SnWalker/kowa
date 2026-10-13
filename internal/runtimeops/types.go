// Package runtimeops owns runtime observation governance: capability-ladder
// observations reported by Runners, the web projection of authorized runtimes,
// and the admin-gated workspace execution configuration.
package runtimeops

import (
	"errors"
	"time"
)

const SchemaVersion = "kowa.runtime-web.v1"

var (
	ErrValidation      = errors.New("runtimeops: invalid input")
	ErrNotFound        = errors.New("runtimeops: not found")
	ErrVersionConflict = errors.New("runtimeops: version conflict")
	ErrRequestConflict = errors.New("runtimeops: request key reused with different content")
	ErrForbidden       = errors.New("runtimeops: forbidden")
)

// Freshness values never merge the four ladder layers into one "healthy"
// answer: unknown and stale are distinct from fresh.
const (
	FreshnessFresh   = "fresh"
	FreshnessStale   = "stale"
	FreshnessUnknown = "unknown"
)

// Stale reasons refine a stale projection; an unknown projection has none.
const (
	StaleExpired         = "expired"
	StaleSupersededEpoch = "superseded-epoch"
)

// Observation is one capability-ladder observation reported by a Runtime.
// ObservedAt is the Runner clock; ReceivedAt is the server watermark and is the
// only time used for staleness decisions.
type Observation struct {
	RuntimeID         string    `json:"runtimeId"`
	RuntimeEpoch      string    `json:"runtimeEpoch"`
	ObservedAt        time.Time `json:"observedAt"`
	ReceivedAt        time.Time `json:"receivedAt"`
	Executable        bool      `json:"executable"`
	CLIVersion        string    `json:"cliVersion"`
	VersionCompatible bool      `json:"versionCompatible"`
	Authenticated     bool      `json:"authenticated"`
	CanExecute        bool      `json:"canExecute"`
	Detail            string    `json:"detail,omitempty"`
}

// Capability identifies one exact capability version.
type Capability struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// ExecutionConfig is the admin-frozen execution configuration of a workspace:
// which exact capabilities may run and which runtimes are authorized there.
// Run admission (S05/S06) consumes one concrete version.
type ExecutionConfig struct {
	WorkspaceID           string       `json:"workspaceId"`
	Version               int64        `json:"version"`
	Capabilities          []Capability `json:"capabilities"`
	Runtimes              []string     `json:"runtimes"`
	RequestKey            string       `json:"-"`
	RequestDigest         string       `json:"-"`
	UpdatedByGitHubUserID string       `json:"updatedByGitHubUserId"`
	UpdatedAt             time.Time    `json:"updatedAt"`
}

// RuntimeView is the web projection of one authorized runtime. It carries no
// credentials, environment values or task content.
type RuntimeView struct {
	RuntimeID             string       `json:"runtimeId"`
	GitHubUserID          string       `json:"gitHubUserId"`
	ProviderID            string       `json:"providerId"`
	ProviderVersion       string       `json:"providerVersion"`
	ProviderAuthenticated bool         `json:"providerAuthenticated"`
	Capabilities          []Capability `json:"capabilities"`
	RegisteredAt          time.Time    `json:"registeredAt"`
	Freshness             string       `json:"freshness"`
	StaleReason           string       `json:"staleReason,omitempty"`
	Observation           *Observation `json:"observation,omitempty"`
}

// UpdateExecutionConfigCommand carries one idempotent admin mutation.
type UpdateExecutionConfigCommand struct {
	WorkspaceID     string
	ActorID         string
	RequestKey      string
	ExpectedVersion int64
	Capabilities    []Capability
	Runtimes        []string
}
