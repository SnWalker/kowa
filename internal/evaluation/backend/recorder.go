package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SnWalker/kowa/internal/observability"
)

// UnavailableRecoveryCondition is the frozen recovery condition recorded when a
// synchronous gate cannot be confirmed. It names the fact an operator has to
// restore, so a gate is never simply "unmet" with no explanation.
const UnavailableRecoveryCondition = "evaluation backend availability must be restored and the record re-submitted"

// RejectedRecoveryCondition is the frozen recovery condition recorded when the
// backend refused a record permanently.
const RejectedRecoveryCondition = "the record must be corrected and re-submitted"

// IDGenerator creates opaque record ids.
type IDGenerator interface {
	New() (string, error)
}

// SubmitCommand is one evaluation submission. Identities come from the caller;
// verdicts never do.
type SubmitCommand struct {
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
}

// GateOutcome is the answer to "may this synchronous gate be treated as
// satisfied". Satisfied is only ever true when the backend confirmed the record;
// an unavailable backend yields false plus the recovery condition.
type GateOutcome struct {
	Satisfied         bool
	RecoveryCondition string
	Record            Record
}

// Recorder admits evaluation records and delivers them to one backend. It owns
// no delivery state: every method that touches the backend either returns an
// observational status or an outcome a gate owner may consult, and none of them
// can change whether a node, a run or a delivery succeeded.
type Recorder struct {
	backend Backend
	spool   Spool
	ids     IDGenerator
	now     func() time.Time
	// maxAttempts bounds the autonomous retry of one record. It is a bound on
	// delivery attempts, not a bound on the record's life.
	maxAttempts int
}

// RecorderOption customizes a Recorder.
type RecorderOption func(*Recorder)

// WithMaxAttempts bounds how often Retry re-delivers one record.
func WithMaxAttempts(attempts int) RecorderOption {
	return func(recorder *Recorder) {
		if attempts > 0 {
			recorder.maxAttempts = attempts
		}
	}
}

// NewRecorder constructs a recorder over one backend and one spool.
func NewRecorder(backend Backend, spool Spool, ids IDGenerator, now func() time.Time, options ...RecorderOption) *Recorder {
	if now == nil {
		now = time.Now
	}
	recorder := &Recorder{backend: backend, spool: spool, ids: ids, now: now, maxAttempts: 5}
	for _, option := range options {
		option(recorder)
	}
	return recorder
}

// SubmitAsync admits one asynchronous evaluation record. A backend failure is
// recorded in the returned record's status and is never returned as an error:
// an offline regression, a cost statistic or a knowledge suggestion failing can
// never be read as a delivery failure.
func (r *Recorder) SubmitAsync(ctx context.Context, command SubmitCommand) (Record, error) {
	if command.Mode != ModeAsync {
		return Record{}, fmt.Errorf("%w: SubmitAsync requires the async mode", ErrValidation)
	}
	return r.submit(ctx, command)
}

// SubmitSyncGate admits one record that the delivery contract declares as a
// blocking gate. It reports the gate as satisfied only when the backend
// confirmed the record. An unavailable backend leaves the gate unsatisfied and
// records the recovery condition instead of degrading to "assumed pass".
func (r *Recorder) SubmitSyncGate(ctx context.Context, command SubmitCommand) (GateOutcome, error) {
	if command.Mode != ModeSyncGate {
		return GateOutcome{}, fmt.Errorf("%w: SubmitSyncGate requires the sync_gate mode", ErrValidation)
	}
	record, err := r.submit(ctx, command)
	if err != nil {
		return GateOutcome{}, err
	}
	outcome := GateOutcome{Record: record, Satisfied: record.Status == StatusAccepted}
	if !outcome.Satisfied {
		outcome.RecoveryCondition = record.RecoveryCondition
	}
	return outcome, nil
}

// Retry re-delivers every record whose last attempt could change. It returns how
// many records were re-delivered and how many are still pending; a failure to
// reach the backend is reported through the records, not as an error.
func (r *Recorder) Retry(ctx context.Context) (retried int, pending int, err error) {
	if r.spool == nil {
		return 0, 0, fmt.Errorf("%w: no spool is configured", ErrSpool)
	}
	records, err := r.spool.List(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, record := range records {
		if !record.Status.Retryable() || record.Attempts >= r.maxAttempts {
			if record.Status.Retryable() {
				pending++
			}
			continue
		}
		delivered, deliverErr := r.deliver(ctx, record)
		if deliverErr != nil {
			return retried, pending, deliverErr
		}
		retried++
		if delivered.Status.Retryable() {
			pending++
		}
	}
	return retried, pending, nil
}

// Records lists the admitted records, oldest first.
func (r *Recorder) Records(ctx context.Context) ([]Record, error) {
	if r.spool == nil {
		return nil, fmt.Errorf("%w: no spool is configured", ErrSpool)
	}
	records, err := r.spool.List(ctx)
	if err != nil {
		return nil, err
	}
	if records == nil {
		records = []Record{}
	}
	return records, nil
}

// PendingCount reports how many records have not settled, for the health
// projection.
func (r *Recorder) PendingCount(ctx context.Context) (int, error) {
	records, err := r.Records(ctx)
	if err != nil {
		return 0, err
	}
	pending := 0
	for _, record := range records {
		if record.Status.Retryable() {
			pending++
		}
	}
	return pending, nil
}

// Health reports the backend's own availability.
func (r *Recorder) Health(ctx context.Context) Health {
	if r.backend == nil {
		return Health{Available: false, Reason: "no evaluation backend is configured", CheckedAt: r.now().UTC()}
	}
	health := r.backend.Health(ctx)
	if health.CheckedAt.IsZero() {
		health.CheckedAt = r.now().UTC()
	}
	if health.Backend == "" {
		health.Backend = r.backend.Name()
	}
	return health
}

// BackendName identifies the configured adapter.
func (r *Recorder) BackendName() string {
	if r.backend == nil {
		return ""
	}
	return r.backend.Name()
}

func (r *Recorder) submit(ctx context.Context, command SubmitCommand) (Record, error) {
	if !KnownKind(command.Kind) || !KnownMode(command.Mode) {
		return Record{}, fmt.Errorf("%w: unknown kind or mode", ErrValidation)
	}
	for _, identity := range []string{command.WorkspaceID, command.WorkItemID, command.WorkflowRunID, command.NodeRunID, command.TaskID} {
		if !validIdentity(identity) {
			return Record{}, fmt.Errorf("%w: malformed identity %q", ErrValidation, identity)
		}
	}
	if !validDigest(command.SubjectDigest) {
		return Record{}, fmt.Errorf("%w: malformed subject digest", ErrValidation)
	}
	if err := command.Trace.Validate(); err != nil {
		return Record{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if r.ids == nil {
		return Record{}, fmt.Errorf("%w: no id generator is configured", ErrValidation)
	}
	recordID, err := r.ids.New()
	if err != nil {
		return Record{}, fmt.Errorf("generate evaluation record id: %w", err)
	}
	now := r.now().UTC()
	record := Record{
		RecordID: recordID, Kind: command.Kind, Mode: command.Mode,
		WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		WorkflowRunID: command.WorkflowRunID, NodeRunID: command.NodeRunID, TaskID: command.TaskID,
		SubjectDigest: command.SubjectDigest, Trace: command.Trace,
		Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	}
	if r.spool == nil {
		return Record{}, fmt.Errorf("%w: no spool is configured", ErrSpool)
	}
	if err := r.spool.Append(ctx, record); err != nil {
		return Record{}, err
	}
	return r.deliver(ctx, record)
}

// deliver makes one attempt and records its outcome. The envelope is built from
// the record only; the adapter never receives the caller's command.
func (r *Recorder) deliver(ctx context.Context, record Record) (Record, error) {
	if r.backend == nil {
		return r.settle(ctx, record, StatusUnavailable, "no evaluation backend is configured")
	}
	envelope := Envelope{
		RecordID: record.RecordID, Kind: record.Kind, Mode: record.Mode,
		WorkspaceID: record.WorkspaceID, WorkItemID: record.WorkItemID,
		WorkflowRunID: record.WorkflowRunID, NodeRunID: record.NodeRunID, TaskID: record.TaskID,
		SubjectDigest: record.SubjectDigest, Trace: record.Trace,
		Attributes: map[string]string{}, At: record.CreatedAt,
	}
	err := r.backend.Emit(ctx, envelope)
	switch {
	case err == nil:
		return r.settle(ctx, record, StatusAccepted, "")
	case errors.Is(err, ErrRejected):
		return r.settle(ctx, record, StatusRejected, observational(err))
	default:
		// Anything else — a transport failure, a timeout, an unrecognised
		// answer — is treated as unavailable, because "we could not tell" must
		// never be read as "it passed".
		return r.settle(ctx, record, StatusUnavailable, observational(err))
	}
}

func (r *Recorder) settle(ctx context.Context, record Record, status Status, reason string) (Record, error) {
	now := r.now().UTC()
	record.Status = status
	record.Attempts++
	record.LastAttemptAt = &now
	record.UpdatedAt = now
	record.FailureReason = reason
	record.Backend = r.BackendName()
	record.RecoveryCondition = ""
	switch {
	case record.Status.Retryable():
		record.RecoveryCondition = UnavailableRecoveryCondition
	case record.Mode == ModeSyncGate:
		// A synchronous gate that was refused permanently is still unmet; the
		// condition names the real cause instead of blaming availability.
		record.RecoveryCondition = RejectedRecoveryCondition
	}
	if err := r.spool.Update(ctx, record); err != nil {
		// The attempt happened; only the record of it could not be stored. That
		// is an observation gap and is reported as one.
		return record, err
	}
	return record, nil
}

// observational keeps a reason readable and free of credentials: a failure
// reason comes from a transport and is shown to operators.
func observational(err error) string {
	reason := observability.Redact(err.Error())
	if len(reason) > 300 {
		reason = reason[:300]
	}
	return reason
}
