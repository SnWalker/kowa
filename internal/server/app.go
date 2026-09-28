// Package server owns the control-plane process composition root.
package server

import (
	"net/http"

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
			health.NewHandler,
			logging.New,
			httpserver.New,
		),
		fx.Invoke(func(*http.Server) {}),
	}
	baseOptions = append(baseOptions, options...)

	return fx.New(baseOptions...)
}
