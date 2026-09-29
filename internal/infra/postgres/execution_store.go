package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workflow"
)

func (s *Store) PublishPlan(ctx context.Context, plan workflow.CompiledPlan) error {
	definition, err := json.Marshal(plan.Definition)
	if err != nil {
		return fmt.Errorf("encode workflow definition: %w", err)
	}
	compiled, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode compiled workflow plan: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workflow publication transaction: %w", err)
	}
	defer rollback(transaction)
	result, err := transaction.ExecContext(ctx, `
		insert into workflow_definition (
			name, version, digest, definition, compiled_plan, created_at
		) values ($1, $2, $3, $4, $5, $6)
		on conflict (name, version) do nothing
	`, plan.Name, plan.Version, plan.Digest, definition, compiled, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert workflow definition: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read workflow publication count: %w", err)
	}
	if rows == 0 {
		var storedDigest string
		if err := transaction.QueryRowContext(ctx, `
			select digest from workflow_definition where name = $1 and version = $2
		`, plan.Name, plan.Version).Scan(&storedDigest); err != nil {
			return fmt.Errorf("query published workflow definition: %w", err)
		}
		if storedDigest != plan.Digest {
			return workflow.ErrDefinitionConflict
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit workflow publication transaction: %w", err)
	}
	return nil
}

func (s *Store) LoadPlan(ctx context.Context, name string, version int) (workflow.CompiledPlan, error) {
	var encoded []byte
	err := s.database.QueryRowContext(ctx, `
		select compiled_plan from workflow_definition where name = $1 and version = $2
	`, name, version).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.CompiledPlan{}, workflow.ErrDefinitionNotFound
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

func (s *Store) RegisterRuntime(ctx context.Context, registration execution.RuntimeRegistration) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime registration transaction: %w", err)
	}
	defer rollback(transaction)

	var existingEpoch string
	var existingGitHubUserID string
	var existingProviderID sql.NullString
	var existingProviderVersion sql.NullString
	var existingAuthenticated bool
	err = transaction.QueryRowContext(ctx, `
		select runtime_epoch, github_user_id, provider_id, provider_version, provider_authenticated
		from runtime_registration
		where runtime_id = $1
		for update
	`, registration.ID).Scan(
		&existingEpoch,
		&existingGitHubUserID,
		&existingProviderID,
		&existingProviderVersion,
		&existingAuthenticated,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = transaction.ExecContext(ctx, `
			insert into runtime_registration (
				runtime_id, runtime_epoch, github_user_id, provider_id, provider_version,
				provider_authenticated, registered_at, last_heartbeat_at, revoked_at
			) values ($1, $2, $3, $4, $5, $6, $7, $7, null)
		`, registration.ID, registration.Epoch, registration.GitHubUserID,
			nullIfEmpty(registration.Provider.ID), nullIfEmpty(registration.Provider.Version),
			registration.ProviderAuthenticated, registration.RegisteredAt)
		if err != nil {
			return fmt.Errorf("insert runtime registration: %w", err)
		}
	case err != nil:
		return fmt.Errorf("query runtime registration: %w", err)
	case existingEpoch == registration.Epoch:
		matches := existingGitHubUserID == registration.GitHubUserID &&
			existingProviderID.String == registration.Provider.ID &&
			existingProviderVersion.String == registration.Provider.Version &&
			existingAuthenticated == registration.ProviderAuthenticated
		if !matches {
			return execution.ErrRuntimeConflict
		}
		existingCapabilities, err := loadRuntimeCapabilities(ctx, transaction, registration.ID)
		if err != nil {
			return err
		}
		if !sameCapabilities(existingCapabilities, registration.Capabilities) {
			return execution.ErrRuntimeConflict
		}
		return transaction.Commit()
	default:
		_, err = transaction.ExecContext(ctx, `
			update runtime_registration
			set runtime_epoch = $2,
				github_user_id = $3,
				provider_id = $4,
				provider_version = $5,
				provider_authenticated = $6,
				registered_at = $7,
				last_heartbeat_at = $7,
				revoked_at = null
			where runtime_id = $1
		`, registration.ID, registration.Epoch, registration.GitHubUserID,
			nullIfEmpty(registration.Provider.ID), nullIfEmpty(registration.Provider.Version),
			registration.ProviderAuthenticated, registration.RegisteredAt)
		if err != nil {
			return fmt.Errorf("update runtime registration: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `delete from runtime_capability where runtime_id = $1`, registration.ID); err != nil {
			return fmt.Errorf("replace runtime capabilities: %w", err)
		}
	}
	for _, capability := range registration.Capabilities {
		_, err := transaction.ExecContext(ctx, `
			insert into runtime_capability (runtime_id, capability_id, capability_version)
			values ($1, $2, $3)
		`, registration.ID, capability.ID, capability.Version)
		if err != nil {
			return fmt.Errorf("insert runtime capability: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit runtime registration transaction: %w", err)
	}
	return nil
}

func (s *Store) CreateRun(ctx context.Context, command execution.CreateRunCommand) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workflow run transaction: %w", err)
	}
	defer rollback(transaction)
	var definitionDigest string
	err = transaction.QueryRowContext(ctx, `
		select digest from workflow_definition where name = $1 and version = $2
	`, command.Run.DefinitionName, command.Run.DefinitionVersion).Scan(&definitionDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.ErrDefinitionNotFound
	}
	if err != nil {
		return fmt.Errorf("query workflow definition for run: %w", err)
	}
	if definitionDigest != command.Run.DefinitionDigest {
		return workflow.ErrDefinitionConflict
	}
	_, err = transaction.ExecContext(ctx, `
		insert into workflow_run (
			workflow_run_id, workspace_id, work_item_id, started_by_github_user_id,
			git_execution_user_id, definition_name, definition_version, definition_digest,
			state, version, created_at, updated_at
		) values ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, $10, $11, $12)
	`, command.Run.ID, command.Run.WorkspaceID, command.Run.WorkItemID,
		command.Run.StartedByGitHubUserID, command.Run.GitExecutionUserID,
		command.Run.DefinitionName, command.Run.DefinitionVersion, command.Run.DefinitionDigest,
		command.Run.State, command.Run.Version, command.Run.CreatedAt, command.Run.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert workflow run: %w", err)
	}
	for _, node := range command.Nodes {
		_, err := transaction.ExecContext(ctx, `
			insert into node_run (
				node_run_id, workflow_run_id, node_id, iteration, state, verdict,
				input_digest, output, version, created_at, updated_at
			) values ($1, $2, $3, $4, $5, nullif($6, ''), $7, null, $8, $9, $9)
		`, node.ID, node.RunID, node.NodeID, node.Iteration, node.State, node.Verdict,
			node.InputDigest, node.Version, command.Run.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert node run: %w", err)
		}
	}
	for _, task := range command.Tasks {
		gitScopes, marshalErr := json.Marshal(task.GitScopes)
		if marshalErr != nil {
			return fmt.Errorf("encode task git scopes: %w", marshalErr)
		}
		_, err := transaction.ExecContext(ctx, `
			insert into execution_task (
				task_id, node_run_id, attempt, attempt_kind, previous_task_id,
				capability_id, capability_version, provider_id, provider_version,
				git_scopes, state, input_digest, deadline_at, created_at, updated_at
			) values ($1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		`, task.ID, task.NodeRunID, task.Attempt, task.AttemptKind, task.PreviousTaskID,
			task.Capability.ID, task.Capability.Version, task.Provider.ID, task.Provider.Version,
			gitScopes, task.State, task.InputDigest, task.DeadlineAt,
			command.Run.CreatedAt)
		if err != nil {
			return fmt.Errorf("insert execution task: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit workflow run transaction: %w", err)
	}
	return nil
}

func (s *Store) LeaseTask(
	ctx context.Context,
	request execution.LeaseRequest,
	now time.Time,
) (execution.TaskLease, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("begin task lease transaction: %w", err)
	}
	defer rollback(transaction)
	var githubUserID string
	err = transaction.QueryRowContext(ctx, `
		select github_user_id
		from runtime_registration
		where runtime_id = $1 and runtime_epoch = $2 and revoked_at is null
		for update
	`, request.RuntimeID, request.RuntimeEpoch).Scan(&githubUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.TaskLease{}, execution.ErrRuntimeConflict
	}
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("lock runtime registration: %w", err)
	}

	var task execution.Task
	var runID string
	var humanResponseID sql.NullString
	var encodedResumeRef []byte
	var encodedGitScopes []byte
	err = transaction.QueryRowContext(ctx, `
		select t.task_id, t.node_run_id, t.attempt, t.attempt_kind,
			coalesce(t.previous_task_id, ''), t.human_response_id, t.resume_ref,
			t.capability_id, t.capability_version, t.provider_id, t.provider_version,
			t.git_scopes,
			t.state, t.input_digest, t.deadline_at, t.fencing_token, n.workflow_run_id
		from execution_task t
		join node_run n on n.node_run_id = t.node_run_id
		join workflow_run r on r.workflow_run_id = n.workflow_run_id
		join runtime_capability c
			on c.runtime_id = $1
			and c.capability_id = t.capability_id
			and c.capability_version = t.capability_version
		join runtime_registration rr on rr.runtime_id = c.runtime_id
		where t.state = 'QUEUED'
			and r.state = 'ACTIVE'
			and (r.git_execution_user_id is null or r.git_execution_user_id = $2)
			and rr.provider_authenticated
			and rr.provider_id = t.provider_id
			and rr.provider_version = t.provider_version
			and t.deadline_at > $3
		order by t.created_at, t.task_id
		limit 1
		for update of t skip locked
		`, request.RuntimeID, githubUserID, now).Scan(
		&task.ID, &task.NodeRunID, &task.Attempt, &task.AttemptKind,
		&task.PreviousTaskID, &humanResponseID, &encodedResumeRef,
		&task.Capability.ID, &task.Capability.Version,
		&task.Provider.ID, &task.Provider.Version, &encodedGitScopes,
		&task.State, &task.InputDigest, &task.DeadlineAt, &task.FencingToken, &runID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.TaskLease{}, execution.ErrCapabilityUnavailable
	}
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("claim queued task: %w", err)
	}
	task.HumanResponseID = humanResponseID.String
	task.GitExecutionUserID = githubUserID
	task.WorkflowRunID = runID
	if len(encodedResumeRef) > 0 {
		if err := json.Unmarshal(encodedResumeRef, &task.ResumeRef); err != nil {
			return execution.TaskLease{}, fmt.Errorf("decode task resume reference: %w", err)
		}
	}
	if err := json.Unmarshal(encodedGitScopes, &task.GitScopes); err != nil {
		return execution.TaskLease{}, fmt.Errorf("decode task git scopes: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
		update workflow_run
		set git_execution_user_id = coalesce(git_execution_user_id, $2),
			version = case when git_execution_user_id is null then version + 1 else version end,
			updated_at = case when git_execution_user_id is null then $3 else updated_at end
		where workflow_run_id = $1
			and (git_execution_user_id is null or git_execution_user_id = $2)
	`, runID, githubUserID, now)
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("freeze run git identity: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("read git identity freeze count: %w", err)
	}
	if rows != 1 {
		return execution.TaskLease{}, execution.ErrRuntimeConflict
	}
	leaseID, err := newLeaseID()
	if err != nil {
		return execution.TaskLease{}, err
	}
	task.FencingToken++
	task.LeaseID = leaseID
	task.LeaseExpiresAt = now.Add(request.LeaseDuration)
	task.RuntimeID = request.RuntimeID
	task.RuntimeEpoch = request.RuntimeEpoch
	task.State = execution.TaskLeased
	_, err = transaction.ExecContext(ctx, `
		update execution_task
		set runtime_id = $2, runtime_epoch = $3, lease_id = $4, fencing_token = $5,
			lease_expires_at = $6, state = 'LEASED', updated_at = $7
		where task_id = $1 and state = 'QUEUED'
	`, task.ID, task.RuntimeID, task.RuntimeEpoch, task.LeaseID, task.FencingToken,
		task.LeaseExpiresAt, now)
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("lease execution task: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		update node_run set state = 'RUNNING', version = version + 1, updated_at = $2
		where node_run_id = $1 and state = 'READY'
	`, task.NodeRunID, now)
	if err != nil {
		return execution.TaskLease{}, fmt.Errorf("start node run: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.TaskLease{}, fmt.Errorf("commit task lease transaction: %w", err)
	}
	return execution.TaskLease{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskLeaseKind,
		LeaseID: task.LeaseID, FencingToken: task.FencingToken,
		RuntimeEpoch: request.RuntimeEpoch, ExpiresAt: task.LeaseExpiresAt,
		Task: executionTaskSpec(task),
	}, nil
}

func (s *Store) ReportProgress(
	ctx context.Context,
	report execution.ProgressReport,
	now time.Time,
) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin progress transaction: %w", err)
	}
	defer rollback(transaction)
	task, err := lockTask(ctx, transaction, report.TaskID)
	if err != nil {
		return err
	}
	if err := validateLease(task, report.LeaseID, report.FencingToken, now); err != nil {
		if errors.Is(err, execution.ErrStaleLease) {
			if expireErr := expireTask(ctx, transaction, task.ID, now); expireErr != nil {
				return expireErr
			}
			if commitErr := transaction.Commit(); commitErr != nil {
				return fmt.Errorf("commit task expiry: %w", commitErr)
			}
		}
		return err
	}
	if report.Sequence <= task.ProgressSeq {
		return nil
	}
	_, err = transaction.ExecContext(ctx, `
		update execution_task
		set state = 'RUNNING', progress_sequence = $2, progress_phase = $3, updated_at = $4
		where task_id = $1
	`, report.TaskID, report.Sequence, report.Phase, now)
	if err != nil {
		return fmt.Errorf("update task progress: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit progress transaction: %w", err)
	}
	return nil
}

func (s *Store) ReportResult(
	ctx context.Context,
	report execution.ResultReport,
	now time.Time,
) (execution.ResultAcceptance, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.ResultAcceptance{}, fmt.Errorf("begin result transaction: %w", err)
	}
	defer rollback(transaction)
	task, err := lockTask(ctx, transaction, report.TaskID)
	if err != nil {
		return execution.ResultAcceptance{}, err
	}
	if task.ResultDigest != "" {
		if task.ResultDigest != report.ResultDigest {
			return execution.ResultAcceptance{}, execution.ErrResultConflict
		}
		return execution.ResultAcceptance{Duplicate: true}, nil
	}
	if err := validateLease(task, report.LeaseID, report.FencingToken, now); err != nil {
		if errors.Is(err, execution.ErrStaleLease) {
			if expireErr := expireTask(ctx, transaction, task.ID, now); expireErr != nil {
				return execution.ResultAcceptance{}, expireErr
			}
			if commitErr := transaction.Commit(); commitErr != nil {
				return execution.ResultAcceptance{}, fmt.Errorf("commit task expiry: %w", commitErr)
			}
		}
		return execution.ResultAcceptance{}, err
	}

	var runID string
	var nodeState execution.NodeState
	err = transaction.QueryRowContext(ctx, `
		select n.workflow_run_id, n.state
		from node_run n
		where n.node_run_id = $1
			and not exists (
				select 1 from node_run newer
				where newer.workflow_run_id = n.workflow_run_id
					and newer.node_id = n.node_id
					and newer.iteration > n.iteration
			)
		for update
	`, task.NodeRunID).Scan(&runID, &nodeState)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.ResultAcceptance{}, execution.ErrStaleLease
	}
	if err != nil {
		return execution.ResultAcceptance{}, fmt.Errorf("lock current node run: %w", err)
	}
	if nodeState != execution.NodeRunning {
		return execution.ResultAcceptance{}, execution.ErrStaleLease
	}
	encodedResult, err := json.Marshal(report)
	if err != nil {
		return execution.ResultAcceptance{}, fmt.Errorf("encode task result: %w", err)
	}
	taskState := execution.TaskCompleted
	nextNodeState := execution.NodeDecided
	switch report.Status {
	case execution.ResultFailed:
		taskState = execution.TaskFailed
		nextNodeState = execution.NodeExecutionFailed
	case execution.ResultAwaitingInput:
		taskState = execution.TaskAwaitingInput
		nextNodeState = execution.NodeWaitingHuman
	}
	_, err = transaction.ExecContext(ctx, `
		update execution_task
		set state = $2, result_digest = $3, result = $4, updated_at = $5
		where task_id = $1
	`, task.ID, taskState, report.ResultDigest, encodedResult, now)
	if err != nil {
		return execution.ResultAcceptance{}, fmt.Errorf("persist task result: %w", err)
	}
	var output any
	var verdict any
	if report.Status == execution.ResultCompleted {
		encodedOutput, marshalErr := json.Marshal(report.Output)
		if marshalErr != nil {
			return execution.ResultAcceptance{}, fmt.Errorf("encode node output: %w", marshalErr)
		}
		output = encodedOutput
		if value, ok := report.Output["verdict"].(string); ok {
			verdict = value
		}
	}
	_, err = transaction.ExecContext(ctx, `
		update node_run
		set state = $2, output = $3, verdict = $4, version = version + 1, updated_at = $5
		where node_run_id = $1
	`, task.NodeRunID, nextNodeState, output, verdict, now)
	if err != nil {
		return execution.ResultAcceptance{}, fmt.Errorf("apply result to node run: %w", err)
	}
	if report.Status == execution.ResultFailed {
		_, err = transaction.ExecContext(ctx, `
			update workflow_run
			set state = 'PAUSED', version = version + 1, updated_at = $2
			where workflow_run_id = $1 and state = 'ACTIVE'
		`, runID, now)
		if err != nil {
			return execution.ResultAcceptance{}, fmt.Errorf("pause failed workflow run: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return execution.ResultAcceptance{}, fmt.Errorf("commit result transaction: %w", err)
	}
	return execution.ResultAcceptance{Applied: true}, nil
}

func (s *Store) RetryTask(
	ctx context.Context,
	taskID string,
	newTaskID string,
	now time.Time,
) (execution.Task, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.Task{}, fmt.Errorf("begin retry transaction: %w", err)
	}
	defer rollback(transaction)
	previous, err := lockTask(ctx, transaction, taskID)
	if err != nil {
		return execution.Task{}, err
	}
	if previous.State != execution.TaskFailed && previous.State != execution.TaskExpired {
		return execution.Task{}, execution.ErrRetryNotAllowed
	}
	next := execution.Task{
		ID: newTaskID, NodeRunID: previous.NodeRunID, Attempt: previous.Attempt + 1,
		AttemptKind: execution.AttemptRetry, PreviousTaskID: previous.ID,
		Capability: previous.Capability, Provider: previous.Provider,
		GitScopes: slices.Clone(previous.GitScopes), State: execution.TaskQueued,
		InputDigest: previous.InputDigest, DeadlineAt: previous.DeadlineAt,
	}
	gitScopes, err := json.Marshal(next.GitScopes)
	if err != nil {
		return execution.Task{}, fmt.Errorf("encode retry git scopes: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into execution_task (
			task_id, node_run_id, attempt, attempt_kind, previous_task_id,
			capability_id, capability_version, provider_id, provider_version,
			git_scopes, state, input_digest, deadline_at, created_at, updated_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'QUEUED', $11, $12, $13, $13)
	`, next.ID, next.NodeRunID, next.Attempt, next.AttemptKind, next.PreviousTaskID,
		next.Capability.ID, next.Capability.Version, next.Provider.ID, next.Provider.Version,
		gitScopes, next.InputDigest, next.DeadlineAt, now)
	if err != nil {
		return execution.Task{}, fmt.Errorf("insert retry task: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		update node_run set state = 'READY', version = version + 1, updated_at = $2
		where node_run_id = $1
	`, next.NodeRunID, now)
	if err != nil {
		return execution.Task{}, fmt.Errorf("ready retried node: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		update workflow_run r
		set state = 'ACTIVE', version = version + 1, updated_at = $2
		from node_run n
		where n.node_run_id = $1 and r.workflow_run_id = n.workflow_run_id
	`, next.NodeRunID, now)
	if err != nil {
		return execution.Task{}, fmt.Errorf("resume workflow run for retry: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.Task{}, fmt.Errorf("commit retry transaction: %w", err)
	}
	return next, nil
}

func (s *Store) ResumeTask(
	ctx context.Context,
	taskID string,
	newTaskID string,
	humanResponseID string,
	inputDigest string,
	resumeRef *execution.ResumeRef,
	now time.Time,
) (execution.Task, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.Task{}, fmt.Errorf("begin resume transaction: %w", err)
	}
	defer rollback(transaction)
	previous, err := lockTask(ctx, transaction, taskID)
	if err != nil {
		return execution.Task{}, err
	}
	if previous.State != execution.TaskAwaitingInput {
		return execution.Task{}, execution.ErrRetryNotAllowed
	}
	var encodedResumeRef any
	if resumeRef != nil {
		encoded, marshalErr := json.Marshal(resumeRef)
		if marshalErr != nil {
			return execution.Task{}, fmt.Errorf("encode resume reference: %w", marshalErr)
		}
		encodedResumeRef = encoded
	}
	next := execution.Task{
		ID: newTaskID, NodeRunID: previous.NodeRunID, Attempt: previous.Attempt + 1,
		AttemptKind: execution.AttemptResume, PreviousTaskID: previous.ID,
		HumanResponseID: humanResponseID, ResumeRef: resumeRef,
		Capability: previous.Capability, Provider: previous.Provider,
		GitScopes: slices.Clone(previous.GitScopes), State: execution.TaskQueued,
		InputDigest: inputDigest, DeadlineAt: previous.DeadlineAt,
	}
	gitScopes, err := json.Marshal(next.GitScopes)
	if err != nil {
		return execution.Task{}, fmt.Errorf("encode resume git scopes: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into execution_task (
			task_id, node_run_id, attempt, attempt_kind, previous_task_id,
			human_response_id, resume_ref, capability_id, capability_version,
			provider_id, provider_version, git_scopes, state, input_digest,
			deadline_at, created_at, updated_at
		) values ($1, $2, $3, 'resume', $4, $5, $6, $7, $8, $9, $10, $11, 'QUEUED', $12, $13, $14, $14)
	`, next.ID, next.NodeRunID, next.Attempt, next.PreviousTaskID,
		next.HumanResponseID, encodedResumeRef, next.Capability.ID,
		next.Capability.Version, next.Provider.ID, next.Provider.Version,
		gitScopes, next.InputDigest, next.DeadlineAt, now)
	if err != nil {
		return execution.Task{}, fmt.Errorf("insert resume task: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		update node_run
		set state = 'READY', input_digest = $2, version = version + 1, updated_at = $3
		where node_run_id = $1
	`, next.NodeRunID, inputDigest, now)
	if err != nil {
		return execution.Task{}, fmt.Errorf("ready resumed node: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		update workflow_run r
		set state = 'ACTIVE', version = version + 1, updated_at = $2
		from node_run n
		where n.node_run_id = $1 and r.workflow_run_id = n.workflow_run_id
	`, next.NodeRunID, now)
	if err != nil {
		return execution.Task{}, fmt.Errorf("resume workflow run after input: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.Task{}, fmt.Errorf("commit resume transaction: %w", err)
	}
	return next, nil
}

func (s *Store) RerunNode(
	ctx context.Context,
	runID string,
	nodeID string,
	newNodeRunID string,
	newTaskID string,
	inputDigest string,
	maxIteration int,
	now time.Time,
) (execution.NodeRun, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("begin rerun transaction: %w", err)
	}
	defer rollback(transaction)
	var runState execution.RunState
	err = transaction.QueryRowContext(ctx, `
		select state from workflow_run where workflow_run_id = $1 for update
	`, runID).Scan(&runState)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.NodeRun{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("lock workflow run for rerun: %w", err)
	}
	if runState == execution.RunSucceeded || runState == execution.RunFailed || runState == execution.RunCanceled {
		return execution.NodeRun{}, execution.ErrRetryNotAllowed
	}
	var current execution.NodeRun
	err = transaction.QueryRowContext(ctx, `
		select node_run_id, iteration
		from node_run
		where workflow_run_id = $1 and node_id = $2
		order by iteration desc
		limit 1
		for update
	`, runID, nodeID).Scan(&current.ID, &current.Iteration)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.NodeRun{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("lock current node for rerun: %w", err)
	}
	if maxIteration > 0 && current.Iteration >= maxIteration {
		return execution.NodeRun{}, execution.ErrPolicyBlocked
	}
	var activeTasks int
	if err := transaction.QueryRowContext(ctx, `
		select count(*) from execution_task
		where node_run_id = $1 and state in ('LEASED', 'RUNNING')
	`, current.ID).Scan(&activeTasks); err != nil {
		return execution.NodeRun{}, fmt.Errorf("count active tasks before rerun: %w", err)
	}
	if activeTasks > 0 {
		return execution.NodeRun{}, execution.ErrRetryNotAllowed
	}
	if _, err := transaction.ExecContext(ctx, `
		update execution_task set state = 'CANCELED', updated_at = $2
		where node_run_id = $1 and state = 'QUEUED'
	`, current.ID, now); err != nil {
		return execution.NodeRun{}, fmt.Errorf("cancel superseded tasks: %w", err)
	}
	var sourceTask execution.Task
	var encodedGitScopes []byte
	err = transaction.QueryRowContext(ctx, `
		select capability_id, capability_version, provider_id, provider_version, git_scopes, deadline_at
		from execution_task where node_run_id = $1
		order by attempt desc limit 1
	`, current.ID).Scan(
		&sourceTask.Capability.ID,
		&sourceTask.Capability.Version,
		&sourceTask.Provider.ID,
		&sourceTask.Provider.Version,
		&encodedGitScopes,
		&sourceTask.DeadlineAt,
	)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("query rerun capability: %w", err)
	}
	if err := json.Unmarshal(encodedGitScopes, &sourceTask.GitScopes); err != nil {
		return execution.NodeRun{}, fmt.Errorf("decode rerun git scopes: %w", err)
	}
	next := execution.NodeRun{
		ID: newNodeRunID, RunID: runID, NodeID: nodeID, Iteration: current.Iteration + 1,
		State: execution.NodeReady, InputDigest: inputDigest, Version: 1,
	}
	_, err = transaction.ExecContext(ctx, `
		insert into node_run (
			node_run_id, workflow_run_id, node_id, iteration, state,
			input_digest, version, created_at, updated_at
		) values ($1, $2, $3, $4, 'READY', $5, 1, $6, $6)
	`, next.ID, next.RunID, next.NodeID, next.Iteration, next.InputDigest, now)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("insert rerun node: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into execution_task (
			task_id, node_run_id, attempt, attempt_kind, capability_id,
			capability_version, provider_id, provider_version, git_scopes,
			state, input_digest, deadline_at, created_at, updated_at
		) values ($1, $2, 1, 'initial', $3, $4, $5, $6, $7, 'QUEUED', $8, $9, $10, $10)
	`, newTaskID, next.ID, sourceTask.Capability.ID, sourceTask.Capability.Version,
		sourceTask.Provider.ID, sourceTask.Provider.Version, encodedGitScopes,
		inputDigest, sourceTask.DeadlineAt, now)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("insert rerun task: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		update workflow_run set state = 'ACTIVE', version = version + 1, updated_at = $2
		where workflow_run_id = $1
	`, runID, now)
	if err != nil {
		return execution.NodeRun{}, fmt.Errorf("resume workflow run for rerun: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.NodeRun{}, fmt.Errorf("commit rerun transaction: %w", err)
	}
	return next, nil
}

func (s *Store) CancelRun(ctx context.Context, runID string, expectedVersion int64, now time.Time) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin cancellation transaction: %w", err)
	}
	defer rollback(transaction)
	var version int64
	err = transaction.QueryRowContext(ctx, `
		select version from workflow_run where workflow_run_id = $1 for update
	`, runID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock workflow run for cancellation: %w", err)
	}
	if version != expectedVersion {
		return execution.ErrVersionConflict
	}
	if _, err := transaction.ExecContext(ctx, `
		update execution_task t set state = 'CANCELED', updated_at = $2
		from node_run n
		where t.node_run_id = n.node_run_id and n.workflow_run_id = $1 and t.state = 'QUEUED'
	`, runID, now); err != nil {
		return fmt.Errorf("cancel queued tasks: %w", err)
	}
	var activeTasks int
	if err := transaction.QueryRowContext(ctx, `
		select count(*)
		from execution_task t join node_run n on n.node_run_id = t.node_run_id
		where n.workflow_run_id = $1 and t.state in ('LEASED', 'RUNNING')
	`, runID).Scan(&activeTasks); err != nil {
		return fmt.Errorf("count active tasks during cancellation: %w", err)
	}
	nextState := execution.RunCanceled
	if activeTasks > 0 {
		nextState = execution.RunCanceling
	}
	_, err = transaction.ExecContext(ctx, `
		update workflow_run set state = $2, version = version + 1, updated_at = $3
		where workflow_run_id = $1
	`, runID, nextState, now)
	if err != nil {
		return fmt.Errorf("update workflow cancellation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit cancellation transaction: %w", err)
	}
	return nil
}

func (s *Store) RunView(ctx context.Context, runID string) (execution.RunView, error) {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return execution.RunView{}, fmt.Errorf("begin run view transaction: %w", err)
	}
	defer rollback(transaction)
	var view execution.RunView
	var gitExecutionUserID sql.NullString
	err = transaction.QueryRowContext(ctx, `
		select workflow_run_id, work_item_id, started_by_github_user_id,
			git_execution_user_id, definition_name, definition_version,
			definition_digest, state, version
		from workflow_run where workflow_run_id = $1
	`, runID).Scan(
		&view.ID, &view.WorkItemID, &view.StartedByGitHubUserID,
		&gitExecutionUserID, &view.DefinitionName, &view.DefinitionVersion,
		&view.DefinitionDigest, &view.State, &view.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.RunView{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.RunView{}, fmt.Errorf("query workflow run view: %w", err)
	}
	if gitExecutionUserID.Valid {
		view.GitExecutionUserID = gitExecutionUserID.String
	}
	view.SchemaVersion = execution.SchemaVersion
	view.Kind = execution.RunViewKind
	rows, err := transaction.QueryContext(ctx, `
		select distinct on (node_id)
			node_run_id, node_id, iteration, state, coalesce(verdict, ''), input_digest, output
		from node_run
		where workflow_run_id = $1
		order by node_id, iteration desc
	`, runID)
	if err != nil {
		return execution.RunView{}, fmt.Errorf("query node views: %w", err)
	}
	defer func() { _ = rows.Close() }()
	view.Nodes = []execution.NodeView{}
	for rows.Next() {
		var node execution.NodeView
		var output []byte
		if err := rows.Scan(
			&node.ID, &node.NodeID, &node.Iteration, &node.State,
			&node.Verdict, &node.InputDigest, &output,
		); err != nil {
			return execution.RunView{}, fmt.Errorf("scan node view: %w", err)
		}
		if len(output) > 0 {
			if err := json.Unmarshal(output, &node.Output); err != nil {
				return execution.RunView{}, fmt.Errorf("decode node output: %w", err)
			}
		}
		view.Nodes = append(view.Nodes, node)
	}
	if err := rows.Err(); err != nil {
		return execution.RunView{}, fmt.Errorf("iterate node views: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return execution.RunView{}, fmt.Errorf("commit run view transaction: %w", err)
	}
	return view, nil
}

func lockTask(ctx context.Context, transaction *sql.Tx, taskID string) (execution.Task, error) {
	var task execution.Task
	var previousTaskID sql.NullString
	var humanResponseID sql.NullString
	var encodedResumeRef []byte
	var encodedGitScopes []byte
	var runtimeID sql.NullString
	var runtimeEpoch sql.NullString
	var leaseID sql.NullString
	var leaseExpiresAt sql.NullTime
	var resultDigest sql.NullString
	err := transaction.QueryRowContext(ctx, `
		select task_id, node_run_id, attempt, attempt_kind, previous_task_id,
			human_response_id, resume_ref,
			capability_id, capability_version, provider_id, provider_version,
			git_scopes, state, input_digest, deadline_at,
			runtime_id, runtime_epoch, lease_id, fencing_token, lease_expires_at,
			result_digest, progress_sequence
		from execution_task where task_id = $1 for update
	`, taskID).Scan(
		&task.ID, &task.NodeRunID, &task.Attempt, &task.AttemptKind, &previousTaskID,
		&humanResponseID, &encodedResumeRef,
		&task.Capability.ID, &task.Capability.Version, &task.Provider.ID, &task.Provider.Version,
		&encodedGitScopes, &task.State, &task.InputDigest, &task.DeadlineAt,
		&runtimeID, &runtimeEpoch, &leaseID, &task.FencingToken, &leaseExpiresAt,
		&resultDigest, &task.ProgressSeq,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.Task{}, execution.ErrNotFound
	}
	if err != nil {
		return execution.Task{}, fmt.Errorf("lock execution task: %w", err)
	}
	task.PreviousTaskID = previousTaskID.String
	task.HumanResponseID = humanResponseID.String
	if len(encodedResumeRef) > 0 {
		if err := json.Unmarshal(encodedResumeRef, &task.ResumeRef); err != nil {
			return execution.Task{}, fmt.Errorf("decode task resume reference: %w", err)
		}
	}
	if err := json.Unmarshal(encodedGitScopes, &task.GitScopes); err != nil {
		return execution.Task{}, fmt.Errorf("decode task git scopes: %w", err)
	}
	task.RuntimeID = runtimeID.String
	task.RuntimeEpoch = runtimeEpoch.String
	task.LeaseID = leaseID.String
	task.ResultDigest = resultDigest.String
	if leaseExpiresAt.Valid {
		task.LeaseExpiresAt = leaseExpiresAt.Time
	}
	return task, nil
}

func validateLease(task execution.Task, leaseID string, fencingToken int64, now time.Time) error {
	validState := task.State == execution.TaskLeased || task.State == execution.TaskRunning
	if !validState || task.LeaseID != leaseID || task.FencingToken != fencingToken || !now.Before(task.LeaseExpiresAt) {
		return execution.ErrStaleLease
	}
	return nil
}

func expireTask(ctx context.Context, transaction *sql.Tx, taskID string, now time.Time) error {
	_, err := transaction.ExecContext(ctx, `
		update execution_task set state = 'EXPIRED', updated_at = $2
		where task_id = $1 and state in ('LEASED', 'RUNNING')
	`, taskID, now)
	if err != nil {
		return fmt.Errorf("expire stale task: %w", err)
	}
	return nil
}

func loadRuntimeCapabilities(
	ctx context.Context,
	transaction *sql.Tx,
	runtimeID string,
) ([]execution.Capability, error) {
	rows, err := transaction.QueryContext(ctx, `
		select capability_id, capability_version
		from runtime_capability where runtime_id = $1
		order by capability_id, capability_version
	`, runtimeID)
	if err != nil {
		return nil, fmt.Errorf("query runtime capabilities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	capabilities := []execution.Capability{}
	for rows.Next() {
		var capability execution.Capability
		if err := rows.Scan(&capability.ID, &capability.Version); err != nil {
			return nil, fmt.Errorf("scan runtime capability: %w", err)
		}
		capabilities = append(capabilities, capability)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime capabilities: %w", err)
	}
	return capabilities, nil
}

func sameCapabilities(left, right []execution.Capability) bool {
	left = slices.Clone(left)
	right = slices.Clone(right)
	slices.SortFunc(left, compareCapability)
	slices.SortFunc(right, compareCapability)
	return slices.Equal(left, right)
}

func compareCapability(left, right execution.Capability) int {
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	return left.Version - right.Version
}

func newLeaseID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate lease id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func executionTaskSpec(task execution.Task) execution.TaskSpec {
	return execution.TaskSpec{
		ID: task.ID, WorkflowRunID: task.WorkflowRunID, NodeRunID: task.NodeRunID,
		Attempt: task.Attempt, AttemptKind: task.AttemptKind,
		PreviousTaskID: task.PreviousTaskID, HumanResponseID: task.HumanResponseID,
		ResumeRef: task.ResumeRef, Capability: task.Capability,
		ProviderSelection: task.Provider, InputDigest: task.InputDigest,
		GitExecutionUserID: task.GitExecutionUserID, GitScopes: slices.Clone(task.GitScopes),
		DeadlineAt: task.DeadlineAt,
	}
}

var _ execution.Store = (*Store)(nil)
