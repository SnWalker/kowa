package runner

import (
	"context"
	"log/slog"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
)

// GitChannel is exported for black-box tests of the controlled Git channel.
type GitChannel = gitChannel

// NewTestGitChannel constructs a channel with a fake identity resolver and an
// explicit environment.
func NewTestGitChannel(
	repositories map[string]string,
	protectedRefs []string,
	identity func(context.Context) (string, error),
	environment []string,
	logger *slog.Logger,
) *GitChannel {
	return newGitChannel(repositories, protectedRefs, identity, environment, logger)
}

// SetGitRunnerForTest replaces the command runner so tests can simulate unknown
// push outcomes without losing the real remote effect.
func SetGitRunnerForTest(channel *GitChannel, run func(context.Context, proc.Spec) (proc.Outcome, error)) {
	channel.run = run
}

// DeliveryRecord is exported for black-box delivery assertions.
type DeliveryRecord = deliveryRecord

func (c *GitChannel) MaterializeScopeForTest(
	ctx context.Context,
	scope execution.GitScope,
	workspace string,
	marker string,
) error {
	return c.materializeScope(ctx, scope, workspace, marker)
}

func (c *GitChannel) DeliverScopeForTest(
	ctx context.Context,
	scope execution.GitScope,
	task execution.TaskSpec,
	workspace string,
	marker string,
) (DeliveryRecord, error) {
	return c.deliverScope(ctx, scope, task, workspace, marker)
}
