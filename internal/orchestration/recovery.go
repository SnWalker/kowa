package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/workflow"
	"github.com/SnWalker/kowa/internal/workitem"
)

// maxRecoveryProbes bounds how many of this module's own deterministic attempt
// identities one retry enumerates.
const maxRecoveryProbes = 8

// Recover applies one authorized recovery command. Every precondition is
// re-read from the owning module: the run must still hold advancement
// authority, the node must be the one the command names, and a new round is
// only granted inside the frozen budget or by an explicit human grant.
func (s *Service) Recover(ctx context.Context, command RecoveryCommand) (RunRecovery, error) {
	if command.WorkspaceID == "" || command.WorkItemID == "" || command.RunID == "" ||
		!validActor(command.ActorID) || !requestKeyRule.MatchString(command.RequestKey) ||
		command.ExpectedVersion < 1 {
		return RunRecovery{}, ErrValidation
	}
	switch command.Kind {
	case RecoveryRetry, RecoveryRerun:
		if command.NodeID == "" {
			return RunRecovery{}, ErrValidation
		}
	case RecoveryCancel:
		if command.NodeID != "" {
			return RunRecovery{}, ErrValidation
		}
	case RecoveryRepair:
		if command.NodeID != "" || command.Reason == "" {
			return RunRecovery{}, ErrValidation
		}
	default:
		return RunRecovery{}, ErrValidation
	}
	if err := s.authorizeWriter(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return RunRecovery{}, err
	}
	digest := commandDigest("orchestration.recovery", command.WorkspaceID, command.RunID, string(command.Kind),
		command.NodeID, command.Reason, fmt.Sprint(command.ExpectedVersion))
	if replayed, found, err := s.store.ReplayedCommand(ctx, command.ActorID, CommandRecovery,
		command.RunID, command.RequestKey, digest); err != nil || found {
		if err != nil {
			return RunRecovery{}, err
		}
		var recovery RunRecovery
		if err := json.Unmarshal(replayed, &recovery); err != nil {
			return RunRecovery{}, fmt.Errorf("decode the recorded recovery: %w", err)
		}
		return recovery, nil
	}
	item, err := s.workitems.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return RunRecovery{}, err
	}
	if item.State != workitem.StateOpen {
		return RunRecovery{}, fmt.Errorf("%w: the work item is %s", ErrPolicyBlocked, item.State)
	}
	run, err := s.execution.Run(ctx, command.RunID)
	if err != nil {
		return RunRecovery{}, err
	}
	if run.WorkspaceID != command.WorkspaceID || run.WorkItemID != command.WorkItemID {
		return RunRecovery{}, fmt.Errorf("%w: the run does not belong to this work item", ErrForbidden)
	}

	recovery := RunRecovery{
		RunID: command.RunID, Kind: command.Kind, RecordedAt: s.now().UTC(),
		RunState: string(run.State), RunVersion: run.Version,
	}
	switch command.Kind {
	case RecoveryCancel:
		if command.ExpectedVersion != run.Version {
			return RunRecovery{}, ErrVersionConflict
		}
		if err := s.execution.CancelRun(ctx, command.RunID, command.ExpectedVersion); err != nil {
			return RunRecovery{}, err
		}
		recovery.Applied = true
		recovery.Detail = "cancellation was requested; in-flight attempts settle before the run ends"
	case RecoveryRetry:
		detail, err := s.retryNode(ctx, command)
		if err != nil {
			return RunRecovery{}, err
		}
		recovery.Applied, recovery.Detail = true, detail
	case RecoveryRerun:
		if item.CurrentRunID != command.RunID {
			return RunRecovery{}, fmt.Errorf(
				"%w: only the run holding advancement authority may rerun a node", ErrPolicyBlocked)
		}
		detail, err := s.rerunNode(ctx, command)
		if err != nil {
			return RunRecovery{}, err
		}
		recovery.Applied, recovery.Detail = true, detail
	case RecoveryRepair:
		baseline, err := s.repairBaseline(ctx, command)
		if err != nil {
			return RunRecovery{}, err
		}
		result, err := s.Admit(ctx, AdmitCommand{
			WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID, ActorID: command.ActorID,
			RequestKey: command.RequestKey, ExpectedVersion: command.ExpectedVersion,
			Kind: workitem.BindingRepair, SourceRunID: command.RunID, Reason: command.Reason,
			MergedBaseline: baseline,
		})
		if err != nil {
			return RunRecovery{}, err
		}
		recovery.Applied = true
		recovery.Detail = "repair run " + result.Admission.RunID + " holds advancement authority"
		recovery.RunID = result.Admission.RunID
	}
	if current, err := s.execution.Run(ctx, recovery.RunID); err == nil {
		recovery.RunState, recovery.RunVersion = string(current.State), current.Version
	}
	encoded, err := json.Marshal(recovery)
	if err != nil {
		return RunRecovery{}, fmt.Errorf("encode the recovery outcome: %w", err)
	}
	if err := s.store.RecordCommand(ctx, command.ActorID, CommandRecovery, command.RunID,
		command.RequestKey, digest, encoded); err != nil {
		return RunRecovery{}, err
	}
	// Advance after the command is recorded: a crash before this point leaves
	// an idempotent command the next reconciliation completes.
	if recovery.Applied && command.Kind != RecoveryCancel && command.Kind != RecoveryRepair {
		if err := s.Advance(ctx, command.RunID); err != nil {
			return RunRecovery{}, err
		}
	}
	return recovery, nil
}

// retryNode creates the next attempt of the node's current round. Retry keeps
// the effective input, so the frozen binding is reused unchanged.
func (s *Service) retryNode(ctx context.Context, command RecoveryCommand) (string, error) {
	node, found := s.plan.Nodes[command.NodeID]
	if !found || node.Type != workflow.NodeCapability {
		return "", fmt.Errorf("%w: node %q is not a capability node", ErrValidation, command.NodeID)
	}
	view, err := s.execution.RunView(ctx, command.RunID)
	if err != nil {
		return "", err
	}
	round, found := s.roundOf(view, command.NodeID)
	if !found {
		return "", fmt.Errorf("%w: node %q has no round to retry", ErrPolicyBlocked, command.NodeID)
	}
	for probe := 0; probe < maxRecoveryProbes; probe++ {
		candidate := attemptID(command.RunID, command.NodeID, round.Iteration) + retrySuffix(probe)
		status, err := s.execution.TaskStatus(ctx, candidate)
		if err == nil && status.State != execution.TaskFailed &&
			status.State != execution.TaskExpired && status.State != execution.TaskCanceled {
			// The newest attempt is not retryable; an input-demanding attempt
			// needs Resume, not Retry.
			continue
		}
		if err != nil {
			continue
		}
		next, err := s.execution.RetryTask(ctx, candidate, candidate+"-next")
		if err != nil {
			return "", err
		}
		return "attempt " + strconv.Itoa(next.Attempt) + " of node " + command.NodeID + " was queued with the frozen input", nil
	}
	return "", fmt.Errorf("%w: node %q has no failed attempt to retry", ErrPolicyBlocked, command.NodeID)
}

// retrySuffix names the attempts of one round deterministically, so a recovery
// can find the newest attempt this module created.
func retrySuffix(probe int) string {
	if probe == 0 {
		return ""
	}
	return fmt.Sprintf(":retry:%d", probe)
}

// rerunNode opens one new round of the node inside its budget. A rerun is an
// explicit human action, so it grants exactly one further round.
func (s *Service) rerunNode(ctx context.Context, command RecoveryCommand) (string, error) {
	node, found := s.plan.Nodes[command.NodeID]
	if !found {
		return "", fmt.Errorf("%w: node %q is not in the frozen plan", ErrValidation, command.NodeID)
	}
	view, err := s.execution.RunView(ctx, command.RunID)
	if err != nil {
		return "", err
	}
	round, found := s.roundOf(view, command.NodeID)
	if !found {
		return "", fmt.Errorf("%w: node %q has no round to rerun", ErrPolicyBlocked, command.NodeID)
	}
	if round.State != execution.NodeDecided && round.State != execution.NodeExecutionFailed {
		return "", fmt.Errorf("%w: the current round of node %q is %s",
			ErrPolicyBlocked, command.NodeID, round.State)
	}
	admission, err := s.store.Admission(ctx, command.RunID)
	if err != nil {
		return "", err
	}
	if node.Type == workflow.NodeCapability {
		dispatched, err := s.dispatchCapability(ctx, admission, node, execution.RoundRerun, view)
		if err != nil {
			return "", err
		}
		if !dispatched {
			return "", fmt.Errorf("%w: the rerun of node %q could not be materialised", ErrPolicyBlocked, command.NodeID)
		}
		return "node " + command.NodeID + " opened a new round", nil
	}
	executed, err := s.executeControlNode(ctx, admission, node, view)
	if err != nil {
		return "", err
	}
	if !executed {
		return "", fmt.Errorf("%w: the rerun of node %q could not be materialised", ErrPolicyBlocked, command.NodeID)
	}
	return "node " + command.NodeID + " opened a new round", nil
}

// repairBaseline freezes the merged delivery the repair continues from. It is
// the actual merge commit of the closed delivery, never the target branch head.
func (s *Service) repairBaseline(ctx context.Context, command RecoveryCommand) (string, error) {
	closure, found, err := s.evaluation.Closure(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return "", err
	}
	if found && oidPattern.MatchString(closure.MergeCommitOID) {
		return closure.MergeCommitOID, nil
	}
	admission, err := s.store.Admission(ctx, command.RunID)
	if err != nil {
		return "", err
	}
	observation, found, err := s.mergeObservation(ctx, admission)
	if err != nil {
		return "", err
	}
	if found && oidPattern.MatchString(observation.PullRequest.MergeCommitOID) {
		return observation.PullRequest.MergeCommitOID, nil
	}
	return "", fmt.Errorf("%w: the merged delivery commit of the previous run is not established", ErrPolicyBlocked)
}

var _ = fixedflow.NodeClose
