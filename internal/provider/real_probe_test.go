//go:build providerprobe

package provider_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/provider"
	"github.com/SnWalker/kowa/internal/runner"
)

// Real provider probes are opt-in: they call the host CLIs and consume quota.
// Run with:
//
//	KOWA_REAL_PROVIDER_PROBE=true go test -tags providerprobe -run TestRealProvider -v ./internal/provider/
//
// CI never passes the build tag, so no pipeline consumes provider quota.
func TestRealProviderProbes(t *testing.T) {
	if os.Getenv("KOWA_REAL_PROVIDER_PROBE") != "true" {
		t.Skip("set KOWA_REAL_PROVIDER_PROBE=true to run real provider probes")
	}
	// The probe is wired exactly like the production composition root
	// (internal/runner/app.go newAdapter): the allowlisted environment is built
	// by runner.BuildWorkerEnvironment and pinned on the adapter, so a probe
	// process never receives the Runner's own credential or any other
	// secret-shaped variable. The fallback allowlist mirrors the production
	// default in internal/platform/config.
	allowlist := strings.TrimSpace(os.Getenv("KOWA_RUNNER_ENV_ALLOWLIST"))
	if allowlist == "" {
		allowlist = "PATH,HOME,LANG,LC_ALL,TERM"
	}
	probeEnvironment := runner.BuildWorkerEnvironment(strings.Split(allowlist, ","), os.Environ(), nil)
	for _, entry := range probeEnvironment {
		name, _, _ := strings.Cut(entry, "=")
		lowered := strings.ToLower(name)
		if strings.Contains(lowered, "token") || strings.Contains(lowered, "secret") ||
			strings.Contains(lowered, "credential") {
			t.Fatalf("probe environment carries a secret-shaped variable %q", name)
		}
	}

	codex := provider.NewCodexWithCLI("", os.Getenv("KOWA_REAL_CODEX_MODEL"))
	codex.SetProbeEnvironment(probeEnvironment)
	claude := provider.NewClaudeWithCLI("", os.Getenv("KOWA_REAL_CLAUDE_MODEL"))
	claude.SetProbeEnvironment(probeEnvironment)

	cases := []struct {
		name    string
		adapter provider.Adapter
		model   string
	}{
		{"codex", codex, os.Getenv("KOWA_REAL_CODEX_MODEL")},
		{"claude", claude, os.Getenv("KOWA_REAL_CLAUDE_MODEL")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			probeID, err := randomProbeID()
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			probe, err := provider.ProbeExecution(testCase.adapter, t.Context())
			if err != nil {
				t.Fatalf("probe error: %v", err)
			}
			evidence, _ := json.MarshalIndent(map[string]any{
				"provider": testCase.name, "probeId": probeID,
				"startedAt": started.Format(time.RFC3339), "finishedAt": time.Now().UTC().Format(time.RFC3339),
				"elapsedSeconds": time.Since(started).Seconds(),
				"model":          testCase.model, "environmentNames": environmentNames(probeEnvironment),
				"probe": probe,
			}, "", "  ")
			t.Logf("probe evidence:\n%s", evidence)
			if !probe.Executable {
				t.Fatalf("provider CLI is not executable: %s", probe.Detail)
			}
			if !probe.Authenticated {
				t.Fatalf("provider CLI is not authenticated: %s", probe.Detail)
			}
			t.Logf("%s: probeId=%s executable=%v version=%q compatible=%v authenticated=%v canExecute=%v detail=%q",
				testCase.name, probeID, probe.Executable, probe.Version, probe.VersionCompatible,
				probe.Authenticated, probe.CanExecute, probe.Detail)
		})
	}
}

// TestRealClaudeAttemptReadsFrozenInput is the S04 rework's layer-2 real
// acceptance for the worker-delivery fix: one minimal real Claude attempt runs
// through the production adapter with a synthetic attempt workspace laid out
// exactly like a Runner attempt (input/bundle.json, repos/<id>), and the
// capability result must carry a nonce that exists only inside the frozen input
// bundle plus the exit status of a command run inside the repository checkout.
// Run with:
//
//	KOWA_REAL_PROVIDER_PROBE=true KOWA_REAL_CLAUDE_TASK_PROBE=true \
//	go test -tags providerprobe -count=1 -run TestRealClaudeAttemptReadsFrozenInput -v ./internal/provider/
func TestRealClaudeAttemptReadsFrozenInput(t *testing.T) {
	if os.Getenv("KOWA_REAL_PROVIDER_PROBE") != "true" || os.Getenv("KOWA_REAL_CLAUDE_TASK_PROBE") != "true" {
		t.Skip("set KOWA_REAL_PROVIDER_PROBE=true and KOWA_REAL_CLAUDE_TASK_PROBE=true to run the real task probe")
	}
	workspace := t.TempDir()
	repositoryID := "1401511579"
	workDir := filepath.Join(workspace, "repos", repositoryID)
	inputDir := filepath.Join(workspace, "input")
	for _, directory := range []string{workDir, inputDir, filepath.Join(workspace, "logs"), filepath.Join(workspace, "tmp")} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	nonce, err := randomProbeID()
	if err != nil {
		t.Fatal(err)
	}
	// The bundle is the only carrier of the nonce; the attempt can satisfy the
	// assertion only by reading the task directory it was delivered into.
	bundle := fmt.Sprintf(`{"schemaVersion":"kowa.knowledge-artifact.v1","probeNonce":%q,`+
		`"attemptInstructions":"probe scaffolding: put probeNonce into output.probeNonce and the exit status of `+
		`go test ./... run in the working directory into output.goTest"}`, nonce)
	bundlePath := filepath.Join(inputDir, "bundle.json")
	if err := os.WriteFile(bundlePath, []byte(bundle), 0o600); err != nil {
		t.Fatal(err)
	}
	module := "module probe\n\ngo 1.26\n"
	source := "package probe\n\nfunc Add(a, b int) int { return a + b }\n"
	testSource := "package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"add\")\n\t}\n}\n"
	for name, content := range map[string]string{"go.mod": module, "probe.go": source, "probe_test.go": testSource} {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	allowlist := strings.TrimSpace(os.Getenv("KOWA_RUNNER_ENV_ALLOWLIST"))
	if allowlist == "" {
		allowlist = "PATH,HOME,LANG,LC_ALL,TERM"
	}
	environment := runner.BuildWorkerEnvironment(strings.Split(allowlist, ","), os.Environ(),
		map[string]string{"TMPDIR": filepath.Join(workspace, "tmp")})
	adapter := provider.NewClaudeWithCLI("", os.Getenv("KOWA_REAL_CLAUDE_MODEL"))
	adapter.SetProbeEnvironment(environment)

	started := time.Now().UTC()
	outcome, runErr := adapter.Execute(t.Context(), provider.Request{
		TaskID: "probe-task", Capability: execution.Capability{ID: "plan.create", Version: 1},
		Workspace: workspace, WorkDir: workDir, LogDirectory: filepath.Join(workspace, "logs"),
		BundlePath: bundlePath, OutputPath: filepath.Join(workspace, "outcome.json"),
		Environment: environment, Marker: "claude-task-probe",
	})
	if runErr != nil {
		t.Fatalf("execute: %v", runErr)
	}
	evidence, _ := json.MarshalIndent(map[string]any{
		"startedAt": started.Format(time.RFC3339), "finishedAt": time.Now().UTC().Format(time.RFC3339),
		"elapsedSeconds": time.Since(started).Seconds(), "workspace": workspace,
		"model": outcome.Telemetry.Model, "cliVersion": outcome.Telemetry.CLIVersion,
		"command": outcome.Telemetry.Command, "exitCode": outcome.Telemetry.ExitCode,
		"status": outcome.Status, "summary": outcome.Summary, "output": outcome.Output,
		"failure": outcome.Failure, "bundleNonce": nonce,
	}, "", "  ")
	t.Logf("real task probe evidence:\n%s", evidence)
	if outcome.Status != provider.StatusCompleted {
		t.Fatalf("attempt did not complete: %+v", outcome)
	}
	if got := outcome.Output["probeNonce"]; got != nonce {
		t.Fatalf("attempt could not read the frozen input bundle: probeNonce = %v, want %s", got, nonce)
	}
	if got := outcome.Output["goTest"]; got != float64(0) {
		t.Fatalf("attempt could not run a command in its repository: goTest = %v (%t)", got, got)
	}
}

// randomProbeID gives every probe run a fresh identity so a later run can never
// be mistaken for an earlier one.
func randomProbeID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// environmentNames records which variables a probe received without ever
// capturing a value.
func environmentNames(environment []string) []string {
	names := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	return names
}
