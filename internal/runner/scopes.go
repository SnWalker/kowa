package runner

import (
	"context"
	"fmt"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/provider"
)

// classifyScopes splits every scope of a task into read and write groups and
// refuses operations outside the frozen enum: an unrecognized operation must
// fail the attempt rather than be silently ignored (fail closed).
func classifyScopes(scopes []execution.GitScope) (read, write []execution.GitScope, err error) {
	for _, scope := range scopes {
		switch scope.Operation {
		case "clone", "fetch", "pull":
			read = append(read, scope)
		case "commit", "push", "publish_pr":
			write = append(write, scope)
		default:
			return nil, nil, &gitFailure{
				Code:    "git_scope_refused",
				Message: fmt.Sprintf("operation %q is not in the frozen git scope enum", scope.Operation),
			}
		}
	}
	return read, write, nil
}

// materializeScopes checks out every read scope into the attempt workspace and
// returns the provider working directory: the single project checkout when the
// task binds exactly one project repository, otherwise the neutral work dir.
func (l *Loop) materializeScopes(
	ctx context.Context,
	lease execution.TaskLease,
	workspace string,
	marker string,
) (string, error) {
	readScopes, _, err := classifyScopes(lease.Task.GitScopes)
	if err != nil {
		return "", err
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
	_, writeScopes, err := classifyScopes(lease.Task.GitScopes)
	if err != nil {
		telemetry := outcome.Telemetry
		*outcome = withTelemetry(gitFailureOutcome(err), telemetry)
		return nil
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
		delivery := map[string]any{
			"repositoryId": record.RepositoryID, "ref": record.Ref,
			"commitOid": record.CommitOid, "baseOid": record.BaseOid,
			"reconciled": record.Reconciled,
		}
		if record.PullRequestNumber != "" {
			// The publish_pr write reports the pull request the controlled
			// channel created or reused; the flow's declared output pr_number
			// reads exactly this value, and the control plane still settles the
			// operation by observing GitHub itself.
			delivery["pullRequestNumber"] = record.PullRequestNumber
			delivery["pullRequestUrl"] = record.PullRequestURL
		}
		deliveries = append(deliveries, delivery)
	}
	if len(deliveries) > 0 {
		if outcome.Output == nil {
			outcome.Output = map[string]any{}
		}
		outcome.Output["delivery"] = deliveries
		for _, delivery := range deliveries {
			if number, ok := delivery["pullRequestNumber"].(string); ok && number != "" {
				// The publish_pr capability declares pr_number as its output;
				// the controlled write is its producer.
				outcome.Output["pr_number"] = number
			}
		}
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
