// Package githubdelivery owns the control plane's side of GitHub delivery:
// admission and reconciliation of the controlled external operations
// (verify_change, publish_pr), the read-only pull-request/review/check/merge
// facts, and the delivery projection consumed by the Web and by closure.
//
// The control plane never writes to GitHub here and never accepts an agent's
// self-report as a platform fact: an operation result is accepted only from a
// reconciled observation, and an unreadable fact stays unknown.
package githubdelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

const (
	SchemaVersion = "kowa.delivery-governance.v1"

	ExternalOperationKind = "externalOperation"
	OperationListKind     = "externalOperationList"
	DeliveryViewKind      = "deliveryView"
)

var (
	ErrValidation      = errors.New("githubdelivery: invalid input")
	ErrNotFound        = errors.New("githubdelivery: not found")
	ErrForbidden       = errors.New("githubdelivery: forbidden")
	ErrVersionConflict = errors.New("githubdelivery: version conflict")
	ErrPolicyBlocked   = errors.New("githubdelivery: policy blocked")
	ErrExternalUnknown = errors.New("githubdelivery: external state is unknown")
	ErrNotImplemented  = errors.New("githubdelivery: not implemented")
)

// OperationKind is the frozen set of external operations this package admits.
type OperationKind string

const (
	// OperationVerifyChange reconciles one pushed commit against the remote
	// branch head; it never writes.
	OperationVerifyChange OperationKind = "verify_change"
	// OperationPublishPR admits and reconciles one pull-request creation. The
	// write itself belongs to the Runner's independent publish_pr capability.
	OperationPublishPR OperationKind = "publish_pr"
)

// OperationState is the frozen reconciliation state of one external operation.
type OperationState string

const (
	// StatePending means the operation was admitted and no confirmed external
	// effect exists yet.
	StatePending OperationState = "PENDING"
	// StateAttempted means a write was issued but the outcome is not yet
	// reconciled. A retry is forbidden until the object is searched for.
	StateAttempted OperationState = "ATTEMPTED"
	// StateSucceeded means a verified GitHub fact confirms the intended object.
	StateSucceeded OperationState = "SUCCEEDED"
	// StateFailed means reconciliation established that the intended object was
	// not created (or the target contradicts the request).
	StateFailed OperationState = "FAILED"
	// StateUnknown means the outcome cannot be established right now; the
	// operation is not retried, it is reconciled again later.
	StateUnknown OperationState = "UNKNOWN"
)

// Subject kinds of the observed GitHub facts.
const (
	SubjectPullRequest = "pull_request"
	SubjectReview      = "pull_request_review"
	SubjectChecks      = "pull_request_checks"
	SubjectBranchHead  = "branch_head"
)

// Checks states: `none` is distinct from `pending` and never reads as passed.
const (
	ChecksNone    = "none"
	ChecksPending = "pending"
	ChecksFailed  = "failed"
	ChecksPassed  = "passed"
)

// OperationRequest is the frozen intent of one external operation: the exact
// repository role, ref, commit and the identity that is allowed to perform it.
type OperationRequest struct {
	RepositoryID            string `json:"repositoryId"`
	Role                    string `json:"role"`
	Operation               string `json:"operation"`
	Ref                     string `json:"ref"`
	BaseRef                 string `json:"baseRef,omitempty"`
	CommitOID               string `json:"commitOid"`
	ExpectedGitHubUserID    string `json:"expectedGitHubUserId"`
	RequestedByGitHubUserID string `json:"requestedByGitHubUserId"`
	TitleArtifactID         string `json:"titleArtifactId,omitempty"`
}

// OperationResult is the reconciled external fact that settled one operation.
// Reconciled marks that an earlier unknown attempt was recovered by search
// instead of by a second write.
type OperationResult struct {
	RepositoryID         string    `json:"repositoryId"`
	Ref                  string    `json:"ref"`
	CommitOID            string    `json:"commitOid"`
	PullRequestNumber    string    `json:"pullRequestNumber,omitempty"`
	PullRequestAuthorID  string    `json:"pullRequestAuthorId,omitempty"`
	BaseRef              string    `json:"baseRef,omitempty"`
	Reconciled           bool      `json:"reconciled"`
	ObservationID        string    `json:"observationId"`
	ObservedAt           time.Time `json:"observedAt"`
	ObservedByGitHubUser string    `json:"observedByGitHubUserId"`
}

// ExternalOperation is one admitted external operation with its provenance.
type ExternalOperation struct {
	OperationID   string           `json:"operationId"`
	WorkspaceID   string           `json:"workspaceId"`
	WorkItemID    string           `json:"workItemId"`
	RunID         string           `json:"workflowRunId"`
	NodeRunID     string           `json:"nodeRunId,omitempty"`
	TaskID        string           `json:"taskId,omitempty"`
	Kind          OperationKind    `json:"kind"`
	State         OperationState   `json:"state"`
	Request       OperationRequest `json:"request"`
	RequestDigest string           `json:"requestDigest"`
	Result        *OperationResult `json:"result,omitempty"`
	UnknownReason string           `json:"unknownReason,omitempty"`
	Version       int64            `json:"version"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
	SettledAt     *time.Time       `json:"settledAt,omitempty"`
}

// Observation is one durable GitHub fact, or one failed read attempt. Unknown
// observations never replace a known fact: the fact columns stay untouched and
// only the attempt watermark moves.
type Observation struct {
	ObservationID string    `json:"observationId"`
	WorkspaceID   string    `json:"workspaceId"`
	RepositoryID  string    `json:"repositoryId"`
	SubjectKind   string    `json:"subjectKind"`
	SubjectKey    string    `json:"subjectKey"`
	ObservedAt    time.Time `json:"observedAt"`
	ActorID       string    `json:"actorId,omitempty"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`

	PullRequest *PullRequestFact `json:"pullRequest,omitempty"`
	Review      *ReviewFact      `json:"review,omitempty"`
	Checks      *ChecksFact      `json:"checks,omitempty"`
	Ref         *RefFact         `json:"ref,omitempty"`
}

// PullRequestFact is the observed pull request, including the actual merge
// result (commit oid, merging identity, time) — never the target branch tip.
type PullRequestFact struct {
	Number         string    `json:"number"`
	AuthorID       string    `json:"authorId"`
	HeadOID        string    `json:"headOid"`
	HeadRef        string    `json:"headRef"`
	BaseRef        string    `json:"baseRef"`
	State          string    `json:"state"`
	Merged         bool      `json:"merged"`
	MergeCommitOID string    `json:"mergeCommitOid,omitempty"`
	MergedByID     string    `json:"mergedById,omitempty"`
	MergedAt       time.Time `json:"mergedAt,omitempty"`
}

// ReviewFact is one observed review with the commit it applies to.
type ReviewFact struct {
	ReviewID   string    `json:"reviewId"`
	ReviewerID string    `json:"reviewerId"`
	State      string    `json:"state"`
	CommitOID  string    `json:"commitOid,omitempty"`
	Submitted  time.Time `json:"submittedAt,omitempty"`
}

// RefFact is the observed head of one branch.
type RefFact struct {
	CommitOID string `json:"commitOid"`
}

// ChecksFact is the aggregated check state of one commit.
type ChecksFact struct {
	CommitOID string `json:"commitOid"`
	State     string `json:"state"`
	Total     int    `json:"total"`
	Failed    int    `json:"failed"`
	Pending   int    `json:"pending"`
}

// AdmitOperationCommand admits one operation. Admission never performs a write.
type AdmitOperationCommand struct {
	WorkspaceID          string
	WorkItemID           string
	RunID                string
	NodeRunID            string
	TaskID               string
	Kind                 OperationKind
	RepositoryID         string
	Ref                  string
	BaseRef              string
	CommitOID            string
	TitleArtifactID      string
	RequestedByUserID    string
	RequestKey           string
	VerificationIDs      []string
	IndependentReviewIDs []string
}

// AdmitMutation is the atomic store command for admission.
type AdmitMutation struct {
	Operation     ExternalOperation
	RequestKey    string
	RequestDigest string
}

// UpdateMutation is the atomic, versioned operation transition.
type UpdateMutation struct {
	WorkspaceID     string
	OperationID     string
	ExpectedVersion int64
	State           OperationState
	Result          *OperationResult
	UnknownReason   string
	At              time.Time
}

// AbandonCommand settles an unknown operation as failed after reconciliation
// confirmed no external object exists. It never abandons blindly: the service
// re-observes first and refuses while the read stays unreadable.
type AbandonCommand struct {
	WorkspaceID     string
	OperationID     string
	ExpectedVersion int64
	ActorID         string
	RequestKey      string
	Reason          string
}

// RecordObservationMutation is the atomic store command for one observation.
type RecordObservationMutation struct {
	Observation Observation
}

// Store persists operations and observations.
type Store interface {
	CreateOperation(ctx context.Context, mutation AdmitMutation) (ExternalOperation, error)
	Operation(ctx context.Context, workspaceID, operationID string) (ExternalOperation, error)
	Operations(ctx context.Context, workspaceID, workItemID string) ([]ExternalOperation, error)
	OperationForRequest(ctx context.Context, workspaceID string, kind OperationKind, repositoryID, ref, commitOID string) (ExternalOperation, bool, error)
	UpdateOperation(ctx context.Context, mutation UpdateMutation) (ExternalOperation, error)
	RecordObservation(ctx context.Context, mutation RecordObservationMutation) (Observation, error)
	Observation(ctx context.Context, workspaceID, observationID string) (Observation, bool, error)
	LatestObservations(ctx context.Context, workspaceID, repositoryID string) ([]Observation, error)
	UnsettledOperations(ctx context.Context, workspaceID, workItemID string) (int, error)
}

// IDGenerator creates opaque operation and observation ids.
type IDGenerator interface {
	New() (string, error)
}

// RunSource reads the delivery run facts this package gates on. The execution
// service is the authority for run state and the frozen machine identity.
type RunSource interface {
	RunView(ctx context.Context, runID string) (execution.RunView, error)
	Run(ctx context.Context, runID string) (execution.Run, error)
}

// WorkItemSource reads WorkItem facts; creating and canceling them stays with
// the WorkItem service.
type WorkItemSource interface {
	Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error)
	RunBindings(ctx context.Context, workspaceID, workItemID string) ([]workitem.RunBinding, error)
}

// PullRequestProjection is the delivery fact set of one pull request as read
// from the reconciliation seam. The projection carries facts only; the service
// stamps the observation identity, watermark and acting identity.
type PullRequestProjection struct {
	RepositoryID  string
	Number        string
	PullRequest   PullRequestFact
	Reviews       []ReviewFact
	Checks        *ChecksFact
	ActorID       string
	Unknown       bool
	UnknownReason string
}

// FactSource reads real GitHub facts. Implemented by the verified gh adapter;
// every method reports unknown instead of guessing when the read fails.
type FactSource interface {
	PullRequest(ctx context.Context, repositoryID, number string) (PullRequestProjection, error)
	PullRequestsByHead(ctx context.Context, repositoryID, headBranch string) ([]PullRequestProjection, error)
	BranchHead(ctx context.Context, repositoryID, ref string) (string, bool, error)
}

// EvidenceSource reads verification evidence and the closure record. A missing
// or mismatched row is never treated as a passed gate.
type EvidenceSource interface {
	Verification(ctx context.Context, workspaceID, verificationID string) (evaluation.Verification, bool, error)
	VerificationsFor(ctx context.Context, workspaceID, workItemID string) ([]evaluation.Verification, error)
	Closure(ctx context.Context, workspaceID, workItemID string) (evaluation.ClosureRecord, bool, error)
}

// FactSettler settles external_fact human tasks from reconciled facts and
// expires tasks whose bound head moved on. The HumanTask service owns tasks.
type FactSettler interface {
	ResolveForObject(ctx context.Context, workspaceID string, objectKind, objectID, objectVersion, observationID, actorID string) (bool, error)
	ExpireBoundTo(ctx context.Context, workspaceID, objectKind, objectID, exceptVersion, reason, actorID string) (int, error)
}

var (
	requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	externalIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	oidPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	refPattern        = regexp.MustCompile(`^refs/heads/[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
)

// Digest is the frozen request-digest rule for command idempotency.
func Digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validRequestKey(key string) bool { return requestKeyPattern.MatchString(key) }
func validActor(actor string) bool    { return externalIDPattern.MatchString(actor) }
