package githubobs

import (
	"context"
	"time"

	"github.com/SnWalker/kowa/internal/platform/proc"
)

// SetRunnerForTest replaces the command runner for black-box tests.
func SetRunnerForTest(observer *GHObserver, run func(context.Context, proc.Spec) (proc.Outcome, error)) {
	observer.run = run
}

// SetClockForTest pins the observation clock.
func SetClockForTest(observer *GHObserver, now func() time.Time) {
	observer.now = now
}
