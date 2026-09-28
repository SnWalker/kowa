package runner

import (
	"context"
	"log/slog"

	"go.uber.org/fx"
)

// Runtime is the lifecycle seam for the independent Runner process.
type Runtime struct{}

// NewRuntime constructs the Runner lifecycle without implementing task execution.
func NewRuntime(lifecycle fx.Lifecycle, logger *slog.Logger) *Runtime {
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			logger.Info("runner started")
			return nil
		},
		OnStop: func(context.Context) error {
			logger.Info("runner stopping")
			return nil
		},
	})

	return &Runtime{}
}
