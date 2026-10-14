package provider_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/provider"
)

// writeFakeCLI installs a scriptable fake provider CLI. Every scenario is
// driven by FAKE_SCENARIO and a call counter file, so the adapter contract is
// tested without touching a real provider.
func writeFakeCLI(t *testing.T, flavor string) (path string, counterPath string) {
	t.Helper()
	directory := t.TempDir()
	counterPath = filepath.Join(directory, "calls")
	script := fakeCodexScript
	if flavor == "claude" {
		script = fakeClaudeScript
	}
	path = filepath.Join(directory, flavor)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, counterPath
}

const fakeCodexScript = `#!/bin/sh
printf '%s\n' "$(printf '%s' "$*" | tr '\n' ' ')" >> "$FAKE_COUNTER"
case "$1" in
  --version) env > "$(dirname "$0")/probe-env.txt" 2>/dev/null || true; echo "codex-cli 0.162.1"; exit 0 ;;
  login)
    if [ "$FAKE_SCENARIO" = "not-logged-in" ]; then echo "Not logged in"; exit 1; fi
    echo "Logged in using ChatGPT"; exit 0 ;;
esac
# exec invocation: find -o and resume arguments
output=""
prev=""
resume=""
for arg in "$@"; do
  if [ "$prev" = "-o" ]; then output="$arg"; fi
  if [ "$prev" = "resume" ]; then resume="$arg"; fi
  prev="$arg"
done
case "$resume" in
  "") ;;
  *) case "$FAKE_SCENARIO" in
      session-missing) echo '{"type":"turn.failed","error":{"message":"session not found"}}'; echo "session not found" >&2; exit 1 ;;
     esac ;;
esac
case "$FAKE_SCENARIO" in
  hang) sleep 30; exit 0 ;;
  zero-exit-no-terminal) echo '{"type":"thread.started","thread_id":"thread-1"}'; exit 0 ;;
  quota) printf '%s\n' '{"type":"turn.failed","error":{"message":"You have hit your usage limit"}}'; exit 1 ;;
  truncated) echo '{"type":"thread.started","thread_id":"thread-1"}'; echo '{"type":"turn.completed"}'; printf '%s' '{"status":"completed","summary":"cut off' > "$output"; exit 0 ;;
  unknown-field) echo '{"type":"thread.started","thread_id":"thread-1"}'; echo '{"type":"turn.completed"}'; printf '%s' '{"status":"completed","summary":"ok","outputJson":"{}","extra":1}' > "$output"; exit 0 ;;
  bad-payload) echo '{"type":"thread.started","thread_id":"thread-1"}'; echo '{"type":"turn.completed"}'; printf '%s' '{"status":"completed","summary":"ok","outputJson":"not json"}' > "$output"; exit 0 ;;
  awaiting-input) echo '{"type":"thread.started","thread_id":"thread-1"}'; echo '{"type":"turn.completed"}'; printf '%s' '{"status":"awaiting_input","summary":"need a decision","inputRequestJson":"{\"question\":\"which branch?\"}"}' > "$output"; exit 0 ;;
  *) echo '{"type":"thread.started","thread_id":"thread-1"}'; echo '{"type":"turn.completed"}'; printf '%s' '{"status":"completed","summary":"fake completion","outputJson":"{\"verdict\":\"approved\"}"}' > "$output"; exit 0 ;;
esac
`

const fakeClaudeScript = `#!/bin/sh
printf '%s\n' "$(printf '%s' "$*" | tr '\n' ' ')" >> "$FAKE_COUNTER"
case "$1" in
  --version) env > "$(dirname "$0")/probe-env.txt" 2>/dev/null || true; echo "2.1.296 (Claude Code)"; exit 0 ;;
  auth)
    if [ "$FAKE_SCENARIO" = "not-logged-in" ]; then echo '{"loggedIn":false,"authMethod":"none"}'; exit 1; fi
    echo '{"loggedIn":true,"authMethod":"claude.ai"}'; exit 0 ;;
esac
resume=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--resume" ]; then resume="$arg"; fi
  prev="$arg"
done
case "$resume" in
  "") ;;
  *) case "$FAKE_SCENARIO" in
      session-missing) echo '{"subtype":"error_during_execution","is_error":true,"terminal_reason":"error","result":"No conversation found with session ID: deadbeef"}'; exit 1 ;;
     esac ;;
esac
case "$FAKE_SCENARIO" in
  hang) sleep 30; exit 0 ;;
  max-turns) echo '{"subtype":"error_max_turns","is_error":true,"terminal_reason":"error","result":"Reached maximum number of turns (3)","structured_output":null}'; exit 1 ;;
  auth-error) echo '{"subtype":"success","is_error":true,"terminal_reason":"error","api_error_status":401,"result":"401 unauthorized","structured_output":null}'; exit 1 ;;
  no-structured) echo '{"subtype":"success","is_error":false,"terminal_reason":"completed","session_id":"session-1","structured_output":null,"result":"plain text"}'; exit 0 ;;
  truncated) printf '%s' '{"subtype":"success","is_error":false,"terminal_reason":"completed","session_id":"session-1","structured_output":{"status":"completed","summary"' ; exit 0 ;;
  awaiting-input) echo '{"subtype":"success","is_error":false,"terminal_reason":"completed","session_id":"session-1","structured_output":{"status":"awaiting_input","summary":"need a decision","inputRequest":{"question":"which branch?"}}}'; exit 0 ;;
  *) echo '{"subtype":"success","is_error":false,"terminal_reason":"completed","session_id":"session-1","structured_output":{"status":"completed","summary":"fake completion","output":{"verdict":"approved"}}}'; exit 0 ;;
esac
`

func adapterRequest(t *testing.T, outputName string) (provider.Request, string) {
	t.Helper()
	workspace := t.TempDir()
	logDirectory := filepath.Join(workspace, "logs")
	if err := os.MkdirAll(logDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	workDir := filepath.Join(workspace, "work")
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		t.Fatal(err)
	}
	return provider.Request{
		TaskID: "task-1", Capability: execution.Capability{ID: "plan.create", Version: 1},
		Workspace: workspace, WorkDir: workDir, LogDirectory: logDirectory,
		OutputPath: filepath.Join(workspace, outputName),
		Environment: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
			"FAKE_SCENARIO=" + os.Getenv("FAKE_SCENARIO"),
			"FAKE_COUNTER=" + os.Getenv("FAKE_COUNTER"),
		},
		Marker: "adapter-test-marker",
	}, workspace
}

func setFakeEnv(t *testing.T, scenario, counter string) {
	t.Helper()
	t.Setenv("FAKE_SCENARIO", scenario)
	t.Setenv("FAKE_COUNTER", counter)
}

func invocationCount(t *testing.T, counterPath string) int {
	t.Helper()
	content, err := os.ReadFile(counterPath)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(content)), "\n"))
}

func TestCodexAdapterMatrix(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "codex")
	cases := []struct {
		scenario   string
		wantStatus string
		wantCode   string
	}{
		{"success", provider.StatusCompleted, ""},
		{"awaiting-input", provider.StatusAwaitingInput, ""},
		{"zero-exit-no-terminal", provider.StatusFailed, provider.CodeOutputInvalid},
		{"quota", provider.StatusFailed, provider.CodeProviderQuota},
		{"truncated", provider.StatusFailed, provider.CodeOutputInvalid},
		{"unknown-field", provider.StatusFailed, provider.CodeOutputInvalid},
		{"bad-payload", provider.StatusFailed, provider.CodeOutputInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.scenario, func(t *testing.T) {
			setFakeEnv(t, testCase.scenario, counter)
			adapter := newCodexForTest(cliPath, "gpt-5.5")
			request, _ := adapterRequest(t, "codex-last.json")
			outcome, err := adapter.Execute(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Status != testCase.wantStatus {
				t.Fatalf("status = %s (%+v), want %s", outcome.Status, outcome.Failure, testCase.wantStatus)
			}
			if testCase.wantCode != "" && (outcome.Failure == nil || outcome.Failure.Code != testCase.wantCode) {
				t.Fatalf("failure = %+v, want code %s", outcome.Failure, testCase.wantCode)
			}
			if outcome.Status == provider.StatusCompleted {
				if outcome.Telemetry.Model != "gpt-5.5" || outcome.Telemetry.ExitCode != 0 {
					t.Fatalf("telemetry = %+v", outcome.Telemetry)
				}
				if outcome.SessionRef != "thread-1" {
					t.Fatalf("session ref = %q, want thread-1", outcome.SessionRef)
				}
			}
		})
	}
}

func TestCodexAdapterCancellationAndTimeout(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "codex")
	setFakeEnv(t, "hang", counter)
	adapter := newCodexForTest(cliPath, "gpt-5.5")
	adapter.Grace = time.Second

	// Cancellation converges as canceled, never as success.
	request, _ := adapterRequest(t, "codex-last.json")
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	outcome, err := adapter.Execute(ctx, request)
	if err != nil || outcome.Status != provider.StatusFailed || outcome.Failure.Code != provider.CodeCanceled {
		t.Fatalf("canceled attempt = %+v, %v", outcome, err)
	}

	// The caller's deadline maps to the provider timeout class.
	request, _ = adapterRequest(t, "codex-last.json")
	deadlineContext, cancelDeadline := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancelDeadline()
	outcome, err = adapter.Execute(deadlineContext, request)
	if err != nil || outcome.Failure == nil || outcome.Failure.Code != provider.CodeProviderTimeout {
		t.Fatalf("timed-out attempt = %+v, %v", outcome, err)
	}
	if !outcome.Telemetry.TimedOut {
		t.Fatalf("timeout telemetry = %+v", outcome.Telemetry)
	}
}

func TestCodexAdapterResumeFallsBackWhenSessionIsGone(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "codex")
	setFakeEnv(t, "session-missing", counter)
	adapter := newCodexForTest(cliPath, "gpt-5.5")
	request, _ := adapterRequest(t, "codex-last.json")
	request.SessionRef = "deadbeef"
	outcome, err := adapter.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	// The first invocation resumes and fails; the fallback runs a fresh,
	// session-less attempt that succeeds with the full input.
	if outcome.Status != provider.StatusCompleted {
		t.Fatalf("fallback outcome = %+v", outcome)
	}
	if calls := invocationCount(t, counter); calls != 2 {
		t.Fatalf("invocations = %d, want resume plus fresh fallback", calls)
	}
	content, err := os.ReadFile(counter)
	if err != nil || !strings.Contains(string(content), "resume deadbeef") {
		t.Fatalf("resume was not attempted: %s, %v", content, err)
	}
}

func TestClaudeAdapterMatrix(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "claude")
	cases := []struct {
		scenario   string
		wantStatus string
		wantCode   string
	}{
		{"success", provider.StatusCompleted, ""},
		{"awaiting-input", provider.StatusAwaitingInput, ""},
		{"max-turns", provider.StatusFailed, provider.CodeProviderProtocol},
		{"auth-error", provider.StatusFailed, provider.CodeProviderAuth},
		{"no-structured", provider.StatusFailed, provider.CodeOutputInvalid},
		{"truncated", provider.StatusFailed, provider.CodeOutputInvalid},
	}
	for _, testCase := range cases {
		t.Run(testCase.scenario, func(t *testing.T) {
			setFakeEnv(t, testCase.scenario, counter)
			adapter := newClaudeForTest(cliPath, "")
			request, _ := adapterRequest(t, "claude-result.json")
			outcome, err := adapter.Execute(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Status != testCase.wantStatus {
				t.Fatalf("status = %s (%+v), want %s", outcome.Status, outcome.Failure, testCase.wantStatus)
			}
			if testCase.wantCode != "" && (outcome.Failure == nil || outcome.Failure.Code != testCase.wantCode) {
				t.Fatalf("failure = %+v, want code %s", outcome.Failure, testCase.wantCode)
			}
			if outcome.Status == provider.StatusCompleted && outcome.SessionRef != "session-1" {
				t.Fatalf("session ref = %q, want session-1", outcome.SessionRef)
			}
		})
	}
}

func TestClaudeAdapterResumeFallsBackWhenSessionIsGone(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "claude")
	setFakeEnv(t, "session-missing", counter)
	adapter := newClaudeForTest(cliPath, "")
	request, _ := adapterRequest(t, "claude-result.json")
	request.SessionRef = "deadbeef"
	outcome, err := adapter.Execute(t.Context(), request)
	if err != nil || outcome.Status != provider.StatusCompleted {
		t.Fatalf("fallback outcome = %+v, %v", outcome, err)
	}
	if calls := invocationCount(t, counter); calls != 2 {
		t.Fatalf("invocations = %d, want resume plus fresh fallback", calls)
	}
}

func TestProbeLadder(t *testing.T) {
	for _, flavor := range []string{"codex", "claude"} {
		t.Run(flavor+"/authenticated", func(t *testing.T) {
			cliPath, counter := writeFakeCLI(t, flavor)
			setFakeEnv(t, "success", counter)
			adapter := newAdapterFor(flavor, cliPath)
			probe, err := adapter.Probe(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !probe.Executable || !probe.VersionCompatible || !probe.Authenticated {
				t.Fatalf("probe = %+v", probe)
			}
		})
		t.Run(flavor+"/not-authenticated", func(t *testing.T) {
			cliPath, counter := writeFakeCLI(t, flavor)
			setFakeEnv(t, "not-logged-in", counter)
			adapter := newAdapterFor(flavor, cliPath)
			probe, err := adapter.Probe(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !probe.Executable || probe.Authenticated {
				t.Fatalf("probe = %+v, want executable but unauthenticated", probe)
			}
		})
		t.Run(flavor+"/missing-cli", func(t *testing.T) {
			setFakeEnv(t, "success", filepath.Join(t.TempDir(), "counter"))
			adapter := newAdapterFor(flavor, filepath.Join(t.TempDir(), "does-not-exist"))
			probe, err := adapter.Probe(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if probe.Executable {
				t.Fatalf("probe = %+v, want not executable", probe)
			}
		})
	}
}

// newCodexForTest and newClaudeForTest construct adapters whose probe
// environment is the ambient test environment, so the fake CLIs can observe
// the harness variables. The production constructors keep the filtered
// default; these helpers are harness plumbing only.
func newCodexForTest(cliPath, model string) *provider.Codex {
	adapter := provider.NewCodexWithCLI(cliPath, model)
	adapter.SetProbeEnvironment(os.Environ())
	return adapter
}

func newClaudeForTest(cliPath, model string) *provider.Claude {
	adapter := provider.NewClaudeWithCLI(cliPath, model)
	adapter.SetProbeEnvironment(os.Environ())
	return adapter
}

func newAdapterFor(flavor, cliPath string) interface {
	ID() string
	Probe(context.Context) (provider.Probe, error)
	Execute(context.Context, provider.Request) (provider.Outcome, error)
} {
	if flavor == "codex" {
		return newCodexForTest(cliPath, "gpt-5.5")
	}
	return newClaudeForTest(cliPath, "")
}

func TestCodexRejectsMissingExplicitModel(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "codex")
	setFakeEnv(t, "success", counter)
	adapter := newCodexForTest(cliPath, "")
	request, _ := adapterRequest(t, "codex-last.json")
	outcome, err := adapter.Execute(t.Context(), request)
	if err != nil || outcome.Failure == nil || outcome.Failure.Code != provider.CodeProviderProtocol {
		t.Fatalf("outcome = %+v, %v", outcome, err)
	}
	if calls := invocationCount(t, counter); calls != 0 {
		t.Fatalf("host default model was used: %d invocations", calls)
	}
	_ = fmt.Sprint()
}

func TestClaudeAdapterIsolatesSettingsAndMCP(t *testing.T) {
	cliPath, counter := writeFakeCLI(t, "claude")
	setFakeEnv(t, "success", counter)
	adapter := newClaudeForTest(cliPath, "")
	request, workspace := adapterRequest(t, "claude-result.json")
	if _, err := adapter.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	// The attempt must carry an empty MCP server file and honor only user
	// settings, so repository-provided settings cannot alter the attempt.
	mcpConfig, err := os.ReadFile(filepath.Join(workspace, "claude-mcp.json"))
	if err != nil || strings.TrimSpace(string(mcpConfig)) != `{"mcpServers":{}}` {
		t.Fatalf("mcp config = %s, %v", mcpConfig, err)
	}
	invocations, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	line := string(invocations)
	if !strings.Contains(line, "--setting-sources user") ||
		!strings.Contains(line, "--strict-mcp-config") ||
		!strings.Contains(line, "claude-mcp.json") {
		t.Fatalf("claude argv = %s", line)
	}
}

func TestCodexSchemaSatisfiesStrictResponseFormat(t *testing.T) {
	// The Codex strict response format requires additionalProperties=false on
	// every declared object; a free-form object is rejected by the real CLI.
	var schema map[string]any
	if err := json.Unmarshal([]byte(provider.CodexEnvelopeSchema), &schema); err != nil {
		t.Fatal(err)
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("top-level schema = %v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	for name, raw := range properties {
		property, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("property %s = %v", name, raw)
		}
		if property["type"] == "object" {
			t.Fatalf("property %s declares an object without additionalProperties", name)
		}
	}
	// The payload carrier must stay a string so no free-form object is declared.
	if _, ok := properties["outputJson"].(map[string]any); !ok {
		t.Fatal("carrier lost outputJson")
	}
	// The strict format also requires every property to be listed in required.
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("schema has no required array")
	}
	requiredSet := map[string]bool{}
	for _, entry := range required {
		name, _ := entry.(string)
		requiredSet[name] = true
	}
	for name := range properties {
		if !requiredSet[name] {
			t.Fatalf("property %s is missing from required", name)
		}
	}
}

// TestProbesNeverReceiveRunnerSecrets proves the probe path cannot leak the
// Runner's own credential: adapters default to a filtered environment, and an
// explicit allowlist is honored exactly.
func TestProbesNeverReceiveRunnerSecrets(t *testing.T) {
	t.Setenv("KOWA_RUNNER_RUNTIME_TOKEN", "probe-secret-token")
	t.Setenv("GITHUB_TOKEN", "another-host-secret")
	for _, flavor := range []string{"codex", "claude"} {
		t.Run(flavor, func(t *testing.T) {
			cliPath, counter := writeFakeCLI(t, flavor)
			setFakeEnv(t, "success", counter)
			// The production constructors are used unchanged: the default probe
			// environment must be the filtered one, so this test would fail if
			// the default ever forwarded the ambient secrets below.
			adapter := adapterWithDefaultProbe(flavor, cliPath)
			probe, err := adapter.Probe(t.Context())
			if err != nil || !probe.Executable {
				t.Fatalf("probe = %+v, %v", probe, err)
			}
			// The fake CLI writes its environment next to itself, so the dump
			// path itself never has to pass through the filtered allowlist.
			contents, err := os.ReadFile(filepath.Join(filepath.Dir(cliPath), "probe-env.txt"))
			if err != nil {
				t.Fatalf("probe environment dump missing: %v", err)
			}
			text := string(contents)
			if strings.Contains(text, "probe-secret-token") || strings.Contains(text, "another-host-secret") {
				t.Fatalf("probe process received a host secret:\n%s", text)
			}
			if !strings.Contains(text, "PATH=") {
				t.Fatalf("probe process lost PATH:\n%s", text)
			}
		})
	}
}

// adapterWithDefaultProbe constructs the production adapters without touching
// their probe environment, exercising the filtered default.
func adapterWithDefaultProbe(flavor, cliPath string) interface {
	ID() string
	Probe(context.Context) (provider.Probe, error)
	Execute(context.Context, provider.Request) (provider.Outcome, error)
} {
	if flavor == "codex" {
		return provider.NewCodexWithCLI(cliPath, "gpt-5.5")
	}
	return provider.NewClaudeWithCLI(cliPath, "")
}
