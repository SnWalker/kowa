package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/platform/proc"
)

// Codex adapter invariants (verified against the CLI's own help output and the
// S04 pre-risk spike):
//   - the model is always passed explicitly; the host's default model is never
//     treated as a frozen configuration;
//   - stdin is closed by the supervisor (the CLI otherwise waits on piped
//     stdin);
//   - user configuration is ignored so a machine-local config cannot silently
//     change the attempt parameters.
type Codex struct {
	probe cliProbe

	// Model pins the exact model for both probes and attempts.
	Model string
	// SandboxMode selects the CLI sandbox: probes use read-only, capability
	// attempts default to workspace-write (the Runner owns commits and pushes).
	SandboxMode string
	// PersistSessions keeps provider session state so an attempt may be
	// resumed; disabled attempts run ephemeral.
	PersistSessions bool
	Grace           time.Duration
}

// MinimumCodexVersion is the oldest release whose flags this adapter uses.
var minimumCodexVersion = version{major: 0, minor: 157, patch: 0}

func NewCodex(model string) *Codex {
	return NewCodexWithCLI("", model)
}

func NewCodexWithCLI(cliPath, model string) *Codex {
	return &Codex{
		probe: cliProbe{
			CLIName: "codex", CLIPath: cliPath, MinVersion: minimumCodexVersion,
			VersionArgs: []string{"--version"}, AuthArgs: []string{"login", "status"},
			AuthOK: func(outcome commandOutcome) bool {
				return strings.Contains(outcome.Stdout+outcome.Stderr, "Logged in")
			},
		},
		Model: model, SandboxMode: "workspace-write", Grace: 10 * time.Second,
	}
}

func (c *Codex) ID() string { return "codex" }

func (c *Codex) Probe(ctx context.Context) (Probe, error) {
	return c.probe.probeLayers(ctx)
}

// ProbeExecution runs the fourth ladder layer: a minimal real call that must
// end with a completed turn and a decodable envelope. It costs quota, so it
// runs only when explicitly requested.
func (c *Codex) ProbeExecution(ctx context.Context) (Probe, error) {
	probe, err := c.probe.probeLayers(ctx)
	if err != nil || !probe.Executable || !probe.VersionCompatible || !probe.Authenticated {
		return probe, err
	}
	if c.Model == "" {
		probe.Detail = "no explicit model configured; refusing to use the host default"
		return probe, nil
	}
	directory, err := probeWorkspace("codex-execute")
	if err != nil {
		return probe, err
	}
	defer func() { _ = removeWorkspace(directory) }()
	request := Request{
		TaskID: "probe", Capability: probeCapability(),
		Workspace: directory, WorkDir: directory, LogDirectory: directory,
		OutputPath: filepath.Join(directory, "probe-outcome.json"),
		Environment: []string{
			"PATH=" + pathEnvironment(), "HOME=" + homeEnvironment(),
		},
		Marker: "codex-probe",
	}
	request.Environment = append(request.Environment, "CODEX_PROBE=1")
	outcome, runErr := c.execute(ctx, request, true)
	if runErr != nil {
		probe.Detail = fmt.Sprintf("probe execution failed: %v", runErr)
		return probe, nil
	}
	if outcome.Status == StatusCompleted {
		probe.CanExecute = true
	} else {
		probe.Detail = "probe execution did not complete: " + outcome.Summary
	}
	return probe, nil
}

func (c *Codex) Execute(ctx context.Context, request Request) (Outcome, error) {
	return c.execute(ctx, request, false)
}

// execute builds and supervises one codex invocation. probeMode tightens the
// sandbox and prompt for the availability check.
func (c *Codex) execute(ctx context.Context, request Request, probeMode bool) (Outcome, error) {
	cliPath, err := c.probe.resolve()
	if err != nil {
		return Failed(CodeProviderProtocol, "codex CLI is not installed", false), nil
	}
	if c.Model == "" {
		return Failed(CodeProviderProtocol, "no explicit codex model is configured", false), nil
	}
	schemaPath, err := writeOutputSchema(request, CodexEnvelopeSchema)
	if err != nil {
		return Failed(CodeWorkspaceError, err.Error(), false), nil
	}
	lastMessagePath := request.OutputPath + ".last"
	eventsPath := filepath.Join(request.LogDirectory, "codex-events.jsonl")
	sandbox := c.SandboxMode
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	if probeMode {
		sandbox = "read-only"
	}
	base := []string{
		cliPath, "exec",
		"-m", c.Model,
		"--skip-git-repo-check",
		"--sandbox", sandbox,
		"--json",
		"--ignore-user-config",
		"--ignore-rules",
		"--output-schema", schemaPath,
		"-o", lastMessagePath,
	}
	prompt := buildTaskPromptStyle(request, true)
	argv := append([]string{}, base...)
	if !c.PersistSessions {
		argv = append(argv, "--ephemeral")
	}
	if request.SessionRef != "" && !probeMode {
		// Resume the recorded thread; the prompt is passed after the session id.
		argv = append(argv, "resume", request.SessionRef)
	}
	argv = append(argv, prompt)

	started := time.Now()
	processOutcome, runErr := proc.Run(ctx, proc.Spec{
		Command: argv, Dir: request.WorkDir, Env: request.Environment,
		StdoutPath: eventsPath,
		StderrPath: filepath.Join(request.LogDirectory, "stderr.log"),
		Marker:     request.Marker, Grace: c.Grace,
	})
	telemetry := Telemetry{
		ProviderID: c.ID(), CLIVersion: c.probe.reported, Model: c.Model,
		Command: redactPrompt(argv), ExitCode: processOutcome.ExitCode,
		DurationMS: time.Since(started).Milliseconds(),
		TimedOut:   processOutcome.TimedOut, Canceled: processOutcome.Canceled,
		Residual: processOutcome.Residual,
	}
	fail := func(code, message string, retryable bool) (Outcome, error) {
		outcome := Failed(code, message, retryable)
		outcome.Telemetry = telemetry
		return outcome, nil
	}
	if runErr != nil && !processStarted(runErr) {
		return fail(CodeWorkspaceError, runErr.Error(), false)
	}
	if processOutcome.TimedOut {
		return fail(CodeProviderTimeout, "attempt exceeded its deadline", true)
	}
	if processOutcome.Canceled {
		return fail(CodeCanceled, "attempt was canceled", false)
	}
	_, terminal, threadID := readEventStream(eventsPath)
	if processOutcome.ExitCode != 0 {
		message := terminalErrorMessage(terminal)
		if message == "" {
			message = fmt.Sprintf("codex exited with code %d", processOutcome.ExitCode)
		}
		// A resume attempt whose session no longer exists must fall back to a
		// fresh session carrying the full input instead of failing the task.
		if request.SessionRef != "" && !probeMode && mentionsSessionFailure(message) {
			fresh := request
			fresh.SessionRef = ""
			return c.execute(ctx, fresh, probeMode)
		}
		code, retryable := classifyProviderMessage(message)
		return fail(code, message, retryable)
	}
	// Exit code zero cannot prove completion: the spike observed the wrapper
	// exiting zero without a terminal event. Require both the terminal event
	// and a decodable envelope.
	if terminal == nil {
		return fail(CodeOutputInvalid, "codex exited without a terminal turn event", false)
	}
	if terminal["type"] == "turn.failed" {
		message := terminalErrorMessage(terminal)
		if message == "" {
			message = "codex reported a failed turn"
		}
		code, retryable := classifyProviderMessage(message)
		return fail(code, message, retryable)
	}
	outcome, decodeErr := decodeCodexOutcomeFile(lastMessagePath)
	if decodeErr != nil {
		return fail(CodeOutputInvalid, decodeErr.Error(), false)
	}
	if threadID != "" {
		outcome.SessionRef = threadID
	}
	outcome.Telemetry = telemetry
	return outcome, nil
}

// decodeCodexOutcomeFile reads the CLI's last-message file and converts the
// Codex carrier into the frozen envelope.
func decodeCodexOutcomeFile(path string) (Outcome, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: structured outcome is missing: %v", ErrInvalidOutcome, err)
	}
	return decodeCodexEnvelope(encoded)
}

// redactPrompt keeps telemetry small and free of prompt content.
func redactPrompt(argv []string) []string {
	redacted := make([]string, 0, len(argv))
	for _, argument := range argv {
		if len(argument) > 120 {
			redacted = append(redacted, "<prompt>")
			continue
		}
		redacted = append(redacted, argument)
	}
	return redacted
}
