//go:build darwin

package proc

import (
	"os/exec"
	"strconv"
	"strings"
)

// scanSurvivors finds attempt-marked processes on macOS. `ps -E` appends each
// process environment to its command column, so a marker that was inherited by
// an escaped (setsid) descendant is visible without ptrace.
func scanSurvivors(marker string) ([]survivor, error) {
	if marker == "" {
		return nil, nil
	}
	output, err := exec.Command("ps", "-A", "-E", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	needle := AttemptMarkerEnv + "=" + marker
	var survivors []survivor
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.Contains(trimmed, needle) {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		survivors = append(survivors, survivor{PID: pid, Command: strings.Join(fields[1:], " ")})
	}
	return survivors, nil
}
