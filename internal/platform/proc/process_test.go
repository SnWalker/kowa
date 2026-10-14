package proc_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/platform/proc"
)

func spec(t *testing.T, dir, command string, marker string, timeout time.Duration) (proc.Spec, context.Context, context.CancelFunc) {
	t.Helper()
	ctx := t.Context()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	return proc.Spec{
		Command: []string{"/bin/sh", "-c", command},
		Dir:     dir, Env: os.Environ(),
		StdoutPath: filepath.Join(dir, "stdout.log"),
		StderrPath: filepath.Join(dir, "stderr.log"),
		Marker:     marker, Grace: 2 * time.Second,
	}, ctx, cancel
}

func TestRunCapturesStreamsAndExitCode(t *testing.T) {
	dir := t.TempDir()
	processSpec, ctx, cancel := spec(t, dir, `echo out; echo err >&2; exit 3`, "marker-normal", 0)
	defer cancel()
	outcome, err := proc.Run(ctx, processSpec)
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || outcome.ExitCode != 3 {
		t.Fatalf("outcome = %+v, err = %v", outcome, err)
	}
	stdout, _ := os.ReadFile(filepath.Join(dir, "stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(dir, "stderr.log"))
	if !strings.Contains(string(stdout), "out") || !strings.Contains(string(stderr), "err") {
		t.Fatalf("streams = %q / %q", stdout, stderr)
	}
	if outcome.TimedOut || outcome.Canceled {
		t.Fatalf("outcome flags = %+v", outcome)
	}
}

func TestRunClosesStdin(t *testing.T) {
	dir := t.TempDir()
	// A process that reads stdin must see EOF immediately, never inherit the
	// test process terminal.
	processSpec, ctx, cancel := spec(t, dir, `if read line; then echo "got:$line"; else echo "eof"; fi`, "marker-stdin", 5*time.Second)
	defer cancel()
	if _, err := proc.Run(ctx, processSpec); err != nil {
		t.Fatal(err)
	}
	stdout, _ := os.ReadFile(filepath.Join(dir, "stdout.log"))
	if strings.TrimSpace(string(stdout)) != "eof" {
		t.Fatalf("stdin was not closed: %q", stdout)
	}
}

func TestRunTimesOutAndKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	// A child in the same process group must not survive the group kill.
	processSpec, ctx, cancel := spec(t, dir, `sleep 30 & echo $! > child.pid; wait`, "marker-timeout", 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	outcome, err := proc.Run(ctx, processSpec)
	if err != nil {
		t.Fatalf("run = %v", err)
	}
	if !outcome.TimedOut || !outcome.Canceled {
		t.Fatalf("outcome = %+v, want timeout", outcome)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("termination took %s", elapsed)
	}
	childPID, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
	pid := strings.TrimSpace(string(childPID))
	if pid != "" {
		if err := exec.Command("kill", "-0", pid).Run(); err == nil {
			_ = exec.Command("kill", "-9", pid).Run()
			t.Fatalf("process-group child %s survived the group kill", pid)
		}
	}
}

func TestRunFindsEscapedSurvivorAndRecordsEvidence(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the setsid escape scenario")
	}
	dir := t.TempDir()
	// The escapee leaves the process group via setsid, exactly like the spike
	// report describes for real providers.
	script := python + ` -c "import os,time; os.setsid(); time.sleep(30)" & sleep 0.3; wait`
	processSpec, ctx, cancel := spec(t, dir, script, "marker-escape-xyz", 800*time.Millisecond)
	defer cancel()
	outcome, err := proc.Run(ctx, processSpec)
	if err != nil {
		t.Fatalf("run = %v", err)
	}
	found := false
	for _, entry := range outcome.Residual {
		if strings.Contains(entry, "pid=") {
			found = true
		}
	}
	if !found {
		t.Fatalf("escaped survivor not reported: %+v", outcome)
	}
	// Evidence must be persistable for the attempt.
	evidencePath := filepath.Join(dir, "residual.json")
	if err := proc.WriteResidualEvidence(evidencePath, "marker-escape-xyz", outcome.Residual); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(evidencePath)
	if err != nil || !strings.Contains(string(encoded), "marker-escape-xyz") {
		t.Fatalf("residual evidence = %s, %v", encoded, err)
	}
	// The survivor must be terminated, not merely reported: a second scan finds nothing.
	if remaining := proc.TerminateSurvivors("marker-escape-xyz"); len(remaining) > 0 {
		t.Fatalf("survivors remain after termination: %+v", remaining)
	}
}
