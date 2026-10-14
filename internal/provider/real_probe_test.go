//go:build providerprobe

package provider_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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
