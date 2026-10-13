package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
	"github.com/SnWalker/kowa/internal/provider"
	"github.com/SnWalker/kowa/internal/runnertransport"
)

// errCancelDirective is the cause used when the control plane directs a cancel
// for the active attempt (run cancellation), as opposed to process shutdown.
var errCancelDirective = errors.New("runner: cancel directed by the control plane")

// LoopConfig bounds the frozen M1 execution loop.
type LoopConfig struct {
	RuntimeID       string
	GitHubUserID    string
	Epoch           string
	Capabilities    []execution.Capability
	ProviderVersion string
	ProviderModel   string
	LeaseSeconds    int
	WaitSeconds     int
	HeartbeatEvery  time.Duration
	ReportWindow    time.Duration
	BackoffMin      time.Duration
	BackoffMax      time.Duration
	WorkRoot        string
	// ReportMargin keeps execution and result reporting inside the lease
	// validity window; it bounds the input-fetch window as well.
	ReportMargin time.Duration
	// ProbeExecution requests the fourth provider ladder layer (a minimal real
	// execution) at startup; it costs provider quota, so it is opt-in.
	ProbeExecution bool
}

// Loop drives one Runner process: register, recover, long-poll, execute and
// report. It never re-executes an attempt because of a restart, a duplicate
// delivery or a lost response.
type Loop struct {
	client  *runnertransport.Client
	journal *Journal
	host    *Host
	adapter provider.Adapter
	config  LoopConfig
	logger  *slog.Logger
	now     func() time.Time

	mu     sync.Mutex
	active *activeAttempt
}

type activeAttempt struct {
	taskID string
	cancel context.CancelCauseFunc
}

func NewLoop(
	client *runnertransport.Client,
	journal *Journal,
	host *Host,
	adapter provider.Adapter,
	config LoopConfig,
	logger *slog.Logger,
) *Loop {
	if logger == nil {
		logger = slog.Default()
	}
	if config.HeartbeatEvery <= 0 {
		config.HeartbeatEvery = 10 * time.Second
	}
	if config.ReportWindow <= 0 {
		config.ReportWindow = 5 * time.Minute
	}
	if config.ReportMargin <= 0 {
		config.ReportMargin = 30 * time.Second
	}
	if config.BackoffMin <= 0 {
		config.BackoffMin = time.Second
	}
	if config.BackoffMax <= 0 {
		config.BackoffMax = 30 * time.Second
	}
	return &Loop{
		client: client, journal: journal, host: host, adapter: adapter,
		config: config, logger: logger, now: time.Now,
	}
}

// Run registers, recovers previous attempts and then serves leases until the
// context ends. Fatal transport conditions return an error (non-zero exit);
// process shutdown returns nil.
func (l *Loop) Run(ctx context.Context) error {
	if l.config.Epoch == "" {
		epoch, err := l.journal.Epoch()
		if err != nil {
			return err
		}
		l.config.Epoch = epoch
	}
	l.client.Epoch = l.config.Epoch
	registration, err := l.register(ctx)
	if err != nil {
		return err
	}
	l.logger.Info("runner registered",
		"runtimeId", registration.ID, "epoch", registration.Epoch,
		"provider", registration.Provider.ID+"/"+registration.Provider.Version,
		"providerAuthenticated", registration.ProviderAuthenticated,
		"capabilities", len(registration.Capabilities))
	if err := l.recover(ctx); err != nil {
		return err
	}
	heartbeatFailures := make(chan error, 1)
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	defer stopHeartbeat()
	go l.heartbeatLoop(heartbeatCtx, heartbeatFailures)
	return l.pollLoop(ctx, heartbeatFailures)
}

func (l *Loop) register(ctx context.Context) (execution.RuntimeRegistration, error) {
	probe, err := l.adapter.Probe(ctx)
	if err != nil {
		l.logger.Warn("provider probe failed; registering as not executable", "error", err)
	}
	if l.config.ProbeExecution {
		if prober, ok := l.adapter.(provider.ExecutionProber); ok {
			executionProbe, probeErr := prober.ProbeExecution(ctx)
			if probeErr != nil {
				l.logger.Warn("provider execution probe failed", "error", probeErr)
			} else {
				probe = executionProbe
				l.logger.Info("provider execution probe",
					"executable", probe.Executable, "version", probe.Version,
					"versionCompatible", probe.VersionCompatible,
					"authenticated", probe.Authenticated, "canExecute", probe.CanExecute,
					"detail", probe.Detail)
			}
		}
	}
	version := l.config.ProviderVersion
	if version == "" {
		version = probe.Version
	}
	if version == "" {
		version = "unversioned"
	}
	registration := execution.RuntimeRegistration{
		ID: l.config.RuntimeID, Epoch: l.config.Epoch, GitHubUserID: l.config.GitHubUserID,
		Provider:              execution.ProviderSelection{ID: l.adapter.ID(), Version: version},
		ProviderAuthenticated: probe.Authenticated,
		Capabilities:          append([]execution.Capability{}, l.config.Capabilities...),
	}
	backoff := l.config.BackoffMin
	rotated := false
	for {
		if _, err := l.client.Register(ctx, registration); err == nil {
			return registration, nil
		} else if errors.Is(err, runnertransport.ErrConflict) && !rotated {
			// The persisted generation exists with a different declaration
			// (for example the provider CLI was upgraded): this Runner must
			// declare a new generation instead of mutating the old one.
			rotated = true
			epoch, rotateErr := l.journal.RotateEpoch()
			if rotateErr != nil {
				return execution.RuntimeRegistration{}, rotateErr
			}
			l.logger.Warn("registration declaration changed; declaring a new epoch",
				"previousEpoch", registration.Epoch, "newEpoch", epoch)
			registration.Epoch = epoch
			l.config.Epoch = epoch
			l.client.Epoch = epoch
			continue
		} else if fatalTransportError(err) || errors.Is(err, runnertransport.ErrConflict) {
			return execution.RuntimeRegistration{}, fmt.Errorf("registration rejected: %w", err)
		} else if ctx.Err() != nil {
			return execution.RuntimeRegistration{}, ctx.Err()
		} else {
			l.logger.Warn("registration attempt failed; retrying", "error", err, "backoff", backoff)
		}
		if !sleepContext(ctx, jitter(backoff)) {
			return execution.RuntimeRegistration{}, ctx.Err()
		}
		backoff = growBackoff(backoff, l.config.BackoffMax)
	}
}

// recover converges attempts that were in flight when a previous Runner
// process stopped. It first cleans escaped worker processes, then re-reports a
// journalled result or investigates the live attempt; it never re-executes.
func (l *Loop) recover(ctx context.Context) error {
	records, err := l.journal.Attempts()
	if err != nil {
		return err
	}
	for _, record := range records {
		if residual := proc.TerminateSurvivors(record.Marker); len(residual) > 0 {
			l.logger.Warn("recovered escaped worker processes",
				"task", record.TaskID, "count", len(residual))
			_ = proc.WriteResidualEvidence(
				filepath.Join(record.Workspace, "residual.json"), record.Marker, residual)
		}
		if err := l.recoverAttempt(ctx, record); err != nil {
			if fatalTransportError(err) || ctx.Err() != nil {
				return err
			}
			l.logger.Warn("attempt recovery deferred", "task", record.TaskID, "error", err)
		}
	}
	return nil
}

func (l *Loop) recoverAttempt(ctx context.Context, record AttemptRecord) error {
	if payload, pending, err := l.journal.Result(record.TaskID); err != nil {
		return err
	} else if pending {
		ack, err := l.client.ReportResultPayload(ctx, record.TaskID, payload)
		if err == nil {
			l.logger.Info("pending result re-delivered", "task", record.TaskID, "duplicate", ack.Duplicate)
			return l.clearAttempt(record.TaskID)
		}
		if settleableTransportError(err) {
			l.logger.Info("pending result dropped as settled", "task", record.TaskID, "error", err)
			return l.clearAttempt(record.TaskID)
		}
		return err
	}
	status, err := l.client.TaskStatus(ctx, record.TaskID)
	if err != nil {
		if settleableTransportError(err) {
			l.logger.Info("in-flight attempt is no longer visible; dropping journal record",
				"task", record.TaskID, "error", err)
			return l.clearAttempt(record.TaskID)
		}
		return err
	}
	current := status.RuntimeID == record.RuntimeID && status.LeaseID == record.LeaseID &&
		status.FencingToken == record.FencingToken &&
		(status.State == string(execution.TaskLeased) || status.State == string(execution.TaskRunning))
	if current && status.LeaseExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339Nano, status.LeaseExpiresAt)
		if err != nil || !l.now().UTC().Before(expiresAt) {
			current = false
		}
	}
	if !current {
		l.logger.Info("in-flight attempt was superseded; dropping journal record",
			"task", record.TaskID, "state", status.State)
		return l.clearAttempt(record.TaskID)
	}
	// The lease is still current but the worker died with the previous Runner
	// process: converge to a deterministic failure. Re-execution requires a new
	// attempt authorized by the control plane.
	_, payload, buildErr := buildResultPayload(record.TaskID, record.LeaseID, record.FencingToken,
		provider.Failed(provider.CodeRuntimeRestarted,
			"runner restarted while the attempt was in flight; a new attempt must be authorized", true))
	if buildErr != nil {
		return buildErr
	}
	if _, err := l.client.ReportResultPayload(ctx, record.TaskID, payload); err != nil &&
		!settleableTransportError(err) {
		return err
	}
	l.logger.Info("in-flight attempt converged after restart", "task", record.TaskID)
	return l.clearAttempt(record.TaskID)
}

func (l *Loop) clearAttempt(taskID string) error {
	if err := l.journal.ClearResult(taskID); err != nil {
		return err
	}
	return l.journal.ClearAttempt(taskID)
}

func (l *Loop) pollLoop(ctx context.Context, heartbeatFailures <-chan error) error {
	backoff := l.config.BackoffMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		select {
		case err := <-heartbeatFailures:
			return err
		default:
		}
		lease, found, err := l.client.Lease(ctx, l.config.WaitSeconds, l.config.LeaseSeconds)
		if err != nil {
			if fatalTransportError(err) {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			l.logger.Warn("lease poll failed; backing off", "error", err, "backoff", backoff)
			if !sleepContext(ctx, jitter(backoff)) {
				return nil
			}
			backoff = growBackoff(backoff, l.config.BackoffMax)
			continue
		}
		backoff = l.config.BackoffMin
		if !found {
			continue
		}
		if err := l.execute(ctx, lease); err != nil {
			if fatalTransportError(err) {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			l.logger.Warn("attempt execution failed", "task", lease.Task.ID, "error", err)
		}
	}
}

func (l *Loop) execute(ctx context.Context, lease execution.TaskLease) error {
	taskID := lease.Task.ID
	marker, err := randomHex(16)
	if err != nil {
		return err
	}
	workspace := filepath.Join(l.config.WorkRoot, "tasks",
		fmt.Sprintf("%s-f%d", SafeName(taskID), lease.FencingToken))
	record := AttemptRecord{
		TaskID: taskID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		RuntimeID: l.config.RuntimeID, RuntimeEpoch: l.config.Epoch,
		WorkflowRunID: lease.Task.WorkflowRunID, NodeRunID: lease.Task.NodeRunID,
		AttemptKind: lease.Task.AttemptKind, PreviousTaskID: lease.Task.PreviousTaskID,
		Capability: lease.Task.Capability, Phase: PhaseExecuting,
		Workspace: workspace, Marker: marker, StartedAt: l.now().UTC(), UpdatedAt: l.now().UTC(),
	}
	if err := l.journal.SaveAttempt(record); err != nil {
		return err
	}
	l.reportProgress(ctx, lease, 1, "executing")
	bundlePath, inputErr := l.fetchFrozenInput(ctx, lease.Task, lease, workspace)
	if inputErr != nil {
		return l.settleInputFailure(ctx, lease, record, inputErr)
	}
	sessionRef := l.sessionFor(lease)
	execCtx, cancel := context.WithCancelCause(ctx)
	l.setActive(taskID, cancel)
	outcome := l.host.Execute(execCtx, ExecutionRequest{
		Task: lease.Task, Lease: lease, Workspace: workspace,
		Marker: marker, SessionRef: sessionRef, BundlePath: bundlePath,
	})
	cause := context.Cause(execCtx)
	l.clearActive(taskID)
	cancel(nil)
	switch {
	case errors.Is(cause, errCancelDirective):
		outcome = provider.Failed(provider.CodeCanceled, "attempt canceled at control-plane direction", false)
	case cause != nil && ctx.Err() != nil:
		outcome = provider.Failed(provider.CodeRuntimeShutdown,
			"runner is shutting down; the attempt must be re-authorized", true)
	}
	if outcome.SessionRef != "" {
		l.saveSession(record, outcome.SessionRef)
	}
	digest, payload, buildErr := buildResultPayload(taskID, lease.LeaseID, lease.FencingToken, outcome)
	if buildErr != nil {
		digest, payload, buildErr = buildResultPayload(taskID, lease.LeaseID, lease.FencingToken,
			provider.Failed(provider.CodeOutputInvalid, buildErr.Error(), false))
		if buildErr != nil {
			return buildErr
		}
	}
	record.Phase = PhaseReporting
	record.ResultDigest = digest
	record.UpdatedAt = l.now().UTC()
	if err := l.journal.SaveAttempt(record); err != nil {
		return err
	}
	l.reportProgress(ctx, lease, 2, "settled")
	l.logger.Info("attempt settled",
		"task", taskID, "status", outcome.Status, "digest", digest,
		"durationMs", outcome.Telemetry.DurationMS, "residual", len(outcome.Telemetry.Residual))
	return l.reportResult(ctx, taskID, payload)
}

// reportResult delivers the journalled payload, retrying transient failures for
// a bounded window. When the window passes the payload stays journalled and a
// later recovery re-delivers it byte for byte.
func (l *Loop) reportResult(ctx context.Context, taskID string, payload []byte) error {
	deadline := l.now().Add(l.config.ReportWindow)
	backoff := l.config.BackoffMin
	for {
		reportCtx := ctx
		if ctx.Err() != nil {
			// Shutdown: one best-effort delivery outside the canceled context so
			// the attempt settles deterministically.
			var cancel context.CancelFunc
			reportCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
		}
		ack, err := l.client.ReportResultPayload(reportCtx, taskID, payload)
		if err == nil {
			l.logger.Info("result accepted", "task", taskID, "duplicate", ack.Duplicate)
			return l.clearAttempt(taskID)
		}
		if fatalTransportError(err) {
			return err
		}
		if settleableTransportError(err) {
			l.logger.Info("result dropped as settled", "task", taskID, "error", err)
			return l.clearAttempt(taskID)
		}
		if ctx.Err() != nil || l.now().After(deadline) {
			l.logger.Warn("result delivery deferred; journal retains the payload",
				"task", taskID, "error", err)
			return nil
		}
		l.logger.Warn("result delivery failed; retrying", "task", taskID, "error", err)
		if err := l.journal.SaveResult(taskID, payload); err != nil {
			return err
		}
		if !sleepContext(ctx, jitter(backoff)) {
			return nil
		}
		backoff = growBackoff(backoff, l.config.BackoffMax)
	}
}

// settleInputFailure converges an attempt whose frozen input could not be
// delivered. Verification failures settle instantly without executing;
// availability failures settle as retryable; lost tasks and fatal conditions
// propagate to the caller without fabricating a result.
func (l *Loop) settleInputFailure(
	ctx context.Context,
	lease execution.TaskLease,
	record AttemptRecord,
	inputErr error,
) error {
	switch {
	case errors.Is(inputErr, errInputInvalid):
		return l.settleWithFailure(ctx, lease, record,
			provider.Failed(provider.CodeInputInvalid, inputErr.Error(), false))
	case errors.Is(inputErr, errInputUnavailable):
		return l.settleWithFailure(ctx, lease, record,
			provider.Failed(provider.CodeInputUnavailable, inputErr.Error(), true))
	case settleableTransportError(inputErr):
		l.logger.Info("attempt is no longer current; dropping journal record",
			"task", lease.Task.ID, "error", inputErr)
		return l.clearAttempt(lease.Task.ID)
	default:
		// Shutdown or fatal transport conditions: keep the journal so the next
		// start investigates before any re-execution.
		return inputErr
	}
}

// settleWithFailure reports one deterministic failed result for an attempt that
// never executed and clears the journal on success.
func (l *Loop) settleWithFailure(
	ctx context.Context,
	lease execution.TaskLease,
	record AttemptRecord,
	outcome provider.Outcome,
) error {
	digest, payload, err := buildResultPayload(lease.Task.ID, lease.LeaseID, lease.FencingToken, outcome)
	if err != nil {
		return err
	}
	record.Phase = PhaseReporting
	record.ResultDigest = digest
	record.UpdatedAt = l.now().UTC()
	if err := l.journal.SaveAttempt(record); err != nil {
		return err
	}
	l.logger.Info("attempt settled without execution", "task", lease.Task.ID,
		"code", outcome.Failure.Code)
	return l.reportResult(ctx, lease.Task.ID, payload)
}

func (l *Loop) sessionFor(lease execution.TaskLease) string {
	if lease.Task.AttemptKind != execution.AttemptResume {
		return ""
	}
	previousTaskID := lease.Task.PreviousTaskID
	if previousTaskID == "" {
		return ""
	}
	record, found, err := l.journal.Session(previousTaskID)
	if err != nil || !found {
		return ""
	}
	if record.ProviderID != l.adapter.ID() {
		return ""
	}
	return record.SessionRef
}

func (l *Loop) saveSession(record AttemptRecord, sessionRef string) {
	err := l.journal.SaveSession(SessionRecord{
		TaskID: record.TaskID, WorkflowRunID: record.WorkflowRunID, NodeRunID: record.NodeRunID,
		ProviderID: l.adapter.ID(), SessionRef: sessionRef, UpdatedAt: l.now().UTC(),
	})
	if err != nil {
		l.logger.Warn("persist provider session", "task", record.TaskID, "error", err)
	}
}

func (l *Loop) reportProgress(ctx context.Context, lease execution.TaskLease, sequence int64, phase string) {
	if ctx.Err() != nil {
		return
	}
	err := l.client.ReportProgress(ctx, execution.ProgressReport{
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Sequence: sequence, Phase: phase, Message: phase,
		ObservedAt: l.now().UTC(),
	})
	if err != nil && !settleableTransportError(err) && !fatalTransportError(err) {
		l.logger.Warn("progress observation failed", "task", lease.Task.ID, "error", err)
	}
}

func (l *Loop) heartbeatLoop(ctx context.Context, failures chan<- error) {
	ticker := time.NewTicker(l.config.HeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ack, err := l.client.Heartbeat(ctx, l.activeTaskIDs())
			if err != nil {
				if fatalTransportError(err) {
					select {
					case failures <- err:
					default:
					}
					return
				}
				l.logger.Warn("heartbeat failed", "error", err)
				continue
			}
			for _, taskID := range ack.CancelTaskIDs {
				if l.cancelActive(taskID) {
					l.logger.Info("cancel directive applied", "task", taskID)
				}
			}
		}
	}
}

func (l *Loop) setActive(taskID string, cancel context.CancelCauseFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active = &activeAttempt{taskID: taskID, cancel: cancel}
}

func (l *Loop) clearActive(taskID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != nil && l.active.taskID == taskID {
		l.active = nil
	}
}

func (l *Loop) activeTaskIDs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		return []string{}
	}
	return []string{l.active.taskID}
}

func (l *Loop) cancelActive(taskID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil || l.active.taskID != taskID {
		return false
	}
	l.active.cancel(errCancelDirective)
	return true
}

// buildResultPayload maps an adapter outcome onto the frozen TaskResult wire,
// computes the digest over the content and returns both the digest and the
// exact bytes that must be journalled and reported.
func buildResultPayload(
	taskID string,
	leaseID string,
	fencingToken int64,
	outcome provider.Outcome,
) (string, []byte, error) {
	if outcome.Status == provider.StatusCompleted && len(outcome.Output) > 64 {
		outcome = provider.Failed(provider.CodeOutputInvalid,
			"structured output exceeds the frozen 64-property bound", false)
	}
	summary := proc.Truncate(outcome.Summary, 16384)
	if summary == "" {
		summary = "provider returned no summary"
	}
	type content struct {
		Status       execution.ResultStatus `json:"status"`
		Summary      string                 `json:"summary"`
		Output       *map[string]any        `json:"output,omitempty"`
		Error        *execution.TaskError   `json:"error,omitempty"`
		InputRequest *map[string]any        `json:"inputRequest,omitempty"`
	}
	value := content{Summary: summary}
	switch outcome.Status {
	case provider.StatusCompleted:
		output := outcome.Output
		if output == nil {
			output = map[string]any{}
		}
		value.Status = execution.ResultCompleted
		value.Output = &output
	case provider.StatusAwaitingInput:
		request := outcome.InputRequest
		if request == nil {
			request = map[string]any{}
		}
		value.Status = execution.ResultAwaitingInput
		value.InputRequest = &request
	default:
		failure := outcome.Failure
		if failure == nil {
			failure = &provider.Failure{Code: provider.CodeProviderProtocol, Message: summary}
		}
		message := proc.Truncate(failure.Message, 4096)
		if message == "" {
			message = failure.Code
		}
		value.Status = execution.ResultFailed
		value.Error = &execution.TaskError{Code: failure.Code, Message: message, Retryable: failure.Retryable}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", nil, fmt.Errorf("encode result content: %w", err)
	}
	digest := ResultDigest(canonical)
	payload, err := json.Marshal(struct {
		SchemaVersion string                 `json:"schemaVersion"`
		Kind          string                 `json:"kind"`
		TaskID        string                 `json:"taskId"`
		LeaseID       string                 `json:"leaseId"`
		FencingToken  int64                  `json:"fencingToken"`
		Status        execution.ResultStatus `json:"status"`
		ResultDigest  string                 `json:"resultDigest"`
		Summary       string                 `json:"summary"`
		Output        *map[string]any        `json:"output,omitempty"`
		Error         *execution.TaskError   `json:"error,omitempty"`
		InputRequest  *map[string]any        `json:"inputRequest,omitempty"`
	}{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: taskID, LeaseID: leaseID, FencingToken: fencingToken,
		Status: value.Status, ResultDigest: digest, Summary: summary,
		Output: value.Output, Error: value.Error, InputRequest: value.InputRequest,
	})
	if err != nil {
		return "", nil, fmt.Errorf("encode result payload: %w", err)
	}
	return digest, payload, nil
}

// SafeName maps an opaque identity to a filesystem-safe component.
func SafeName(value string) string {
	safe := strings.Map(func(character rune) rune {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9', character == '-', character == '_', character == '.':
			return character
		default:
			return '_'
		}
	}, value)
	if len(safe) > 80 {
		safe = safe[:80]
	}
	if safe != value || value == "" {
		safe = safe + "-" + ResultDigest([]byte(value))[7:15]
	}
	return safe
}

func fatalTransportError(err error) bool {
	return errors.Is(err, runnertransport.ErrUnauthorized) ||
		errors.Is(err, runnertransport.ErrSuperseded) ||
		errors.Is(err, runnertransport.ErrProtocol)
}

func settleableTransportError(err error) bool {
	return errors.Is(err, runnertransport.ErrStale) ||
		errors.Is(err, runnertransport.ErrConflict) ||
		errors.Is(err, runnertransport.ErrUnavailable) ||
		errors.Is(err, runnertransport.ErrForbidden)
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("runner: random source: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func growBackoff(current, maximum time.Duration) time.Duration {
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}

// jitter spreads retries so multiple Runners do not synchronize.
func jitter(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	spread, err := rand.Int(rand.Reader, big.NewInt(int64(base/4)+1))
	if err != nil {
		return base
	}
	return base + time.Duration(spread.Int64())
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
