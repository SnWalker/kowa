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
			config.NewObservability,
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
			newGitHubObserver,
			newRuntimeTransport,
			newDeliverySurface,
			newWorkItemService,
			newHumanTaskService,
			newGitHubDeliveryService,
			newEvaluationService,
			newGitRemoteService,
			newOrchestrationService,
			newWebApplication,
			newObservabilityRegistry,
			newEvaluationRecorder,
			newObservabilityProjector,
			newObservabilitySurface,
			health.NewHandler,
			logging.New,
			httpserver.New,
		),
		fx.Invoke(mountIdentityWorkspace),
		fx.Invoke(mountRuntimeTransport),
		fx.Invoke(mountDeliverySurface),
		fx.Invoke(mountWebApplicationSurface),
		fx.Invoke(mountObservabilitySurface),
		fx.Invoke(startExecutionSweeper),
		fx.Invoke(startOrchestrationReconciler),
	}
	baseOptions = append(baseOptions, options...)

	return fx.New(baseOptions...)
}
