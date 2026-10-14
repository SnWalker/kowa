package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SnWalker/kowa/internal/platform/proc"
)

// ScriptedOutputEnv names the file the scripted provider must write its
// structured outcome envelope to.
const ScriptedOutputEnv = "KOWA_SCRIPTED_OUTPUT"

// Scripted is the deterministic test provider. It runs a shell script inside
// the supervised attempt workspace; the script writes the outcome envelope to
// $KOWA_SCRIPTED_OUTPUT. It is a test double: its probe only proves the
// supervision path, never real model execution.
type Scripted struct {
	Script string
	Grace  time.Duration
}

func NewScripted(script string) *Scripted {
	if script == "" {
		script = `printf '%s' '{"status":"completed","summary":"scripted completion","output":{"verdict":"approved","detail":"synthetic"}}' > "$KOWA_SCRIPTED_OUTPUT"`
	}
	return &Scripted{Script: script, Grace: 10 * time.Second}
}

func (s *Scripted) ID() string { return "scripted" }

// Version is the declared version string for the scripted provider.
func (s *Scripted) Version() string { return "scripted-1" }

// Probe runs the cheapest possible execution to prove the supervision ladder
// end to end inside this process.
func (s *Scripted) Probe(ctx context.Context) (Probe, error) {
	shell, err := lookShell()
	if err != nil {
		return Probe{Detail: "no POSIX shell available"}, nil
	}
	workspace, err := os.MkdirTemp("", "kowa-scripted-probe-")
	if err != nil {
		return Probe{}, err
	}
	defer func() { _ = os.RemoveAll(workspace) }()
	outcome, err := proc.Run(ctx, proc.Spec{
		Command: []string{shell, "-c", "exit 0"},
		Dir:     workspace, Env: os.Environ(),
		StdoutPath: filepath.Join(workspace, "stdout.log"),
		StderrPath: filepath.Join(workspace, "stderr.log"),
		Marker:     fmt.Sprintf("scripted-probe-%d", time.Now().UnixNano()),
		Grace:      s.Grace,
	})
	if err != nil {
		return Probe{Executable: true, Version: s.Version(), Detail: err.Error()}, nil
	}
	return Probe{
		Executable: true, Version: s.Version(), VersionCompatible: true,
		Authenticated: true, CanExecute: outcome.ExitCode == 0,
		Detail: "scripted provider is a test double; CanExecute only proves the supervision path",
	}, nil
}

func (s *Scripted) Execute(ctx context.Context, request Request) (Outcome, error) {
	shell, err := lookShell()
	if err != nil {
		return Failed(CodeWorkspaceError, "no POSIX shell available", false), nil
	}
	if err := os.MkdirAll(request.LogDirectory, 0o750); err != nil {
		return Failed(CodeWorkspaceError, "create log directory: "+err.Error(), false), nil
	}
	environment := append([]string{}, request.Environment...)
	environment = append(environment, ScriptedOutputEnv+"="+request.OutputPath)
	started := time.Now()
	processOutcome, err := proc.Run(ctx, proc.Spec{
		Command: []string{shell, "-c", s.Script},
		Dir:     request.WorkDir, Env: environment,
		StdoutPath: filepath.Join(request.LogDirectory, "stdout.log"),
		StderrPath: filepath.Join(request.LogDirectory, "stderr.log"),
		Marker:     request.Marker, Grace: s.Grace,
	})
	telemetry := Telemetry{
		ProviderID: s.ID(), CLIVersion: s.Version(), Model: "scripted",
		Command: []string{shell, "-c", "<scripted>"}, ExitCode: processOutcome.ExitCode,
		DurationMS: time.Since(started).Milliseconds(),
		TimedOut:   processOutcome.TimedOut, Canceled: processOutcome.Canceled,
		Residual: processOutcome.Residual,
	}
	withTelemetry := func(outcome Outcome) Outcome {
		outcome.Telemetry = telemetry
		return outcome
	}
	if err != nil && !processStarted(err) {
		return withTelemetry(Failed(CodeWorkspaceError, err.Error(), false)), nil
	}
	if processOutcome.TimedOut {
		return withTelemetry(Failed(CodeProviderTimeout, "attempt exceeded its deadline", true)), nil
	}
	if processOutcome.Canceled {
		return withTelemetry(Failed(CodeCanceled, "attempt was canceled", false)), nil
	}
	if processOutcome.ExitCode != 0 {
		return withTelemetry(Failed(CodeProviderProtocol,
			fmt.Sprintf("scripted provider exited with code %d", processOutcome.ExitCode), false)), nil
	}
	decoded, readErr := os.ReadFile(request.OutputPath)
	if readErr != nil {
		return withTelemetry(Failed(CodeOutputInvalid,
			"structured outcome is missing: "+readErr.Error(), false)), nil
	}
	outcome, decodeErr := DecodeOutcome(decoded)
	if decodeErr != nil {
		return withTelemetry(Failed(CodeOutputInvalid, "structured outcome rejected: "+decodeErr.Error(), false)), nil
	}
	return withTelemetry(outcome), nil
}

func lookShell() (string, error) {
	for _, candidate := range []string{"/bin/sh", "/usr/bin/sh"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("provider: no POSIX shell found")
}
