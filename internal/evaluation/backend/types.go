// Package backend owns the evaluation backend port. A backend (self-hosted
// Langfuse in the MVP) receives Kowa's evaluation and trajectory records through
// this boundary and is replaceable behind it.
//
// Two rules are structural, not conventions:
//
//   - A backend never owns Kowa state. The port can write records and report its
//     own health; it has no read path back into Kowa, and nothing in this package
//     can reach the delivery authority. The import guard in
//     import_guard_test.go keeps it that way.
//   - A synchronous gate and an asynchronous evaluation are separated. An
//     unavailable backend leaves a synchronous gate unsatisfied with a recorded
//     recovery condition and never degrades to "assumed pass"; an asynchronous
//     failure is recorded and retried and never changes whether a delivery
//     succeeded.
package backend

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/SnWalker/kowa/internal/observability"
)

// SchemaVersion is the frozen identity of an evaluation record.
const SchemaVersion = "kowa.evaluation.v1"

var (
	// ErrValidation marks a record that cannot be admitted.
	ErrValidation = errors.New("evaluation backend: invalid record")
	// ErrUnavailable marks a backend that could not be reached. It is retryable
	// and never authority to pass a gate.
	ErrUnavailable = errors.New("evaluation backend: unavailable")
	// ErrRejected marks a record the backend refused. Retrying cannot help.
	ErrRejected = errors.New("evaluation backend: record rejected")
	// ErrGateUnmet marks a synchronous gate that is not satisfied.
	ErrGateUnmet = errors.New("evaluation backend: synchronous gate is not satisfied")
	// ErrSpool is a local spool failure. It is an observation problem, never a
	// business one.
	ErrSpool = errors.New("evaluation backend: observation spool failed")
)

// Mode separates the two ways an evaluation result is used. The mode is frozen
// by the delivery contract of the moment, never chosen at failure time.
type Mode string

const (
	// ModeSyncGate is a gate the delivery contract declares as blocking: it must
	// be confirmed by the backend before it can be reported satisfied.
	ModeSyncGate Mode = "sync_gate"
	// ModeAsync is an offline regression, a cost/latency statistic, a knowledge
	// suggestion or a calibration run. Its failure is recorded and retried; it
	// never changes an already decided delivery.
	ModeAsync Mode = "async"
)

// KnownMode reports whether the mode is part of the frozen vocabulary.
func KnownMode(mode Mode) bool { return mode == ModeSyncGate || mode == ModeAsync }

// Kind is the frozen evaluation vocabulary. It mirrors the auxiliary evidence
// kinds the delivery governance already records, so a reader never has to map
// two naming systems.
type Kind string

const (
	KindOfflineRegression   Kind = "offline_regression"
	KindCostLatency         Kind = "cost_latency"
	KindKnowledgeSuggestion Kind = "knowledge_suggestion"
	KindCalibration         Kind = "calibration"
	KindSecurityEvaluation  Kind = "security_evaluation"
)

// KnownKind reports whether the kind is part of the frozen vocabulary.
func KnownKind(kind Kind) bool {
	switch kind {
	case KindOfflineRegression, KindCostLatency, KindKnowledgeSuggestion,
		KindCalibration, KindSecurityEvaluation:
		return true
	default:
		return false
	}
}

// Status is the outcome of the last delivery attempt of one record.
type Status string

const (
	// StatusPending means the record was admitted but never delivered.
	StatusPending Status = "pending"
	// StatusAccepted means the backend confirmed the record.
	StatusAccepted Status = "accepted"
	// StatusUnavailable means the backend could not be reached. The record is
	// kept for a later attempt.
	StatusUnavailable Status = "unavailable"
	// StatusRejected means the backend refused the record permanently.
	StatusRejected Status = "rejected"
)

// Retryable reports whether another attempt could change the status.
func (s Status) Retryable() bool { return s == StatusPending || s == StatusUnavailable }

// Record is one evaluation record and its delivery state. It is an observation
// record: losing it costs a trajectory, never a business fact.
type Record struct {
	RecordID          string                     `json:"recordId"`
	Kind              Kind                       `json:"kind"`
	Mode              Mode                       `json:"mode"`
	WorkspaceID       string                     `json:"workspaceId,omitempty"`
	WorkItemID        string                     `json:"workItemId,omitempty"`
	WorkflowRunID     string                     `json:"workflowRunId,omitempty"`
	NodeRunID         string                     `json:"nodeRunId,omitempty"`
	TaskID            string                     `json:"taskId,omitempty"`
	SubjectDigest     string                     `json:"subjectDigest,omitempty"`
	Trace             observability.TraceContext `json:"trace"`
	Backend           string                     `json:"backend,omitempty"`
	BackendVersion    string                     `json:"backendVersion,omitempty"`
	Status            Status                     `json:"status"`
	Attempts          int                        `json:"attempts"`
	LastAttemptAt     *time.Time                 `json:"lastAttemptAt,omitempty"`
	FailureReason     string                     `json:"failureReason,omitempty"`
	RecoveryCondition string                     `json:"recoveryCondition,omitempty"`
	CreatedAt         time.Time                  `json:"createdAt"`
	UpdatedAt         time.Time                  `json:"updatedAt"`
}

// Envelope is what one backend receives. It carries identities, digests and
// short classifications — never a prompt body, a complete user input or a
// credential. The adapter may only narrow it further.
type Envelope struct {
	RecordID      string
	Kind          Kind
	Mode          Mode
	WorkspaceID   string
	WorkItemID    string
	WorkflowRunID string
	NodeRunID     string
	TaskID        string
	SubjectDigest string
	Trace         observability.TraceContext
	Attributes    map[string]string
	At            time.Time
}

// Health is a backend's own availability statement.
type Health struct {
	Available bool      `json:"available"`
	Backend   string    `json:"backend,omitempty"`
	Version   string    `json:"version,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// Backend is the replaceable port. Implementations must classify a transport
// failure as ErrUnavailable and a permanent refusal as ErrRejected, and must
// never report a gate as satisfied.
type Backend interface {
	// Name identifies the adapter (for example "langfuse").
	Name() string
	// Emit delivers one record. It is safe to retry an ErrUnavailable result;
	// the same RecordID must never create a second backend object.
	Emit(ctx context.Context, envelope Envelope) error
	// Health reports the backend's own availability.
	Health(ctx context.Context) Health
}

// Spool persists records between attempts. It is a local observation store: a
// spool that fails is reported as an observation gap and never as a business
// failure.
type Spool interface {
	Append(ctx context.Context, record Record) error
	Update(ctx context.Context, record Record) error
	List(ctx context.Context) ([]Record, error)
}

var (
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$`)
)

// validDigest accepts the frozen content-digest shape; an absent digest is
// allowed because a record may describe an event rather than an artifact.
func validDigest(digest string) bool { return digest == "" || digestPattern.MatchString(digest) }

func validIdentity(identity string) bool { return identity == "" || idPattern.MatchString(identity) }
