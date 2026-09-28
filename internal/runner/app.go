// Package runner owns the execution-plane process composition root.
package runner

import (
	"github.com/SnWalker/kowa/internal/platform/logging"
	runnerplatform "github.com/SnWalker/kowa/internal/platform/runner"
	"go.uber.org/fx"
)

// New constructs the independent Runner application.
func New(options ...fx.Option) *fx.App {
	baseOptions := []fx.Option{
		fx.NopLogger,
		fx.Provide(
			logging.New,
			runnerplatform.NewRuntime,
		),
		fx.Invoke(func(*runnerplatform.Runtime) {}),
	}
	baseOptions = append(baseOptions, options...)

	return fx.New(baseOptions...)
}
