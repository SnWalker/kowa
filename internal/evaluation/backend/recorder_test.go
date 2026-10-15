// secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.

package backend_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation/backend"
	"github.com/SnWalker/kowa/internal/observability"
)

type stubIDGenerator struct{ next int }

func (g *stubIDGenerator) New() (string, error) {
	g.next++
	return "eval_" + strings.Repeat("0", 4-len(itoa(g.next))) + itoa(g.next), nil
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

// stubBackend fails or answers on demand and counts its deliveries, so a test
// can prove that an unavailable backend was actually attempted.
type stubBackend struct {
	name         string
	err          error
	calls        int
	health       backend.Health
	lastEnvelope backend.Envelope
}

func (b *stubBackend) Name() string { return b.name }

func (b *stubBackend) Emit(_ context.Context, envelope backend.Envelope) error {
	b.calls++
	b.lastEnvelope = envelope
	return b.err
}

func (b *stubBackend) Health(context.Context) backend.Health { return b.health }

func fixedTrace() observability.TraceContext {
	return observability.TraceForAttempt("run_1", "node_2", "task_3")
}

func asyncCommand() backend.SubmitCommand {
	return backend.SubmitCommand{
		Kind: backend.KindOfflineRegression, Mode: backend.ModeAsync,
		WorkspaceID: "ws_1", WorkItemID: "wi_1", WorkflowRunID: "run_1", NodeRunID: "node_2",
		Trace: fixedTrace(),
	}
}

// TestAsyncFailureNeverBecomesAnError is the S07 invariant: an asynchronous
// evaluation that cannot be delivered is recorded and reported through the
// record, and the caller is never given an error it could turn into a delivery
// failure.
func TestAsyncFailureNeverBecomesAnError(t *testing.T) {
	stub := &stubBackend{name: "stub", err: errors.New("connection refused")}
	spool := backend.NewMemorySpool()
	recorder := backend.NewRecorder(stub, spool, &stubIDGenerator{}, time.Now)

	record, err := recorder.SubmitAsync(context.Background(), asyncCommand())
	if err != nil {
		t.Fatalf("an asynchronous backend failure must not be an error: %v", err)
	}
	if record.Status != backend.StatusUnavailable {
		t.Fatalf("the record must report the failure, got %s", record.Status)
	}
	if record.RecoveryCondition == "" {
		t.Fatalf("the record must name a recovery condition: %+v", record)
	}
	if record.Attempts != 1 || record.LastAttemptAt == nil {
		t.Fatalf("the attempt must be recorded: %+v", record)
	}
	if stub.calls != 1 {
		t.Fatalf("the backend must actually be attempted, got %d calls", stub.calls)
	}
	// An observation problem cannot be read as a business one: the record is
	// kept for a later attempt rather than dropped.
	stored, err := recorder.Records(context.Background())
	if err != nil || len(stored) != 1 || !stored[0].Status.Retryable() {
		t.Fatalf("the failed record must stay retryable: %+v (%v)", stored, err)
	}
}

func TestRetryDeliversAPreviouslyUnavailableRecord(t *testing.T) {
	stub := &stubBackend{name: "stub", err: backend.ErrUnavailable}
	spool := backend.NewMemorySpool()
	recorder := backend.NewRecorder(stub, spool, &stubIDGenerator{}, time.Now)
	if _, err := recorder.SubmitAsync(context.Background(), asyncCommand()); err != nil {
		t.Fatalf("submitting failed: %v", err)
	}

	stub.err = nil
	retried, pending, err := recorder.Retry(context.Background())
	if err != nil {
		t.Fatalf("retrying failed: %v", err)
	}
	if retried != 1 || pending != 0 {
		t.Fatalf("retry must re-deliver the record: retried=%d pending=%d", retried, pending)
	}
	records, _ := recorder.Records(context.Background())
	if records[0].Status != backend.StatusAccepted || records[0].Attempts != 2 {
		t.Fatalf("the retry must settle the record: %+v", records[0])
	}
}

func TestRetryIsBoundedAndReportsWhatIsStillPending(t *testing.T) {
	stub := &stubBackend{name: "stub", err: backend.ErrUnavailable}
	recorder := backend.NewRecorder(stub, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now,
		backend.WithMaxAttempts(2))
	ctx := context.Background()
	if _, err := recorder.SubmitAsync(ctx, asyncCommand()); err != nil {
		t.Fatalf("submitting failed: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, _, err := recorder.Retry(ctx); err != nil {
			t.Fatalf("retrying failed: %v", err)
		}
	}
	pending, err := recorder.PendingCount(ctx)
	if err != nil {
		t.Fatalf("counting failed: %v", err)
	}
	if pending != 1 {
		t.Fatalf("a bounded-out record must still be reported as pending, got %d", pending)
	}
	if stub.calls > 2 {
		t.Fatalf("the retry bound must be honoured, got %d deliveries", stub.calls)
	}
}

// TestSyncGateNeverDegradesToAssumedPass is the third S07 invariant: with the
// backend unreachable the gate stays unmet and the recovery condition is named;
// once the backend answers, a legal re-submission satisfies it.
func TestSyncGateNeverDegradesToAssumedPass(t *testing.T) {
	stub := &stubBackend{name: "stub", err: backend.ErrUnavailable}
	recorder := backend.NewRecorder(stub, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	command := backend.SubmitCommand{
		Kind: backend.KindSecurityEvaluation, Mode: backend.ModeSyncGate,
		WorkspaceID: "ws_1", WorkItemID: "wi_1", Trace: fixedTrace(),
	}
	outcome, err := recorder.SubmitSyncGate(context.Background(), command)
	if err != nil {
		t.Fatalf("an unreachable backend is an outcome, not an error: %v", err)
	}
	if outcome.Satisfied {
		t.Fatal("an unavailable backend must never satisfy a synchronous gate")
	}
	if outcome.RecoveryCondition != backend.UnavailableRecoveryCondition {
		t.Fatalf("the recovery condition must be recorded, got %q", outcome.RecoveryCondition)
	}

	stub.err = nil
	outcome, err = recorder.SubmitSyncGate(context.Background(), command)
	if err != nil {
		t.Fatalf("re-submitting failed: %v", err)
	}
	if !outcome.Satisfied || outcome.RecoveryCondition != "" {
		t.Fatalf("a confirmed record must satisfy the gate: %+v", outcome)
	}
}

func TestSyncGateRefusalNamesTheRealCause(t *testing.T) {
	stub := &stubBackend{name: "stub", err: backend.ErrRejected}
	recorder := backend.NewRecorder(stub, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	outcome, err := recorder.SubmitSyncGate(context.Background(), backend.SubmitCommand{
		Kind: backend.KindSecurityEvaluation, Mode: backend.ModeSyncGate,
		WorkspaceID: "ws_1", Trace: fixedTrace(),
	})
	if err != nil {
		t.Fatalf("a refusal is an outcome, not an error: %v", err)
	}
	if outcome.Satisfied {
		t.Fatal("a refused record must not satisfy a gate")
	}
	if outcome.RecoveryCondition != backend.RejectedRecoveryCondition {
		t.Fatalf("a permanent refusal must not be reported as unavailability: %q", outcome.RecoveryCondition)
	}
	if outcome.Record.Status.Retryable() {
		t.Fatalf("a refused record must not be retried forever: %+v", outcome.Record)
	}
}

func TestModesAreNotInterchangeable(t *testing.T) {
	recorder := backend.NewRecorder(&stubBackend{name: "stub"}, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	if _, err := recorder.SubmitAsync(context.Background(), backend.SubmitCommand{
		Kind: backend.KindOfflineRegression, Mode: backend.ModeSyncGate, WorkspaceID: "ws_1", Trace: fixedTrace(),
	}); err == nil {
		t.Fatal("SubmitAsync must refuse a synchronous gate")
	}
	if _, err := recorder.SubmitSyncGate(context.Background(), backend.SubmitCommand{
		Kind: backend.KindOfflineRegression, Mode: backend.ModeAsync, WorkspaceID: "ws_1", Trace: fixedTrace(),
	}); err == nil {
		t.Fatal("SubmitSyncGate must refuse an asynchronous record")
	}
}

func TestRecorderRefusesMalformedCommands(t *testing.T) {
	recorder := backend.NewRecorder(&stubBackend{name: "stub"}, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	cases := map[string]backend.SubmitCommand{
		"unknown kind":      {Kind: "invented", Mode: backend.ModeAsync, WorkspaceID: "ws_1", Trace: fixedTrace()},
		"missing trace":     {Kind: backend.KindCalibration, Mode: backend.ModeAsync, WorkspaceID: "ws_1"},
		"malformed digest":  {Kind: backend.KindCalibration, Mode: backend.ModeAsync, WorkspaceID: "ws_1", SubjectDigest: "sha256:zz", Trace: fixedTrace()},
		"malformed subject": {Kind: backend.KindCalibration, Mode: backend.ModeAsync, WorkspaceID: "not a workspace", Trace: fixedTrace()},
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := recorder.SubmitAsync(context.Background(), command); !errors.Is(err, backend.ErrValidation) {
				t.Fatalf("want a validation refusal, got %v", err)
			}
		})
	}
}

func TestBackendFailureReasonIsScrubbed(t *testing.T) {
	stub := &stubBackend{name: "stub", err: errors.New(
		"post https://pk-lf-12345678:sk-lf-abcdefghij@langfuse.internal/api/public/otel/v1/traces: dial tcp: timeout")}
	recorder := backend.NewRecorder(stub, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	record, err := recorder.SubmitAsync(context.Background(), asyncCommand())
	if err != nil {
		t.Fatalf("submitting failed: %v", err)
	}
	if strings.Contains(record.FailureReason, "sk-lf-abcdefghij") || strings.Contains(record.FailureReason, "pk-lf-12345678") {
		t.Fatalf("a credential reached the recorded failure reason: %q", record.FailureReason)
	}
	if !strings.Contains(record.FailureReason, "langfuse.internal") {
		t.Fatalf("the reason must stay useful: %q", record.FailureReason)
	}
}

func TestFileSpoolSurvivesARestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "spool")
	first, err := backend.NewFileSpool(directory)
	if err != nil {
		t.Fatalf("creating the spool failed: %v", err)
	}
	record := backend.Record{
		RecordID: "eval_0001", Kind: backend.KindCostLatency, Mode: backend.ModeAsync,
		WorkspaceID: "ws_1", Trace: fixedTrace(), Status: backend.StatusPending,
		Attempts: 1, CreatedAt: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC),
	}
	if err := first.Append(context.Background(), record); err != nil {
		t.Fatalf("appending failed: %v", err)
	}
	// A second instance is what a restarted process sees.
	second, err := backend.NewFileSpool(directory)
	if err != nil {
		t.Fatalf("reopening the spool failed: %v", err)
	}
	records, err := second.List(context.Background())
	if err != nil || len(records) != 1 {
		t.Fatalf("the record must survive a restart: %+v (%v)", records, err)
	}
	if records[0].RecordID != "eval_0001" || records[0].Kind != backend.KindCostLatency {
		t.Fatalf("the record lost its content: %+v", records[0])
	}
	// A second delivery of the same record must update, not duplicate.
	record.Status = backend.StatusAccepted
	if err := second.Update(context.Background(), record); err != nil {
		t.Fatalf("updating failed: %v", err)
	}
	records, _ = second.List(context.Background())
	if len(records) != 1 || records[0].Status != backend.StatusAccepted {
		t.Fatalf("the update must be in place: %+v", records)
	}
	if err := second.Append(context.Background(), record); err == nil {
		t.Fatal("appending the same record twice must be refused")
	}
}

func TestFileSpoolRefusesAnEmptyDirectory(t *testing.T) {
	if _, err := backend.NewFileSpool("  "); !errors.Is(err, backend.ErrSpool) {
		t.Fatalf("an empty directory must be refused, got %v", err)
	}
}

func TestRecorderReportsBackendHealth(t *testing.T) {
	stub := &stubBackend{name: "stub", health: backend.Health{Available: true, Version: "3.0.0"}}
	recorder := backend.NewRecorder(stub, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	health := recorder.Health(context.Background())
	if !health.Available || health.Version != "3.0.0" || health.Backend != "stub" || health.CheckedAt.IsZero() {
		t.Fatalf("the health statement must be passed through and stamped: %+v", health)
	}
	empty := backend.NewRecorder(nil, backend.NewMemorySpool(), &stubIDGenerator{}, time.Now)
	if health := empty.Health(context.Background()); health.Available {
		t.Fatalf("an absent backend must not read as available: %+v", health)
	}
}
