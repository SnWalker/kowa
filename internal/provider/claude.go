package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/platform/proc"
)

// Claude adapter invariants (verified against the CLI's own help output and the
// S04 pre-risk spike):
//   - user-level settings and MCP servers are excluded explicitly so a machine
//     configuration cannot change attempt parameters;
//   - stdin is closed by the supervisor;
//   - the structured result object is decoded strictly; the CLI's exit status
//     alone never proves completion.
type Claude struct {
	probe cliProbe

	// Model optionally pins the model name; empty leaves the CLI default.
	Model string
	// MaxTurns bounds the agent loop for one attempt.
	MaxTurns int
	// PersistSessions keeps provider session state so an attempt may be
	// resumed.
	PersistSessions bool
	Grace           time.Duration
}

// MinimumClaudeVersion is the oldest release whose flags this adapter uses.
var minimumClaudeVersion = version{major: 2, minor: 0, patch: 0}

// claudeMCPConfig disables every MCP server for attempt processes. The CLI's
// --mcp-config takes a file path, so the adapter writes this content to a
// per-attempt file before invoking the CLI.
const claudeMCPConfig = `{"mcpServers":{}}`

// claudeAttemptTools declares the tools one attempt may use without an
// interactive approver. Print mode has no host to answer permission prompts,
// so anything left on the default ask path is denied: without this list the
// attempt can neither read files outside its process working directory (the
// frozen input bundle and the non-project repositories of the task directory)
// nor run the capability's own commands. The list stays deliberately narrow —
// no network or delegation tools — and grants no bypass of the CLI's file
// access boundary, which remains the process working directory plus the task
// directory added with --add-dir.
var claudeAttemptTools = []string{"Bash", "Edit", "Write", "Read", "Glob", "Grep"}

// writeMCPConfig persists the empty MCP server configuration for one attempt.
func writeMCPConfig(request Request) (string, error) {
	path := filepath.Join(filepath.Dir(request.OutputPath), "claude-mcp.json")
	if err := os.WriteFile(path, []byte(claudeMCPConfig), 0o600); err != nil {
		return "", fmt.Errorf("write claude mcp config: %w", err)
	}
	return path, nil
}

func NewClaude(model string) *Claude {
	return NewClaudeWithCLI("", model)
}

func NewClaudeWithCLI(cliPath, model string) *Claude {
	return &Claude{
		probe: cliProbe{
			CLIName: "claude", CLIPath: cliPath, MinVersion: minimumClaudeVersion,
			VersionArgs: []string{"--version"}, AuthArgs: []string{"auth", "status"},
			AuthOK: claudeAuthOK,
		},
		Model: model, MaxTurns: 30, Grace: 10 * time.Second,
	}
}

// claudeAuthOK parses the CLI's machine-readable auth status.
func claudeAuthOK(outcome commandOutcome) bool {
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(outcome.Stdout)), &status); err != nil {
		return false
	}
	return status.LoggedIn != nil && *status.LoggedIn
}

func (c *Claude) ID() string { return "claude" }

// SetProbeEnvironment pins the allowlisted environment probe commands receive.
func (c *Claude) SetProbeEnvironment(environment []string) { c.probe.Environment = environment }

func (c *Claude) Probe(ctx context.Context) (Probe, error) {
	return c.probe.probeLayers(ctx)
}

// ProbeExecution runs the fourth ladder layer: a minimal real call whose
// structured output must decode. It costs quota, so it runs only on request.
func (c *Claude) ProbeExecution(ctx context.Context) (Probe, error) {
	probe, err := c.probe.probeLayers(ctx)
	if err != nil || !probe.Executable || !probe.VersionCompatible || !probe.Authenticated {
		return probe, err
	}
	directory, err := probeWorkspace("claude-execute")
	if err != nil {
		return probe, err
	}
	defer func() { _ = removeWorkspace(directory) }()
	request := Request{
		TaskID: "probe", Capability: probeCapability(),
		Workspace: directory, WorkDir: directory, LogDirectory: directory,
		OutputPath:  filepath.Join(directory, "probe-outcome.json"),
		Environment: c.probe.executableLayerEnvironment(),
		Marker:      "claude-probe",
	}
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

func (c *Claude) Execute(ctx context.Context, request Request) (Outcome, error) {
	return c.execute(ctx, request, false)
}

type claudeResult struct {
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	TerminalReason   string          `json:"terminal_reason"`
	SessionID        string          `json:"session_id"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Result           *string         `json:"result"`
	APIErrorStatus   *int            `json:"api_error_status"`
}

func (c *Claude) execute(ctx context.Context, request Request, probeMode bool) (Outcome, error) {
	cliPath, err := c.probe.resolve()
	if err != nil {
		return Failed(CodeProviderProtocol, "claude CLI is not installed", false), nil
	}
	mcpConfigPath, err := writeMCPConfig(request)
	if err != nil {
		return Failed(CodeWorkspaceError, err.Error(), false), nil
	}
	argv := []string{
		cliPath, "-p",
		"--output-format", "json",
		"--json-schema", EnvelopeSchema,
		"--strict-mcp-config",
		"--mcp-config", mcpConfigPath,
		// Only user-level settings are honored: repository-provided project
		// settings must not change attempt parameters, while host
		// authentication (a user-level credential) keeps working.
		"--setting-sources", "user",
	}
	// The frozen worker-delivery rule (stage/S04 M4-5) is that the worker reads
	// the verified content of the task directory: the input bundle the host
	// materialized and every repository checked out next to it. The process
	// working directory is the single project checkout, so the attempt
	// directory is added explicitly as a second working directory; both
	// variadic options below must stay ahead of the fixed options so the final
	// prompt argument is never consumed as one of their values.
	if request.Workspace != "" {
		argv = append(argv, "--add-dir", request.Workspace)
	}
	argv = append(argv, "--allowedTools", strings.Join(claudeAttemptTools, ","))
	argv = append(argv, "--max-turns", fmt.Sprintf("%d", c.maxTurns()))
	if c.Model != "" {
		argv = append(argv, "--model", c.Model)
	}
	if !c.PersistSessions {
		argv = append(argv, "--no-session-persistence")
	}
	if request.SessionRef != "" && !probeMode {
		argv = append(argv, "--resume", request.SessionRef)
	}
	argv = append(argv, buildTaskPrompt(request))

	started := time.Now()
	processOutcome, runErr := proc.Run(ctx, proc.Spec{
		Command: argv, Dir: request.WorkDir, Env: request.Environment,
		StdoutPath: filepath.Join(request.LogDirectory, "claude-result.json"),
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
	encoded, readErr := os.ReadFile(filepath.Join(request.LogDirectory, "claude-result.json"))
	if readErr != nil {
		return fail(CodeOutputInvalid, "claude result object is missing or malformed: "+readErr.Error(), false)
	}
	var result claudeResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return fail(CodeOutputInvalid, "claude result object is malformed: "+err.Error(), false)
	}
	message := ""
	if result.Result != nil {
		message = *result.Result
	}
	if result.IsError || processOutcome.ExitCode != 0 || result.TerminalReason != "completed" {
		// A resume attempt whose session is gone must fall back to a fresh
		// session carrying the full input instead of failing the task.
		combined := message + " " + result.Subtype
		if request.SessionRef != "" && !probeMode && mentionsSessionFailure(combined) {
			fresh := request
			fresh.SessionRef = ""
			return c.execute(ctx, fresh, probeMode)
		}
		if message == "" {
			message = fmt.Sprintf("claude ended with subtype %q (exit %d)", result.Subtype, processOutcome.ExitCode)
		}
		if result.APIErrorStatus != nil && *result.APIErrorStatus == 401 {
			return fail(CodeProviderAuth, message, false)
		}
		code, retryable := classifyProviderMessage(message + " " + result.Subtype)
		return fail(code, message, retryable)
	}
	if len(result.StructuredOutput) == 0 || string(result.StructuredOutput) == "null" {
		return fail(CodeOutputInvalid, "claude returned no structured output", false)
	}
	outcome, decodeErr := DecodeOutcome(result.StructuredOutput)
	if decodeErr != nil {
		return fail(CodeOutputInvalid, decodeErr.Error(), false)
	}
	if result.SessionID != "" {
		outcome.SessionRef = result.SessionID
	}
	outcome.Telemetry = telemetry
	return outcome, nil
}

func (c *Claude) maxTurns() int {
	if c.MaxTurns <= 0 {
		return 30
	}
	return c.MaxTurns
}
