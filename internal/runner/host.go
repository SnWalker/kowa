package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
	"github.com/SnWalker/kowa/internal/provider"
)

// HostConfig carries the supervision bounds frozen in the M1 contract.
type HostConfig struct {
	// Environment is the Runner process environment workers are derived from.
	Environment []string
	// EnvAllowlist names the only variables passed through to workers.
	EnvAllowlist []string
	// Grace bounds SIGTERM-to-SIGKILL escalation for a worker process group.
	Grace time.Duration
	// ReportMargin keeps the result report inside the lease validity window.
	ReportMargin  time.Duration
	ProviderModel string
	Now           func() time.Time
}

// Host supervises one attempt end to end: it enforces the effective deadline,
// prepares the isolated workspace and worker environment, invokes the adapter
// and persists attempt evidence.
type Host struct {
	adapter provider.Adapter
	config  HostConfig
	logger  *slog.Logger
}

func NewHost(adapter provider.Adapter, config HostConfig, logger *slog.Logger) *Host {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Grace <= 0 {
		config.Grace = 10 * time.Second
	}
	if config.ReportMargin <= 0 {
		config.ReportMargin = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Host{adapter: adapter, config: config, logger: logger}
}

// ExecutionRequest is one attempt to run: the frozen TaskSpec, its lease
// identity and the already-created attempt workspace.
type ExecutionRequest struct {
	Task       execution.TaskSpec
	Lease      execution.TaskLease
	Workspace  string
	WorkDir    string
	Marker     string
	SessionRef string
	BundlePath string
}

// Execute runs the attempt and always returns an outcome; infrastructure
// errors that prevent reporting are returned as a failed outcome instead.
func (h *Host) Execute(ctx context.Context, request ExecutionRequest) provider.Outcome {
	now := h.config.Now().UTC()
	effectiveDeadline := request.Task.DeadlineAt.UTC()
	leaseDeadline := request.Lease.ExpiresAt.UTC().Add(-h.config.ReportMargin)
	if leaseDeadline.Before(effectiveDeadline) {
		effectiveDeadline = leaseDeadline
	}
	if !now.Before(effectiveDeadline) {
		return provider.Failed(provider.CodeDeadlineExceeded,
			"attempt deadline elapsed before execution could start", false)
	}
	workDir := request.WorkDir
	if workDir == "" {
		workDir = filepath.Join(request.Workspace, "work")
	}
	logDir := filepath.Join(request.Workspace, "logs")
	tmpDir := filepath.Join(request.Workspace, "tmp")
	for _, directory := range []string{workDir, logDir, tmpDir} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return provider.Failed(provider.CodeWorkspaceError,
				"create attempt workspace: "+err.Error(), false)
		}
	}
	environment := BuildWorkerEnvironment(h.config.EnvAllowlist, h.config.Environment, map[string]string{
		"TMPDIR": tmpDir,
	})
	executionContext, cancel := context.WithDeadline(ctx, effectiveDeadline)
	defer cancel()
	outcome, err := h.adapter.Execute(executionContext, provider.Request{
		TaskID:       request.Task.ID,
		Capability:   request.Task.Capability,
		Workspace:    request.Workspace,
		WorkDir:      workDir,
		LogDirectory: logDir,
		BundlePath:   request.BundlePath,
		OutputPath:   filepath.Join(request.Workspace, "outcome.json"),
		Environment:  environment,
		Marker:       request.Marker,
		SessionRef:   request.SessionRef,
		Model:        h.config.ProviderModel,
		Deadline:     effectiveDeadline,
	})
	if err != nil {
		outcome = provider.Failed(provider.CodeProviderProtocol,
			fmt.Sprintf("provider adapter failed: %v", err), false)
	}
	// Cancellation observed by the host wins over adapter classification: a
	// canceled attempt must never be reported as completed.
	if executionContext.Err() != nil && outcome.Status == provider.StatusCompleted {
		code := provider.CodeCanceled
		if context.Cause(executionContext) == context.DeadlineExceeded || executionContext.Err() == context.DeadlineExceeded {
			code = provider.CodeProviderTimeout
		}
		outcome = provider.Failed(code, "attempt terminated by the runtime", code == provider.CodeProviderTimeout)
	}
	h.recordEvidence(request, outcome)
	return outcome
}

// recordEvidence persists the attempt telemetry next to the workspace. It never
// includes environment values or credentials.
func (h *Host) recordEvidence(request ExecutionRequest, outcome provider.Outcome) {
	evidence := struct {
		TaskID       string               `json:"taskId"`
		LeaseID      string               `json:"leaseId"`
		FencingToken int64                `json:"fencingToken"`
		Capability   execution.Capability `json:"capability"`
		Marker       string               `json:"marker"`
		ObservedAt   time.Time            `json:"observedAt"`
		Status       string               `json:"status"`
		FailureCode  string               `json:"failureCode,omitempty"`
		Telemetry    provider.Telemetry   `json:"telemetry"`
	}{
		TaskID: request.Task.ID, LeaseID: request.Lease.LeaseID,
		FencingToken: request.Lease.FencingToken, Capability: request.Task.Capability,
		Marker: request.Marker, ObservedAt: h.config.Now().UTC(),
		Status: outcome.Status, Telemetry: outcome.Telemetry,
	}
	if outcome.Failure != nil {
		evidence.FailureCode = outcome.Failure.Code
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		h.logger.Warn("encode attempt evidence", "task", request.Task.ID, "error", err)
		return
	}
	path := filepath.Join(request.Workspace, "evidence.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		h.logger.Warn("write attempt evidence", "task", request.Task.ID, "error", err)
	}
	if len(outcome.Telemetry.Residual) > 0 {
		residualPath := filepath.Join(request.Workspace, "residual.json")
		if err := proc.WriteResidualEvidence(residualPath, request.Marker, outcome.Telemetry.Residual); err != nil {
			h.logger.Warn("write residual evidence", "task", request.Task.ID, "error", err)
		}
	}
}

// BuildWorkerEnvironment derives the worker environment from an explicit
// allowlist. Values never come from anywhere else, TMPDIR is overridden by the
// caller and no secret-shaped variable can be forwarded.
func BuildWorkerEnvironment(allowlist, source []string, overrides map[string]string) []string {
	values := make(map[string]string, len(source))
	for _, entry := range source {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		values[name] = value
	}
	environment := make([]string, 0, len(allowlist)+len(overrides))
	for _, name := range allowlist {
		if value, ok := values[name]; ok {
			environment = append(environment, name+"="+value)
		}
	}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
	}
	return environment
}
