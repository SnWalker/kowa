// Package server owns the control-plane process composition root.
package server

import (
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/platform/health"
	"github.com/SnWalker/kowa/internal/platform/httpserver"
	"github.com/SnWalker/kowa/internal/platform/logging"
	"go.uber.org/fx"
)

// New constructs the control-plane application.
func New(options ...fx.Option) *fx.App {
	baseOptions := []fx.Option{
		fx.NopLogger,
		fx.Provide(
			config.NewServer,
			config.NewIdentity,
			config.NewRuntime,
			newDatabase,
			newOAuthClient,
			newIdentityClock,
			newRepositoryVisibility,
			newIdentityService,
			newWorkspaceService,
			newIdentityWorkspace,
			newExecutionService,
			newRuntimeOpsService,
			newRuntimeWeb,
			newRuntimeTransport,
			health.NewHandler,
			logging.New,
			httpserver.New,
		),
		fx.Invoke(mountIdentityWorkspace),
		fx.Invoke(mountRuntimeTransport),
		fx.Invoke(startExecutionSweeper),
	}
	baseOptions = append(baseOptions, options...)

	return fx.New(baseOptions...)
}
