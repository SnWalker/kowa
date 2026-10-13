package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/runnertransport"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

// executionSweepInterval paces the control-plane lease sweep: expired leases
// become EXPIRED and idle CANCELING runs finalize, without any heartbeat
// renewing a lease.
const executionSweepInterval = 15 * time.Second

// newDatabase provides the shared control-plane pool when either the identity
// or the runtime surface is enabled. Identity configuration owns the URL when
// both are configured.
func newDatabase(lifecycle fx.Lifecycle, identityConfig config.Identity, runtimeConfig config.Runtime) (*sql.DB, error) {
	if err := runtimeConfig.Validate(); err != nil {
		return nil, err
	}
	databaseURL := identityConfig.DatabaseURL
	if databaseURL == "" {
		databaseURL = runtimeConfig.DatabaseURL
	}
	if !identityConfig.Enabled && !runtimeConfig.Enabled {
		return nil, nil
	}
	if databaseURL == "" {
		return nil, errors.New("database url is required when identity or runtime transport is enabled")
	}
	db, err := postgres.NewPool(databaseURL)
	if err != nil {
		return nil, err
	}
	lifecycle.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return errors.Join(err, db.Close())
		}
		return nil
	}, OnStop: func(context.Context) error { return db.Close() }})
	return db, nil
}

// newExecutionService exposes the frozen execution domain over the shared
// pool. It is nil when the runtime transport is disabled.
func newExecutionService(db *sql.DB, runtimeConfig config.Runtime) *execution.Service {
	if !runtimeConfig.Enabled || db == nil {
		return nil
	}
	return execution.NewService(postgres.NewStore(db), time.Now)
}

func newRuntimeTransport(
	service *execution.Service,
	runtimeOps *runtimeops.Service,
	runtimeConfig config.Runtime,
	logger *slog.Logger,
) *httpapi.RuntimeTransportHandler {
	if service == nil {
		return nil
	}
	var observations httpapi.RuntimeObservationSink
	if runtimeOps != nil {
		observations = runtimeOps
	}
	return httpapi.NewRuntimeTransportHandler(runtimeConfig, service, unboundTaskInput{}, observations, logger)
}

// newRuntimeOpsService is the runtime observation and execution-configuration
// authority over the shared pool.
func newRuntimeOpsService(db *sql.DB, service *execution.Service, runtimeConfig config.Runtime) *runtimeops.Service {
	if !runtimeConfig.Enabled || db == nil || service == nil {
		return nil
	}
	return runtimeops.NewService(postgres.NewStore(db), service, time.Now)
}

// newRuntimeWeb exposes kowa.runtime-web.v1 through the S01 session and
// membership authorities.
func newRuntimeWeb(
	identityConfig config.Identity,
	runtimeConfig config.Runtime,
	auth *identity.Service,
	workspaces *workspace.Service,
	runtimeOps *runtimeops.Service,
) *httpapi.RuntimeWebHandler {
	if !identityConfig.Enabled || auth == nil || workspaces == nil || runtimeOps == nil {
		return nil
	}
	targets := runtimeWebTargets{runtimeOps}
	return httpapi.NewRuntimeWebHandler(auth, workspaces, targets, identityConfig.TrustedOrigin)
}

// runtimeWebTargets adapts the runtime-operations service to the handler's
// consumer interface without exposing the concrete type.
type runtimeWebTargets struct{ service *runtimeops.Service }

func (t runtimeWebTargets) WorkspaceRuntimes(ctx context.Context, workspaceID string) ([]runtimeops.RuntimeView, error) {
	return t.service.WorkspaceRuntimes(ctx, workspaceID)
}

func (t runtimeWebTargets) WorkspaceRuntime(ctx context.Context, workspaceID, runtimeID string) (runtimeops.RuntimeView, error) {
	return t.service.WorkspaceRuntime(ctx, workspaceID, runtimeID)
}

func (t runtimeWebTargets) ExecutionConfig(ctx context.Context, workspaceID string) (runtimeops.ExecutionConfig, error) {
	return t.service.ExecutionConfig(ctx, workspaceID)
}

func (t runtimeWebTargets) UpdateExecutionConfig(
	ctx context.Context,
	command runtimeops.UpdateExecutionConfigCommand,
) (runtimeops.ExecutionConfig, error) {
	return t.service.UpdateExecutionConfig(ctx, command)
}

// unboundTaskInput fails closed for the frozen-input routes: the task → input
// binding is persisted by run admission (S05/S06), and until that producer
// exists the control plane must not serve unverified content to a Runner.
type unboundTaskInput struct{}

func (unboundTaskInput) TaskInput(context.Context, string) (runnertransport.InputManifestResponse, error) {
	return runnertransport.InputManifestResponse{}, httpapi.ErrTaskInputUnbound
}

func (unboundTaskInput) TaskInputArtifact(context.Context, string, string) ([]byte, error) {
	return nil, httpapi.ErrTaskInputUnbound
}

func mountRuntimeTransport(srv *http.Server, handler *httpapi.RuntimeTransportHandler) {
	var api http.Handler = handler
	if handler == nil {
		// A disabled transport fails closed with the frozen error envelope so
		// a Runner treats it as a retryable unavailable control plane.
		api = http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Cache-Control", "no-store")
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"schemaVersion":"kowa.workflow-execution.v1","kind":"apiError","error":"RESOURCE_UNAVAILABLE","message":"runtime transport is disabled"}`))
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler)
	mux.Handle("/api/v1/runtime/", api)
	srv.Handler = mux
}

// startExecutionSweeper runs the lease sweep on a background ticker whenever
// the execution service is available.
func startExecutionSweeper(lifecycle fx.Lifecycle, service *execution.Service, logger *slog.Logger) {
	if service == nil {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				ticker := time.NewTicker(executionSweepInterval)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
					}
					result, err := service.SweepExpiredLeases(context.Background())
					if err != nil {
						logger.Error("lease sweep failed", "error", err)
						continue
					}
					if result.ExpiredTasks > 0 || result.FinalizedRuns > 0 {
						logger.Info("lease sweep applied",
							"expiredTasks", result.ExpiredTasks, "finalizedRuns", result.FinalizedRuns)
					}
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			close(stop)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				logger.Warn("lease sweeper did not stop within the shutdown timeout")
			}
			return nil
		},
	})
}
