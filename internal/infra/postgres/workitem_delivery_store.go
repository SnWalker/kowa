package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/workitem"
)

// replayCommand returns the recorded outcome of an identical command. The same
// (caller, command type, target, request key) triple with a different content
// digest is a version conflict.
func replayCommand(
	ctx context.Context,
	queryer queryer,
	actor, commandType, targetID, requestKey, requestDigest string,
	target any,
) (bool, error) {
	var storedDigest string
	var response []byte
	err := queryer.QueryRowContext(ctx, `
		select request_digest, response
		from command_idempotency
		where actor_github_user_id = $1
			and command_type = $2
			and target_id = $3
			and request_key = $4
	`, actor, commandType, targetID, requestKey).Scan(&storedDigest, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query command idempotency: %w", err)
	}
	if storedDigest != requestDigest {
		return false, workitem.ErrVersionConflict
	}
	if err := json.Unmarshal(response, target); err != nil {
		return false, fmt.Errorf("decode idempotent response: %w", err)
	}
	return true, nil
}

// saveCommand records one accepted command outcome inside the same transaction.
func saveCommand(
	ctx context.Context,
	transaction *sql.Tx,
	actor, commandType, targetID, requestKey, requestDigest string,
	response any,
	now time.Time,
) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into command_idempotency (
			actor_github_user_id, command_type, target_id, request_key,
			request_digest, response, created_at
		) values ($1, $2, $3, $4, $5, $6, $7)
	`, actor, commandType, targetID, requestKey, requestDigest, encoded, now)
	if err != nil {
		return fmt.Errorf("insert command idempotency: %w", err)
	}
	return nil
}

const workItemColumns = `work_item_id, workspace_id, title, target_repository_id,
	requirement, requirement_digest, acceptance, acceptance_digest,
	state, version, current_run_id, created_by_github_user_id,
	created_at, updated_at, closed_at, canceled_at`

// CreateWorkItem persists one OPEN work item; the target repository is the
// workspace project binding resolved by the service.
func (s *Store) CreateWorkItem(ctx context.Context, mutation workitem.CreateMutation) (workitem.WorkItem, error) {
	item := mutation.WorkItem
	requirement, err := json.Marshal(item.Requirement)
	if err != nil {
		return workitem.WorkItem{}, fmt.Errorf("encode requirement: %w", err)
	}
	acceptance, err := json.Marshal(item.Acceptance)
	if err != nil {
		return workitem.WorkItem{}, fmt.Errorf("encode acceptance: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return workitem.WorkItem{}, fmt.Errorf("begin work item transaction: %w", err)
	}
	defer rollback(transaction)
	replayed, err := replayCommand(ctx, transaction, mutation.Actor.GitHubUserID, "workitem.create",
		item.WorkspaceID, mutation.RequestKey, mutation.RequestDigest, &item)
	if err != nil {
		return workitem.WorkItem{}, err
	}
	if replayed {
		if err := transaction.Commit(); err != nil {
			return workitem.WorkItem{}, fmt.Errorf("commit work item replay: %w", err)
		}
		return item, nil
	}
	var boundProject sql.NullString
	if err := transaction.QueryRowContext(ctx, `
		select github_repository_id from repository_binding
		where workspace_id = $1 and role = 'project'
	`, item.WorkspaceID).Scan(&boundProject); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workitem.WorkItem{}, fmt.Errorf("%w: the workspace has no project binding", workitem.ErrPolicyBlocked)
		}
		return workitem.WorkItem{}, fmt.Errorf("read project binding: %w", err)
	}
	if boundProject.String != mutation.TargetProjectID {
		return workitem.WorkItem{}, fmt.Errorf("%w: the project binding changed", workitem.ErrVersionConflict)
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into work_item (
			work_item_id, workspace_id, title, target_repository_id,
			requirement, requirement_digest, acceptance, acceptance_digest,
			state, version, current_run_id, created_by_github_user_id,
			created_at, updated_at, closed_at, canceled_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, item.ID, item.WorkspaceID, item.Title, item.TargetRepositoryID,
		requirement, item.Requirement.Digest, acceptance, item.Acceptance.Digest,
		string(item.State), item.Version, nullIfEmpty(item.CurrentRunID), item.CreatedByUserID,
		item.CreatedAt, item.UpdatedAt, item.ClosedAt, item.CanceledAt); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("insert work item: %w", err)
	}
	if err := saveCommand(ctx, transaction, mutation.Actor.GitHubUserID, "workitem.create",
		item.WorkspaceID, mutation.RequestKey, mutation.RequestDigest, item, time.Now().UTC()); err != nil {
		return workitem.WorkItem{}, err
	}
	workspaceID := item.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, mutation.Actor.GitHubUserID,
		"workitem.created", &item.Version, map[string]any{"workItemId": item.ID}); err != nil {
		return workitem.WorkItem{}, err
	}
	if err := transaction.Commit(); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("commit work item: %w", err)
	}
	return item, nil
}

func scanWorkItem(row interface {
	Scan(...any) error
}, item *workitem.WorkItem) error {
	var requirement, acceptance []byte
	var currentRun sql.NullString
	var closedAt, canceledAt sql.NullTime
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Title, &item.TargetRepositoryID,
		&requirement, &item.Requirement.Digest, &acceptance, &item.Acceptance.Digest,
		&item.State, &item.Version, &currentRun, &item.CreatedByUserID,
		&item.CreatedAt, &item.UpdatedAt, &closedAt, &canceledAt); err != nil {
		return err
	}
	if err := json.Unmarshal(requirement, &item.Requirement); err != nil {
		return fmt.Errorf("decode requirement: %w", err)
	}
	if err := json.Unmarshal(acceptance, &item.Acceptance); err != nil {
		return fmt.Errorf("decode acceptance: %w", err)
	}
	item.CurrentRunID = currentRun.String
	if closedAt.Valid {
		closed := closedAt.Time
		item.ClosedAt = &closed
	}
	if canceledAt.Valid {
		canceled := canceledAt.Time
		item.CanceledAt = &canceled
	}
	return nil
}

// WorkItem reads one work item inside its workspace.
func (s *Store) WorkItem(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error) {
	row := s.database.QueryRowContext(ctx, `select `+workItemColumns+`
		from work_item where work_item_id = $1 and workspace_id = $2`, workItemID, workspaceID)
	var item workitem.WorkItem
	if err := scanWorkItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workitem.WorkItem{}, workitem.ErrNotFound
		}
		return workitem.WorkItem{}, fmt.Errorf("read work item: %w", err)
	}
	return item, nil
}

// WorkItems lists the work items of one workspace, newest first.
func (s *Store) WorkItems(ctx context.Context, workspaceID string) ([]workitem.WorkItem, error) {
	rows, err := s.database.QueryContext(ctx, `select `+workItemColumns+`
		from work_item where workspace_id = $1 order by created_at desc, work_item_id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list work items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := []workitem.WorkItem{}
	for rows.Next() {
		var item workitem.WorkItem
		if err := scanWorkItem(rows, &item); err != nil {
			return nil, fmt.Errorf("scan work item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate work items: %w", err)
	}
	return items, nil
}

func lockWorkItem(ctx context.Context, transaction *sql.Tx, workspaceID, workItemID string) (workitem.WorkItem, error) {
	row := transaction.QueryRowContext(ctx, `select `+workItemColumns+`
		from work_item where work_item_id = $1 and workspace_id = $2 for update`, workItemID, workspaceID)
	var item workitem.WorkItem
	if err := scanWorkItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workitem.WorkItem{}, workitem.ErrNotFound
		}
		return workitem.WorkItem{}, fmt.Errorf("lock work item: %w", err)
	}
	return item, nil
}

// CancelWorkItem applies the versioned OPEN -> CANCELED transition.
func (s *Store) CancelWorkItem(ctx context.Context, mutation workitem.CancelMutation) (workitem.WorkItem, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return workitem.WorkItem{}, fmt.Errorf("begin cancel transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed workitem.WorkItem
	replay, err := replayCommand(ctx, transaction, mutation.Actor.GitHubUserID, "workitem.cancel",
		mutation.WorkItemID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return workitem.WorkItem{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return workitem.WorkItem{}, fmt.Errorf("commit cancel replay: %w", err)
		}
		return replayed, nil
	}
	item, err := lockWorkItem(ctx, transaction, mutation.WorkspaceID, mutation.WorkItemID)
	if err != nil {
		return workitem.WorkItem{}, err
	}
	if item.Version != mutation.ExpectedVersion {
		return workitem.WorkItem{}, workitem.ErrVersionConflict
	}
	if item.State != workitem.StateOpen {
		return workitem.WorkItem{}, fmt.Errorf("%w: only an OPEN work item can be canceled", workitem.ErrPolicyBlocked)
	}
	item.State = workitem.StateCanceled
	item.Version++
	item.UpdatedAt = mutation.CanceledAt
	canceledAt := mutation.CanceledAt
	item.CanceledAt = &canceledAt
	if _, err := transaction.ExecContext(ctx, `
		update work_item set state = $2, version = $3, updated_at = $4, canceled_at = $4
		where work_item_id = $1
	`, item.ID, string(item.State), item.Version, mutation.CanceledAt); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("cancel work item: %w", err)
	}
	if err := saveCommand(ctx, transaction, mutation.Actor.GitHubUserID, "workitem.cancel",
		item.ID, mutation.RequestKey, mutation.RequestDigest, item, mutation.CanceledAt); err != nil {
		return workitem.WorkItem{}, err
	}
	workspaceID := item.WorkspaceID
	reason := mutation.Reason
	if err := insertAudit(ctx, transaction, &workspaceID, mutation.Actor.GitHubUserID,
		"workitem.canceled", &item.Version, map[string]any{"workItemId": item.ID, "reason": reason}); err != nil {
		return workitem.WorkItem{}, err
	}
	if err := transaction.Commit(); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("commit cancel: %w", err)
	}
	return item, nil
}

// AssociateRun binds or replaces the run holding advancement authority.
func (s *Store) AssociateRun(ctx context.Context, mutation workitem.AssociateRunMutation) (workitem.WorkItem, error) {
	binding := mutation.Binding
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return workitem.WorkItem{}, fmt.Errorf("begin run binding transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed workitem.WorkItem
	replay, err := replayCommand(ctx, transaction, binding.BoundByUserID, "workitem.associate_run",
		mutation.WorkItemID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return workitem.WorkItem{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return workitem.WorkItem{}, fmt.Errorf("commit run binding replay: %w", err)
		}
		return replayed, nil
	}
	row := transaction.QueryRowContext(ctx, `select `+workItemColumns+`
		from work_item where work_item_id = $1 for update`, mutation.WorkItemID)
	var item workitem.WorkItem
	if err := scanWorkItem(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return workitem.WorkItem{}, workitem.ErrNotFound
		}
		return workitem.WorkItem{}, fmt.Errorf("lock work item: %w", err)
	}
	if item.Version != mutation.ExpectedVersion {
		return workitem.WorkItem{}, workitem.ErrVersionConflict
	}
	if item.State != workitem.StateOpen {
		return workitem.WorkItem{}, fmt.Errorf("%w: a %s work item cannot bind a run", workitem.ErrPolicyBlocked, item.State)
	}
	switch binding.Kind {
	case workitem.BindingInitial:
		if item.CurrentRunID != "" && item.CurrentRunID != binding.RunID {
			return workitem.WorkItem{}, fmt.Errorf("%w: the work item already has a current run", workitem.ErrPolicyBlocked)
		}
	case workitem.BindingRepair:
		if item.CurrentRunID != binding.SourceRunID {
			return workitem.WorkItem{}, fmt.Errorf("%w: a repair run must replace the current run", workitem.ErrPolicyBlocked)
		}
	default:
		return workitem.WorkItem{}, workitem.ErrValidation
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into work_item_run_binding (
			work_item_id, workflow_run_id, kind, source_run_id, reason, merged_baseline_oid,
			bound_by_github_user_id, bound_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8)
	`, binding.WorkItemID, binding.RunID, string(binding.Kind), nullIfEmpty(binding.SourceRunID),
		nullIfEmpty(binding.Reason), nullIfEmpty(binding.MergedBaselineOID), binding.BoundByUserID, binding.BoundAt); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("insert run binding: %w", err)
	}
	item.CurrentRunID = binding.RunID
	item.Version++
	item.UpdatedAt = mutation.BoundAt
	if _, err := transaction.ExecContext(ctx, `
		update work_item set current_run_id = $2, version = $3, updated_at = $4 where work_item_id = $1
	`, item.ID, binding.RunID, item.Version, mutation.BoundAt); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("update current run: %w", err)
	}
	if err := saveCommand(ctx, transaction, binding.BoundByUserID, "workitem.associate_run",
		item.ID, mutation.RequestKey, mutation.RequestDigest, item, mutation.BoundAt); err != nil {
		return workitem.WorkItem{}, err
	}
	workspaceID := item.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, binding.BoundByUserID,
		"workitem.run_bound", &item.Version,
		map[string]any{"workItemId": item.ID, "runId": binding.RunID, "kind": string(binding.Kind)}); err != nil {
		return workitem.WorkItem{}, err
	}
	if err := transaction.Commit(); err != nil {
		return workitem.WorkItem{}, fmt.Errorf("commit run binding: %w", err)
	}
	return item, nil
}

// WorkItemRunBindings lists the run lineage of one work item.
func (s *Store) WorkItemRunBindings(ctx context.Context, workspaceID, workItemID string) ([]workitem.RunBinding, error) {
	rows, err := s.database.QueryContext(ctx, `
		select binding.work_item_id, binding.workflow_run_id, binding.kind, binding.source_run_id,
			binding.reason, binding.merged_baseline_oid, binding.bound_by_github_user_id, binding.bound_at
		from work_item_run_binding binding
		join work_item item on item.work_item_id = binding.work_item_id
		where binding.work_item_id = $1 and item.workspace_id = $2
		order by binding.bound_at, binding.workflow_run_id
	`, workItemID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list run bindings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	bindings := []workitem.RunBinding{}
	for rows.Next() {
		var binding workitem.RunBinding
		var sourceRun, reason, baseline sql.NullString
		if err := rows.Scan(&binding.WorkItemID, &binding.RunID, &binding.Kind, &sourceRun,
			&reason, &baseline, &binding.BoundByUserID, &binding.BoundAt); err != nil {
			return nil, fmt.Errorf("scan run binding: %w", err)
		}
		binding.SourceRunID, binding.Reason, binding.MergedBaselineOID = sourceRun.String, reason.String, baseline.String
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run bindings: %w", err)
	}
	return bindings, nil
}

const humanTaskColumns = `human_task_id, workspace_id, work_item_id, workflow_run_id, node_run_id,
	request_version, request, request_digest, completion_mode, kind,
	object_kind, object_id, object_version, object_digest, state, version,
	candidates, roles, created_at, updated_at, resolved_at, resolved_by_github_user_id,
	resolution_kind, external_fact_id, expired_reason`

func scanHumanTask(row interface {
	Scan(...any) error
}, task *humantask.HumanTask) error {
	var request, candidates, roles []byte
	var runID, nodeRunID, objectDigest, resolvedBy, resolutionKind, externalFact, expiredReason sql.NullString
	var resolvedAt sql.NullTime
	if err := row.Scan(&task.ID, &task.WorkspaceID, &task.WorkItemID, &runID, &nodeRunID,
		&task.Request.Version, &request, &task.RequestDigest, &task.Request.Mode, &task.Request.Kind,
		&task.Request.Object.Kind, &task.Request.Object.ID, &task.Request.Object.Version, &objectDigest,
		&task.State, &task.Version, &candidates, &roles, &task.CreatedAt, &task.UpdatedAt,
		&resolvedAt, &resolvedBy, &resolutionKind, &externalFact, &expiredReason); err != nil {
		return err
	}
	if err := json.Unmarshal(request, &task.Request); err != nil {
		return fmt.Errorf("decode human task request: %w", err)
	}
	if err := json.Unmarshal(candidates, &task.Request.Candidates); err != nil {
		return fmt.Errorf("decode human task candidates: %w", err)
	}
	if err := json.Unmarshal(roles, &task.Request.Roles); err != nil {
		return fmt.Errorf("decode human task roles: %w", err)
	}
	task.RunID, task.NodeRunID = runID.String, nodeRunID.String
	task.Request.Object.Digest = objectDigest.String
	task.ResolvedByUserID, task.ResolutionKind = resolvedBy.String, resolutionKind.String
	task.ExternalFactID, task.ExpiredReason = externalFact.String, expiredReason.String
	if resolvedAt.Valid {
		resolved := resolvedAt.Time
		task.ResolvedAt = &resolved
	}
	return nil
}

// CreateHumanTask persists one OPEN human request.
func (s *Store) CreateHumanTask(ctx context.Context, mutation humantask.CreateMutation) (humantask.HumanTask, error) {
	task := mutation.Task
	request, err := json.Marshal(task.Request)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("encode human task request: %w", err)
	}
	candidates, err := json.Marshal(task.Request.Candidates)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("encode human task candidates: %w", err)
	}
	roles, err := json.Marshal(task.Request.Roles)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("encode human task roles: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("begin human task transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed humantask.HumanTask
	replay, err := replayCommand(ctx, transaction, mutation.ActorID, "humantask.create",
		task.WorkspaceID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return humantask.HumanTask{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("commit human task replay: %w", err)
		}
		return replayed, nil
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into human_task (
			human_task_id, workspace_id, work_item_id, workflow_run_id, node_run_id,
			request_version, request, request_digest, completion_mode, kind,
			object_kind, object_id, object_version, object_digest, state, version,
			candidates, roles, created_at, updated_at, resolved_at, resolved_by_github_user_id,
			resolution_kind, external_fact_id, expired_reason
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22, $23, $24, $25)
	`, task.ID, task.WorkspaceID, task.WorkItemID, nullIfEmpty(task.RunID), nullIfEmpty(task.NodeRunID),
		task.Request.Version, request, task.RequestDigest, string(task.Request.Mode), task.Request.Kind,
		task.Request.Object.Kind, task.Request.Object.ID, task.Request.Object.Version,
		nullIfEmpty(task.Request.Object.Digest), string(task.State), task.Version,
		candidates, roles, task.CreatedAt, task.UpdatedAt, nil, nil, nil, nil, nil); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("insert human task: %w", err)
	}
	if err := saveCommand(ctx, transaction, mutation.ActorID, "humantask.create",
		task.WorkspaceID, mutation.RequestKey, mutation.RequestDigest, task, time.Now().UTC()); err != nil {
		return humantask.HumanTask{}, err
	}
	workspaceID := task.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, mutation.ActorID,
		"humantask.created", &task.Version,
		map[string]any{"humanTaskId": task.ID, "kind": task.Request.Kind, "mode": string(task.Request.Mode)}); err != nil {
		return humantask.HumanTask{}, err
	}
	if err := transaction.Commit(); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("commit human task: %w", err)
	}
	return task, nil
}

// HumanTask reads one task inside its workspace, including its response and
// continuation when they exist.
func (s *Store) HumanTask(ctx context.Context, workspaceID, taskID string) (humantask.HumanTask, error) {
	row := s.database.QueryRowContext(ctx, `select `+humanTaskColumns+`
		from human_task where human_task_id = $1 and workspace_id = $2`, taskID, workspaceID)
	var task humantask.HumanTask
	if err := scanHumanTask(row, &task); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return humantask.HumanTask{}, humantask.ErrNotFound
		}
		return humantask.HumanTask{}, fmt.Errorf("read human task: %w", err)
	}
	if err := s.attachHumanTaskClosure(ctx, &task); err != nil {
		return humantask.HumanTask{}, err
	}
	return task, nil
}

func (s *Store) attachHumanTaskClosure(ctx context.Context, task *humantask.HumanTask) error {
	var response humantask.Response
	var payload []byte
	var objectKind, objectID, objectVersion, objectDigest sql.NullString
	err := s.database.QueryRowContext(ctx, `
		select human_response_id, workspace_id, request_version, object_kind, object_id, object_version,
			actor_github_user_id, decision, payload, payload_digest, created_at
		from human_response where human_task_id = $1
	`, task.ID).Scan(&response.ResponseID, &response.WorkspaceID, &response.RequestVersion,
		&objectKind, &objectID, &objectVersion, &response.ActorID, &response.Decision,
		&payload, &response.PayloadDigest, &response.CreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read human response: %w", err)
	default:
		if err := json.Unmarshal(payload, &response.Values); err != nil {
			return fmt.Errorf("decode human response payload: %w", err)
		}
		response.TaskID = task.ID
		response.Object = humantask.ObjectBinding{
			Kind: objectKind.String, ID: objectID.String, Version: objectVersion.String, Digest: objectDigest.String,
		}
		task.Response = &response
	}
	var continuation humantask.Continuation
	var nodeRunID sql.NullString
	var consumedAt sql.NullTime
	var consumedBy sql.NullString
	err = s.database.QueryRowContext(ctx, `
		select continuation_id, human_response_id, human_task_id, workspace_id, workflow_run_id, node_run_id,
			intent, state, created_at, consumed_at, consumed_by_github_user_id
		from human_task_continuation where human_task_id = $1
	`, task.ID).Scan(&continuation.ID, &continuation.ResponseID, &continuation.TaskID,
		&continuation.WorkspaceID, &continuation.RunID, &nodeRunID, &continuation.Intent,
		&continuation.State, &continuation.CreatedAt, &consumedAt, &consumedBy)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read human task continuation: %w", err)
	default:
		continuation.NodeRunID = nodeRunID.String
		continuation.ConsumedBy = consumedBy.String
		if consumedAt.Valid {
			consumed := consumedAt.Time
			continuation.ConsumedAt = &consumed
		}
		task.Continuation = &continuation
	}
	return nil
}

// ListHumanTasks lists the workspace tasks, newest first.
func (s *Store) ListHumanTasks(ctx context.Context, workspaceID string, openOnly bool) ([]humantask.HumanTask, error) {
	query := `select ` + humanTaskColumns + ` from human_task where workspace_id = $1`
	arguments := []any{workspaceID}
	if openOnly {
		query += ` and state = 'OPEN'`
	}
	query += ` order by created_at desc, human_task_id`
	rows, err := s.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list human tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tasks := []humantask.HumanTask{}
	for rows.Next() {
		var task humantask.HumanTask
		if err := scanHumanTask(rows, &task); err != nil {
			return nil, fmt.Errorf("scan human task: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate human tasks: %w", err)
	}
	return tasks, nil
}

// AnswerHumanTask writes the response, the resolution and the continuation
// intent in one transaction.
func (s *Store) AnswerHumanTask(ctx context.Context, mutation humantask.AnswerMutation) (humantask.HumanTask, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("begin answer transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed humantask.HumanTask
	replay, err := replayCommand(ctx, transaction, mutation.ActorID, "humantask.answer",
		mutation.TaskID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return humantask.HumanTask{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("commit answer replay: %w", err)
		}
		return s.HumanTask(ctx, mutation.WorkspaceID, mutation.TaskID)
	}
	row := transaction.QueryRowContext(ctx, `select `+humanTaskColumns+`
		from human_task where human_task_id = $1 and workspace_id = $2 for update`,
		mutation.TaskID, mutation.WorkspaceID)
	var task humantask.HumanTask
	if err := scanHumanTask(row, &task); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return humantask.HumanTask{}, humantask.ErrNotFound
		}
		return humantask.HumanTask{}, fmt.Errorf("lock human task: %w", err)
	}
	if task.State != humantask.StateOpen {
		return humantask.HumanTask{}, fmt.Errorf("%w: the request is %s", humantask.ErrPolicyBlocked, task.State)
	}
	var resolved string
	if err := transaction.QueryRowContext(ctx, `
		select human_response_id from human_response where human_task_id = $1
	`, task.ID).Scan(&resolved); err == nil {
		return humantask.HumanTask{}, fmt.Errorf("%w: the request is already resolved", humantask.ErrPolicyBlocked)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return humantask.HumanTask{}, fmt.Errorf("read existing response: %w", err)
	}
	response := mutation.Response
	payload, err := json.Marshal(response.Values)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("encode response payload: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into human_response (
			human_response_id, human_task_id, workspace_id, request_version,
			object_kind, object_id, object_version, actor_github_user_id, decision,
			payload, payload_digest, created_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, response.ResponseID, task.ID, task.WorkspaceID, response.RequestVersion,
		response.Object.Kind, response.Object.ID, response.Object.Version, response.ActorID,
		response.Decision, payload, response.PayloadDigest, response.CreatedAt); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("insert human response: %w", err)
	}
	continuation := mutation.Continuation
	if _, err := transaction.ExecContext(ctx, `
		insert into human_task_continuation (
			continuation_id, human_response_id, human_task_id, workspace_id, workflow_run_id, node_run_id,
			intent, state, created_at, consumed_at, consumed_by_github_user_id
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, continuation.ID, continuation.ResponseID, task.ID, task.WorkspaceID, continuation.RunID,
		continuation.NodeRunID, continuation.Intent, continuation.State, continuation.CreatedAt,
		nil, nil); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("insert human task continuation: %w", err)
	}
	task.State = humantask.StateResolved
	task.Version++
	task.UpdatedAt = response.CreatedAt
	task.ResolvedAt = &response.CreatedAt
	task.ResolvedByUserID = response.ActorID
	task.ResolutionKind = string(humantask.ModeKowaAnswer)
	task.Response = &response
	task.Continuation = &continuation
	if _, err := transaction.ExecContext(ctx, `
		update human_task set state = $2, version = $3, updated_at = $4, resolved_at = $4,
			resolved_by_github_user_id = $5, resolution_kind = $6
		where human_task_id = $1
	`, task.ID, string(task.State), task.Version, response.CreatedAt, response.ActorID,
		task.ResolutionKind); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("resolve human task: %w", err)
	}
	if err := saveCommand(ctx, transaction, mutation.ActorID, "humantask.answer",
		task.ID, mutation.RequestKey, mutation.RequestDigest, task, response.CreatedAt); err != nil {
		return humantask.HumanTask{}, err
	}
	workspaceID := task.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, response.ActorID,
		"humantask.answered", &task.Version,
		map[string]any{"humanTaskId": task.ID, "decision": response.Decision}); err != nil {
		return humantask.HumanTask{}, err
	}
	if err := transaction.Commit(); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("commit human answer: %w", err)
	}
	return task, nil
}

// ResolveHumanTaskFromFact settles an external_fact task; no response row is
// written, because no human answered inside Kowa.
func (s *Store) ResolveHumanTaskFromFact(ctx context.Context, mutation humantask.ResolveExternalFactMutation) (humantask.HumanTask, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("begin fact resolution transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed humantask.HumanTask
	replay, err := replayCommand(ctx, transaction, mutation.ActorID, "humantask.resolve_external",
		mutation.TaskID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return humantask.HumanTask{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("commit fact resolution replay: %w", err)
		}
		return replayed, nil
	}
	row := transaction.QueryRowContext(ctx, `select `+humanTaskColumns+`
		from human_task where human_task_id = $1 and workspace_id = $2 for update`,
		mutation.TaskID, mutation.WorkspaceID)
	var task humantask.HumanTask
	if err := scanHumanTask(row, &task); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return humantask.HumanTask{}, humantask.ErrNotFound
		}
		return humantask.HumanTask{}, fmt.Errorf("lock human task: %w", err)
	}
	if task.State != humantask.StateOpen {
		return humantask.HumanTask{}, fmt.Errorf("%w: the request is %s", humantask.ErrPolicyBlocked, task.State)
	}
	task.State = humantask.StateResolved
	task.Version++
	task.UpdatedAt = mutation.ResolvedAt
	task.ResolvedAt = &mutation.ResolvedAt
	task.ResolvedByUserID = mutation.ActorID
	task.ResolutionKind = string(humantask.ModeExternalFact)
	task.ExternalFactID = mutation.ObservationID
	if _, err := transaction.ExecContext(ctx, `
		update human_task set state = $2, version = $3, updated_at = $4, resolved_at = $4,
			resolved_by_github_user_id = $5, resolution_kind = $6, external_fact_id = $7
		where human_task_id = $1
	`, task.ID, string(task.State), task.Version, mutation.ResolvedAt, mutation.ActorID,
		task.ResolutionKind, mutation.ObservationID); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("settle human task: %w", err)
	}
	if err := saveCommand(ctx, transaction, mutation.ActorID, "humantask.resolve_external",
		task.ID, mutation.RequestKey, mutation.RequestDigest, task, mutation.ResolvedAt); err != nil {
		return humantask.HumanTask{}, err
	}
	workspaceID := task.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, mutation.ActorID,
		"humantask.external_fact_settled", &task.Version,
		map[string]any{"humanTaskId": task.ID, "observationId": mutation.ObservationID}); err != nil {
		return humantask.HumanTask{}, err
	}
	if err := transaction.Commit(); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("commit fact resolution: %w", err)
	}
	return task, nil
}

// SetHumanTaskState applies the fail-closed EXPIRED or CANCELED transition.
func (s *Store) SetHumanTaskState(ctx context.Context, mutation humantask.StateMutation) (humantask.HumanTask, error) {
	if mutation.State != humantask.StateExpired && mutation.State != humantask.StateCanceled {
		return humantask.HumanTask{}, humantask.ErrValidation
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("begin task state transaction: %w", err)
	}
	defer rollback(transaction)
	row := transaction.QueryRowContext(ctx, `select `+humanTaskColumns+`
		from human_task where human_task_id = $1 and workspace_id = $2 for update`,
		mutation.TaskID, mutation.WorkspaceID)
	var task humantask.HumanTask
	if err := scanHumanTask(row, &task); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return humantask.HumanTask{}, humantask.ErrNotFound
		}
		return humantask.HumanTask{}, fmt.Errorf("lock human task: %w", err)
	}
	if task.State != humantask.StateOpen {
		return humantask.HumanTask{}, fmt.Errorf("%w: the request is %s", humantask.ErrPolicyBlocked, task.State)
	}
	if _, err := transaction.ExecContext(ctx, `
		update human_task set state = $2, version = $3, updated_at = $4, expired_reason = $5
		where human_task_id = $1
	`, task.ID, string(mutation.State), task.Version+1, mutation.At, mutation.Reason); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("update human task state: %w", err)
	}
	task.State = mutation.State
	task.Version++
	task.UpdatedAt = mutation.At
	task.ExpiredReason = mutation.Reason
	workspaceID := task.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, mutation.ActorID,
		"humantask."+string(mutation.State), &task.Version,
		map[string]any{"humanTaskId": task.ID, "reason": mutation.Reason}); err != nil {
		return humantask.HumanTask{}, err
	}
	if err := transaction.Commit(); err != nil {
		return humantask.HumanTask{}, fmt.Errorf("commit task state: %w", err)
	}
	return task, nil
}

// ExpireHumanTasksBoundTo invalidates the OPEN tasks bound to the same object
// but an older version, with one statement so the expiry is atomic.
func (s *Store) ExpireHumanTasksBoundTo(
	ctx context.Context,
	workspaceID, objectKind, objectID, exceptVersion, reason string,
	at time.Time,
) (int, error) {
	result, err := s.database.ExecContext(ctx, `
		update human_task set state = 'EXPIRED', version = version + 1, updated_at = $4, expired_reason = $5
		where workspace_id = $1 and object_kind = $2 and object_id = $3
			and object_version <> $6 and state = 'OPEN'
	`, workspaceID, objectKind, objectID, at, reason, exceptVersion)
	if err != nil {
		return 0, fmt.Errorf("expire bound human tasks: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expired count: %w", err)
	}
	return int(affected), nil
}

const continuationColumns = `continuation_id, human_response_id, human_task_id, workspace_id,
	workflow_run_id, node_run_id, intent, state, created_at, consumed_at, consumed_by_github_user_id`

func scanContinuation(row interface {
	Scan(...any) error
}, continuation *humantask.Continuation) error {
	var nodeRunID, consumedBy sql.NullString
	var consumedAt sql.NullTime
	if err := row.Scan(&continuation.ID, &continuation.ResponseID, &continuation.TaskID,
		&continuation.WorkspaceID, &continuation.RunID, &nodeRunID, &continuation.Intent,
		&continuation.State, &continuation.CreatedAt, &consumedAt, &consumedBy); err != nil {
		return err
	}
	continuation.NodeRunID, continuation.ConsumedBy = nodeRunID.String, consumedBy.String
	if consumedAt.Valid {
		consumed := consumedAt.Time
		continuation.ConsumedAt = &consumed
	}
	return nil
}

// ReplayHumanTaskCommand returns one recorded task command outcome.
func (s *Store) ReplayHumanTaskCommand(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (humantask.HumanTask, bool, error) {
	var task humantask.HumanTask
	found, err := replayCommand(ctx, s.database, actor, commandType, targetID, requestKey, digest, &task)
	return task, found, err
}

// ReplayHumanContinuation returns one recorded continuation command outcome.
func (s *Store) ReplayHumanContinuation(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (humantask.Continuation, bool, error) {
	var continuation humantask.Continuation
	found, err := replayCommand(ctx, s.database, actor, commandType, targetID, requestKey, digest, &continuation)
	return continuation, found, err
}

// PendingHumanContinuations lists the accepted answers a run still has to take
// up, oldest first.
func (s *Store) PendingHumanContinuations(ctx context.Context, workspaceID, runID string) ([]humantask.Continuation, error) {
	query := `select ` + continuationColumns + `
		from human_task_continuation where workspace_id = $1 and state = 'PENDING'`
	arguments := []any{workspaceID}
	if runID != "" {
		query += ` and workflow_run_id = $2`
		arguments = append(arguments, runID)
	}
	query += ` order by created_at, continuation_id`
	rows, err := s.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list continuations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	continuations := []humantask.Continuation{}
	for rows.Next() {
		var continuation humantask.Continuation
		if err := scanContinuation(rows, &continuation); err != nil {
			return nil, fmt.Errorf("scan continuation: %w", err)
		}
		continuations = append(continuations, continuation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate continuations: %w", err)
	}
	return continuations, nil
}

// ConsumeHumanContinuation marks one continuation as taken up exactly once.
func (s *Store) ConsumeHumanContinuation(ctx context.Context, mutation humantask.ConsumeContinuationMutation) (humantask.Continuation, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return humantask.Continuation{}, fmt.Errorf("begin consume transaction: %w", err)
	}
	defer rollback(transaction)
	digest := humantask.Digest(mutation.ContinuationID)
	var replayed humantask.Continuation
	replay, err := replayCommand(ctx, transaction, mutation.ActorID, "humantask.consume_continuation",
		mutation.ContinuationID, "consume", digest, &replayed)
	if err != nil {
		return humantask.Continuation{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return humantask.Continuation{}, fmt.Errorf("commit consume replay: %w", err)
		}
		return replayed, nil
	}
	row := transaction.QueryRowContext(ctx, `select `+continuationColumns+`
		from human_task_continuation where continuation_id = $1 and workspace_id = $2 for update`,
		mutation.ContinuationID, mutation.WorkspaceID)
	var continuation humantask.Continuation
	if err := scanContinuation(row, &continuation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return humantask.Continuation{}, humantask.ErrNotFound
		}
		return humantask.Continuation{}, fmt.Errorf("lock continuation: %w", err)
	}
	if continuation.State == humantask.ContinuationConsumed {
		if err := transaction.Commit(); err != nil {
			return humantask.Continuation{}, fmt.Errorf("commit consumed continuation: %w", err)
		}
		return continuation, nil
	}
	if _, err := transaction.ExecContext(ctx, `
		update human_task_continuation set state = 'CONSUMED', consumed_at = $2, consumed_by_github_user_id = $3
		where continuation_id = $1
	`, continuation.ID, mutation.At, mutation.ActorID); err != nil {
		return humantask.Continuation{}, fmt.Errorf("consume continuation: %w", err)
	}
	continuation.State = humantask.ContinuationConsumed
	consumedAt := mutation.At
	continuation.ConsumedAt = &consumedAt
	continuation.ConsumedBy = mutation.ActorID
	if err := saveCommand(ctx, transaction, mutation.ActorID, "humantask.consume_continuation",
		continuation.ID, "consume", digest, continuation, mutation.At); err != nil {
		return humantask.Continuation{}, err
	}
	if err := transaction.Commit(); err != nil {
		return humantask.Continuation{}, fmt.Errorf("commit continuation: %w", err)
	}
	return continuation, nil
}

// Integration with execution: the delivery-run success transition used by the
// closure transaction.
func completeRunInTransaction(
	ctx context.Context,
	transaction *sql.Tx,
	runID string,
	expectedVersion int64,
	at time.Time,
) (execution.Run, error) {
	row := transaction.QueryRowContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, started_by_github_user_id,
			coalesce(git_execution_user_id, ''), definition_name, definition_version, definition_digest,
			state, version, created_at, updated_at
		from workflow_run where workflow_run_id = $1 for update
	`, runID)
	var run execution.Run
	if err := row.Scan(&run.ID, &run.WorkspaceID, &run.WorkItemID, &run.StartedByGitHubUserID,
		&run.GitExecutionUserID, &run.DefinitionName, &run.DefinitionVersion, &run.DefinitionDigest,
		&run.State, &run.Version, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return execution.Run{}, execution.ErrNotFound
		}
		return execution.Run{}, fmt.Errorf("lock delivery run: %w", err)
	}
	if err := execution.CompletionAllowed(run); err != nil {
		return execution.Run{}, fmt.Errorf("%w: %v", evaluation.ErrPolicyBlocked, err)
	}
	if expectedVersion != 0 && run.Version != expectedVersion {
		return execution.Run{}, evaluation.ErrVersionConflict
	}
	if _, err := transaction.ExecContext(ctx, `
		update workflow_run set state = 'SUCCEEDED', version = version + 1, updated_at = $2
		where workflow_run_id = $1
	`, runID, at); err != nil {
		return execution.Run{}, fmt.Errorf("complete delivery run: %w", err)
	}
	run.State = execution.RunSucceeded
	run.Version++
	run.UpdatedAt = at
	return run, nil
}

// RecordVerificationEvidence persists one verification fact.
func (s *Store) RecordVerificationEvidence(
	ctx context.Context,
	verification evaluation.Verification,
	requestKey, requestDigest string,
) (evaluation.Verification, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return evaluation.Verification{}, fmt.Errorf("begin verification transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed evaluation.Verification
	replay, err := replayCommand(ctx, transaction, verification.ProducerID, "evaluation.record_verification",
		verification.WorkItemID, requestKey, requestDigest, &replayed)
	if err != nil {
		return evaluation.Verification{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return evaluation.Verification{}, fmt.Errorf("commit verification replay: %w", err)
		}
		return replayed, nil
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into verification_evidence (
			verification_id, workspace_id, work_item_id, workflow_run_id, node_run_id,
			kind, subject_kind, repository_id, commit_oid, artifact_id, artifact_digest,
			configuration_version, producer_kind, producer_id, source, result,
			report_artifact_id, report_digest, observed_at, created_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`, verification.VerificationID, verification.WorkspaceID, verification.WorkItemID,
		nullIfEmpty(verification.RunID), nullIfEmpty(verification.NodeRunID), verification.Kind,
		verification.SubjectKind, nullIfEmpty(verification.RepositoryID), nullIfEmpty(verification.CommitOID),
		nullIfEmpty(verification.ArtifactID), nullIfEmpty(verification.ArtifactDigest),
		verification.ConfigurationVersion, verification.ProducerKind, verification.ProducerID,
		verification.Source, verification.Result, nullIfEmpty(verification.ReportArtifactID),
		nullIfEmpty(verification.ReportDigest), verification.ObservedAt, verification.CreatedAt); err != nil {
		return evaluation.Verification{}, fmt.Errorf("insert verification evidence: %w", err)
	}
	if err := saveCommand(ctx, transaction, verification.ProducerID, "evaluation.record_verification",
		verification.WorkItemID, requestKey, requestDigest, verification, time.Now().UTC()); err != nil {
		return evaluation.Verification{}, err
	}
	workspaceID := verification.WorkspaceID
	if err := insertAudit(ctx, transaction, &workspaceID, verification.ProducerID,
		"evaluation.verification_recorded", nil,
		map[string]any{"verificationId": verification.VerificationID, "kind": verification.Kind,
			"result": verification.Result, "source": verification.Source}); err != nil {
		return evaluation.Verification{}, err
	}
	if err := transaction.Commit(); err != nil {
		return evaluation.Verification{}, fmt.Errorf("commit verification: %w", err)
	}
	return verification, nil
}

// VerificationEvidence reads one verification row inside its workspace.
func (s *Store) VerificationEvidence(ctx context.Context, workspaceID, verificationID string) (evaluation.Verification, bool, error) {
	row := s.database.QueryRowContext(ctx, `
		select verification_id, workspace_id, work_item_id, coalesce(workflow_run_id, ''),
			coalesce(node_run_id, ''), kind, subject_kind, coalesce(repository_id, ''),
			coalesce(commit_oid, ''), coalesce(artifact_id, ''), coalesce(artifact_digest, ''),
			configuration_version, producer_kind, producer_id, source, result,
			coalesce(report_artifact_id, ''), coalesce(report_digest, ''), observed_at, created_at
		from verification_evidence where verification_id = $1 and workspace_id = $2
	`, verificationID, workspaceID)
	var verification evaluation.Verification
	if err := row.Scan(&verification.VerificationID, &verification.WorkspaceID, &verification.WorkItemID,
		&verification.RunID, &verification.NodeRunID, &verification.Kind, &verification.SubjectKind,
		&verification.RepositoryID, &verification.CommitOID, &verification.ArtifactID,
		&verification.ArtifactDigest, &verification.ConfigurationVersion, &verification.ProducerKind,
		&verification.ProducerID, &verification.Source, &verification.Result,
		&verification.ReportArtifactID, &verification.ReportDigest, &verification.ObservedAt,
		&verification.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return evaluation.Verification{}, false, nil
		}
		return evaluation.Verification{}, false, fmt.Errorf("read verification: %w", err)
	}
	return verification, true, nil
}

// VerificationsForWorkItem lists the verification rows of one work item.
func (s *Store) VerificationsForWorkItem(ctx context.Context, workspaceID, workItemID string) ([]evaluation.Verification, error) {
	rows, err := s.database.QueryContext(ctx, `
		select verification_id from verification_evidence
		where workspace_id = $1 and work_item_id = $2 order by created_at, verification_id
	`, workspaceID, workItemID)
	if err != nil {
		return nil, fmt.Errorf("list verification ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan verification id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate verification ids: %w", err)
	}
	verifications := make([]evaluation.Verification, 0, len(ids))
	for _, id := range ids {
		verification, found, err := s.VerificationEvidence(ctx, workspaceID, id)
		if err != nil {
			return nil, err
		}
		if found {
			verifications = append(verifications, verification)
		}
	}
	return verifications, nil
}

// CloseDeliveryWorkItem commits the run success, the WorkItem close and the
// closure record in one transaction: no observer can see one without the other.
func (s *Store) CloseDeliveryWorkItem(ctx context.Context, mutation evaluation.CloseMutation) (evaluation.ClosureRecord, error) {
	record := mutation.Record
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return evaluation.ClosureRecord{}, fmt.Errorf("begin closure transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed evaluation.ClosureRecord
	replay, err := replayCommand(ctx, transaction, mutation.ActorID, "evaluation.close",
		record.WorkItemID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return evaluation.ClosureRecord{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return evaluation.ClosureRecord{}, fmt.Errorf("commit closure replay: %w", err)
		}
		return replayed, nil
	}
	item, err := lockWorkItem(ctx, transaction, record.WorkspaceID, record.WorkItemID)
	if err != nil {
		return evaluation.ClosureRecord{}, err
	}
	if item.State != workitem.StateOpen {
		return evaluation.ClosureRecord{}, fmt.Errorf("%w: the work item is %s", evaluation.ErrPolicyBlocked, item.State)
	}
	if item.Version != mutation.ExpectedWorkItemVersion {
		return evaluation.ClosureRecord{}, evaluation.ErrVersionConflict
	}
	if item.CurrentRunID != record.RunID {
		return evaluation.ClosureRecord{}, fmt.Errorf(
			"%w: the closure must bind the current delivery run of the work item", evaluation.ErrPolicyBlocked)
	}
	if _, err := completeRunInTransaction(ctx, transaction, record.RunID, mutation.ExpectedRunVersion, mutation.ClosedAt); err != nil {
		return evaluation.ClosureRecord{}, err
	}
	testIDs, err := json.Marshal(record.RequiredTestIDs)
	if err != nil {
		return evaluation.ClosureRecord{}, fmt.Errorf("encode required test ids: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into closure_evidence (
			closure_id, workspace_id, work_item_id, workflow_run_id,
			definition_name, definition_version, definition_digest, policy_version, requirement_digest,
			knowledge_snapshot_id, knowledge_binding_event_id,
			merge_repository_id, merge_commit_oid, pull_request_number,
			merge_observation_id, review_observation_id, review_head_oid, reviewer_github_user_id,
			required_test_ids, independent_review_id, post_merge_verification_id,
			checklist_digest, created_by_github_user_id, created_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22, $23, $24)
	`, record.ClosureID, record.WorkspaceID, record.WorkItemID, record.RunID,
		record.DefinitionName, record.DefinitionVersion, record.DefinitionDigest, record.PolicyVersion,
		record.RequirementDigest, nullIfEmpty(record.KnowledgeSnapshotID), nullIfEmpty(record.KnowledgeBindingID),
		record.MergeRepositoryID, record.MergeCommitOID, record.PullRequestNumber,
		record.MergeObservationID, record.ReviewObservationID, record.ReviewHeadOID,
		record.ReviewerGitHubUserID, testIDs, record.IndependentReviewID,
		record.PostMergeVerificationID, record.ChecklistDigest, record.CreatedByGitHubUserID,
		record.CreatedAt); err != nil {
		return evaluation.ClosureRecord{}, fmt.Errorf("insert closure evidence: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		update work_item set state = 'CLOSED', version = version + 1, updated_at = $2, closed_at = $2
		where work_item_id = $1
	`, item.ID, mutation.ClosedAt); err != nil {
		return evaluation.ClosureRecord{}, fmt.Errorf("close work item: %w", err)
	}
	item.State = workitem.StateClosed
	item.Version++
	item.UpdatedAt = mutation.ClosedAt
	closedAt := mutation.ClosedAt
	item.ClosedAt = &closedAt
	if err := saveCommand(ctx, transaction, mutation.ActorID, "evaluation.close",
		item.ID, mutation.RequestKey, mutation.RequestDigest, record, mutation.ClosedAt); err != nil {
		return evaluation.ClosureRecord{}, err
	}
	workspaceID := record.WorkspaceID
	version := item.Version
	if err := insertAudit(ctx, transaction, &workspaceID, mutation.ActorID,
		"workitem.closed", &version,
		map[string]any{"workItemId": item.ID, "closureId": record.ClosureID, "mergeCommitOid": record.MergeCommitOID}); err != nil {
		return evaluation.ClosureRecord{}, err
	}
	if err := transaction.Commit(); err != nil {
		return evaluation.ClosureRecord{}, fmt.Errorf("commit closure: %w", err)
	}
	return record, nil
}

// ClosureEvidenceFor reads the closure record of one work item.
func (s *Store) ClosureEvidenceFor(ctx context.Context, workspaceID, workItemID string) (evaluation.ClosureRecord, bool, error) {
	row := s.database.QueryRowContext(ctx, `
		select closure_id, workspace_id, work_item_id, workflow_run_id,
			definition_name, definition_version, definition_digest, policy_version, requirement_digest,
			coalesce(knowledge_snapshot_id, ''), coalesce(knowledge_binding_event_id, ''),
			merge_repository_id, merge_commit_oid, pull_request_number,
			merge_observation_id, review_observation_id, review_head_oid, reviewer_github_user_id,
			required_test_ids, independent_review_id, post_merge_verification_id,
			checklist_digest, created_by_github_user_id, created_at
		from closure_evidence where work_item_id = $1 and workspace_id = $2
	`, workItemID, workspaceID)
	var record evaluation.ClosureRecord
	var testIDs []byte
	if err := row.Scan(&record.ClosureID, &record.WorkspaceID, &record.WorkItemID, &record.RunID,
		&record.DefinitionName, &record.DefinitionVersion, &record.DefinitionDigest, &record.PolicyVersion,
		&record.RequirementDigest, &record.KnowledgeSnapshotID, &record.KnowledgeBindingID,
		&record.MergeRepositoryID, &record.MergeCommitOID, &record.PullRequestNumber,
		&record.MergeObservationID, &record.ReviewObservationID, &record.ReviewHeadOID,
		&record.ReviewerGitHubUserID, &testIDs, &record.IndependentReviewID,
		&record.PostMergeVerificationID, &record.ChecklistDigest, &record.CreatedByGitHubUserID,
		&record.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return evaluation.ClosureRecord{}, false, nil
		}
		return evaluation.ClosureRecord{}, false, fmt.Errorf("read closure evidence: %w", err)
	}
	if err := json.Unmarshal(testIDs, &record.RequiredTestIDs); err != nil {
		return evaluation.ClosureRecord{}, false, fmt.Errorf("decode required test ids: %w", err)
	}
	return record, true, nil
}

// CreateExternalOperation persists one admitted operation.
func (s *Store) CreateExternalOperation(ctx context.Context, mutation githubdelivery.AdmitMutation) (githubdelivery.ExternalOperation, error) {
	operation := mutation.Operation
	request, err := json.Marshal(operation.Request)
	if err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("encode operation request: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("begin operation transaction: %w", err)
	}
	defer rollback(transaction)
	var replayed githubdelivery.ExternalOperation
	replay, err := replayCommand(ctx, transaction, operation.Request.RequestedByGitHubUserID,
		"delivery.admit."+string(operation.Kind), operation.WorkItemID, mutation.RequestKey, mutation.RequestDigest, &replayed)
	if err != nil {
		return githubdelivery.ExternalOperation{}, err
	}
	if replay {
		if err := transaction.Commit(); err != nil {
			return githubdelivery.ExternalOperation{}, fmt.Errorf("commit operation replay: %w", err)
		}
		return replayed, nil
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into external_operation (
			operation_id, workspace_id, work_item_id, workflow_run_id, node_run_id, task_id,
			kind, state, repository_id, ref, base_ref, commit_oid,
			expected_github_user_id, requested_by_github_user_id,
			request, request_digest, request_key, result, unknown_reason,
			version, created_at, updated_at, settled_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22, $23)
	`, operation.OperationID, operation.WorkspaceID, operation.WorkItemID, operation.RunID,
		nullIfEmpty(operation.NodeRunID), nullIfEmpty(operation.TaskID), string(operation.Kind),
		string(operation.State), operation.Request.RepositoryID, operation.Request.Ref,
		nullIfEmpty(operation.Request.BaseRef), operation.Request.CommitOID,
		operation.Request.ExpectedGitHubUserID, operation.Request.RequestedByGitHubUserID,
		request, operation.RequestDigest, mutation.RequestKey, nil, nil,
		operation.Version, operation.CreatedAt, operation.UpdatedAt, nil); err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("insert external operation: %w", err)
	}
	if err := saveCommand(ctx, transaction, operation.Request.RequestedByGitHubUserID,
		"delivery.admit."+string(operation.Kind), operation.WorkItemID, mutation.RequestKey,
		mutation.RequestDigest, operation, time.Now().UTC()); err != nil {
		return githubdelivery.ExternalOperation{}, err
	}
	workspaceID := operation.WorkspaceID
	actor := operation.Request.RequestedByGitHubUserID
	if err := insertAudit(ctx, transaction, &workspaceID, actor, "delivery.operation_admitted", &operation.Version,
		map[string]any{"operationId": operation.OperationID, "kind": string(operation.Kind),
			"repositoryId": operation.Request.RepositoryID, "ref": operation.Request.Ref,
			"commitOid": operation.Request.CommitOID}); err != nil {
		return githubdelivery.ExternalOperation{}, err
	}
	if err := transaction.Commit(); err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("commit operation: %w", err)
	}
	return operation, nil
}

const operationColumns = `operation_id, workspace_id, work_item_id, workflow_run_id, node_run_id, task_id,
	kind, state, request, request_digest, result, unknown_reason, version, created_at, updated_at, settled_at`

func scanOperation(row interface {
	Scan(...any) error
}, operation *githubdelivery.ExternalOperation) error {
	var request, result []byte
	var nodeRunID, taskID, unknownReason sql.NullString
	var settledAt sql.NullTime
	if err := row.Scan(&operation.OperationID, &operation.WorkspaceID, &operation.WorkItemID,
		&operation.RunID, &nodeRunID, &taskID, &operation.Kind, &operation.State,
		&request, &operation.RequestDigest, &result, &unknownReason,
		&operation.Version, &operation.CreatedAt, &operation.UpdatedAt, &settledAt); err != nil {
		return err
	}
	if err := json.Unmarshal(request, &operation.Request); err != nil {
		return fmt.Errorf("decode operation request: %w", err)
	}
	if len(result) > 0 {
		var value githubdelivery.OperationResult
		if err := json.Unmarshal(result, &value); err != nil {
			return fmt.Errorf("decode operation result: %w", err)
		}
		operation.Result = &value
	}
	operation.NodeRunID, operation.TaskID = nodeRunID.String, taskID.String
	operation.UnknownReason = unknownReason.String
	if settledAt.Valid {
		settled := settledAt.Time
		operation.SettledAt = &settled
	}
	return nil
}

// ExternalOperation reads one operation inside its workspace.
func (s *Store) ExternalOperation(ctx context.Context, workspaceID, operationID string) (githubdelivery.ExternalOperation, error) {
	row := s.database.QueryRowContext(ctx, `select `+operationColumns+`
		from external_operation where operation_id = $1 and workspace_id = $2`, operationID, workspaceID)
	var operation githubdelivery.ExternalOperation
	if err := scanOperation(row, &operation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return githubdelivery.ExternalOperation{}, githubdelivery.ErrNotFound
		}
		return githubdelivery.ExternalOperation{}, fmt.Errorf("read external operation: %w", err)
	}
	return operation, nil
}

// ExternalOperations lists the operations of one work item.
func (s *Store) ExternalOperations(ctx context.Context, workspaceID, workItemID string) ([]githubdelivery.ExternalOperation, error) {
	rows, err := s.database.QueryContext(ctx, `select `+operationColumns+`
		from external_operation where workspace_id = $1 and work_item_id = $2
		order by created_at, operation_id`, workspaceID, workItemID)
	if err != nil {
		return nil, fmt.Errorf("list external operations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	operations := []githubdelivery.ExternalOperation{}
	for rows.Next() {
		var operation githubdelivery.ExternalOperation
		if err := scanOperation(rows, &operation); err != nil {
			return nil, fmt.Errorf("scan external operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate external operations: %w", err)
	}
	return operations, nil
}

// ExternalOperationForRequest finds the operation admitted for one request
// identity, so a second write for the same head is never admitted by accident.
func (s *Store) ExternalOperationForRequest(
	ctx context.Context,
	workspaceID string,
	kind githubdelivery.OperationKind,
	repositoryID, ref, commitOID string,
) (githubdelivery.ExternalOperation, bool, error) {
	row := s.database.QueryRowContext(ctx, `select `+operationColumns+`
		from external_operation
		where workspace_id = $1 and kind = $2 and repository_id = $3 and ref = $4 and commit_oid = $5
		order by created_at desc, operation_id desc limit 1`,
		workspaceID, string(kind), repositoryID, ref, commitOID)
	var operation githubdelivery.ExternalOperation
	if err := scanOperation(row, &operation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return githubdelivery.ExternalOperation{}, false, nil
		}
		return githubdelivery.ExternalOperation{}, false, fmt.Errorf("read operation for request: %w", err)
	}
	return operation, true, nil
}

// UpdateExternalOperation applies one versioned operation transition.
func (s *Store) UpdateExternalOperation(ctx context.Context, mutation githubdelivery.UpdateMutation) (githubdelivery.ExternalOperation, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("begin operation update: %w", err)
	}
	defer rollback(transaction)
	row := transaction.QueryRowContext(ctx, `select `+operationColumns+`
		from external_operation where operation_id = $1 and workspace_id = $2 for update`,
		mutation.OperationID, mutation.WorkspaceID)
	var operation githubdelivery.ExternalOperation
	if err := scanOperation(row, &operation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return githubdelivery.ExternalOperation{}, githubdelivery.ErrNotFound
		}
		return githubdelivery.ExternalOperation{}, fmt.Errorf("lock external operation: %w", err)
	}
	if operation.Version != mutation.ExpectedVersion {
		return githubdelivery.ExternalOperation{}, githubdelivery.ErrVersionConflict
	}
	var result []byte
	if mutation.Result != nil {
		encoded, err := json.Marshal(mutation.Result)
		if err != nil {
			return githubdelivery.ExternalOperation{}, fmt.Errorf("encode operation result: %w", err)
		}
		result = encoded
	}
	settledAt := any(nil)
	if mutation.State == githubdelivery.StateSucceeded || mutation.State == githubdelivery.StateFailed {
		settledAt = mutation.At
	}
	if _, err := transaction.ExecContext(ctx, `
		update external_operation set state = $2, result = coalesce($3, result), unknown_reason = $4,
			version = version + 1, updated_at = $5, settled_at = $6
		where operation_id = $1
	`, operation.OperationID, string(mutation.State), result, nullIfEmpty(mutation.UnknownReason),
		mutation.At, settledAt); err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("update external operation: %w", err)
	}
	operation.State = mutation.State
	if mutation.Result != nil {
		operation.Result = mutation.Result
	}
	operation.UnknownReason = mutation.UnknownReason
	operation.Version++
	operation.UpdatedAt = mutation.At
	if settledAt != nil {
		settled := mutation.At
		operation.SettledAt = &settled
	}
	workspaceID := operation.WorkspaceID
	actor := operation.Request.RequestedByGitHubUserID
	if err := insertAudit(ctx, transaction, &workspaceID, actor, "delivery.operation_"+string(mutation.State),
		&operation.Version, map[string]any{"operationId": operation.OperationID, "state": string(mutation.State)}); err != nil {
		return githubdelivery.ExternalOperation{}, err
	}
	if err := transaction.Commit(); err != nil {
		return githubdelivery.ExternalOperation{}, fmt.Errorf("commit operation update: %w", err)
	}
	return operation, nil
}

// RecordGitHubObservation appends one observation; unknown attempts never
// replace a known fact.
func (s *Store) RecordGitHubObservation(ctx context.Context, mutation githubdelivery.RecordObservationMutation) (githubdelivery.Observation, error) {
	observation := mutation.Observation
	encoded, err := json.Marshal(observation)
	if err != nil {
		return githubdelivery.Observation{}, fmt.Errorf("encode observation: %w", err)
	}
	var fact any
	if !observation.Unknown {
		fact = encoded
	}
	if _, err := s.database.ExecContext(ctx, `
		insert into github_observation (
			observation_id, workspace_id, repository_id, subject_kind, subject_key,
			observed_at, actor_github_user_id, unknown, unknown_reason, fact
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		on conflict (observation_id) do nothing
	`, observation.ObservationID, observation.WorkspaceID, observation.RepositoryID,
		observation.SubjectKind, observation.SubjectKey, observation.ObservedAt,
		nullIfEmpty(observation.ActorID), observation.Unknown, nullIfEmpty(observation.UnknownReason),
		fact); err != nil {
		return githubdelivery.Observation{}, fmt.Errorf("insert observation: %w", err)
	}
	return observation, nil
}

// GitHubObservation reads one observation inside its workspace.
func (s *Store) GitHubObservation(ctx context.Context, workspaceID, observationID string) (githubdelivery.Observation, bool, error) {
	row := s.database.QueryRowContext(ctx, `
		select observation_id, workspace_id, repository_id, subject_kind, subject_key,
			observed_at, coalesce(actor_github_user_id, ''), unknown, coalesce(unknown_reason, ''),
			coalesce(fact, '{}'::jsonb)
		from github_observation where observation_id = $1 and workspace_id = $2
	`, observationID, workspaceID)
	var observation githubdelivery.Observation
	var fact []byte
	if err := row.Scan(&observation.ObservationID, &observation.WorkspaceID, &observation.RepositoryID,
		&observation.SubjectKind, &observation.SubjectKey, &observation.ObservedAt, &observation.ActorID,
		&observation.Unknown, &observation.UnknownReason, &fact); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return githubdelivery.Observation{}, false, nil
		}
		return githubdelivery.Observation{}, false, fmt.Errorf("read observation: %w", err)
	}
	if !observation.Unknown {
		if err := json.Unmarshal(fact, &observation); err != nil {
			return githubdelivery.Observation{}, false, fmt.Errorf("decode observation: %w", err)
		}
	}
	return observation, true, nil
}

// LatestGitHubObservations returns the newest observation per subject.
func (s *Store) LatestGitHubObservations(ctx context.Context, workspaceID, repositoryID string) ([]githubdelivery.Observation, error) {
	rows, err := s.database.QueryContext(ctx, `
		select distinct on (subject_kind, subject_key) observation_id
		from github_observation
		where workspace_id = $1 and repository_id = $2
		order by subject_kind, subject_key, observed_at desc, observation_id desc
	`, workspaceID, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("list latest observation ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan observation id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate observation ids: %w", err)
	}
	observations := make([]githubdelivery.Observation, 0, len(ids))
	for _, id := range ids {
		observation, found, err := s.GitHubObservation(ctx, workspaceID, id)
		if err != nil {
			return nil, err
		}
		if found {
			observations = append(observations, observation)
		}
	}
	return observations, nil
}

// UnsettledExternalOperations counts the operations whose outcome is open.
func (s *Store) UnsettledExternalOperations(ctx context.Context, workspaceID, workItemID string) (int, error) {
	var count int
	if err := s.database.QueryRowContext(ctx, `
		select count(*) from external_operation
		where workspace_id = $1 and work_item_id = $2 and state not in ('SUCCEEDED', 'FAILED')
	`, workspaceID, workItemID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unsettled operations: %w", err)
	}
	return count, nil
}
