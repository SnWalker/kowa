package execution

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/SnWalker/kowa/internal/workflow"
)

// PublishPlan registers one immutable compiled plan in the in-memory adapter so
// the dispatch port can resolve a run's frozen node responsibilities. The
// PostgreSQL adapter reads the same facts from the workflow_definition table.
func (s *MemoryStore) PublishPlan(_ context.Context, plan workflow.CompiledPlan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.plans == nil {
		s.plans = map[string]workflow.CompiledPlan{}
	}
	key := planKey(plan.Name, plan.Version)
	if existing, found := s.plans[key]; found {
		if existing.Digest != plan.Digest {
			return workflow.ErrDefinitionConflict
		}
		return nil
	}
	s.plans[key] = plan
	return nil
}

func planKey(name string, version int) string { return fmt.Sprintf("%s\x00%d", name, version) }

// DispatchNode materialises one node round and its first attempt. A replay of
// the same identity with the same content is answered with the frozen facts and
// never creates a second execution.
func (s *MemoryStore) DispatchNode(
	_ context.Context,
	command DispatchNodeCommand,
	now time.Time,
) (DispatchOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, found := s.tasks[command.TaskID]; found {
		if !sameDispatchContent(existing, command) {
			return DispatchOutcome{}, ErrResultConflict
		}
		return DispatchOutcome{NodeRun: s.nodes[existing.NodeRunID], Task: existing, Duplicate: true}, nil
	}
	run, exists := s.runs[command.RunID]
	if !exists {
		return DispatchOutcome{}, ErrNotFound
	}
	if run.State != RunActive {
		return DispatchOutcome{}, fmt.Errorf("%w: a %s run does not dispatch new rounds", ErrPolicyBlocked, run.State)
	}
	if run.Version != command.ExpectedRunVersion {
		return DispatchOutcome{}, ErrVersionConflict
	}
	plan, found := s.plans[planKey(run.DefinitionName, run.DefinitionVersion)]
	if !found {
		return DispatchOutcome{}, fmt.Errorf(
			"%w: the run's frozen compiled plan is not published", ErrNotFound)
	}
	node, known := plan.Nodes[command.NodeID]
	if !known || node.Type != workflow.NodeCapability {
		return DispatchOutcome{}, fmt.Errorf(
			"%w: node %q is not a capability node of the frozen plan", ErrInvalidInput, command.NodeID)
	}
	if node.Capability.ID != command.Capability.ID || node.Capability.Version != command.Capability.Version {
		return DispatchOutcome{}, fmt.Errorf(
			"%w: node %q is frozen with capability %s@%d", ErrInvalidInput,
			command.NodeID, node.Capability.ID, node.Capability.Version)
	}
	// The deadline is checked only for a new materialisation: a legal replay of
	// an existing attempt must keep working after its deadline passed.
	if !now.Before(command.DeadlineAt.UTC()) {
		return DispatchOutcome{}, fmt.Errorf("%w: the attempt deadline is not in the future", ErrInvalidInput)
	}

	round, err := s.materializeRound(run, command, now)
	if err != nil {
		return DispatchOutcome{}, err
	}
	task := Task{
		ID: command.TaskID, WorkflowRunID: run.ID, NodeRunID: round.ID, Attempt: 1,
		AttemptKind: AttemptInitial, Capability: command.Capability, Provider: command.Provider,
		GitScopes: slices.Clone(command.GitScopes), State: TaskQueued,
		InputDigest: command.InputDigest, DeadlineAt: command.DeadlineAt.UTC(),
	}
	s.nodes[round.ID] = cloneNodeRun(round)
	s.tasks[task.ID] = task
	return DispatchOutcome{NodeRun: cloneNodeRun(round), Task: task}, nil
}

// materializeRound applies the shared round rules of this package.
func (s *MemoryStore) materializeRound(
	run Run,
	command DispatchNodeCommand,
	now time.Time,
) (NodeRun, error) {
	current, found := s.currentRound(run.ID, command.NodeID)
	if taken, exists := s.nodes[command.NodeRunID]; exists && (!found || taken.ID != current.ID) {
		// The identity already belongs to another round.
		return NodeRun{}, ErrVersionConflict
	}
	round, err := PlannedRound(
		current, found,
		found && s.roundHasTask(current.ID), found && s.hasUnsettledTask(current.ID),
		command.Round, run.ID, command.NodeID, command.NodeRunID, command.InputDigest,
		NodeReady, NodeReady, []NodeState{NodePending, NodeReady},
	)
	if err != nil {
		return NodeRun{}, err
	}
	_ = now
	return round, nil
}

// OpenNodeRound materialises one round without an attempt. A replay of the same
// identity is idempotent; a different identity for the same round conflicts.
func (s *MemoryStore) OpenNodeRound(
	_ context.Context,
	command OpenRoundCommand,
	now time.Time,
) (NodeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[command.RunID]
	if !exists {
		return NodeRun{}, ErrNotFound
	}
	if run.State != RunActive {
		return NodeRun{}, fmt.Errorf("%w: a %s run does not open new rounds", ErrPolicyBlocked, run.State)
	}
	if run.Version != command.ExpectedRunVersion {
		return NodeRun{}, ErrVersionConflict
	}
	if current, found := s.currentRound(run.ID, command.NodeID); found && current.ID == command.NodeRunID {
		if current.InputDigest != command.InputDigest {
			return NodeRun{}, ErrVersionConflict
		}
		return cloneNodeRun(current), nil
	}
	if taken, exists := s.nodes[command.NodeRunID]; exists && taken.RunID != run.ID {
		return NodeRun{}, ErrVersionConflict
	}
	if _, exists := s.nodes[command.NodeRunID]; exists {
		return NodeRun{}, ErrVersionConflict
	}
	current, found := s.currentRound(run.ID, command.NodeID)
	round, err := PlannedRound(
		current, found, found && s.roundHasTask(current.ID), found && s.hasUnsettledTask(current.ID),
		command.Round, run.ID, command.NodeID, command.NodeRunID, command.InputDigest,
		NodeReady, NodeReady, []NodeState{NodePending, NodeReady},
	)
	if err != nil {
		return NodeRun{}, err
	}
	_ = now
	s.nodes[round.ID] = cloneNodeRun(round)
	return cloneNodeRun(round), nil
}

// RunRounds lists every persisted node round of one run, oldest first.
func (s *MemoryStore) RunRounds(_ context.Context, runID string) ([]NodeRoundRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rounds := []NodeRoundRef{}
	for _, node := range s.nodes {
		if node.RunID != runID {
			continue
		}
		rounds = append(rounds, NodeRoundRef{
			NodeRunID: node.ID, NodeID: node.NodeID, Iteration: node.Iteration,
			State: node.State, Verdict: node.Verdict, InputDigest: node.InputDigest,
			Output: maps.Clone(node.Output),
		})
	}
	slices.SortFunc(rounds, func(left, right NodeRoundRef) int {
		return cmp.Or(cmp.Compare(left.NodeID, right.NodeID), cmp.Compare(left.Iteration, right.Iteration))
	})
	return rounds, nil
}

// RunAttempts lists the attempts of every round of one run, oldest first.
func (s *MemoryStore) RunAttempts(_ context.Context, runID string) ([]NodeAttemptRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts := []NodeAttemptRef{}
	for _, task := range s.tasks {
		node, found := s.nodes[task.NodeRunID]
		if !found || node.RunID != runID {
			continue
		}
		attempts = append(attempts, NodeAttemptRef{
			TaskID: task.ID, NodeRunID: task.NodeRunID, NodeID: node.NodeID,
			Attempt: task.Attempt, AttemptKind: task.AttemptKind, State: task.State,
			InputDigest: task.InputDigest, ResultDigest: task.ResultDigest,
			RuntimeID: task.RuntimeID, RuntimeEpoch: task.RuntimeEpoch,
			FencingToken: task.FencingToken, LeaseID: task.LeaseID,
		})
	}
	slices.SortFunc(attempts, func(left, right NodeAttemptRef) int {
		if left.NodeRunID != right.NodeRunID {
			return cmp.Compare(left.NodeRunID, right.NodeRunID)
		}
		return cmp.Compare(left.Attempt, right.Attempt)
	})
	return attempts, nil
}

// DecideNode records one control-plane node decision.
func (s *MemoryStore) DecideNode(
	_ context.Context,
	command DecideNodeCommand,
	now time.Time,
) (DecideOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[command.RunID]
	if !exists {
		return DecideOutcome{}, ErrNotFound
	}
	if run.State != RunActive {
		return DecideOutcome{}, fmt.Errorf("%w: a %s run does not decide new rounds", ErrPolicyBlocked, run.State)
	}
	if run.Version != command.ExpectedRunVersion {
		return DecideOutcome{}, ErrVersionConflict
	}
	plan, found := s.plans[planKey(run.DefinitionName, run.DefinitionVersion)]
	if !found {
		return DecideOutcome{}, fmt.Errorf("%w: the run's frozen compiled plan is not published", ErrNotFound)
	}
	node, known := plan.Nodes[command.NodeID]
	if !known {
		return DecideOutcome{}, fmt.Errorf("%w: node %q is not in the frozen plan", ErrInvalidInput, command.NodeID)
	}
	if node.Type == workflow.NodeCapability {
		return DecideOutcome{}, fmt.Errorf(
			"%w: a capability round is decided by result acceptance", ErrInvalidInput)
	}
	current, found := s.currentRound(run.ID, command.NodeID)
	if taken, exists := s.nodes[command.NodeRunID]; exists && (!found || taken.ID != current.ID) {
		return DecideOutcome{}, ErrVersionConflict
	}
	if found && current.State == NodeDecided && current.ID == command.NodeRunID {
		if current.Verdict == command.Verdict && sameOutput(current.Output, command.Output) {
			return DecideOutcome{NodeRun: cloneNodeRun(current), Duplicate: true}, nil
		}
		return DecideOutcome{}, ErrResultConflict
	}
	round, err := PlannedRound(
		current, found, false, false, command.Round, run.ID, command.NodeID,
		command.NodeRunID, command.InputDigest, NodePending, NodePending,
		[]NodeState{NodePending, NodeReady, NodeWaitingHuman},
	)
	if err != nil {
		return DecideOutcome{}, err
	}
	if found && round.ID == current.ID && current.State == NodeDecided {
		return DecideOutcome{}, ErrResultConflict
	}
	switch command.State {
	case NodeWaitingHuman:
		if round.State != NodeReady && round.State != NodePending {
			return DecideOutcome{}, fmt.Errorf(
				"%w: a %s round cannot wait for a human", ErrPolicyBlocked, round.State)
		}
	case NodeDecided:
		if round.State != NodeReady && round.State != NodePending && round.State != NodeWaitingHuman {
			return DecideOutcome{}, fmt.Errorf("%w: a %s round cannot be decided", ErrPolicyBlocked, round.State)
		}
	default:
		return DecideOutcome{}, ErrInvalidInput
	}
	round.State = command.State
	round.Verdict = command.Verdict
	if command.Output != nil {
		round.Output = command.Output
	}
	round.Version++
	s.nodes[round.ID] = round
	return DecideOutcome{NodeRun: cloneNodeRun(round)}, nil
}

func (s *MemoryStore) currentRound(runID, nodeID string) (NodeRun, bool) {
	var current NodeRun
	found := false
	for _, node := range s.nodes {
		if node.RunID != runID || node.NodeID != nodeID {
			continue
		}
		if !found || node.Iteration > current.Iteration {
			current, found = node, true
		}
	}
	return current, found
}

func (s *MemoryStore) roundHasTask(nodeRunID string) bool {
	for _, task := range s.tasks {
		if task.NodeRunID == nodeRunID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) hasUnsettledTask(nodeRunID string) bool {
	for _, task := range s.tasks {
		if task.NodeRunID != nodeRunID {
			continue
		}
		switch task.State {
		case TaskCompleted, TaskFailed, TaskExpired, TaskCanceled:
		default:
			return true
		}
	}
	return false
}

// sameDispatchContent compares the frozen attempt identity, including the
// deadline the round was materialised with.
func sameDispatchContent(task Task, command DispatchNodeCommand) bool {
	return task.NodeRunID == command.NodeRunID && task.InputDigest == command.InputDigest &&
		task.Capability == command.Capability && task.Provider == command.Provider &&
		slices.Equal(task.GitScopes, command.GitScopes) && SameInstant(task.DeadlineAt, command.DeadlineAt) &&
		task.Attempt == 1 && task.AttemptKind == AttemptInitial
}

func sameOutput(left, right map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, found := right[key]
		if !found || fmt.Sprint(other) != fmt.Sprint(value) {
			return false
		}
	}
	return true
}
