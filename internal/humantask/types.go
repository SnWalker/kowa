// Package humantask owns versioned human requests and their two completion
// modes. A kowa_answer is a structured, authenticated answer accepted inside
// Kowa; an external_fact task is settled only by a reconciled GitHub fact and
// never by a browser "I approved" substitute. External-fact tasks therefore
// never produce a HumanResponse.
package humantask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	SchemaVersion = "kowa.delivery-governance.v1"

	HumanTaskKind     = "humanTask"
	HumanTaskListKind = "humanTaskList"
)

var (
	ErrValidation      = errors.New("humantask: invalid input")
	ErrNotFound        = errors.New("humantask: not found")
	ErrForbidden       = errors.New("humantask: forbidden")
	ErrVersionConflict = errors.New("humantask: version conflict")
	ErrPolicyBlocked   = errors.New("humantask: policy blocked")
	ErrNotImplemented  = errors.New("humantask: not implemented")
)

// Mode is the frozen completion mode of a human task.
type Mode string

const (
	// ModeKowaAnswer is answered by an authorized member inside Kowa.
	ModeKowaAnswer Mode = "kowa_answer"
	// ModeExternalFact is settled only by a reconciled external fact.
	ModeExternalFact Mode = "external_fact"
)

// State is the frozen HumanTask lifecycle.
type State string

const (
	StateOpen     State = "OPEN"
	StateResolved State = "RESOLVED"
	StateExpired  State = "EXPIRED"
	StateCanceled State = "CANCELED"
)

// Decision values accepted from an authenticated member.
const (
	DecisionApproved  = "approved"
	DecisionRejected  = "rejected"
	DecisionSubmitted = "submitted"
)

// Role is the subset of Workspace roles a task may be assigned to.
type Role string

const (
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// Field describes one structured answer field. Unknown fields are rejected;
// a prefilled value never counts as an answer.
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"` // text | decision | choice | comment
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// ObjectBinding freezes the exact object version an answer applies to. A later
// version of the same object never inherits this task's answer.
type ObjectBinding struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest,omitempty"`
}

// Request is the frozen, versioned request definition rendered by any channel.
type Request struct {
	Version    int           `json:"version"`
	Title      string        `json:"title"`
	Kind       string        `json:"kind"`
	Mode       Mode          `json:"completionMode"`
	Fields     []Field       `json:"fields"`
	Object     ObjectBinding `json:"object"`
	Candidates []string      `json:"candidates,omitempty"`
	Roles      []Role        `json:"roles,omitempty"`
}

// Response is one accepted kowa_answer. Its payload digest covers the exact
// submitted values, so an identical replay is idempotent and a different
// payload under the same request key is a conflict.
type Response struct {
	ResponseID     string            `json:"humanResponseId"`
	TaskID         string            `json:"humanTaskId"`
	WorkspaceID    string            `json:"workspaceId"`
	RequestVersion int               `json:"requestVersion"`
	Object         ObjectBinding     `json:"object"`
	ActorID        string            `json:"githubUserId"`
	Decision       string            `json:"decision"`
	Values         map[string]string `json:"values"`
	PayloadDigest  string            `json:"payloadDigest"`
	CreatedAt      time.Time         `json:"createdAt"`
}

// Continuation is the durable follow-up intent of an accepted answer. It is
// written in the same transaction as the response and the task resolution; the
// workflow (S06) consumes it and dispatches a new Task from the accepted
// answer. This package never starts a workflow run itself.
type Continuation struct {
	ID          string     `json:"continuationId"`
	ResponseID  string     `json:"humanResponseId"`
	TaskID      string     `json:"humanTaskId"`
	WorkspaceID string     `json:"workspaceId"`
	RunID       string     `json:"workflowRunId"`
	NodeRunID   string     `json:"nodeRunId"`
	Intent      string     `json:"intent"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"createdAt"`
	ConsumedAt  *time.Time `json:"consumedAt,omitempty"`
	ConsumedBy  string     `json:"consumedByGitHubUserId,omitempty"`
}

// Continuation intents.
const (
	IntentResumeNode     = "resume_node"
	ContinuationPending  = "PENDING"
	ContinuationConsumed = "CONSUMED"
)

// HumanTask is the authorized projection of one human request.
type HumanTask struct {
	ID               string        `json:"humanTaskId"`
	WorkspaceID      string        `json:"workspaceId"`
	WorkItemID       string        `json:"workItemId"`
	RunID            string        `json:"workflowRunId,omitempty"`
	NodeRunID        string        `json:"nodeRunId,omitempty"`
	Request          Request       `json:"request"`
	RequestDigest    string        `json:"requestDigest"`
	State            State         `json:"state"`
	Version          int64         `json:"version"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	ResolvedAt       *time.Time    `json:"resolvedAt,omitempty"`
	ResolvedByUserID string        `json:"resolvedByGitHubUserId,omitempty"`
	ResolutionKind   string        `json:"resolutionKind,omitempty"`
	ExternalFactID   string        `json:"externalObservationId,omitempty"`
	ExpiredReason    string        `json:"expiredReason,omitempty"`
	Response         *Response     `json:"response,omitempty"`
	Continuation     *Continuation `json:"continuation,omitempty"`
}

// CreateCommand creates one task. The producer is the workflow server side
// (S06); an external_fact task must bind a GitHub object.
type CreateCommand struct {
	WorkspaceID string
	WorkItemID  string
	RunID       string
	NodeRunID   string
	ActorID     string
	RequestKey  string
	Title       string
	Kind        string
	Mode        Mode
	Fields      []Field
	Object      ObjectBinding
	Candidates  []string
	Roles       []Role
}

// AnswerCommand is one authenticated kowa_answer. The client echoes the request
// version and object version it answered; both must still be the frozen ones.
type AnswerCommand struct {
	WorkspaceID    string
	TaskID         string
	RequestKey     string
	RequestVersion int
	Object         ObjectBinding
	Decision       string
	Values         map[string]string
}

// ResolveExternalFactCommand settles an external_fact task from a reconciled
// observation. It produces no HumanResponse.
type ResolveExternalFactCommand struct {
	WorkspaceID   string
	TaskID        string
	RequestKey    string
	ObservationID string
	ActorID       string
}

// ExpireCommand invalidates a task whose bound object moved on, or whose
// request version was superseded. A late answer to an expired task is refused.
type ExpireCommand struct {
	WorkspaceID string
	TaskID      string
	ActorID     string
	Reason      string
}

// CancelCommand cancels a task whose target was canceled.
type CancelCommand struct {
	WorkspaceID string
	TaskID      string
	ActorID     string
	Reason      string
}

// CreateMutation is the atomic store command for creation.
type CreateMutation struct {
	Task          HumanTask
	ActorID       string
	RequestKey    string
	RequestDigest string
}

// AnswerMutation is the atomic store command for one accepted answer: the
// response, the task resolution and the continuation intent commit together.
type AnswerMutation struct {
	TaskID        string
	WorkspaceID   string
	ActorID       string
	RequestKey    string
	RequestDigest string
	Response      Response
	Continuation  Continuation
}

// ResolveExternalFactMutation settles an external_fact task atomically.
type ResolveExternalFactMutation struct {
	TaskID        string
	WorkspaceID   string
	ActorID       string
	RequestKey    string
	RequestDigest string
	ObservationID string
	ResolvedAt    time.Time
}

// StateMutation expires or cancels one task.
type StateMutation struct {
	TaskID      string
	WorkspaceID string
	State       State
	Reason      string
	ActorID     string
	At          time.Time
}

// ConsumeContinuationMutation marks one continuation intent as consumed by the
// workflow that dispatched the follow-up task.
type ConsumeContinuationMutation struct {
	ContinuationID string
	WorkspaceID    string
	ActorID        string
	At             time.Time
}

// Store performs the atomic writes and reads of human tasks.
type Store interface {
	// ReplayTask returns the recorded outcome of an identical task command, or
	// false when this (actor, commandType, target, requestKey) was never
	// recorded. The same key with different content is a version conflict.
	ReplayTask(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (HumanTask, bool, error)
	// ReplayContinuation is the continuation analogue of ReplayTask.
	ReplayContinuation(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (Continuation, bool, error)
	Create(ctx context.Context, mutation CreateMutation) (HumanTask, error)
	Get(ctx context.Context, workspaceID, taskID string) (HumanTask, error)
	List(ctx context.Context, workspaceID string, openOnly bool) ([]HumanTask, error)
	Answer(ctx context.Context, mutation AnswerMutation) (HumanTask, error)
	ResolveExternalFact(ctx context.Context, mutation ResolveExternalFactMutation) (HumanTask, error)
	SetState(ctx context.Context, mutation StateMutation) (HumanTask, error)
	ExpireBoundTo(ctx context.Context, workspaceID string, object ObjectBinding, reason string, at time.Time) (int, error)
	PendingContinuations(ctx context.Context, workspaceID, runID string) ([]Continuation, error)
	ConsumeContinuation(ctx context.Context, mutation ConsumeContinuationMutation) (Continuation, error)
}

// IDGenerator creates opaque task, response and continuation ids.
type IDGenerator interface {
	New() (string, error)
}

// ObservationSource verifies that an external fact exists, belongs to the
// workspace, and matches the task's bound object. The GitHub delivery service
// owns that fact; an unknown or mismatched observation never settles a task.
type ObservationSource interface {
	FactMatches(ctx context.Context, workspaceID string, observationID string, object ObjectBinding) (bool, error)
}

var (
	requestKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	externalIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	kindPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldNamePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	oidPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Digest is the frozen request-digest rule for command idempotency.
func Digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func validRequestKey(key string) bool { return requestKeyPattern.MatchString(key) }
func validActor(actor string) bool    { return externalIDPattern.MatchString(actor) }
