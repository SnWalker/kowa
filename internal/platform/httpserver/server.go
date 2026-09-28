package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/SnWalker/kowa/internal/platform/config"
	"go.uber.org/fx"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 10 * time.Second
)

// New constructs an HTTP server and binds its lifecycle to Fx.
func New(
	lifecycle fx.Lifecycle,
	serverConfig config.Server,
	healthHandler http.Handler,
	logger *slog.Logger,
) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", healthHandler)

	server := &http.Server{
		Addr:              serverConfig.Address,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			listener, err := net.Listen("tcp", server.Addr)
			if err != nil {
				return err
			}
			logger.Info("server started", "address", listener.Addr().String())
			go serve(server, listener, logger)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownContext, cancel := context.WithTimeout(ctx, shutdownTimeout)
			defer cancel()

			logger.Info("server stopping")
			return server.Shutdown(shutdownContext)
		},
	})

	return server
}

func serve(server *http.Server, listener net.Listener, logger *slog.Logger) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "error", err)
	}
}
