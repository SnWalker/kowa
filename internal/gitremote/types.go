// Package gitremote owns the frozen "Git remote availability and permission
// gap" projection for the runtime view. It reports only real observations:
// which identity asked, which machine identity is registered, which identity
// actually queried GitHub, whether the remote was really readable, and which
// permission GitHub itself reports. A login, a registration or the presence of
// a CLI never proves availability; an unprobed or unreadable fact stays
// unknown and a real write observation is explicitly absent until one happens.
package gitremote

import (
	"context"
	"errors"
	"regexp"
	"time"
)

const (
	SchemaVersion       = "kowa.delivery-governance.v1"
	GitRemoteStatusKind = "gitRemoteStatus"
)

var (
	ErrValidation = errors.New("gitremote: invalid input")
	ErrForbidden  = errors.New("gitremote: forbidden")
)

// Repository roles, matching the Workspace binding vocabulary.
const (
	RoleProject   = "project"
	RoleKnowledge = "knowledge"
)

// Remote read states.
const (
	ReadReadable   = "readable"
	ReadUnreadable = "unreadable"
	ReadUnknown    = "unknown"
)

// Push permission states: GitHub's own permission fact, which is not a write
// observation.
const (
	PermissionGranted = "granted"
	PermissionDenied  = "denied"
	PermissionUnknown = "unknown"
)

// Write observation states: whether Kowa actually observed a real write through
// the remote. Never inferred from a permission fact.
const WriteObservationNone = "none"

// Identity match states.
const (
	IdentityMatched = "matched"
	IdentityDrifted = "drifted"
	IdentityUnknown = "unknown"
)

// RepositoryRef is one repository binding of the workspace, re-read by the
// caller from the S01 authority.
type RepositoryRef struct {
	RepositoryID string `json:"repositoryId"`
	Role         string `json:"role"`
}

// StatusCommand reads the projection for one workspace.
type StatusCommand struct {
	WorkspaceID             string
	RequestedByGitHubUserID string
	Repositories            []RepositoryRef
}

// RemoteRead is the real read observation of one repository remote.
type RemoteRead struct {
	State     string `json:"state"`
	Transport string `json:"transport,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// PushPermission is GitHub's reported permission of the observed identity.
type PushPermission struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// RepositoryStatus is the projection of one repository.
type RepositoryStatus struct {
	RepositoryID           string         `json:"repositoryId"`
	Role                   string         `json:"role"`
	RemoteRead             RemoteRead     `json:"remoteRead"`
	PushPermission         PushPermission `json:"pushPermission"`
	WriteObservation       string         `json:"writeObservation"`
	ObservedAt             time.Time      `json:"observedAt"`
	ObservedByGitHubUserID string         `json:"observedByGitHubUserId,omitempty"`
}

// MachineIdentity separates the registered machine identities from the
// identity that actually performed the query.
type MachineIdentity struct {
	RuntimeIDs              []string `json:"runtimeIds"`
	RegisteredGitHubUserIDs []string `json:"registeredGitHubUserIds"`
	ObservedGitHubUserID    string   `json:"observedGitHubUserId,omitempty"`
	State                   string   `json:"state"`
	Reason                  string   `json:"reason,omitempty"`
}

// Status is the frozen projection consumed by the runtime view.
type Status struct {
	SchemaVersion           string             `json:"schemaVersion"`
	Kind                    string             `json:"kind"`
	WorkspaceID             string             `json:"workspaceId"`
	RequestedByGitHubUserID string             `json:"requestedByGitHubUserId"`
	ProbeOutcome            string             `json:"probeOutcome"`
	ProbeReason             string             `json:"probeReason,omitempty"`
	ObservedAt              time.Time          `json:"observedAt"`
	MachineIdentity         MachineIdentity    `json:"machineIdentity"`
	Repositories            []RepositoryStatus `json:"repositories"`
}

// MachineIdentitySource reports the runtime ids and their registered machine
// GitHub identities for one workspace; the runtime-operations service owns
// those facts.
type MachineIdentitySource interface {
	MachineIdentities(ctx context.Context, workspaceID string) (runtimeIDs []string, gitHubUserIDs []string, err error)
}

// RepositoryFacts reads the identity and permission facts of one repository
// through the verified gh identity; the github observation seam owns them.
type RepositoryFacts interface {
	RepositoryFullName(ctx context.Context, repositoryID string) (fullName string, actorID string, err error)
	PushPermission(ctx context.Context, repositoryID string) (granted bool, actorID string, err error)
}

var externalIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
