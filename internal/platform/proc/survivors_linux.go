//go:build linux

package proc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// scanSurvivors finds attempt-marked processes on Linux by reading
// /proc/<pid>/environ; processes owned by other users are skipped.
func scanSurvivors(marker string) ([]survivor, error) {
	if marker == "" {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	needle := []byte(AttemptMarkerEnv + "=" + marker)
	var survivors []survivor
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		environ, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil || !containsMarker(environ, needle) {
			continue
		}
		command := ""
		if cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline")); err == nil {
			command = strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
		}
		survivors = append(survivors, survivor{PID: pid, Command: command})
	}
	return survivors, nil
}

func containsMarker(environ, needle []byte) bool {
	for _, variable := range splitNul(environ) {
		if len(variable) >= len(needle) && string(variable[:len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

func splitNul(data []byte) [][]byte {
	var parts [][]byte
	start := 0
	for index, value := range data {
		if value == 0 {
			parts = append(parts, data[start:index])
			start = index + 1
		}
	}
	if start < len(data) {
		parts = append(parts, data[start:])
	}
	return parts
}
