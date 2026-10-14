package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
)

// CodexEnvelopeSchema is the Codex-specific carrier of the frozen envelope:
// the Codex strict response format requires additionalProperties=false on every
// object, so a free-form capability object cannot be declared. The capability
// payload therefore travels as a JSON-encoded string and is strictly parsed
// back into the frozen envelope by the adapter.
const CodexEnvelopeSchema = `{
  "type": "object",
  "properties": {
    "status": {"enum": ["completed", "awaiting_input"]},
    "summary": {"type": "string", "minLength": 1, "maxLength": 16384},
    "outputJson": {"type": "string"},
    "inputRequestJson": {"type": "string"}
  },
  "required": ["status", "summary", "outputJson", "inputRequestJson"],
  "additionalProperties": false
}`

// EnvelopeSchema is the frozen adapter output schema: every Provider must
// return the kowa result envelope, with the capability's own output nested
// inside `output` (or the human input request inside `inputRequest`).
const EnvelopeSchema = `{
  "type": "object",
  "properties": {
    "status": {"enum": ["completed", "awaiting_input"]},
    "summary": {"type": "string", "minLength": 1, "maxLength": 16384},
    "output": {"type": "object"},
    "inputRequest": {"type": "object"}
  },
  "required": ["status", "summary"],
  "additionalProperties": false
}`

// executor runs one already-built command with supervision; tests replace it
// to simulate process failures without spawning real processes.
type executor func(ctx context.Context, spec proc.Spec) (proc.Outcome, error)

func defaultExecutor(ctx context.Context, spec proc.Spec) (proc.Outcome, error) {
	return proc.Run(ctx, spec)
}

// cliProbe implements the cheap capability-ladder layers (existence, version,
// authentication) shared by the real CLI adapters.
type cliProbe struct {
	// CLIName is the executable resolved from PATH when CLIPath is empty.
	CLIName string
	// CLIPath pins an explicit executable (deployments and tests).
	CLIPath string
	// MinVersion is the oldest CLI release whose flags this adapter uses.
	MinVersion version
	// VersionArgs and AuthArgs are the cheap probe command arguments.
	VersionArgs []string
	AuthArgs    []string
	// AuthOK decides whether the auth command output proves authentication.
	AuthOK func(commandOutcome) bool
	// Environment is the allowlisted environment probe commands receive; nil
	// falls back to a safe default that never forwards the Runner's own
	// credential-shaped variables.
	Environment []string
	// Execute supervises probe commands; nil uses the real process supervisor.
	Execute executor

	resolvedPath string
	reported     string
}

type version struct{ major, minor, patch int }

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func parseVersion(output string) (version, bool) {
	match := versionPattern.FindStringSubmatch(output)
	if match == nil {
		return version{}, false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return version{major: major, minor: minor, patch: patch}, true
}

func (v version) atLeast(minimum version) bool {
	if v.major != minimum.major {
		return v.major > minimum.major
	}
	if v.minor != minimum.minor {
		return v.minor > minimum.minor
	}
	return v.patch >= minimum.patch
}

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

// ErrProviderNotInstalled marks a missing CLI so callers can express an
// unavailable capability instead of failing a task.
var ErrProviderNotInstalled = errors.New("provider CLI is not installed")

func (p *cliProbe) resolve() (string, error) {
	if p.resolvedPath != "" {
		return p.resolvedPath, nil
	}
	if p.CLIPath != "" {
		info, err := os.Stat(p.CLIPath)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("%w: configured path %s", ErrProviderNotInstalled, p.CLIPath)
		}
		p.resolvedPath = p.CLIPath
		return p.resolvedPath, nil
	}
	path, err := exec.LookPath(p.CLIName)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrProviderNotInstalled, p.CLIName)
	}
	p.resolvedPath = path
	return path, nil
}

func (p *cliProbe) executor() executor {
	if p.Execute != nil {
		return p.Execute
	}
	return defaultExecutor
}

// versionText resolves and caches the raw version output.
func (p *cliProbe) versionText(ctx context.Context) (string, error) {
	if p.reported != "" {
		return p.reported, nil
	}
	path, err := p.resolve()
	if err != nil {
		return "", err
	}
	outcome, err := p.runCommand(ctx, append([]string{path}, p.VersionArgs...), 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("provider version probe failed: %w", err)
	}
	reported := strings.TrimSpace(outcome.Stdout)
	if reported == "" {
		reported = strings.TrimSpace(outcome.Stderr)
	}
	p.reported = reported
	return reported, nil
}

// authenticate runs the login-status command; a nonzero exit or a failing
// check means "not authenticated", not a probe error.
func (p *cliProbe) authenticate(ctx context.Context) bool {
	path, err := p.resolve()
	if err != nil {
		return false
	}
	outcome, err := p.runCommand(ctx, append([]string{path}, p.AuthArgs...), 30*time.Second)
	if err != nil || outcome.ExitCode != 0 {
		return false
	}
	if p.AuthOK != nil {
		return p.AuthOK(outcome)
	}
	return true
}

// probeEnvironment returns the explicitly configured allowlist, or a safe
// default filtered from the process environment. The Runner's runtime token and
// any other secret-shaped variable can therefore never reach a provider CLI
// through the probe path.
func (p *cliProbe) probeEnvironment() []string {
	if p.Environment != nil {
		return p.Environment
	}
	return filterProbeEnvironment(os.Environ())
}

// probeEnvironmentNames is the fixed safe default for probe processes.
var probeEnvironmentNames = []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM"}

func filterProbeEnvironment(source []string) []string {
	values := make(map[string]string, len(source))
	for _, entry := range source {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		values[name] = value
	}
	filtered := make([]string, 0, len(probeEnvironmentNames))
	for _, name := range probeEnvironmentNames {
		if value, ok := values[name]; ok {
			filtered = append(filtered, name+"="+value)
		}
	}
	return filtered
}

// executableLayerEnvironment narrows the probe environment to what a minimal
// execution needs.
func (p *cliProbe) executableLayerEnvironment() []string {
	narrowed := make([]string, 0, 2)
	for _, entry := range p.probeEnvironment() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "PATH" || name == "HOME" {
			narrowed = append(narrowed, entry)
		}
	}
	return narrowed
}

// probeLayers composes the three cheap layers into a Probe value. The fourth
// layer (a real minimal execution) is deliberately separate: it costs quota
// and runs only when explicitly requested.
func (p *cliProbe) probeLayers(ctx context.Context) (Probe, error) {
	if _, err := p.resolve(); err != nil {
		return Probe{Executable: false, Detail: err.Error()}, nil
	}
	reported, err := p.versionText(ctx)
	if err != nil {
		return Probe{Executable: true, Detail: err.Error()}, nil
	}
	result := Probe{Executable: true, Version: reported}
	if parsed, ok := parseVersion(reported); ok && p.MinVersion != (version{}) {
		result.VersionCompatible = parsed.atLeast(p.MinVersion)
	}
	result.Authenticated = p.authenticate(ctx)
	if !result.Authenticated {
		result.Detail = "provider CLI is not authenticated"
	}
	return result, nil
}

type commandOutcome struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (p *cliProbe) runCommand(ctx context.Context, argv []string, timeout time.Duration) (commandOutcome, error) {
	directory, err := os.MkdirTemp("", "kowa-probe-")
	if err != nil {
		return commandOutcome{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	outcome, err := p.executor()(commandCtx, proc.Spec{
		Command: argv, Dir: directory,
		Env:        p.probeEnvironment(),
		StdoutPath: filepath.Join(directory, "stdout.log"),
		StderrPath: filepath.Join(directory, "stderr.log"),
		Marker:     fmt.Sprintf("provider-probe-%d", time.Now().UnixNano()),
		Grace:      2 * time.Second,
	})
	stdout, _ := os.ReadFile(filepath.Join(directory, "stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(directory, "stderr.log"))
	return commandOutcome{
		Stdout: string(stdout), Stderr: string(stderr), ExitCode: outcome.ExitCode,
	}, err
}

// buildTaskPrompt renders the interim MVP prompt: the frozen task identity plus
// the input bundle reference. Per-capability prompt templates belong to the
// fixed workflow stage, so the envelope instruction stays generic.
func buildTaskPrompt(request Request) string {
	return buildTaskPromptStyle(request, false)
}

// buildTaskPromptStyle selects the envelope carrier wording: codex cannot
// declare free-form objects, so its payload travels as a JSON-encoded string.
func buildTaskPromptStyle(request Request, jsonStringPayload bool) string {
	var builder strings.Builder
	builder.WriteString("You are executing a Kowa capability attempt.\n")
	fmt.Fprintf(&builder, "Capability: %s@%d\n", request.Capability.ID, request.Capability.Version)
	fmt.Fprintf(&builder, "Task: %s\n", request.TaskID)
	if request.SessionRef != "" {
		builder.WriteString("This attempt continues a previous session; keep prior context.\n")
	}
	if request.BundlePath != "" {
		fmt.Fprintf(&builder, "Frozen input bundle (JSON): read %s\n", request.BundlePath)
	}
	builder.WriteString("Work only inside the current working directory; do not perform network or Git remote operations.\n")
	builder.WriteString("Return a JSON object matching the provided output schema. ")
	if jsonStringPayload {
		builder.WriteString("Always include both `outputJson` and `inputRequestJson`; use an empty string for the unused one.")
		builder.WriteString(" The capability's own result goes inside `outputJson` as a JSON-encoded object string")
		builder.WriteString(" (for a finished attempt), or inside `inputRequestJson` when human input is required.\n")
	} else {
		builder.WriteString(`Use {"status":"completed","summary":"...","output":{...}}`)
		builder.WriteString(" for a finished attempt, or ")
		builder.WriteString(`{"status":"awaiting_input","summary":"...","inputRequest":{...}}`)
		builder.WriteString(" when human input is required. The capability's own result goes inside `output`.\n")
	}
	return builder.String()
}

// writeOutputSchema persists the adapter's output schema for a CLI invocation.
func writeOutputSchema(request Request, schema string) (string, error) {
	path := filepath.Join(filepath.Dir(request.OutputPath), "output-schema.json")
	if err := os.WriteFile(path, []byte(schema), 0o600); err != nil {
		return "", fmt.Errorf("write output schema: %w", err)
	}
	return path, nil
}

// decodeCodexEnvelope converts the Codex carrier (JSON-encoded payload strings)
// into the frozen envelope and runs the same strict rules every adapter obeys.
func decodeCodexEnvelope(data []byte) (Outcome, error) {
	var carrier struct {
		Status           string `json:"status"`
		Summary          string `json:"summary"`
		OutputJSON       string `json:"outputJson"`
		InputRequestJSON string `json:"inputRequestJson"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&carrier); err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrInvalidOutcome, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Outcome{}, fmt.Errorf("%w: trailing data after envelope", ErrInvalidOutcome)
	}
	envelope := struct {
		Status       string         `json:"status"`
		Summary      string         `json:"summary"`
		Output       map[string]any `json:"output,omitempty"`
		InputRequest map[string]any `json:"inputRequest,omitempty"`
	}{Status: carrier.Status, Summary: carrier.Summary}
	if carrier.OutputJSON != "" {
		output, err := parseJSONObject(carrier.OutputJSON)
		if err != nil {
			return Outcome{}, fmt.Errorf("%w: outputJson is not a JSON object: %v", ErrInvalidOutcome, err)
		}
		envelope.Output = output
	}
	if carrier.InputRequestJSON != "" {
		inputRequest, err := parseJSONObject(carrier.InputRequestJSON)
		if err != nil {
			return Outcome{}, fmt.Errorf("%w: inputRequestJson is not a JSON object: %v", ErrInvalidOutcome, err)
		}
		envelope.InputRequest = inputRequest
	}
	if envelope.Status == StatusCompleted && envelope.Output == nil {
		// A completed attempt may legitimately carry an empty capability
		// result; the frozen envelope requires the object to exist.
		envelope.Output = map[string]any{}
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return Outcome{}, err
	}
	return DecodeOutcome(encoded)
}

// parseJSONObject accepts exactly one JSON object value with no trailing data.
func parseJSONObject(value string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	var target map[string]any
	if err := decoder.Decode(&target); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON object")
	}
	if target == nil {
		return nil, errors.New("not an object")
	}
	return target, nil
}

// classifyProviderMessage maps a provider-reported message onto the frozen
// failure taxonomy. Unrecognized messages stay provider_protocol.
func classifyProviderMessage(message string) (code string, retryable bool) {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "usage limit"), strings.Contains(lower, "quota"),
		strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"):
		return CodeProviderQuota, false
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "not logged in"), strings.Contains(lower, "authentication"):
		return CodeProviderAuth, false
	case strings.Contains(lower, "network"), strings.Contains(lower, "connection"),
		strings.Contains(lower, "reconnecting"):
		return CodeProviderNetwork, true
	default:
		return CodeProviderProtocol, false
	}
}

// readEventStream extracts ordered event type names, the first terminal event
// payload and the thread id from JSON-lines provider output. Malformed lines
// (including a truncated tail) are ignored.
func readEventStream(path string) (types []string, terminal map[string]any, threadID string) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, ""
	}
	for _, line := range strings.Split(string(encoded), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)
		if eventType == "" {
			continue
		}
		types = append(types, eventType)
		if eventType == "thread.started" && threadID == "" {
			if value, ok := event["thread_id"].(string); ok {
				threadID = value
			}
		}
		if eventType == "turn.completed" || eventType == "turn.failed" {
			terminal = event
		}
	}
	return types, terminal, threadID
}

// terminalErrorMessage extracts a human-readable failure message from a
// terminal event without trusting its shape.
func terminalErrorMessage(terminal map[string]any) string {
	if terminal == nil {
		return ""
	}
	if failure, ok := terminal["error"].(map[string]any); ok {
		if message, ok := failure["message"].(string); ok {
			return message
		}
	}
	if message, ok := terminal["message"].(string); ok {
		return message
	}
	return ""
}

// processStarted reports whether a supervisor error still means the process
// ran and exited (so its output and exit code must drive classification). A
// start failure or an infrastructure error is not classifiable this way.
func processStarted(err error) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError)
}

// probeCapability is the synthetic capability used by the executable ladder
// layer; it never reaches the control plane as a business capability.
func probeCapability() execution.Capability {
	return execution.Capability{ID: "kowa.probe.executable", Version: 1}
}

// probeWorkspace creates a disposable directory for a probe execution.
func probeWorkspace(prefix string) (string, error) {
	return os.MkdirTemp("", prefix+"-")
}

func removeWorkspace(path string) error { return os.RemoveAll(path) }

// mentionsSessionFailure reports whether a provider message plausibly means
// the requested session could not be resumed, so a fresh attempt is
// justified instead of failing the task.
func mentionsSessionFailure(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "session") || strings.Contains(lower, "resume")
}
