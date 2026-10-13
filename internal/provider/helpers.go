package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		Env:        os.Environ(),
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
	builder.WriteString("Return a JSON object matching the provided output schema: ")
	builder.WriteString(`{"status":"completed","summary":"...","output":{...}}`)
	builder.WriteString(" for a finished attempt, or ")
	builder.WriteString(`{"status":"awaiting_input","summary":"...","inputRequest":{...}}`)
	builder.WriteString(" when human input is required. The capability's own result goes inside `output`.\n")
	return builder.String()
}

// writeOutputSchema persists the frozen envelope schema for a CLI invocation.
func writeOutputSchema(request Request) (string, error) {
	path := filepath.Join(filepath.Dir(request.OutputPath), "output-schema.json")
	if err := os.WriteFile(path, []byte(EnvelopeSchema), 0o600); err != nil {
		return "", fmt.Errorf("write output schema: %w", err)
	}
	return path, nil
}

// decodeOutcomeFile reads and strictly decodes the adapter result file.
func decodeOutcomeFile(path string) (Outcome, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: structured outcome is missing: %v", ErrInvalidOutcome, err)
	}
	return DecodeOutcome(encoded)
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

// pathEnvironment and homeEnvironment read the two variables every worker
// legitimately needs from the Runner environment.
func pathEnvironment() string { return os.Getenv("PATH") }
func homeEnvironment() string { return os.Getenv("HOME") }

// mentionsSessionFailure reports whether a provider message plausibly means
// the requested session could not be resumed, so a fresh attempt is
// justified instead of failing the task.
func mentionsSessionFailure(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "session") || strings.Contains(lower, "resume")
}
