// Package workitem owns the WorkItem aggregate: the frozen intake versions, the
// OPEN/CLOSED/CANCELED lifecycle, and the single delivery run that holds
// advancement authority. Creation records a goal; it never starts a workflow.
package workitem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

const (
	SchemaVersion = "kowa.workitem.v1"

	WorkItemCreatedKind = "workItem"
	WorkItemListKind    = "workItemList"

	maxTitleLength      = 300
	maxContentLength    = 20000
	maxRequestKeyLength = 128
)

var (
	ErrValidation          = errors.New("workitem: invalid input")
	ErrNotFound            = errors.New("workitem: not found")
	ErrForbidden           = errors.New("workitem: forbidden")
	ErrVersionConflict     = errors.New("workitem: version conflict")
	ErrPolicyBlocked       = errors.New("workitem: policy blocked")
	ErrResourceUnavailable = errors.New("workitem: resource unavailable")
	ErrExternalUnknown     = errors.New("workitem: external state is unknown")

	requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	externalIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	oidPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// State is the frozen WorkItem lifecycle. A closed or canceled WorkItem never
// returns to OPEN; later changes create a new WorkItem.
type State string

const (
	StateOpen     State = "OPEN"
	StateClosed   State = "CLOSED"
	StateCanceled State = "CANCELED"
)

// Actor is the authenticated server-side caller.
type Actor struct {
	GitHubUserID string
}

// VersionedContent is one frozen intake version: the exact content and the
// digest the server computed over it. Prefilled or malformed digests are never
// trusted from the wire.
type VersionedContent struct {
	Version int    `json:"version"`
	Digest  string `json:"digest"`
	Content string `json:"content"`
}

// ContentDigest is the single digest rule for frozen intake versions.
func ContentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validVersionedContent(value VersionedContent) bool {
	return value.Version >= 1 && value.Content != "" && len(value.Content) <= maxContentLength
}

// WorkItem is the authorized projection of one work item.
type WorkItem struct {
	ID                 string           `json:"workItemId"`
	WorkspaceID        string           `json:"workspaceId"`
	Title              string           `json:"title"`
	TargetRepositoryID string           `json:"targetRepositoryId"`
	Requirement        VersionedContent `json:"requirement"`
	Acceptance         VersionedContent `json:"acceptance"`
	State              State            `json:"state"`
	Version            int64            `json:"version"`
	CurrentRunID       string           `json:"currentRunId,omitempty"`
	CreatedByUserID    string           `json:"createdByGitHubUserId"`
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
	ClosedAt           *time.Time       `json:"closedAt,omitempty"`
	CanceledAt         *time.Time       `json:"canceledAt,omitempty"`
}

// RunBindingKind distinguishes the first delivery run from a repair run that
// takes over advancement authority after the previous run ended.
type RunBindingKind string

const (
	BindingInitial RunBindingKind = "initial"
	BindingRepair  RunBindingKind = "repair"
)

// RunBinding is the durable lineage of one run under a WorkItem. A repair
// binding records the source run, the reason and the merged baseline; it never
// inherits approvals from the run it replaces.
type RunBinding struct {
	WorkItemID        string         `json:"workItemId"`
	RunID             string         `json:"workflowRunId"`
	Kind              RunBindingKind `json:"kind"`
	SourceRunID       string         `json:"sourceRunId,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	MergedBaselineOID string         `json:"mergedBaselineOid,omitempty"`
	BoundByUserID     string         `json:"boundByGitHubUserId"`
	BoundAt           time.Time      `json:"boundAt"`
}

// CreateCommand is the validated intake. The target repository is resolved from
// the Workspace project binding by the server, never chosen by the client.
type CreateCommand struct {
	WorkspaceID string
	RequestKey  string
	Title       string
	Requirement VersionedContent
	Acceptance  VersionedContent
}

// CancelCommand terminates the goal. It is accepted only when every external
// operation of the WorkItem has been reconciled: Kowa never claims that an
// already-performed external side effect was rolled back.
type CancelCommand struct {
	WorkspaceID     string
	WorkItemID      string
	RequestKey      string
	ExpectedVersion int64
	Reason          string
}

// AssociateRunCommand binds one workflow run to the WorkItem as the run that
// holds advancement authority. The producer is run admission (backend:S06).
type AssociateRunCommand struct {
	WorkspaceID       string
	WorkItemID        string
	RunID             string
	Kind              RunBindingKind
	SourceRunID       string
	Reason            string
	MergedBaselineOID string
	ExpectedVersion   int64
	ActorID           string
	RequestKey        string
}

// CreateMutation is the atomic store command for creation.
type CreateMutation struct {
	WorkItem        WorkItem
	Actor           Actor
	RequestKey      string
	RequestDigest   string
	TargetProjectID string
}

// CancelMutation is the atomic store command for cancellation.
type CancelMutation struct {
	WorkspaceID     string
	WorkItemID      string
	ExpectedVersion int64
	Actor           Actor
	RequestKey      string
	RequestDigest   string
	Reason          string
	CanceledAt      time.Time
}

// AssociateRunMutation is the atomic store command for taking or replacing
// advancement authority.
type AssociateRunMutation struct {
	WorkItemID      string
	ExpectedVersion int64
	Binding         RunBinding
	RequestKey      string
	RequestDigest   string
	BoundAt         time.Time
}

// IDGenerator creates opaque WorkItem ids.
type IDGenerator interface {
	New() (string, error)
}

// ProjectBindingSource reports the single writable project repository of a
// Workspace; the Identity/Workspace service owns that fact.
type ProjectBindingSource interface {
	ProjectRepositoryID(ctx context.Context, workspaceID string) (string, error)
}

// RunSource reports whether a delivery run has ended. The execution service
// owns run state; this port only consumes its settlement judgment.
type RunSource interface {
	RunSettled(ctx context.Context, runID string) (bool, error)
}

// ExternalOperationGuard reports whether a WorkItem still has external
// operations whose outcome is pending or unknown. The GitHub delivery service
// owns that fact; an unreconciled side effect blocks cancellation.
type ExternalOperationGuard interface {
	UnsettledOperations(ctx context.Context, workspaceID, workItemID string) (int, error)
}
