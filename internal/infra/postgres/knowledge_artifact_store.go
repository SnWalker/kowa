package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/knowledge"
)

// lockKnowledgeWorkspace is also shared by publication, reference insertion and GC.
// Workspace mutations use the same lock, so revocation cannot race a committed write.
func lockKnowledgeWorkspace(ctx context.Context, tx *sql.Tx, scope artifact.Scope, write bool) error {
	var id, role string
	err := tx.QueryRowContext(ctx, `select workspace_id from workspace where workspace_id=$1 for update`, scope.WorkspaceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock artifact workspace: %w", err)
	}
	err = tx.QueryRowContext(ctx, `select role from workspace_member where workspace_id=$1 and github_user_id=$2 and revoked_at is null`, scope.WorkspaceID, scope.ActorID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("authorize artifact workspace: %w", err)
	}
	if write && role != "admin" && role != "developer" {
		return artifact.ErrForbidden
	}
	return nil
}
func (s *Store) WithArtifact(ctx context.Context, scope artifact.Scope, id string, create *artifact.Record, write bool, fn func(*artifact.Record) error) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, write); err != nil {
		return err
	}
	var record artifact.Record
	if create != nil {
		if !write || create.Ref.WorkspaceID != scope.WorkspaceID || create.Ref.ArtifactID != id || create.State != artifact.Staging {
			return artifact.ErrValidation
		}
		if err = create.Ref.Validate(); err != nil {
			return err
		}
		record = *create
		r := record.Ref
		_, err = tx.ExecContext(ctx, `insert into artifact(artifact_id,workspace_id,subject_id,kind,producer_kind,producer_id,digest,size,media_type,state,created_at,requires_approval) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, r.ArtifactID, r.WorkspaceID, r.SubjectID, r.Kind, r.Producer.Kind, r.Producer.ID, r.Digest, r.Size, r.MediaType, record.State, record.CreatedAt, r.RequiresApproval)
		if err != nil {
			return fmt.Errorf("insert staging artifact: %w", err)
		}
	} else {
		record, err = readArtifact(ctx, tx, scope.WorkspaceID, id)
		if err != nil {
			return err
		}
	}
	before := record.State
	original := record.Ref
	if err = fn(&record); err != nil {
		return err
	}
	if !write {
		return tx.Commit()
	}
	if record.Ref != original {
		return artifact.ErrConflict
	}
	if before != artifact.Published && record.State == artifact.Published && record.Ref.Producer.Kind == "task" {
		if err = checkAcceptedArtifact(ctx, tx, record.Ref); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `update artifact set state=$3 where workspace_id=$1 and artifact_id=$2`, scope.WorkspaceID, id, record.State)
	if err != nil {
		return err
	}
	for _, ref := range record.References {
		if err = ref.Validate(); err != nil {
			return err
		}
		if record.State != artifact.Published {
			return artifact.ErrUnpublished
		}
		if err = insertArtifactReference(ctx, tx, scope.WorkspaceID, id, ref); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func readArtifact(ctx context.Context, tx *sql.Tx, ws, id string) (r artifact.Record, err error) {
	r.Ref.SchemaVersion = artifact.SchemaVersion
	r.Ref.StorageRef = "artifact:" + id
	err = tx.QueryRowContext(ctx, `select artifact_id,workspace_id,subject_id,kind,producer_kind,producer_id,digest,size,media_type,state,created_at,requires_approval from artifact where workspace_id=$1 and artifact_id=$2 for update`, ws, id).Scan(&r.Ref.ArtifactID, &r.Ref.WorkspaceID, &r.Ref.SubjectID, &r.Ref.Kind, &r.Ref.Producer.Kind, &r.Ref.Producer.ID, &r.Ref.Digest, &r.Ref.Size, &r.Ref.MediaType, &r.State, &r.CreatedAt, &r.Ref.RequiresApproval)
	if errors.Is(err, sql.ErrNoRows) {
		return r, artifact.ErrUnavailable
	}
	if err != nil {
		return r, err
	}
	rows, err := tx.QueryContext(ctx, `select owner_kind,owner_id from artifact_reference where workspace_id=$1 and artifact_id=$2 order by owner_kind,owner_id`, ws, id)
	if err != nil {
		return r, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var ref artifact.Reference
		if err = rows.Scan(&ref.Kind, &ref.ID); err != nil {
			return r, err
		}
		r.References = append(r.References, ref)
	}
	return r, errors.Join(rows.Err(), rows.Close())
}
func insertArtifactReference(ctx context.Context, tx *sql.Tx, ws, id string, ref artifact.Reference) error {
	_, err := tx.ExecContext(ctx, `insert into artifact_reference(workspace_id,artifact_id,owner_kind,owner_id) values($1,$2,$3,$4) on conflict do nothing`, ws, id, ref.Kind, ref.ID)
	return err
}

// S02's immutable accepted result is the authority. An unaccepted/stale upload cannot publish.
// S03 freezes output.artifactRefs as the result field containing exact v1 references.
func checkAcceptedArtifact(ctx context.Context, tx *sql.Tx, ref artifact.Ref) error {
	var result []byte
	var runID string
	err := tx.QueryRowContext(ctx, `select t.result,r.workflow_run_id from execution_task t join node_run n on n.node_run_id=t.node_run_id join workflow_run r on r.workflow_run_id=n.workflow_run_id where t.task_id=$1 and r.workspace_id=$2 and r.work_item_id=$3 and t.state in ('COMPLETED','AWAITING_INPUT') and t.result_digest is not null`, ref.Producer.ID, ref.WorkspaceID, ref.SubjectID).Scan(&result, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ErrForbidden
	}
	if err != nil {
		return err
	}
	var report struct {
		Output struct {
			ArtifactRefs []artifact.Ref `json:"artifactRefs"`
		} `json:"output"`
	}
	if err = json.Unmarshal(result, &report); err != nil {
		return err
	}
	for _, accepted := range report.Output.ArtifactRefs {
		if accepted == ref {
			// The accepted result is already a live run reference; publication retains it atomically.
			return insertArtifactReference(ctx, tx, ref.WorkspaceID, ref.ArtifactID, artifact.Reference{Kind: "run", ID: runID})
		}
	}
	return artifact.ErrForbidden
}

// PublishKnowledgeSnapshot makes a verified candidate readable without confirming it.
func (s *Store) PublishKnowledgeSnapshot(ctx context.Context, scope artifact.Scope, snapshot knowledge.Snapshot) error {
	if scope.WorkspaceID != snapshot.Content.WorkspaceID {
		return artifact.ErrForbidden
	}
	if err := knowledge.ValidateSnapshot(snapshot); err != nil {
		return err
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, true); err != nil {
		return err
	}
	var repositoryID string
	err = tx.QueryRowContext(ctx, `select github_repository_id from repository_binding where workspace_id=$1 and role='knowledge'`, scope.WorkspaceID).Scan(&repositoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.ErrUnavailable
	}
	if err != nil {
		return err
	}
	if repositoryID != snapshot.Content.RepositoryID {
		return artifact.ErrForbidden
	}
	_, err = tx.ExecContext(ctx, `insert into knowledge_snapshot(workspace_id,snapshot_id,repository_id,commit_oid,digest,canonical) values($1,$2,$3,$4,$5,$6) on conflict do nothing`, scope.WorkspaceID, snapshot.ID, repositoryID, snapshot.Content.Commit, snapshot.Digest, snapshot.Canonical)
	if err != nil {
		return err
	}
	stored, err := readSnapshot(ctx, tx, scope.WorkspaceID, snapshot.ID)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored.Canonical, snapshot.Canonical) {
		return artifact.ErrIntegrity
	}
	return tx.Commit()
}

func (s *Store) ConfirmKnowledge(ctx context.Context, scope artifact.Scope, c knowledge.ConfirmCommand) (knowledge.BindingEvent, error) {
	var event knowledge.BindingEvent
	if scope.WorkspaceID != c.Snapshot.Content.WorkspaceID {
		return event, artifact.ErrForbidden
	}
	if !artifact.ValidID(c.SubjectID) || !artifact.ValidID(c.HumanResponseID) || c.ExpectedVersion < 0 {
		return event, knowledge.ErrValidation
	}
	if err := knowledge.ValidateSnapshot(c.Snapshot); err != nil {
		return event, err
	}
	// Expected version belongs to the compared request, not the idempotency lookup key.
	encoded, err := json.Marshal(struct {
		SnapshotID      string
		ExpectedVersion int64
	}{c.Snapshot.ID, c.ExpectedVersion})
	if err != nil {
		return event, err
	}
	requestDigest := artifact.Digest(encoded)
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return event, err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, true); err != nil {
		return event, err
	}
	var existingID, existingDigest string
	err = tx.QueryRowContext(ctx, `select event_id,request_digest from knowledge_binding_event where workspace_id=$1 and subject_id=$2 and actor_id=$3 and human_response_id=$4`, scope.WorkspaceID, c.SubjectID, scope.ActorID, c.HumanResponseID).Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != requestDigest {
			return event, knowledge.ErrConflict
		}
		event, err = readBindingEvent(ctx, tx, scope.WorkspaceID, existingID)
		if err != nil {
			return event, err
		}
		return event, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return event, err
	}
	var repositoryID string
	err = tx.QueryRowContext(ctx, `select github_repository_id from repository_binding where workspace_id=$1 and role='knowledge'`, scope.WorkspaceID).Scan(&repositoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return event, knowledge.ErrUnavailable
	}
	if err != nil {
		return event, err
	}
	if repositoryID != c.Snapshot.Content.RepositoryID {
		return event, artifact.ErrForbidden
	}
	var version int64
	var previous string
	err = tx.QueryRowContext(ctx, `select version,event_id from knowledge_binding where workspace_id=$1 and subject_id=$2`, scope.WorkspaceID, c.SubjectID).Scan(&version, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return event, err
	}
	if version != c.ExpectedVersion {
		return event, knowledge.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `insert into knowledge_snapshot(workspace_id,snapshot_id,repository_id,commit_oid,digest,canonical) values($1,$2,$3,$4,$5,$6) on conflict do nothing`, scope.WorkspaceID, c.Snapshot.ID, repositoryID, c.Snapshot.Content.Commit, c.Snapshot.Digest, c.Snapshot.Canonical)
	if err != nil {
		return event, err
	}
	stored, err := readSnapshot(ctx, tx, scope.WorkspaceID, c.Snapshot.ID)
	if err != nil {
		return event, err
	}
	if !bytes.Equal(stored.Canonical, c.Snapshot.Canonical) {
		return event, artifact.ErrIntegrity
	}
	identity, err := json.Marshal([]string{scope.WorkspaceID, c.SubjectID, scope.ActorID, c.HumanResponseID})
	if err != nil {
		return event, err
	}
	event = knowledge.BindingEvent{ID: "kb_" + strings.TrimPrefix(artifact.Digest(identity), "sha256:"), WorkspaceID: scope.WorkspaceID, SubjectID: c.SubjectID, SnapshotID: c.Snapshot.ID, HumanResponseID: c.HumanResponseID, PreviousEventID: previous, ActorID: scope.ActorID, Version: version + 1, ConfirmedAt: time.Now().UTC()}
	_, err = tx.ExecContext(ctx, `insert into knowledge_binding_event(workspace_id,event_id,subject_id,snapshot_id,human_response_id,previous_event_id,actor_id,version,request_digest,confirmed_at) values($1,$2,$3,$4,$5,nullif($6,''),$7,$8,$9,$10)`, event.WorkspaceID, event.ID, event.SubjectID, event.SnapshotID, event.HumanResponseID, event.PreviousEventID, event.ActorID, event.Version, requestDigest, event.ConfirmedAt)
	if err != nil {
		return event, err
	}
	_, err = tx.ExecContext(ctx, `insert into knowledge_binding(workspace_id,subject_id,version,event_id) values($1,$2,$3,$4) on conflict(workspace_id,subject_id) do update set version=excluded.version,event_id=excluded.event_id`, event.WorkspaceID, event.SubjectID, event.Version, event.ID)
	if err != nil {
		return event, err
	}
	return event, tx.Commit()
}
func readBindingEvent(ctx context.Context, tx *sql.Tx, ws, id string) (knowledge.BindingEvent, error) {
	var e knowledge.BindingEvent
	err := tx.QueryRowContext(ctx, `select event_id,workspace_id,subject_id,snapshot_id,human_response_id,coalesce(previous_event_id,''),actor_id,version,confirmed_at from knowledge_binding_event where workspace_id=$1 and event_id=$2`, ws, id).Scan(&e.ID, &e.WorkspaceID, &e.SubjectID, &e.SnapshotID, &e.HumanResponseID, &e.PreviousEventID, &e.ActorID, &e.Version, &e.ConfirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return e, knowledge.ErrUnconfirmed
	}
	return e, err
}
func readSnapshot(ctx context.Context, tx *sql.Tx, ws, id string) (knowledge.Snapshot, error) {
	var s knowledge.Snapshot
	err := tx.QueryRowContext(ctx, `select snapshot_id,digest,canonical from knowledge_snapshot where workspace_id=$1 and snapshot_id=$2`, ws, id).Scan(&s.ID, &s.Digest, &s.Canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return s, artifact.ErrUnavailable
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(s.Canonical, &s.Content); err != nil {
		return s, err
	}
	return s, knowledge.ValidateSnapshot(s)
}
func (s *Store) ReadKnowledgeSnapshot(ctx context.Context, scope artifact.Scope, id string) (knowledge.Snapshot, error) {
	var snapshot knowledge.Snapshot
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return snapshot, err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, false); err != nil {
		return snapshot, err
	}
	snapshot, err = readSnapshot(ctx, tx, scope.WorkspaceID, id)
	if err != nil {
		return snapshot, err
	}
	return snapshot, tx.Commit()
}
func (s *Store) AssembleKnowledgeInput(ctx context.Context, scope artifact.Scope, req knowledge.InputRequest, eventID string) (knowledge.EffectiveInput, error) {
	var input knowledge.EffectiveInput
	if eventID == "" {
		return input, knowledge.ErrUnconfirmed
	}
	if req.WorkspaceID != scope.WorkspaceID {
		return input, artifact.ErrForbidden
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, true); err != nil {
		return input, err
	}
	event, err := readBindingEvent(ctx, tx, scope.WorkspaceID, eventID)
	if err != nil {
		return input, err
	}
	var current string
	err = tx.QueryRowContext(ctx, `select event_id from knowledge_binding where workspace_id=$1 and subject_id=$2`, scope.WorkspaceID, req.SubjectID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return input, knowledge.ErrUnconfirmed
	}
	if err != nil {
		return input, err
	}
	if event.ID != current {
		return input, knowledge.ErrConflict
	}
	snapshot, err := readSnapshot(ctx, tx, scope.WorkspaceID, event.SnapshotID)
	if err != nil {
		return input, err
	}
	input, err = knowledge.Assemble(req, snapshot, event)
	if err != nil {
		return input, err
	}
	for _, upstream := range input.Content.Upstream {
		record, e := readArtifact(ctx, tx, scope.WorkspaceID, upstream.Ref.ArtifactID)
		if e != nil {
			return input, e
		}
		if record.State != artifact.Published {
			return input, artifact.ErrUnpublished
		}
		if !record.Approved() {
			return input, artifact.ErrUnapproved
		}
		if record.Ref != upstream.Ref {
			return input, artifact.ErrIntegrity
		}
		if e = insertArtifactReference(ctx, tx, scope.WorkspaceID, record.Ref.ArtifactID, artifact.Reference{Kind: "input", ID: input.ID}); e != nil {
			return input, e
		}
	}
	_, err = tx.ExecContext(ctx, `insert into effective_input(workspace_id,input_id,subject_id,snapshot_id,event_id,digest,canonical) values($1,$2,$3,$4,$5,$6,$7) on conflict do nothing`, scope.WorkspaceID, input.ID, req.SubjectID, snapshot.ID, event.ID, input.Digest, input.Canonical)
	if err != nil {
		return input, err
	}
	var stored []byte
	if err = tx.QueryRowContext(ctx, `select canonical from effective_input where workspace_id=$1 and input_id=$2`, scope.WorkspaceID, input.ID).Scan(&stored); err != nil {
		return input, err
	}
	if !bytes.Equal(stored, input.Canonical) {
		return input, artifact.ErrIntegrity
	}
	return input, tx.Commit()
}
func (s *Store) ReadEffectiveInput(ctx context.Context, scope artifact.Scope, id string) (knowledge.EffectiveInput, error) {
	var input knowledge.EffectiveInput
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, false); err != nil {
		return input, err
	}
	err = tx.QueryRowContext(ctx, `select input_id,digest,canonical from effective_input where workspace_id=$1 and input_id=$2`, scope.WorkspaceID, id).Scan(&input.ID, &input.Digest, &input.Canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return input, artifact.ErrUnavailable
	}
	if err != nil {
		return input, err
	}
	if artifact.Digest(input.Canonical) != input.Digest || input.ID != "ei_"+strings.TrimPrefix(input.Digest, "sha256:") {
		return input, artifact.ErrIntegrity
	}
	if err = json.Unmarshal(input.Canonical, &input.Content); err != nil {
		return input, err
	}
	return input, tx.Commit()
}

var _ artifact.Catalog = (*Store)(nil)
var _ knowledge.Store = (*Store)(nil)

// Orphans returns bounded candidates only. It never authorizes a deletion.
func (s *Store) Orphans(ctx context.Context, scope artifact.Scope, before time.Time, limit int) (ids []string, err error) {
	if limit < 1 || limit > 1000 {
		return nil, artifact.ErrValidation
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	if err = lockKnowledgeWorkspace(ctx, tx, scope, true); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `select a.artifact_id from artifact a where a.workspace_id=$1 and a.created_at<=$2 and a.state<>'DELETED' and not exists(select 1 from artifact_reference r where r.workspace_id=a.workspace_id and r.artifact_id=a.artifact_id) order by a.created_at,a.artifact_id limit $3`, scope.WorkspaceID, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	ids = []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}
