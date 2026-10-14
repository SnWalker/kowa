package runner

import "sync"

// ExitCode carries the process exit code so a fatal loop failure reaches
// main() after the fx application stops.
type ExitCode struct {
	mu   sync.Mutex
	code int
}

func NewExitCode() *ExitCode { return &ExitCode{} }

func (e *ExitCode) Set(code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.code = code
}

func (e *ExitCode) Code() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.code
}
