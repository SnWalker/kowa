package runner_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/provider"
	"github.com/SnWalker/kowa/internal/runner"
)

func hostFixture(t *testing.T, script string) *runner.Host {
	t.Helper()
	adapter := provider.NewScripted(script)
	return runner.NewHost(adapter, runner.HostConfig{
		Environment: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
			"GITHUB_TOKEN=must-not-leave", "ANTHROPIC_API_KEY=must-not-leave",
		},
		EnvAllowlist: []string{"PATH", "HOME"},
		Grace:        time.Second, ReportMargin: 30 * time.Second, Now: time.Now,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func hostLease(expiresAt time.Time) execution.TaskLease {
	return execution.TaskLease{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskLeaseKind,
		LeaseID: "lease-1", FencingToken: 1, RuntimeEpoch: "epoch-1",
		ExpiresAt: expiresAt,
		Task: execution.TaskSpec{
			ID: "task-1", WorkflowRunID: "run-1", NodeRunID: "node-1",
			Attempt: 1, AttemptKind: execution.AttemptInitial,
			Capability:         execution.Capability{ID: "plan.create", Version: 1},
			ProviderSelection:  execution.ProviderSelection{ID: "scripted", Version: "scripted-1"},
			InputDigest:        "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			GitExecutionUserID: "9001", GitScopes: []execution.GitScope{},
			DeadlineAt: expiresAt.Add(time.Hour),
		},
	}
}

func TestHostRejectsExpiredDeadlineWithoutSpawning(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	host := hostFixture(t, counterScript(counter))
	outcome := host.Execute(t.Context(), runner.ExecutionRequest{
		Task: hostLease(time.Now().Add(-time.Minute)).Task,
		Lease: execution.TaskLease{
			ExpiresAt: time.Now().Add(-time.Minute),
			Task:      hostLease(time.Now().Add(-time.Minute)).Task,
		},
		Workspace: t.TempDir(), Marker: "marker-deadline",
	})
	if outcome.Status != provider.StatusFailed || outcome.Failure == nil ||
		outcome.Failure.Code != provider.CodeDeadlineExceeded {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := os.Stat(counter); !os.IsNotExist(err) {
		t.Fatal("expired attempt still spawned a worker")
	}
}

func TestHostUsesLeaseMarginAsDeadline(t *testing.T) {
	script := `sleep 30; printf '%s' '{"status":"completed","summary":"late","output":{}}' > "$KOWA_SCRIPTED_OUTPUT"`
	host := hostFixture(t, script)
	workspace := t.TempDir()
	started := time.Now()
	// The lease expires in two seconds; with a 30s report margin the effective
	// deadline is already in the past, so the attempt must not start.
	outcome := host.Execute(t.Context(), runner.ExecutionRequest{
		Task:      hostLease(time.Now().Add(2 * time.Second)).Task,
		Lease:     hostLease(time.Now().Add(2 * time.Second)),
		Workspace: workspace, Marker: "marker-margin",
	})
	if outcome.Failure == nil || outcome.Failure.Code != provider.CodeDeadlineExceeded {
		t.Fatalf("outcome = %+v, want deadline exceeded", outcome)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("deadline was not enforced promptly: %s", elapsed)
	}
}

func TestHostWritesEvidenceWithoutSecrets(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	host := hostFixture(t, counterScript(counter))
	workspace := t.TempDir()
	outcome := host.Execute(t.Context(), runner.ExecutionRequest{
		Task:      hostLease(time.Now().Add(10 * time.Minute)).Task,
		Lease:     hostLease(time.Now().Add(10 * time.Minute)),
		Workspace: workspace, Marker: "marker-evidence",
	})
	if outcome.Status != provider.StatusCompleted {
		t.Fatalf("outcome = %+v", outcome)
	}
	evidence, err := os.ReadFile(filepath.Join(workspace, "evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(evidence)
	if !strings.Contains(text, `"taskId": "task-1"`) || !strings.Contains(text, `"exitCode": 0`) {
		t.Fatalf("evidence = %s", text)
	}
	for _, secret := range []string{"must-not-leave", "GITHUB_TOKEN", "ANTHROPIC_API_KEY"} {
		if strings.Contains(text, secret) {
			t.Fatalf("evidence leaked %q", secret)
		}
	}
}

func TestBuildWorkerEnvironmentAllowlistOnly(t *testing.T) {
	environment := runner.BuildWorkerEnvironment(
		[]string{"PATH", "HOME"},
		[]string{"PATH=/bin", "HOME=/home/user", "GITHUB_TOKEN=secret", "LD_PRELOAD=/evil"},
		map[string]string{"TMPDIR": "/task/tmp"},
	)
	joined := strings.Join(environment, " ")
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "HOME=/home/user") {
		t.Fatalf("allowlisted entries missing: %q", joined)
	}
	if !strings.Contains(joined, "TMPDIR=/task/tmp") {
		t.Fatalf("override missing: %q", joined)
	}
	if strings.Contains(joined, "secret") || strings.Contains(joined, "LD_PRELOAD") {
		t.Fatalf("non-allowlisted entry forwarded: %q", joined)
	}
}
