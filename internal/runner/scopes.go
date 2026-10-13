package runner

import (
	"context"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/provider"
)

// materializeScopes checks out every read scope into the attempt workspace and
// returns the provider working directory: the single project checkout when the
// task binds exactly one project repository, otherwise the neutral work dir.
func (l *Loop) materializeScopes(
	ctx context.Context,
	lease execution.TaskLease,
	workspace string,
	marker string,
) (string, error) {
	readScopes := make([]execution.GitScope, 0, len(lease.Task.GitScopes))
	for _, scope := range lease.Task.GitScopes {
		switch scope.Operation {
		case "clone", "fetch", "pull":
			readScopes = append(readScopes, scope)
		}
	}
	if len(readScopes) == 0 {
		return "", nil
	}
	if l.git == nil {
		return "", &gitFailure{
			Code:    "git_scope_refused",
			Message: "this runtime has no repository registry configured",
		}
	}
	projectDirectories := make([]string, 0, len(readScopes))
	for _, scope := range readScopes {
		if err := l.git.materializeScope(ctx, scope, workspace, marker); err != nil {
			return "", err
		}
		if scope.Role == "project" {
			projectDirectories = append(projectDirectories, materializedDirectory(workspace, scope.RepositoryID))
		}
	}
	if len(projectDirectories) == 1 {
		return projectDirectories[0], nil
	}
	return "", nil
}

// settleScopeFailure converges an attempt whose repository materialization
// failed, using the frozen Git error classification.
func (l *Loop) settleScopeFailure(
	ctx context.Context,
	lease execution.TaskLease,
	record AttemptRecord,
	scopeErr error,
) error {
	if failure, ok := gitFailureOf(scopeErr); ok {
		return l.settleWithFailure(ctx, lease, record,
			provider.Failed(failure.Code, failure.Message, failure.Retryable))
	}
	if settleableTransportError(scopeErr) {
		l.logger.Info("attempt is no longer current; dropping journal record",
			"task", lease.Task.ID, "error", scopeErr)
		return l.clearAttempt(lease.Task.ID)
	}
	return scopeErr
}

// deliverScopes performs the controlled commit/push for every writable scope of
// a completed attempt and augments the outcome with compact delivery evidence.
// A delivery failure replaces the outcome with a classified failure; the
// worker's own result is never reported as delivered when it was not.
func (l *Loop) deliverScopes(
	ctx context.Context,
	lease execution.TaskLease,
	workspace string,
	marker string,
	outcome *provider.Outcome,
) error {
	writeScopes := make([]execution.GitScope, 0, len(lease.Task.GitScopes))
	for _, scope := range lease.Task.GitScopes {
		switch scope.Operation {
		case "commit", "push", "publish_pr":
			writeScopes = append(writeScopes, scope)
		}
	}
	if len(writeScopes) == 0 {
		return nil
	}
	telemetry := outcome.Telemetry
	if l.git == nil {
		*outcome = withTelemetry(provider.Failed("git_scope_refused",
			"this runtime has no repository registry configured", false), telemetry)
		return nil
	}
	deliveries := make([]map[string]any, 0, len(writeScopes))
	for _, scope := range writeScopes {
		record, err := l.git.deliverScope(ctx, scope, lease.Task, workspace, marker)
		if err != nil {
			l.logger.Warn("controlled delivery failed",
				"task", lease.Task.ID, "ref", scope.Ref, "error", err)
			*outcome = withTelemetry(gitFailureOutcome(err), telemetry)
			return nil
		}
		deliveries = append(deliveries, map[string]any{
			"repositoryId": record.RepositoryID, "branch": record.Branch,
			"commitOid": record.CommitOid, "baseOid": record.BaseOid,
			"reconciled": record.Reconciled,
		})
	}
	if len(deliveries) > 0 {
		if outcome.Output == nil {
			outcome.Output = map[string]any{}
		}
		outcome.Output["delivery"] = deliveries
	}
	return nil
}

func withTelemetry(outcome provider.Outcome, telemetry provider.Telemetry) provider.Outcome {
	outcome.Telemetry = telemetry
	return outcome
}

func gitFailureOutcome(err error) provider.Outcome {
	if failure, ok := gitFailureOf(err); ok {
		return provider.Failed(failure.Code, failure.Message, failure.Retryable)
	}
	return provider.Failed("git_push_failed", err.Error(), false)
}
