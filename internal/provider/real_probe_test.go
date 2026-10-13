//go:build providerprobe

package provider_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/provider"
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
	cases := []struct {
		name    string
		adapter provider.Adapter
	}{
		{"codex", provider.NewCodexWithCLI("", os.Getenv("KOWA_REAL_CODEX_MODEL"))},
		{"claude", provider.NewClaudeWithCLI("", os.Getenv("KOWA_REAL_CLAUDE_MODEL"))},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			started := time.Now()
			probe, err := provider.ProbeExecution(testCase.adapter, t.Context())
			if err != nil {
				t.Fatalf("probe error: %v", err)
			}
			evidence, _ := json.MarshalIndent(map[string]any{
				"provider": testCase.name, "probe": probe,
				"elapsedSeconds": time.Since(started).Seconds(),
			}, "", "  ")
			t.Logf("probe evidence:\n%s", evidence)
			if !probe.Executable {
				t.Fatalf("provider CLI is not executable: %s", probe.Detail)
			}
			if !probe.Authenticated {
				t.Fatalf("provider CLI is not authenticated: %s", probe.Detail)
			}
			t.Logf("%s: executable=%v version=%q compatible=%v authenticated=%v canExecute=%v detail=%q",
				testCase.name, probe.Executable, probe.Version, probe.VersionCompatible,
				probe.Authenticated, probe.CanExecute, probe.Detail)
		})
	}
}
