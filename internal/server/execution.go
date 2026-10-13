package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/platform/config"
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
	runtimeConfig config.Runtime,
	logger *slog.Logger,
) *httpapi.RuntimeTransportHandler {
	if service == nil {
		return nil
	}
	return httpapi.NewRuntimeTransportHandler(runtimeConfig, service, logger)
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
