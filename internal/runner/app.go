// Package runner owns the execution-plane process composition root.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/platform/logging"
	"github.com/SnWalker/kowa/internal/provider"
	"github.com/SnWalker/kowa/internal/runnertransport"
	"go.uber.org/fx"
)

const (
	workerGracePeriod = 10 * time.Second
	reportMargin      = 30 * time.Second
	shutdownTimeout   = 15 * time.Second
)

// New constructs the independent Runner application. The returned ExitCode
// reflects a fatal loop failure once the application has stopped.
func New(exit *ExitCode, options ...fx.Option) *fx.App {
	baseOptions := []fx.Option{
		fx.NopLogger,
		fx.Provide(logging.New, config.NewRunner),
		fx.Provide(newClient, newJournal, newAdapter, newHost, newLoop),
		fx.Invoke(runLoop(exit)),
	}
	baseOptions = append(baseOptions, options...)

	return fx.New(baseOptions...)
}

func newClient(runnerConfig config.Runner) *runnertransport.Client {
	return runnertransport.NewClient(runnerConfig.ServerURL, runnerConfig.RuntimeID, runnerConfig.Token)
}

func newJournal(runnerConfig config.Runner) (*Journal, error) {
	return NewJournal(runnerConfig.WorkRoot)
}

func newAdapter(runnerConfig config.Runner) (provider.Adapter, error) {
	switch runnerConfig.Provider {
	case "scripted":
		return provider.NewScripted(runnerConfig.Script), nil
	default:
		return nil, fmt.Errorf("runner: provider %q is not implemented in this build", runnerConfig.Provider)
	}
}

func newHost(adapter provider.Adapter, runnerConfig config.Runner, logger *slog.Logger) *Host {
	return NewHost(adapter, HostConfig{
		Environment:   os.Environ(),
		EnvAllowlist:  runnerConfig.EnvAllowlist,
		Grace:         workerGracePeriod,
		ReportMargin:  reportMargin,
		ProviderModel: runnerConfig.ProviderModel,
		Now:           time.Now,
	}, logger)
}

func newLoop(
	client *runnertransport.Client,
	journal *Journal,
	host *Host,
	adapter provider.Adapter,
	runnerConfig config.Runner,
	logger *slog.Logger,
) *Loop {
	capabilities := make([]execution.Capability, 0, len(runnerConfig.Capabilities))
	for _, capability := range runnerConfig.Capabilities {
		capabilities = append(capabilities, execution.Capability{ID: capability.ID, Version: capability.Version})
	}
	return NewLoop(client, journal, host, adapter, LoopConfig{
		RuntimeID:       runnerConfig.RuntimeID,
		GitHubUserID:    runnerConfig.GitHubUserID,
		Capabilities:    capabilities,
		ProviderVersion: runnerConfig.ProviderVersion,
		ProviderModel:   runnerConfig.ProviderModel,
		LeaseSeconds:    runnerConfig.LeaseSeconds,
		WaitSeconds:     25,
		HeartbeatEvery:  10 * time.Second,
		WorkRoot:        runnerConfig.WorkRoot,
	}, logger)
}

func runLoop(exit *ExitCode) func(fx.Lifecycle, *Loop, *slog.Logger, fx.Shutdowner) {
	return func(lifecycle fx.Lifecycle, loop *Loop, logger *slog.Logger, shutdowner fx.Shutdowner) {
		var cancel context.CancelFunc
		var done chan struct{}
		lifecycle.Append(fx.Hook{
			OnStart: func(context.Context) error {
				logger.Info("runner started", "provider", loop.adapter.ID(), "runtimeId", loop.config.RuntimeID)
				ctx, stop := context.WithCancel(context.Background())
				cancel = stop
				done = make(chan struct{})
				go func() {
					defer close(done)
					err := loop.Run(ctx)
					switch {
					case err == nil, errors.Is(err, context.Canceled):
						logger.Info("runner loop stopped")
					default:
						logger.Error("runner loop stopped with a fatal error", "error", err)
						exit.Set(1)
					}
					_ = shutdowner.Shutdown()
				}()
				return nil
			},
			OnStop: func(context.Context) error {
				logger.Info("runner stopping")
				if cancel != nil {
					cancel()
				}
				if done != nil {
					select {
					case <-done:
					case <-time.After(shutdownTimeout):
						logger.Warn("runner loop did not stop within the shutdown timeout")
					}
				}
				return nil
			},
		})
	}
}
