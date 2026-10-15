package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workflow"
)

// DispatchNode materialises one node round and its first attempt. The run lock,
// the round rules and the frozen plan check all run inside one transaction, so
// two dispatch attempts for one round can never both win, and a legal replay of
// an existing attempt is answered from the frozen facts without a new
// execution.
func (s *Store) DispatchNode(
	ctx context.Context,
	command execution.DispatchNodeCommand,
	now time.Time,
) (execution.DispatchOutcome, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.DispatchOutcome{}, fmt.Errorf("begin dispatch transaction: %w", err)
	}
	defer rollback(transaction)
	if existing, nodeRun, found, err := readDispatchedAttempt(ctx, transaction, command.TaskID); err != nil {
		return execution.DispatchOutcome{}, err
	} else if found {
		if !sameDispatchedAttempt(existing, command) {
			return execution.DispatchOutcome{}, execution.ErrResultConflict
		}
		return execution.DispatchOutcome{NodeRun: nodeRun, Task: existing, Duplicate: true}, nil
	}
	run, err := lockRun(ctx, transaction, command.RunID)
	if err != nil {
		return execution.DispatchOutcome{}, err
	}
	if run.State != execution.RunActive {
		return execution.DispatchOutcome{}, fmt.Errorf(
			"%w: a %s run does not dispatch new rounds", execution.ErrPolicyBlocked, run.State)
	}
	if run.Version != command.ExpectedRunVersion {
		return execution.DispatchOutcome{}, execution.ErrVersionConflict
	}
	plan, err := readRunPlan(ctx, transaction, run.DefinitionName, run.DefinitionVersion)
	if err != nil {
		return execution.DispatchOutcome{}, err
	}
	node, known := plan.Nodes[command.NodeID]
	if !known || node.Type != workflow.NodeCapability {
		return execution.DispatchOutcome{}, fmt.Errorf(
			"%w: node %q is not a capability node of the frozen plan", execution.ErrInvalidInput, command.NodeID)
	}
	if node.Capability.ID != command.Capability.ID || node.Capability.Version != command.Capability.Version {
		return execution.DispatchOutcome{}, fmt.Errorf(
			"%w: node %q is frozen with capability %s@%d", execution.ErrInvalidInput,
			command.NodeID, node.Capability.ID, node.Capability.Version)
	}
	if !now.Before(command.DeadlineAt.UTC()) {
		return execution.DispatchOutcome{}, fmt.Errorf(
			"%w: the attempt deadline is not in the future", execution.ErrInvalidInput)
	}
	round, err := materializeRounded(ctx, transaction, run.ID, command, now)
	if err != nil {
		return execution.DispatchOutcome{}, err
	}
	encodedScopes, err := json.Marshal(command.GitScopes)
	if err != nil {
		return execution.DispatchOutcome{}, fmt.Errorf("encode dispatch git scopes: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into execution_task (
			task_id, node_run_id, attempt, attempt_kind, capability_id,
			capability_version, provider_id, provider_version, git_scopes,
			state, input_digest, deadline_at, created_at, updated_at
		) values ($1, $2, 1, 'initial', $3, $4, $5, $6, $7, 'QUEUED', $8, $9, $10, $10)
	`, command.TaskID, round.ID, command.Capability.ID, command.Capability.Version,
		command.Provider.ID, command.Provider.Version, encodedScopes,
		command.InputDigest, command.DeadlineAt.UTC(), now); err != nil {
		return execution.DispatchOutcome{}, fmt.Errorf("insert dispatched attempt: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.DispatchOutcome{}, fmt.Errorf("commit dispatch transaction: %w", err)
	}
	return execution.DispatchOutcome{
		NodeRun: round,
		Task: execution.Task{
			ID: command.TaskID, WorkflowRunID: run.ID, NodeRunID: round.ID, Attempt: 1,
			AttemptKind: execution.AttemptInitial, Capability: command.Capability,
			Provider: command.Provider, GitScopes: slices.Clone(command.GitScopes),
			State: execution.TaskQueued, InputDigest: command.InputDigest,
			DeadlineAt: command.DeadlineAt.UTC(),
		},
	}, nil
}

// materializeRounded applies the shared round rules of the execution module
// inside the dispatch transaction.
func materializeRounded(
	ctx context.Context,
	transaction *sql.Tx,
	runID string,
	command execution.DispatchNodeCommand,
	now time.Time,
) (execution.NodeRun, error) {
	current, found, err := lockCurrentRound(ctx, transaction, runID, command.NodeID)
	if err != nil {
		return execution.NodeRun{}, err
	}
	if taken, err := nodeRunExists(ctx, transaction, command.NodeRunID); err != nil {
		return execution.NodeRun{}, err
	} else if taken && (!found || current.ID != command.NodeRunID) {
		return execution.NodeRun{}, execution.ErrVersionConflict
	}
	hasTask, unsettled := false, false
	if found {
		if hasTask, err = roundHasTask(ctx, transaction, current.ID); err != nil {
			return execution.NodeRun{}, err
		}
		if unsettled, err = roundHasUnsettledTask(ctx, transaction, current.ID); err != nil {
			return execution.NodeRun{}, err
		}
	}
	round, err := execution.PlannedRound(
		current, found, hasTask, unsettled, command.Round, runID, command.NodeID,
		command.NodeRunID, command.InputDigest, execution.NodeReady, execution.NodeReady,
		[]execution.NodeState{execution.NodePending, execution.NodeReady},
	)
	if err != nil {
		return execution.NodeRun{}, err
	}
	if found && round.ID == current.ID {
		if _, err := transaction.ExecContext(ctx, `
			update node_run set state = $2, input_digest = $3, version = version + 1, updated_at = $4
			where node_run_id = $1
		`, round.ID, round.State, round.InputDigest, now); err != nil {
			return execution.NodeRun{}, fmt.Errorf("bind the admitted round: %w", err)
		}
		return round, nil
	}
	_ = now
	return insertDispatchedRound(ctx, transaction, round, now)
}

// DecideNode records one control-plane node decision. A decision never decides
// a capability round and never completes a run: the delivery closure
// transaction stays the only way to a successful run.
func (s *Store) DecideNode(
	ctx context.Context,
	command execution.DecideNodeCommand,
	now time.Time,
) (execution.DecideOutcome, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.DecideOutcome{}, fmt.Errorf("begin decision transaction: %w", err)
	}
	defer rollback(transaction)
	run, err := lockRun(ctx, transaction, command.RunID)
	if err != nil {
		return execution.DecideOutcome{}, err
	}
	if run.State != execution.RunActive {
		return execution.DecideOutcome{}, fmt.Errorf(
			"%w: a %s run does not decide new rounds", execution.ErrPolicyBlocked, run.State)
	}
	if run.Version != command.ExpectedRunVersion {
		return execution.DecideOutcome{}, execution.ErrVersionConflict
	}
	plan, err := readRunPlan(ctx, transaction, run.DefinitionName, run.DefinitionVersion)
	if err != nil {
		return execution.DecideOutcome{}, err
	}
	node, known := plan.Nodes[command.NodeID]
	if !known {
		return execution.DecideOutcome{}, fmt.Errorf(
			"%w: node %q is not in the frozen plan", execution.ErrInvalidInput, command.NodeID)
	}
	if node.Type == workflow.NodeCapability {
		return execution.DecideOutcome{}, fmt.Errorf(
			"%w: a capability round is decided by result acceptance", execution.ErrInvalidInput)
	}
	current, found, err := lockCurrentRound(ctx, transaction, command.RunID, command.NodeID)
	if err != nil {
		return execution.DecideOutcome{}, err
	}
	if taken, err := nodeRunExists(ctx, transaction, command.NodeRunID); err != nil {
		return execution.DecideOutcome{}, err
	} else if taken && (!found || current.ID != command.NodeRunID) {
		return execution.DecideOutcome{}, execution.ErrVersionConflict
	}
	if found && current.State == execution.NodeDecided && current.ID == command.NodeRunID {
		if current.Verdict == command.Verdict && sameNodeOutput(current.Output, command.Output) {
			return execution.DecideOutcome{NodeRun: current, Duplicate: true}, nil
		}
		return execution.DecideOutcome{}, execution.ErrResultConflict
	}
	planned, err := execution.PlannedRound(
		current, found, false, false, command.Round, command.RunID, command.NodeID,
		command.NodeRunID, command.InputDigest, execution.NodePending, execution.NodePending,
		[]execution.NodeState{execution.NodePending, execution.NodeReady, execution.NodeWaitingHuman},
	)
	if err != nil {
		return execution.DecideOutcome{}, err
	}
	if found && planned.ID == current.ID && current.State == execution.NodeDecided {
		return execution.DecideOutcome{}, execution.ErrResultConflict
	}
	created := !found || planned.ID != current.ID
	current = planned
	var encodedOutput any
	if command.Output != nil {
		encoded, marshalErr := json.Marshal(command.Output)
		if marshalErr != nil {
			return execution.DecideOutcome{}, fmt.Errorf("encode node output: %w", marshalErr)
		}
		encodedOutput = encoded
	} else if current.Output != nil {
		encodedOutput = current.Output
	}
	if created {
		if _, err := transaction.ExecContext(ctx, `
			insert into node_run (
				node_run_id, workflow_run_id, node_id, iteration, state, verdict,
				input_digest, output, version, created_at, updated_at
			) values ($1, $2, $3, $4, $5, nullif($6, ''), $7, $8, 1, $9, $9)
		`, current.ID, command.RunID, command.NodeID, current.Iteration, command.State, command.Verdict,
			command.InputDigest, encodedOutput, now); err != nil {
			return execution.DecideOutcome{}, fmt.Errorf("insert control-plane round: %w", err)
		}
	} else if _, err := transaction.ExecContext(ctx, `
		update node_run
		set state = $2, verdict = nullif($3, ''), output = $4, version = version + 1, updated_at = $5
		where node_run_id = $1
	`, current.ID, command.State, command.Verdict, encodedOutput, now); err != nil {
		return execution.DecideOutcome{}, fmt.Errorf("apply node decision: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.DecideOutcome{}, fmt.Errorf("commit decision transaction: %w", err)
	}
	applied := current
	applied.State = command.State
	applied.Verdict = command.Verdict
	applied.Version++
	if command.Output != nil {
		applied.Output = command.Output
	}
	return execution.DecideOutcome{NodeRun: applied}, nil
}

func lockRun(ctx context.Context, transaction *sql.Tx, runID string) (execution.Run, error) {
	var run execution.Run
	var gitExecutionUser sql.NullString
	err := transaction.QueryRowContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, started_by_github_user_id,
			git_execution_user_id, definition_name, definition_version, definition_digest,
			state, version, created_at, updated_at
		from workflow_run where workflow_run_id = $1 for update
	`, runID).Scan(&run.ID, &run.WorkspaceID, &run.WorkItemID, &run.StartedByGitHubUserID,
		&gitExecutionUser, &run.DefinitionName, &run.DefinitionVersion, &run.DefinitionDigest,
		&run.State, &run.Version, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.Run{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.Run{}, fmt.Errorf("lock workflow run: %w", err)
	}
	run.GitExecutionUserID = gitExecutionUser.String
	return run, nil
}

func readRunPlan(
	ctx context.Context,
	transaction *sql.Tx,
	name string,
	version int,
) (workflow.CompiledPlan, error) {
	var encoded []byte
	err := transaction.QueryRowContext(ctx, `
		select compiled_plan from workflow_definition where name = $1 and version = $2
	`, name, version).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.CompiledPlan{}, fmt.Errorf(
			"%w: the run's frozen compiled plan is not published", execution.ErrNotFound)
	}
	if err != nil {
		return workflow.CompiledPlan{}, fmt.Errorf("query compiled workflow plan: %w", err)
	}
	var plan workflow.CompiledPlan
	if err := json.Unmarshal(encoded, &plan); err != nil {
		return workflow.CompiledPlan{}, fmt.Errorf("decode compiled workflow plan: %w", err)
	}
	return plan, nil
}

func lockCurrentRound(
	ctx context.Context,
	transaction *sql.Tx,
	runID, nodeID string,
) (execution.NodeRun, bool, error) {
	var round execution.NodeRun
	var verdict sql.NullString
	var output []byte
	err := transaction.QueryRowContext(ctx, `
		select node_run_id, iteration, state, verdict, input_digest, output, version
		from node_run
		where workflow_run_id = $1 and node_id = $2
		order by iteration desc
		limit 1
		for update
	`, runID, nodeID).Scan(&round.ID, &round.Iteration, &round.State, &verdict,
		&round.InputDigest, &output, &round.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.NodeRun{}, false, nil
	}
	if err != nil {
		return execution.NodeRun{}, false, fmt.Errorf("lock current node round: %w", err)
	}
	round.RunID = runID
	round.NodeID = nodeID
	round.Verdict = verdict.String
	if len(output) > 0 {
		if err := json.Unmarshal(output, &round.Output); err != nil {
			return execution.NodeRun{}, false, fmt.Errorf("decode node output: %w", err)
		}
	}
	return round, true, nil
}

func nodeRunExists(ctx context.Context, transaction *sql.Tx, nodeRunID string) (bool, error) {
	var exists bool
	if err := transaction.QueryRowContext(ctx, `
		select exists(select 1 from node_run where node_run_id = $1)
	`, nodeRunID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check node round identity: %w", err)
	}
	return exists, nil
}

func roundHasTask(ctx context.Context, transaction *sql.Tx, nodeRunID string) (bool, error) {
	var exists bool
	if err := transaction.QueryRowContext(ctx, `
		select exists(select 1 from execution_task where node_run_id = $1)
	`, nodeRunID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check round attempts: %w", err)
	}
	return exists, nil
}

func roundHasUnsettledTask(ctx context.Context, transaction *sql.Tx, nodeRunID string) (bool, error) {
	var exists bool
	if err := transaction.QueryRowContext(ctx, `
		select exists(
			select 1 from execution_task
			where node_run_id = $1 and state not in ('COMPLETED', 'FAILED', 'EXPIRED', 'CANCELED')
		)
	`, nodeRunID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check unsettled attempts: %w", err)
	}
	return exists, nil
}

func insertDispatchedRound(
	ctx context.Context,
	transaction *sql.Tx,
	round execution.NodeRun,
	now time.Time,
) (execution.NodeRun, error) {
	if _, err := transaction.ExecContext(ctx, `
		insert into node_run (
			node_run_id, workflow_run_id, node_id, iteration, state,
			input_digest, version, created_at, updated_at
		) values ($1, $2, $3, $4, 'READY', $5, 1, $6, $6)
	`, round.ID, round.RunID, round.NodeID, round.Iteration, round.InputDigest, now); err != nil {
		return execution.NodeRun{}, fmt.Errorf("insert dispatched round: %w", err)
	}
	return round, nil
}

// readDispatchedAttempt reads the frozen attempt a replay must be answered
// from, together with its round.
func readDispatchedAttempt(
	ctx context.Context,
	transaction *sql.Tx,
	taskID string,
) (execution.Task, execution.NodeRun, bool, error) {
	var task execution.Task
	var round execution.NodeRun
	var encodedScopes []byte
	var verdict sql.NullString
	err := transaction.QueryRowContext(ctx, `
		select t.task_id, t.node_run_id, t.attempt, t.attempt_kind, t.capability_id,
			t.capability_version, t.provider_id, t.provider_version, t.git_scopes,
			t.state, t.input_digest, t.deadline_at,
			n.workflow_run_id, n.node_id, n.iteration, n.state, n.verdict, n.input_digest, n.version
		from execution_task t
		join node_run n on n.node_run_id = t.node_run_id
		where t.task_id = $1
	`, taskID).Scan(&task.ID, &task.NodeRunID, &task.Attempt, &task.AttemptKind,
		&task.Capability.ID, &task.Capability.Version, &task.Provider.ID, &task.Provider.Version,
		&encodedScopes, &task.State, &task.InputDigest, &task.DeadlineAt,
		&round.RunID, &round.NodeID, &round.Iteration, &round.State, &verdict,
		&round.InputDigest, &round.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.Task{}, execution.NodeRun{}, false, nil
	}
	if err != nil {
		return execution.Task{}, execution.NodeRun{}, false, fmt.Errorf("read dispatched attempt: %w", err)
	}
	if err := json.Unmarshal(encodedScopes, &task.GitScopes); err != nil {
		return execution.Task{}, execution.NodeRun{}, false, fmt.Errorf("decode attempt git scopes: %w", err)
	}
	round.ID = task.NodeRunID
	round.Verdict = verdict.String
	return task, round, true, nil
}

func sameDispatchedAttempt(task execution.Task, command execution.DispatchNodeCommand) bool {
	return task.NodeRunID == command.NodeRunID && task.InputDigest == command.InputDigest &&
		task.Capability == command.Capability && task.Provider == command.Provider &&
		slices.Equal(task.GitScopes, command.GitScopes) &&
		execution.SameInstant(task.DeadlineAt, command.DeadlineAt) &&
		task.Attempt == 1 && task.AttemptKind == execution.AttemptInitial
}

func sameNodeOutput(left, right map[string]any) bool {
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

// OpenNodeRound materialises one node round without an attempt inside its own
// transaction, so a later dispatch attaches the attempt to the same round.
func (s *Store) OpenNodeRound(
	ctx context.Context,
	command execution.OpenRoundCommand,
	now time.Time,
) (execution.NodeRun, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("begin open-round transaction: %w", err)
	}
	defer rollback(transaction)
	run, err := lockRun(ctx, transaction, command.RunID)
	if err != nil {
		return execution.NodeRun{}, err
	}
	if run.State != execution.RunActive {
		return execution.NodeRun{}, fmt.Errorf(
			"%w: a %s run does not open new rounds", execution.ErrPolicyBlocked, run.State)
	}
	if run.Version != command.ExpectedRunVersion {
		return execution.NodeRun{}, execution.ErrVersionConflict
	}
	plan, err := readRunPlan(ctx, transaction, run.DefinitionName, run.DefinitionVersion)
	if err != nil {
		return execution.NodeRun{}, err
	}
	node, known := plan.Nodes[command.NodeID]
	if !known {
		return execution.NodeRun{}, fmt.Errorf(
			"%w: node %q is not in the frozen plan", execution.ErrInvalidInput, command.NodeID)
	}
	if node.Type != workflow.NodeCapability {
		return execution.NodeRun{}, fmt.Errorf(
			"%w: a control-plane round is recorded by DecideNode", execution.ErrInvalidInput)
	}
	current, found, err := lockCurrentRound(ctx, transaction, run.ID, command.NodeID)
	if err != nil {
		return execution.NodeRun{}, err
	}
	if found && current.ID == command.NodeRunID {
		if current.InputDigest != command.InputDigest {
			return execution.NodeRun{}, execution.ErrVersionConflict
		}
		return current, nil
	}
	if taken, err := nodeRunExists(ctx, transaction, command.NodeRunID); err != nil {
		return execution.NodeRun{}, err
	} else if taken {
		return execution.NodeRun{}, execution.ErrVersionConflict
	}
	hasTask, unsettled := false, false
	if found {
		if hasTask, err = roundHasTask(ctx, transaction, current.ID); err != nil {
			return execution.NodeRun{}, err
		}
		if unsettled, err = roundHasUnsettledTask(ctx, transaction, current.ID); err != nil {
			return execution.NodeRun{}, err
		}
	}
	round, err := execution.PlannedRound(
		current, found, hasTask, unsettled, command.Round, run.ID, command.NodeID,
		command.NodeRunID, command.InputDigest, execution.NodeReady, execution.NodeReady,
		[]execution.NodeState{execution.NodePending, execution.NodeReady},
	)
	if err != nil {
		return execution.NodeRun{}, err
	}
	if found && round.ID == current.ID {
		round, err = insertOrUpdateRound(ctx, transaction, round, now, false)
	} else {
		round, err = insertOrUpdateRound(ctx, transaction, round, now, true)
	}
	if err != nil {
		return execution.NodeRun{}, err
	}
	if err := transaction.Commit(); err != nil {
		return execution.NodeRun{}, fmt.Errorf("commit open-round transaction: %w", err)
	}
	return round, nil
}

func insertOrUpdateRound(
	ctx context.Context,
	transaction *sql.Tx,
	round execution.NodeRun,
	now time.Time,
	created bool,
) (execution.NodeRun, error) {
	if created {
		return insertDispatchedRound(ctx, transaction, round, now)
	}
	if _, err := transaction.ExecContext(ctx, `
		update node_run set state = $2, input_digest = $3, version = version + 1, updated_at = $4
		where node_run_id = $1
	`, round.ID, round.State, round.InputDigest, now); err != nil {
		return execution.NodeRun{}, fmt.Errorf("update node round: %w", err)
	}
	return round, nil
}

// RunAttempts lists the attempts of every round of one run, oldest first.
func (s *Store) RunAttempts(ctx context.Context, runID string) ([]execution.NodeAttemptRef, error) {
	rows, err := s.database.QueryContext(ctx, `
		select t.task_id, t.node_run_id, n.node_id, t.attempt, t.attempt_kind, t.state,
			t.input_digest, coalesce(t.result_digest, ''), coalesce(t.runtime_id, ''),
			coalesce(t.runtime_epoch, ''), t.fencing_token, coalesce(t.lease_id, '')
		from execution_task t
		join node_run n on n.node_run_id = t.node_run_id
		where n.workflow_run_id = $1
		order by n.node_id, n.iteration, t.attempt
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("read run attempts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	attempts := []execution.NodeAttemptRef{}
	for rows.Next() {
		var attempt execution.NodeAttemptRef
		if err := rows.Scan(&attempt.TaskID, &attempt.NodeRunID, &attempt.NodeID, &attempt.Attempt,
			&attempt.AttemptKind, &attempt.State, &attempt.InputDigest, &attempt.ResultDigest,
			&attempt.RuntimeID, &attempt.RuntimeEpoch, &attempt.FencingToken, &attempt.LeaseID); err != nil {
			return nil, fmt.Errorf("scan run attempt: %w", err)
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run attempts: %w", err)
	}
	return attempts, nil
}

// RunRounds lists every persisted node round of one run, oldest first.
func (s *Store) RunRounds(ctx context.Context, runID string) ([]execution.NodeRoundRef, error) {
	rows, err := s.database.QueryContext(ctx, `
		select node_run_id, node_id, iteration, state, coalesce(verdict, ''), input_digest, output
		from node_run
		where workflow_run_id = $1
		order by node_id, iteration
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("read run rounds: %w", err)
	}
	defer func() { _ = rows.Close() }()
	rounds := []execution.NodeRoundRef{}
	for rows.Next() {
		var round execution.NodeRoundRef
		var output []byte
		if err := rows.Scan(&round.NodeRunID, &round.NodeID, &round.Iteration, &round.State,
			&round.Verdict, &round.InputDigest, &output); err != nil {
			return nil, fmt.Errorf("scan run round: %w", err)
		}
		if len(output) > 0 {
			if err := json.Unmarshal(output, &round.Output); err != nil {
				return nil, fmt.Errorf("decode round output: %w", err)
			}
		}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run rounds: %w", err)
	}
	return rounds, nil
}
