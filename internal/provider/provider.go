// Package provider owns the Provider adapters the Runner executes: launching a
// host CLI, strictly decoding its structured result, recording telemetry and
// classifying failures. Capability business semantics stay with the capability
// catalog; adapters only carry the frozen result envelope.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

// Failure codes are the frozen M1 taxonomy. Only clearly retryable classes may
// set Retryable; the control plane still owns any actual retry decision.
const (
	CodeProviderTimeout  = "provider_timeout"
	CodeProviderAuth     = "provider_auth"
	CodeProviderQuota    = "provider_quota"
	CodeProviderNetwork  = "provider_network"
	CodeProviderProtocol = "provider_protocol"
	CodeOutputInvalid    = "output_invalid"
	CodeCanceled         = "canceled"
	CodeRuntimeRestarted = "runtime_restarted"
	CodeRuntimeShutdown  = "runtime_shutdown"
	CodeDeadlineExceeded = "deadline_exceeded"
	CodeWorkspaceError   = "workspace_error"
	// Input classes are assembled by the Runner around the frozen-input
	// delivery chain, not by a provider adapter.
	CodeInputInvalid     = "input_invalid"
	CodeInputUnavailable = "input_unavailable"
)

// Outcome statuses mirror the frozen TaskResult semantics one-to-one.
const (
	StatusCompleted     = "completed"
	StatusAwaitingInput = "awaiting_input"
	StatusFailed        = "failed"
)

var ErrInvalidOutcome = errors.New("provider: invalid structured outcome")

// Request is one capability execution handed to an adapter. The host prepares
// the workspace, the complete worker environment (marker included) and the
// paths; adapters only add provider-specific parameters.
type Request struct {
	TaskID       string
	Capability   execution.Capability
	Workspace    string
	WorkDir      string
	LogDirectory string
	BundlePath   string
	OutputPath   string
	Environment  []string
	Marker       string
	SessionRef   string
	Model        string
	Deadline     time.Time
}

// Telemetry records what actually ran; it never carries credentials.
type Telemetry struct {
	ProviderID string
	CLIVersion string
	Model      string
	Command    []string `json:"command,omitempty"`
	ExitCode   int      `json:"exitCode"`
	DurationMS int64    `json:"durationMs"`
	TimedOut   bool     `json:"timedOut"`
	Canceled   bool     `json:"canceled"`
	Residual   []string `json:"residual,omitempty"`
}

// Outcome is the adapter's result for one attempt.
type Outcome struct {
	Status       string
	Summary      string
	Output       map[string]any
	InputRequest map[string]any
	Failure      *Failure
	SessionRef   string
	Telemetry    Telemetry
}

type Failure struct {
	Code      string
	Message   string
	Retryable bool
}

// Probe separates the capability ladder layers: the CLI exists, its version is
// compatible, it is authenticated, and it can actually execute.
type Probe struct {
	Executable        bool
	Version           string
	VersionCompatible bool
	Authenticated     bool
	CanExecute        bool
	Detail            string
}

// Adapter converts one attempt into a host CLI invocation.
type Adapter interface {
	ID() string
	Probe(ctx context.Context) (Probe, error)
	Execute(ctx context.Context, request Request) (Outcome, error)
}

// ExecutionProber optionally exposes the fourth capability-ladder layer: a
// minimal real execution proving the provider can actually run. It costs
// quota, so it is invoked explicitly rather than on every registration.
type ExecutionProber interface {
	ProbeExecution(ctx context.Context) (Probe, error)
}

// ProbeExecution runs the ladder's executable layer for an adapter, returning
// an unexecutable probe for adapters without that layer.
func ProbeExecution(adapter Adapter, ctx context.Context) (Probe, error) {
	prober, ok := adapter.(ExecutionProber)
	if !ok {
		return adapter.Probe(ctx)
	}
	return prober.ProbeExecution(ctx)
}

// DecodeOutcome strictly decodes the adapter protocol envelope: unknown fields,
// missing required values, enum violations and trailing data are rejected. A
// truncated or absent result must surface as an error, never as success.
func DecodeOutcome(data []byte) (Outcome, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Outcome{}, fmt.Errorf("%w: empty body", ErrInvalidOutcome)
	}
	var raw struct {
		Status       string         `json:"status"`
		Summary      string         `json:"summary"`
		Output       map[string]any `json:"output"`
		InputRequest map[string]any `json:"inputRequest"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrInvalidOutcome, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Outcome{}, fmt.Errorf("%w: trailing data after JSON value", ErrInvalidOutcome)
	}
	if raw.Summary == "" || len(raw.Summary) > 16384 {
		return Outcome{}, fmt.Errorf("%w: summary is missing or too long", ErrInvalidOutcome)
	}
	switch raw.Status {
	case StatusCompleted:
		if raw.Output == nil || raw.InputRequest != nil {
			return Outcome{}, fmt.Errorf("%w: completed requires output and forbids inputRequest", ErrInvalidOutcome)
		}
		// The frozen envelope carries the capability's own result in `output`.
		// `status=completed` therefore requires a result to be present: a real
		// attempt reported the blockage in its summary and returned an empty
		// object, and an empty object decided the node with nothing while the
		// flow stalled without any diagnostic. The adapter fails closed here —
		// the control plane accepts a completed result as the node's decision,
		// so "completed with no result" must never reach it.
		if len(raw.Output) == 0 {
			return Outcome{}, fmt.Errorf("%w: completed carries no capability result", ErrInvalidOutcome)
		}
	case StatusAwaitingInput:
		if raw.InputRequest == nil || raw.Output != nil {
			return Outcome{}, fmt.Errorf("%w: awaiting_input requires inputRequest and forbids output", ErrInvalidOutcome)
		}
	default:
		return Outcome{}, fmt.Errorf("%w: unknown status %q", ErrInvalidOutcome, raw.Status)
	}
	return Outcome{
		Status: raw.Status, Summary: raw.Summary,
		Output: raw.Output, InputRequest: raw.InputRequest,
	}, nil
}

// EncodeOutcome serializes an outcome envelope (used by the scripted provider
// and probes; adapters may build the envelope any way that round-trips).
func EncodeOutcome(outcome Outcome) ([]byte, error) {
	envelope := struct {
		Status       string         `json:"status"`
		Summary      string         `json:"summary"`
		Output       map[string]any `json:"output,omitempty"`
		InputRequest map[string]any `json:"inputRequest,omitempty"`
	}{Status: outcome.Status, Summary: outcome.Summary, Output: outcome.Output, InputRequest: outcome.InputRequest}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode provider outcome: %w", err)
	}
	return encoded, nil
}

// Failed builds a failed outcome.
func Failed(code, message string, retryable bool) Outcome {
	return Outcome{
		Status: StatusFailed, Summary: message,
		Failure: &Failure{Code: code, Message: message, Retryable: retryable},
	}
}
