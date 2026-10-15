package execution

import "fmt"

// PlannedRound is the single authority over how a node round comes into being.
// Both adapters and both ports apply it, so an in-memory and a PostgreSQL run
// can never disagree about which round a command materialises.
//
// The rules are: the first round attaches to the round admission already
// created (or is created when the node has none), and a later round exists only
// as a sanctioned revision or rerun of the settled current round.
func PlannedRound(
	current NodeRun,
	found bool,
	roundHasAttempt bool,
	roundHasUnsettledAttempt bool,
	round DispatchRound,
	runID, nodeID, nodeRunID, inputDigest string,
	newRoundState NodeState,
	attachState NodeState,
	attachable []NodeState,
) (NodeRun, error) {
	if _, err := parseRoundShape(round, newRoundState); err != nil {
		return NodeRun{}, err
	}
	switch round.Kind {
	case RoundInitial:
		if !found {
			return NodeRun{
				ID: nodeRunID, RunID: runID, NodeID: nodeID, Iteration: 1,
				State: newRoundState, InputDigest: inputDigest, Version: 1,
			}, nil
		}
		if current.ID != nodeRunID {
			return NodeRun{}, ErrVersionConflict
		}
		if roundHasAttempt {
			return NodeRun{}, fmt.Errorf(
				"%w: the current round of node %q already has an attempt", ErrRetryNotAllowed, nodeID)
		}
		canAttach := false
		for _, state := range attachable {
			if current.State == state {
				canAttach = true
				break
			}
		}
		if !canAttach {
			return NodeRun{}, fmt.Errorf(
				"%w: the current round of node %q is %s", ErrPolicyBlocked, nodeID, current.State)
		}
		attached := current
		attached.State = attachState
		attached.InputDigest = inputDigest
		attached.Version++
		return attached, nil
	case RoundRevision, RoundRerun:
		if !found {
			return NodeRun{}, fmt.Errorf(
				"%w: node %q has no settled round to supersede", ErrPolicyBlocked, nodeID)
		}
		if current.ID != round.SupersedesNodeRunID {
			return NodeRun{}, ErrVersionConflict
		}
		if current.State != NodeDecided && current.State != NodeExecutionFailed {
			return NodeRun{}, fmt.Errorf(
				"%w: the current round of node %q is %s", ErrRetryNotAllowed, nodeID, current.State)
		}
		if roundHasUnsettledAttempt {
			return NodeRun{}, fmt.Errorf(
				"%w: the current round of node %q still has an unsettled attempt", ErrRetryNotAllowed, nodeID)
		}
		iteration := current.Iteration + 1
		if round.MaxRounds < iteration {
			return NodeRun{}, fmt.Errorf(
				"%w: node %q reached its round bound %d", ErrPolicyBlocked, nodeID, round.MaxRounds)
		}
		return NodeRun{
			ID: nodeRunID, RunID: runID, NodeID: nodeID, Iteration: iteration,
			State: newRoundState, InputDigest: inputDigest, Version: 1,
		}, nil
	default:
		return NodeRun{}, ErrInvalidInput
	}
}

func parseRoundShape(round DispatchRound, _ NodeState) (DispatchRoundKind, error) {
	switch round.Kind {
	case RoundInitial:
		if round.SupersedesNodeRunID != "" {
			return "", ErrInvalidInput
		}
	case RoundRevision, RoundRerun:
		if round.SupersedesNodeRunID == "" {
			return "", ErrInvalidInput
		}
	default:
		return "", ErrInvalidInput
	}
	if round.MaxRounds < 1 || round.MaxRounds > maxDispatchRounds {
		return "", ErrInvalidInput
	}
	return round.Kind, nil
}
