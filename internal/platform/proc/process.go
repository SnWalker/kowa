package proc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	// AttemptMarkerEnv names the per-attempt marker variable used to find
	// escaped (setsid) descendants after the process group is gone.
	AttemptMarkerEnv = "KOWA_RUNNER_ATTEMPT"
	// maxLogBytes bounds each captured stream so a runaway task cannot fill disk.
	maxLogBytes = 4 << 20
)

type Spec struct {
	Command    []string
	Dir        string
	Env        []string
	StdoutPath string
	StderrPath string
	Marker     string
	Grace      time.Duration
}

type Outcome struct {
	ExitCode int
	TimedOut bool
	Canceled bool
	Residual []string
}

// Run supervises one worker process in its own process group: stdin is
// closed, both streams go straight to bounded-on-completion files (direct file
// descriptors, so a survivor that inherited them cannot stall Wait), and
// termination goes group SIGTERM -> grace -> group SIGKILL, followed by a
// marker scan for processes that escaped the group.
func Run(ctx context.Context, spec Spec) (Outcome, error) {
	if len(spec.Command) == 0 {
		return Outcome{}, errors.New("proc: empty command")
	}
	stdout, err := openLogFile(spec.StdoutPath)
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := openLogFile(spec.StderrPath)
	if err != nil {
		return Outcome{}, err
	}
	defer func() { _ = stderr.Close() }()

	command := exec.Command(spec.Command[0], spec.Command[1:]...)
	command.Dir = spec.Dir
	command.Env = withMarkerEnv(spec.Env, spec.Marker)
	command.Stdin = nil
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return Outcome{}, fmt.Errorf("start worker process: %w", err)
	}
	groupId := command.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()

	var outcome Outcome
	select {
	case waitErr := <-waited:
		outcome.ExitCode = exitCode(waitErr)
		truncateLog(spec.StdoutPath)
		truncateLog(spec.StderrPath)
		return outcome, waitErr
	case <-ctx.Done():
	}
	outcome.Canceled = true
	outcome.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	_ = syscall.Kill(-groupId, syscall.SIGTERM)
	select {
	case <-waited:
	case <-time.After(spec.Grace):
		_ = syscall.Kill(-groupId, syscall.SIGKILL)
		<-waited
	}
	outcome.Residual = terminateSurvivors(spec.Marker)
	return outcome, nil
}

// withMarkerEnv guarantees the attempt marker is present in the child
// environment: every descendant that inherits the environment (including one
// that escapes the process group via setsid) stays discoverable, and the marker
// can never be ambiguous because an existing entry is replaced.
func withMarkerEnv(environment []string, marker string) []string {
	if marker == "" {
		return environment
	}
	markerEntry := AttemptMarkerEnv + "=" + marker
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if name, _, found := strings.Cut(entry, "="); found && name == AttemptMarkerEnv {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, markerEntry)
}

func exitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}

// TerminateSurvivors finds attempt-marked processes that escaped the process
// group, terminates them and returns evidence descriptions of what was found.
// It is used both after a supervised process ends and during restart recovery.
func TerminateSurvivors(marker string) []string {
	return terminateSurvivors(marker)
}

// terminateSurvivors finds processes that carry the attempt marker but escaped
// the process group, terminates them and returns evidence descriptions of what
// was found. The marker is a per-attempt random value, so a match identifies
// only this attempt's descendants.
func terminateSurvivors(marker string) []string {
	found, err := scanSurvivors(marker)
	if err != nil || len(found) == 0 {
		return nil
	}
	evidence := make([]string, 0, len(found))
	pids := make([]int, 0, len(found))
	for _, survivor := range found {
		evidence = append(evidence, fmt.Sprintf("pid=%d command=%s", survivor.PID, Truncate(survivor.Command, 160)))
		pids = append(pids, survivor.PID)
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(500 * time.Millisecond)
	remaining, err := scanSurvivors(marker)
	if err == nil {
		for _, survivor := range remaining {
			_ = syscall.Kill(survivor.PID, syscall.SIGKILL)
			evidence = append(evidence, fmt.Sprintf("pid=%d survived SIGTERM", survivor.PID))
		}
	}
	return evidence
}

type survivor struct {
	PID     int
	Command string
}

func Truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// openLogFile opens a stream capture file. The worker receives the file
// descriptor directly (never a Go writer), so exec does not add a copying
// pipe that a surviving descendant could keep open.
func openLogFile(path string) (*os.File, error) {
	if path == "" {
		return nil, errors.New("proc: log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return file, nil
}

// truncateLog bounds a captured stream after the process ended. While the
// process runs its output is not capped, so evidence records the final size
// honestly instead of silently dropping bytes.
func truncateLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxLogBytes {
		return
	}
	_ = os.Truncate(path, maxLogBytes)
}

// writeResidualEvidence persists the survivor evidence next to the attempt.
func WriteResidualEvidence(path string, marker string, residual []string) error {
	payload := struct {
		Marker   string    `json:"marker"`
		Observed time.Time `json:"observedAt"`
		Residual []string  `json:"residual"`
	}{Marker: marker, Observed: time.Now().UTC(), Residual: residual}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}
