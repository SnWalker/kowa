package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SnWalker/kowa/internal/orchestration"
)

// This file implements the orchestration store of backend:S06 over
// `run_admission`, `knowledge_recommendation`, `knowledge_candidate`,
// `task_input_binding` and `task_input_artifact`.

func (s *Store) Close() error { return nil }

// AdmitOnce persists one admission together with its command idempotency record
// in a single transaction.
func (s *Store) AdmitOnce(
	ctx context.Context,
	admission orchestration.Admission,
	actor, requestKey, requestDigest string,
) (orchestration.Admission, bool, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return orchestration.Admission{}, false, fmt.Errorf("begin admission transaction: %w", err)
	}
	defer rollback(transaction)
	if existing, found, err := lookupAdmissionIdempotency(ctx, transaction, actor, admission.WorkItemID, requestKey, requestDigest); err != nil || found {
		return existing, found, err
	}
	_, err = transaction.ExecContext(ctx, `
		insert into run_admission (
			workflow_run_id, workspace_id, work_item_id, definition_name, definition_version,
			definition_digest, project_repository_id, knowledge_repository_id, project_commit_oid,
			knowledge_commit_oid, project_branch_ref, working_branch_ref, provider_id, provider_version,
			policy_version, admitted_by_github_user_id, admitted_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
	`, admission.RunID, admission.WorkspaceID, admission.WorkItemID, admission.DefinitionName,
		admission.DefinitionVersion, admission.DefinitionDigest, admission.ProjectRepositoryID,
		admission.KnowledgeRepositoryID, admission.ProjectCommitOID, admission.KnowledgeCommitOID,
		admission.ProjectBranchRef, admission.WorkingBranchRef, admission.ProviderID,
		admission.ProviderVersion, admission.PolicyVersion, admission.AdmittedByUserID, admission.AdmittedAt)
	if err != nil {
		return orchestration.Admission{}, false, fmt.Errorf("insert run admission: %w", err)
	}
	if err := recordCommandIdempotency(ctx, transaction, actor,
		orchestration.CommandAdmit, admission.WorkItemID, requestKey, requestDigest, admission); err != nil {
		return orchestration.Admission{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return orchestration.Admission{}, false, fmt.Errorf("commit admission transaction: %w", err)
	}
	return admission, false, nil
}

// ReplayedAdmission answers an identical admission command before any new run
// is materialised.
func (s *Store) ReplayedAdmission(
	ctx context.Context,
	actor, workItemID, requestKey, requestDigest string,
) (orchestration.Admission, bool, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return orchestration.Admission{}, false, fmt.Errorf("begin admission lookup: %w", err)
	}
	defer rollback(transaction)
	admission, found, err := lookupAdmissionIdempotency(ctx, transaction, actor, workItemID, requestKey, requestDigest)
	if err != nil || !found {
		return orchestration.Admission{}, found, err
	}
	return admission, true, nil
}

func lookupAdmissionIdempotency(
	ctx context.Context,
	transaction *sql.Tx,
	actor, workItemID, requestKey, requestDigest string,
) (orchestration.Admission, bool, error) {
	var storedDigest string
	var response []byte
	err := transaction.QueryRowContext(ctx, `
		select request_digest, response
		from command_idempotency
		where actor_github_user_id = $1 and command_type = $2 and target_id = $3 and request_key = $4
	`, actor, orchestration.CommandAdmit, workItemID, requestKey).Scan(&storedDigest, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.Admission{}, false, nil
	}
	if err != nil {
		return orchestration.Admission{}, false, fmt.Errorf("query admission idempotency: %w", err)
	}
	if storedDigest != requestDigest {
		return orchestration.Admission{}, false, orchestration.ErrVersionConflict
	}
	var admission orchestration.Admission
	if err := json.Unmarshal(response, &admission); err != nil {
		return orchestration.Admission{}, false, fmt.Errorf("decode the admitted run: %w", err)
	}
	return admission, true, nil
}

func recordCommandIdempotency(
	ctx context.Context,
	transaction *sql.Tx,
	actor, commandType, targetID, requestKey, requestDigest string,
	response any,
) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode the recorded command: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into command_idempotency (
			actor_github_user_id, command_type, target_id, request_key, request_digest, response, created_at
		) values ($1, $2, $3, $4, $5, $6, $7)
	`, actor, commandType, targetID, requestKey, requestDigest, encoded, time.Now().UTC()); err != nil {
		return fmt.Errorf("insert command idempotency: %w", err)
	}
	return nil
}

// Admission reads one frozen admission.
func (s *Store) Admission(ctx context.Context, runID string) (orchestration.Admission, error) {
	var admission orchestration.Admission
	err := s.database.QueryRowContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, definition_name, definition_version,
			definition_digest, project_repository_id, knowledge_repository_id, project_commit_oid,
			knowledge_commit_oid, project_branch_ref, working_branch_ref, provider_id, provider_version,
			policy_version, admitted_by_github_user_id, admitted_at
		from run_admission where workflow_run_id = $1
	`, runID).Scan(&admission.RunID, &admission.WorkspaceID, &admission.WorkItemID,
		&admission.DefinitionName, &admission.DefinitionVersion, &admission.DefinitionDigest,
		&admission.ProjectRepositoryID, &admission.KnowledgeRepositoryID, &admission.ProjectCommitOID,
		&admission.KnowledgeCommitOID, &admission.ProjectBranchRef, &admission.WorkingBranchRef,
		&admission.ProviderID, &admission.ProviderVersion, &admission.PolicyVersion,
		&admission.AdmittedByUserID, &admission.AdmittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.Admission{}, fmt.Errorf("%w: no admission for run %s", orchestration.ErrNotFound, runID)
	}
	if err != nil {
		return orchestration.Admission{}, fmt.Errorf("read run admission: %w", err)
	}
	return admission, nil
}

// Recommend persists one knowledge recommendation with its candidates.
func (s *Store) Recommend(
	ctx context.Context,
	recommendation orchestration.Recommendation,
) (orchestration.Recommendation, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("begin recommendation transaction: %w", err)
	}
	defer rollback(transaction)
	existing, found, err := readRecommendation(ctx, transaction, recommendation.RunID)
	if err != nil {
		return orchestration.Recommendation{}, err
	}
	if found {
		if existing.IndexDigest != recommendation.IndexDigest ||
			existing.KnowledgeCommitOID != recommendation.KnowledgeCommitOID {
			return orchestration.Recommendation{}, orchestration.ErrVersionConflict
		}
		return existing, nil
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into knowledge_recommendation (
			workflow_run_id, workspace_id, work_item_id, knowledge_repository_id, knowledge_commit_oid,
			index_path, index_digest, state, human_task_id, recommended_at
		) values ($1, $2, $3, $4, $5, $6, $7, 'RECOMMENDED', $8, $9)
	`, recommendation.RunID, recommendation.WorkspaceID, recommendation.WorkItemID,
		recommendation.KnowledgeRepositoryID, recommendation.KnowledgeCommitOID,
		recommendation.IndexPath, recommendation.IndexDigest,
		humanTaskID(recommendation.HumanTaskID), recommendation.RecommendedAt); err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("insert knowledge recommendation: %w", err)
	}
	for position, candidate := range recommendation.Candidates {
		if _, err := transaction.ExecContext(ctx, `
			insert into knowledge_candidate (workflow_run_id, position, path, digest, scope, required)
			values ($1, $2, $3, $4, $5, $6)
		`, recommendation.RunID, position, candidate.Path, candidate.Digest, candidate.Scope,
			candidate.Required); err != nil {
			return orchestration.Recommendation{}, fmt.Errorf("insert knowledge candidate: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("commit recommendation transaction: %w", err)
	}
	return recommendation, nil
}

// Recommendation reads one run's recommendation.
func (s *Store) Recommendation(ctx context.Context, runID string) (orchestration.Recommendation, error) {
	recommendation, found, err := readRecommendation(ctx, s.database, runID)
	if err != nil {
		return orchestration.Recommendation{}, err
	}
	if !found {
		return orchestration.Recommendation{}, fmt.Errorf(
			"%w: no knowledge recommendation for run %s", orchestration.ErrNotFound, runID)
	}
	return recommendation, nil
}

// recommendationQuerier covers both the pool and one transaction.
type recommendationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readRecommendation(
	ctx context.Context,
	querier recommendationQuerier,
	runID string,
) (orchestration.Recommendation, bool, error) {
	var recommendation orchestration.Recommendation
	var snapshotID, bindingEventID, effectiveInputID, humanTaskID sql.NullString
	var confirmedAt sql.NullTime
	err := querier.QueryRowContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, knowledge_repository_id,
			knowledge_commit_oid, index_path, index_digest, state, human_task_id,
			snapshot_id, binding_event_id, effective_input_id, recommended_at, confirmed_at
		from knowledge_recommendation where workflow_run_id = $1
	`, runID).Scan(&recommendation.RunID, &recommendation.WorkspaceID, &recommendation.WorkItemID,
		&recommendation.KnowledgeRepositoryID, &recommendation.KnowledgeCommitOID,
		&recommendation.IndexPath, &recommendation.IndexDigest, &recommendation.State,
		&humanTaskID, &snapshotID, &bindingEventID, &effectiveInputID,
		&recommendation.RecommendedAt, &confirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.Recommendation{}, false, nil
	}
	if err != nil {
		return orchestration.Recommendation{}, false, fmt.Errorf("read knowledge recommendation: %w", err)
	}
	recommendation.HumanTaskID = humanTaskID.String
	recommendation.SnapshotID = snapshotID.String
	recommendation.BindingEventID = bindingEventID.String
	recommendation.EffectiveInputID = effectiveInputID.String
	if confirmedAt.Valid {
		at := confirmedAt.Time
		recommendation.ConfirmedAt = &at
	}
	rows, err := querier.QueryContext(ctx, `
		select path, digest, scope, required from knowledge_candidate
		where workflow_run_id = $1 order by position
	`, runID)
	if err != nil {
		return orchestration.Recommendation{}, false, fmt.Errorf("read knowledge candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var candidate orchestration.Candidate
		if err := rows.Scan(&candidate.Path, &candidate.Digest, &candidate.Scope, &candidate.Required); err != nil {
			return orchestration.Recommendation{}, false, fmt.Errorf("scan knowledge candidate: %w", err)
		}
		recommendation.Candidates = append(recommendation.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return orchestration.Recommendation{}, false, fmt.Errorf("iterate knowledge candidates: %w", err)
	}
	return recommendation, true, nil
}

// AttachConfirmationTask records the request that carries the decision.
func (s *Store) AttachConfirmationTask(ctx context.Context, runID, humanTaskID string) (orchestration.Recommendation, error) {
	current, found, err := readRecommendation(ctx, s.database, runID)
	if err != nil {
		return orchestration.Recommendation{}, err
	}
	if !found {
		return orchestration.Recommendation{}, fmt.Errorf(
			"%w: no knowledge recommendation for run %s", orchestration.ErrNotFound, runID)
	}
	if current.Confirmed() {
		// The confirmation binds the request that carried the decision; a later
		// request can no longer take its place.
		if current.HumanTaskID != humanTaskID {
			return orchestration.Recommendation{}, orchestration.ErrVersionConflict
		}
		return current, nil
	}
	// While the selection is unconfirmed a re-asked confirmation replaces the
	// attached request: the recommendation always names the pending one.
	if _, err := s.database.ExecContext(ctx, `
		update knowledge_recommendation set human_task_id = $2
		where workflow_run_id = $1 and state = 'RECOMMENDED'
	`, runID, humanTaskID); err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("attach the confirmation task: %w", err)
	}
	current.HumanTaskID = humanTaskID
	return current, nil
}

// ConfirmRecommendation freezes the confirmed binding of one run.
func (s *Store) ConfirmRecommendation(
	ctx context.Context,
	mutation orchestration.ConfirmMutation,
) (orchestration.Recommendation, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("begin confirmation transaction: %w", err)
	}
	defer rollback(transaction)
	current, found, err := readRecommendation(ctx, transaction, mutation.RunID)
	if err != nil {
		return orchestration.Recommendation{}, err
	}
	if !found {
		return orchestration.Recommendation{}, fmt.Errorf(
			"%w: no knowledge recommendation for run %s", orchestration.ErrNotFound, mutation.RunID)
	}
	if current.Confirmed() {
		if current.SnapshotID == mutation.SnapshotID && current.BindingEventID == mutation.BindingEventID &&
			current.EffectiveInputID == mutation.EffectiveInputID {
			return current, nil
		}
		return orchestration.Recommendation{}, orchestration.ErrVersionConflict
	}
	if _, err := transaction.ExecContext(ctx, `
		update knowledge_recommendation
		set state = 'CONFIRMED', human_task_id = coalesce(human_task_id, $2), snapshot_id = $3,
			binding_event_id = $4, effective_input_id = $5, confirmed_at = $6
		where workflow_run_id = $1 and state = 'RECOMMENDED'
	`, mutation.RunID, mutation.HumanTaskID, mutation.SnapshotID, mutation.BindingEventID,
		mutation.EffectiveInputID, mutation.At); err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("confirm the recommendation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return orchestration.Recommendation{}, fmt.Errorf("commit confirmation transaction: %w", err)
	}
	current.State = orchestration.StateConfirmed
	if current.HumanTaskID == "" {
		current.HumanTaskID = mutation.HumanTaskID
	}
	current.SnapshotID = mutation.SnapshotID
	current.BindingEventID = mutation.BindingEventID
	current.EffectiveInputID = mutation.EffectiveInputID
	confirmedAt := mutation.At
	current.ConfirmedAt = &confirmedAt
	return current, nil
}

// ConfirmedKnowledgeVersion counts the confirmations this module recorded for
// one work item.
func (s *Store) ConfirmedKnowledgeVersion(ctx context.Context, workspaceID, workItemID string) (int, error) {
	var count int
	if err := s.database.QueryRowContext(ctx, `
		select count(*) from knowledge_recommendation
		where workspace_id = $1 and work_item_id = $2 and state = 'CONFIRMED'
	`, workspaceID, workItemID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count confirmed knowledge bindings: %w", err)
	}
	return count, nil
}

// BindInput persists one immutable frozen input binding. A replay of the same
// intent returns the stored facts; different content under one attempt identity
// conflicts.
func (s *Store) BindInput(ctx context.Context, binding orchestration.Binding) (orchestration.Binding, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return orchestration.Binding{}, fmt.Errorf("begin binding transaction: %w", err)
	}
	defer rollback(transaction)
	existing, found, err := readBinding(ctx, transaction, binding.TaskID)
	if err != nil {
		return orchestration.Binding{}, err
	}
	if found {
		if field := orchestration.BindingDrift(existing, binding); field != "" {
			return orchestration.Binding{}, fmt.Errorf("%w: task %s is frozen with other %s",
				orchestration.ErrVersionConflict, binding.TaskID, field)
		}
		return existing, nil
	}
	encodedScopes, err := json.Marshal(binding.Scopes)
	if err != nil {
		return orchestration.Binding{}, fmt.Errorf("encode binding scopes: %w", err)
	}
	encodedRefs, err := json.Marshal(binding.CausalRefs)
	if err != nil {
		return orchestration.Binding{}, fmt.Errorf("encode binding causal refs: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into task_input_binding (
			task_id, workspace_id, workflow_run_id, node_id, node_run_id, input_id, input_digest,
			project_repository_id, project_commit_oid, knowledge_repository_id, knowledge_commit_oid,
			capability_id, capability_version, provider_id, provider_version, git_scopes, causal_refs,
			deadline_at, bound_at
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
	`, binding.TaskID, binding.WorkspaceID, binding.RunID, binding.NodeID, binding.NodeRunID,
		binding.InputID, binding.InputDigest, binding.ProjectRepositoryID, binding.ProjectCommitOID,
		binding.KnowledgeRepositoryID, binding.KnowledgeCommitOID, binding.CapabilityID,
		binding.CapabilityVersion, binding.ProviderID, binding.ProviderVersion, encodedScopes,
		encodedRefs, binding.DeadlineAt.UTC(), binding.BoundAt.UTC()); err != nil {
		return orchestration.Binding{}, fmt.Errorf("insert task input binding: %w", err)
	}
	for position, artifactID := range binding.ArtifactIDs {
		if _, err := transaction.ExecContext(ctx, `
			insert into task_input_artifact (task_id, position, workspace_id, artifact_id)
			values ($1, $2, $3, $4)
			on conflict (task_id, artifact_id) do nothing
		`, binding.TaskID, position, binding.WorkspaceID, artifactID); err != nil {
			return orchestration.Binding{}, fmt.Errorf("insert task input artifact: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return orchestration.Binding{}, fmt.Errorf("commit binding transaction: %w", err)
	}
	return binding, nil
}

// Binding reads one attempt's frozen input binding.
func (s *Store) Binding(ctx context.Context, taskID string) (orchestration.Binding, error) {
	binding, found, err := readBinding(ctx, s.database, taskID)
	if err != nil {
		return orchestration.Binding{}, err
	}
	if !found {
		return orchestration.Binding{}, fmt.Errorf(
			"%w: no frozen input binding for task %s", orchestration.ErrNotFound, taskID)
	}
	return binding, nil
}

func readBinding(
	ctx context.Context,
	querier recommendationQuerier,
	taskID string,
) (orchestration.Binding, bool, error) {
	var binding orchestration.Binding
	var encodedScopes, encodedRefs []byte
	err := querier.QueryRowContext(ctx, `
		select task_id, workspace_id, workflow_run_id, node_id, node_run_id, input_id, input_digest,
			project_repository_id, project_commit_oid, knowledge_repository_id, knowledge_commit_oid,
			capability_id, capability_version, provider_id, provider_version, git_scopes, causal_refs,
			deadline_at, bound_at
		from task_input_binding where task_id = $1
	`, taskID).Scan(&binding.TaskID, &binding.WorkspaceID, &binding.RunID, &binding.NodeID,
		&binding.NodeRunID, &binding.InputID, &binding.InputDigest, &binding.ProjectRepositoryID,
		&binding.ProjectCommitOID, &binding.KnowledgeRepositoryID, &binding.KnowledgeCommitOID,
		&binding.CapabilityID, &binding.CapabilityVersion, &binding.ProviderID, &binding.ProviderVersion,
		&encodedScopes, &encodedRefs, &binding.DeadlineAt, &binding.BoundAt)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.Binding{}, false, nil
	}
	if err != nil {
		return orchestration.Binding{}, false, fmt.Errorf("read task input binding: %w", err)
	}
	if err := json.Unmarshal(encodedScopes, &binding.Scopes); err != nil {
		return orchestration.Binding{}, false, fmt.Errorf("decode binding scopes: %w", err)
	}
	if err := json.Unmarshal(encodedRefs, &binding.CausalRefs); err != nil {
		return orchestration.Binding{}, false, fmt.Errorf("decode binding causal refs: %w", err)
	}
	rows, err := querier.QueryContext(ctx, `
		select artifact_id from task_input_artifact where task_id = $1 order by position
	`, taskID)
	if err != nil {
		return orchestration.Binding{}, false, fmt.Errorf("read binding artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var artifactID string
		if err := rows.Scan(&artifactID); err != nil {
			return orchestration.Binding{}, false, fmt.Errorf("scan binding artifact: %w", err)
		}
		binding.ArtifactIDs = append(binding.ArtifactIDs, artifactID)
	}
	if err := rows.Err(); err != nil {
		return orchestration.Binding{}, false, fmt.Errorf("iterate binding artifacts: %w", err)
	}
	return binding, true, nil
}

// ArtifactsForWorkItem reads the published deliverables of one work item from
// the artifact module's own rows; this module adds no artifact semantics.
func (s *Store) ArtifactsForWorkItem(ctx context.Context, workspaceID, workItemID string) ([]orchestration.ArtifactRecord, error) {
	rows, err := s.database.QueryContext(ctx, `
		select artifact_id, subject_id, kind, digest, size, media_type, producer_kind, producer_id,
			requires_approval, state, created_at
		from artifact
		where workspace_id = $1 and subject_id = $2
		order by created_at, artifact_id
	`, workspaceID, workItemID)
	if err != nil {
		return nil, fmt.Errorf("read work item artifacts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := []orchestration.ArtifactRecord{}
	for rows.Next() {
		var record orchestration.ArtifactRecord
		if err := rows.Scan(&record.ArtifactID, &record.SubjectID, &record.Kind, &record.Digest,
			&record.Size, &record.MediaType, &record.ProducerKind, &record.ProducerID,
			&record.RequiresApproval, &record.State, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan work item artifact: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate work item artifacts: %w", err)
	}
	return records, nil
}

// ActiveRuns lists the admitted runs whose delivery run is still live.
func (s *Store) ActiveRuns(ctx context.Context) ([]string, error) {
	rows, err := s.database.QueryContext(ctx, `
		select admission.workflow_run_id
		from run_admission admission
		join workflow_run run on run.workflow_run_id = admission.workflow_run_id
		where run.state = 'ACTIVE'
		order by admission.workflow_run_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list active runs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	identifiers := []string{}
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return nil, fmt.Errorf("scan active run: %w", err)
		}
		identifiers = append(identifiers, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active runs: %w", err)
	}
	return identifiers, nil
}

// VoidAdmission deletes the admission record of a withdrawn attempt together
// with the admission command that could replay it; the run itself is cancelled
// by the caller beforehand.
func (s *Store) VoidAdmission(ctx context.Context, runID, actorID, workItemID, requestKey string) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin void admission: %w", err)
	}
	defer rollback(transaction)
	if _, err := transaction.ExecContext(ctx,
		`delete from run_admission where workflow_run_id = $1`, runID); err != nil {
		return fmt.Errorf("void run admission: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		delete from command_idempotency
		where actor_github_user_id = $1 and command_type = $2 and target_id = $3 and request_key = $4
	`, actorID, orchestration.CommandAdmit, workItemID, requestKey); err != nil {
		return fmt.Errorf("void admission command: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit void admission: %w", err)
	}
	return nil
}

// NoteRunState is a no-op for the durable adapter: it reads the run table.
func (s *Store) NoteRunState(context.Context, string, string) error { return nil }

// ReplayedCommand answers one identical recovery command from its record.
func (s *Store) ReplayedCommand(
	ctx context.Context,
	actor, commandType, targetID, requestKey, requestDigest string,
) ([]byte, bool, error) {
	var storedDigest string
	var response []byte
	err := s.database.QueryRowContext(ctx, `
		select request_digest, response from command_idempotency
		where actor_github_user_id = $1 and command_type = $2 and target_id = $3 and request_key = $4
	`, actor, commandType, targetID, requestKey).Scan(&storedDigest, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("query command idempotency: %w", err)
	}
	if storedDigest != requestDigest {
		return nil, false, orchestration.ErrVersionConflict
	}
	return response, true, nil
}

// RecordCommand stores one command outcome for idempotent replay.
func (s *Store) RecordCommand(
	ctx context.Context,
	actor, commandType, targetID, requestKey, requestDigest string,
	response []byte,
) error {
	if _, err := s.database.ExecContext(ctx, `
		insert into command_idempotency (
			actor_github_user_id, command_type, target_id, request_key, request_digest, response, created_at
		) values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (actor_github_user_id, command_type, target_id, request_key) do nothing
	`, actor, commandType, targetID, requestKey, requestDigest, response, time.Now().UTC()); err != nil {
		return fmt.Errorf("insert command idempotency: %w", err)
	}
	return nil
}

var _ orchestration.Store = (*Store)(nil)

// humanTaskID stores the absent confirmation request as SQL NULL.
func humanTaskID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// suggestionAttemptID names one attempt: the identity is stable and derived
// from the persisted run and attempt numbers, never from a caller.
func suggestionAttemptID(runID string, attempt int) string {
	return fmt.Sprintf("%s:knowledge-suggestion:%d", runID, attempt)
}

// SeedSuggestion materialises the first auxiliary suggestion attempt of one run.
func (s *Store) SeedSuggestion(
	ctx context.Context,
	seed orchestration.SuggestionSeed,
) (orchestration.SuggestionAttempt, error) {
	if _, err := s.database.ExecContext(ctx, `
		insert into knowledge_suggestion (
			workflow_run_id, attempt, workspace_id, work_item_id, attempt_id, state,
			input_digest, requested_at, updated_at, version
		) values ($1, 1, $2, $3, $4, 'PENDING', $5, $6, $6, 1)
		on conflict (workflow_run_id, attempt) do nothing
	`, seed.RunID, seed.WorkspaceID, seed.WorkItemID,
		suggestionAttemptID(seed.RunID, 1), seed.InputDigest, seed.At.UTC()); err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("seed suggestion attempt: %w", err)
	}
	attempt, err := s.suggestionAttempt(ctx, seed.RunID, 1)
	if err != nil {
		return orchestration.SuggestionAttempt{}, err
	}
	if attempt.InputDigest != seed.InputDigest {
		return orchestration.SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt 1 of run %s is frozen on input %s",
			orchestration.ErrVersionConflict, seed.RunID, attempt.InputDigest)
	}
	return attempt, nil
}

// ClaimSuggestion fences one attempt under a lease.
func (s *Store) ClaimSuggestion(
	ctx context.Context,
	claim orchestration.SuggestionClaim,
) (orchestration.SuggestionAttempt, error) {
	result, err := s.database.ExecContext(ctx, `
		update knowledge_suggestion
		set state = 'PROCESSING', claimed_until = $2, updated_at = $3, version = version + 1
		where attempt_id = $1
			and (state = 'PENDING' or (state = 'PROCESSING' and claimed_until <= $3))
	`, claim.AttemptID, claim.LeaseUntil.UTC(), claim.At.UTC())
	if err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("claim suggestion attempt: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		// Classify the refusal from the persisted row.
		attempt, err := s.suggestionAttemptByID(ctx, claim.AttemptID)
		if err != nil {
			return orchestration.SuggestionAttempt{}, err
		}
		switch attempt.State {
		case orchestration.SuggestionProcessing:
			return orchestration.SuggestionAttempt{}, fmt.Errorf(
				"%w: suggestion attempt %s is being processed", orchestration.ErrVersionConflict, claim.AttemptID)
		default:
			return orchestration.SuggestionAttempt{}, fmt.Errorf(
				"%w: suggestion attempt %s is %s", orchestration.ErrPolicyBlocked, claim.AttemptID, attempt.State)
		}
	}
	return s.suggestionAttemptByID(ctx, claim.AttemptID)
}

// FinalizeSuggestion settles one claimed attempt under its lease version.
func (s *Store) FinalizeSuggestion(
	ctx context.Context,
	outcome orchestration.SuggestionOutcome,
) (orchestration.SuggestionAttempt, error) {
	state := orchestration.SuggestionFailed
	if outcome.FailureReason == "" {
		state = orchestration.SuggestionPublished
	}
	result, err := s.database.ExecContext(ctx, `
		update knowledge_suggestion
		set state = $4, artifact_id = $5, artifact_digest = $6, failure_reason = $7,
			claimed_until = null, updated_at = $8, version = version + 1
		where workflow_run_id = $1 and attempt = $2 and state = 'PROCESSING' and version = $3
	`, outcome.RunID, outcome.Attempt, outcome.ExpectedVersion, state,
		nullable(outcome.ArtifactID), nullable(outcome.ArtifactDigest), nullable(outcome.FailureReason),
		outcome.At.UTC())
	if err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("finalize suggestion attempt: %w", err)
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		attempt, err := s.suggestionAttempt(ctx, outcome.RunID, outcome.Attempt)
		if err != nil {
			return orchestration.SuggestionAttempt{}, err
		}
		return orchestration.SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %s is %s at version %d",
			orchestration.ErrVersionConflict, attempt.AttemptID, attempt.State, attempt.Version)
	}
	return s.suggestionAttempt(ctx, outcome.RunID, outcome.Attempt)
}

// SuggestionAttempts lists every attempt of one run, oldest first.
func (s *Store) SuggestionAttempts(
	ctx context.Context,
	runID string,
) ([]orchestration.SuggestionAttempt, error) {
	rows, err := s.database.QueryContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, attempt, attempt_id, state,
			input_digest, claimed_until, artifact_id, artifact_digest, failure_reason,
			requested_at, updated_at, version
		from knowledge_suggestion
		where workflow_run_id = $1
		order by attempt
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("read suggestion attempts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	attempts := []orchestration.SuggestionAttempt{}
	for rows.Next() {
		attempt, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate suggestion attempts: %w", err)
	}
	return attempts, nil
}

// ClaimableSuggestions lists the attempts a worker may process, oldest first.
func (s *Store) ClaimableSuggestions(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]orchestration.SuggestionAttempt, error) {
	rows, err := s.database.QueryContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, attempt, attempt_id, state,
			input_digest, claimed_until, artifact_id, artifact_digest, failure_reason,
			requested_at, updated_at, version
		from knowledge_suggestion
		where state = 'PENDING' or (state = 'PROCESSING' and claimed_until <= $1)
		order by requested_at, attempt_id
		limit $2
	`, now.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("read claimable suggestions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	claimable := []orchestration.SuggestionAttempt{}
	for rows.Next() {
		attempt, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		claimable = append(claimable, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimable suggestions: %w", err)
	}
	return claimable, nil
}

// RequestSuggestion opens the next attempt after a failed one.
func (s *Store) RequestSuggestion(
	ctx context.Context,
	request orchestration.SuggestionRequest,
) (orchestration.SuggestionAttempt, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("begin suggestion request: %w", err)
	}
	defer rollback(transaction)
	newest, err := readNewestSuggestion(ctx, transaction, request.RunID, true)
	if err != nil {
		return orchestration.SuggestionAttempt{}, err
	}
	if newest.State != orchestration.SuggestionFailed {
		return orchestration.SuggestionAttempt{}, fmt.Errorf(
			"%w: the newest suggestion attempt of run %s is %s",
			orchestration.ErrPolicyBlocked, request.RunID, newest.State)
	}
	attempt := orchestration.SuggestionAttempt{
		RunID: newest.RunID, WorkspaceID: newest.WorkspaceID, WorkItemID: newest.WorkItemID,
		Attempt: newest.Attempt + 1, AttemptID: suggestionAttemptID(request.RunID, newest.Attempt+1),
		State: orchestration.SuggestionPending, InputDigest: newest.InputDigest,
		RequestedAt: request.At.UTC(), UpdatedAt: request.At.UTC(), Version: 1,
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into knowledge_suggestion (
			workflow_run_id, attempt, workspace_id, work_item_id, attempt_id, state,
			input_digest, requested_at, updated_at, version
		) values ($1, $2, $3, $4, $5, 'PENDING', $6, $7, $7, 1)
	`, attempt.RunID, attempt.Attempt, attempt.WorkspaceID, attempt.WorkItemID,
		attempt.AttemptID, attempt.InputDigest, attempt.RequestedAt); err != nil {
		if isUniqueViolation(err) {
			return orchestration.SuggestionAttempt{}, fmt.Errorf(
				"%w: run %s already has an open suggestion attempt",
				orchestration.ErrVersionConflict, request.RunID)
		}
		return orchestration.SuggestionAttempt{}, fmt.Errorf("open suggestion attempt: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("commit suggestion request: %w", err)
	}
	return attempt, nil
}

// suggestionAttempt reads one attempt of one run.
func (s *Store) suggestionAttempt(
	ctx context.Context,
	runID string,
	attempt int,
) (orchestration.SuggestionAttempt, error) {
	row := s.database.QueryRowContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, attempt, attempt_id, state,
			input_digest, claimed_until, artifact_id, artifact_digest, failure_reason,
			requested_at, updated_at, version
		from knowledge_suggestion
		where workflow_run_id = $1 and attempt = $2
	`, runID, attempt)
	stored, err := scanSuggestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %d of run %s", orchestration.ErrNotFound, attempt, runID)
	}
	if err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("read suggestion attempt: %w", err)
	}
	return stored, nil
}

// suggestionAttemptByID reads one attempt by its stable identity.
func (s *Store) suggestionAttemptByID(ctx context.Context, attemptID string) (orchestration.SuggestionAttempt, error) {
	row := s.database.QueryRowContext(ctx, `
		select workflow_run_id, workspace_id, work_item_id, attempt, attempt_id, state,
			input_digest, claimed_until, artifact_id, artifact_digest, failure_reason,
			requested_at, updated_at, version
		from knowledge_suggestion
		where attempt_id = $1
	`, attemptID)
	stored, err := scanSuggestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.SuggestionAttempt{}, fmt.Errorf(
			"%w: suggestion attempt %s", orchestration.ErrNotFound, attemptID)
	}
	if err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("read suggestion attempt: %w", err)
	}
	return stored, nil
}

// readNewestSuggestion reads the newest attempt of one run, optionally locking
// it for the request transition.
func readNewestSuggestion(
	ctx context.Context,
	querier queryer,
	runID string,
	forUpdate bool,
) (orchestration.SuggestionAttempt, error) {
	query := `
		select workflow_run_id, workspace_id, work_item_id, attempt, attempt_id, state,
			input_digest, claimed_until, artifact_id, artifact_digest, failure_reason,
			requested_at, updated_at, version
		from knowledge_suggestion
		where workflow_run_id = $1
		order by attempt desc
		limit 1`
	if forUpdate {
		query += " for update"
	}
	stored, err := scanSuggestion(querier.QueryRowContext(ctx, query, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.SuggestionAttempt{}, fmt.Errorf(
			"%w: run %s has no suggestion attempt", orchestration.ErrNotFound, runID)
	}
	if err != nil {
		return orchestration.SuggestionAttempt{}, fmt.Errorf("read newest suggestion: %w", err)
	}
	return stored, nil
}

// suggestionScanner is the shared shape of sql.Row and sql.Rows.
type suggestionScanner interface {
	Scan(dest ...any) error
}

func scanSuggestion(scanner suggestionScanner) (orchestration.SuggestionAttempt, error) {
	var attempt orchestration.SuggestionAttempt
	var claimedAt sql.NullTime
	var artifactID, artifactDigest, failureReason sql.NullString
	if err := scanner.Scan(
		&attempt.RunID, &attempt.WorkspaceID, &attempt.WorkItemID, &attempt.Attempt,
		&attempt.AttemptID, &attempt.State, &attempt.InputDigest, &claimedAt,
		&artifactID, &artifactDigest, &failureReason,
		&attempt.RequestedAt, &attempt.UpdatedAt, &attempt.Version,
	); err != nil {
		return orchestration.SuggestionAttempt{}, err
	}
	if claimedAt.Valid {
		claimed := claimedAt.Time.UTC()
		attempt.ClaimedUntil = &claimed
	}
	attempt.ArtifactID = artifactID.String
	attempt.ArtifactDigest = artifactDigest.String
	attempt.FailureReason = failureReason.String
	attempt.RequestedAt = attempt.RequestedAt.UTC()
	attempt.UpdatedAt = attempt.UpdatedAt.UTC()
	return attempt, nil
}

// nullable stores an absent string as SQL NULL.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
