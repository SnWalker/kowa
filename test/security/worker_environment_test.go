// secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.

package security

import (
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/runner"
)

// TestTheWorkerEnvironmentIsTheMeasuredBoundary binds the claim the operations
// book makes about a task's host-visible scope to the code that actually builds
// a worker's environment. The measurement in
// scripts/test-recovery-drill.sh derives its command from this list, so the
// record and the code cannot drift apart.
func TestTheWorkerEnvironmentIsTheMeasuredBoundary(t *testing.T) {
	// The Runner's frozen default allowlist.
	allowlist := []string{"PATH", "HOME", "LANG", "LC_ALL", "TERM"}
	source := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/Users/example",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
		"TERM=xterm-256color",
		// Credential-shaped variables that must never be forwarded.
		"KOWA_RUNNER_RUNTIME_TOKEN=runner-token-0123456789",
		"KOWA_GITHUB_CLIENT_SECRET=oauth-client-secret-0123456789",
		"GITHUB_TOKEN=ghp_0123456789abcdefghijklmnopqrstuvwx",
		"ANTHROPIC_API_KEY=sk-ant-0123456789abcdef",
	}
	environment := runner.BuildWorkerEnvironment(allowlist, source, map[string]string{"TMPDIR": "/tmp/kowa"})

	forwarded := map[string]string{}
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		forwarded[name] = value
	}
	for _, name := range allowlist {
		if _, present := forwarded[name]; !present {
			t.Fatalf("the allowlisted variable %s must reach the worker", name)
		}
	}
	for _, forbidden := range []string{
		"KOWA_RUNNER_RUNTIME_TOKEN", "KOWA_GITHUB_CLIENT_SECRET", "GITHUB_TOKEN", "ANTHROPIC_API_KEY",
	} {
		if _, present := forwarded[forbidden]; present {
			t.Fatalf("%s must never be forwarded to a worker", forbidden)
		}
	}
	// HOME is forwarded, which is exactly why a single macOS account is not a
	// hard boundary: the worker inherits the account whose home holds the
	// machine's gh and git credentials. The operations book records this as a
	// measured risk, not as isolation.
	if forwarded["HOME"] != "/Users/example" {
		t.Fatalf("the measured boundary changed: HOME = %q", forwarded["HOME"])
	}
	if forwarded["TMPDIR"] != "/tmp/kowa" {
		t.Fatalf("the caller's override must win: TMPDIR = %q", forwarded["TMPDIR"])
	}
}
