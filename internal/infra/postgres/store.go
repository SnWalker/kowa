// Package postgres implements Identity and Workspace storage on PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/workspace"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	maxOpenConnections = 20
	maxIdleConnections = 5
	connectionLifetime = 30 * time.Minute
	connectionIdleTime = 5 * time.Minute
)

// Open creates and verifies a bounded PostgreSQL connection pool.
func Open(ctx context.Context, dataSourceName string) (*sql.DB, error) {
	database, err := NewPool(dataSourceName)
	if err != nil {
		return nil, err
	}
	if err := database.PingContext(ctx); err != nil {
		closeErr := database.Close()
		return nil, errors.Join(fmt.Errorf("ping postgres: %w", err), closeErr)
	}
	return database, nil
}

// NewPool configures a lazy bounded pool; the caller owns ping and close lifecycle.
func NewPool(dataSourceName string) (*sql.DB, error) {
	database, err := sql.Open("pgx", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	database.SetMaxOpenConns(maxOpenConnections)
	database.SetMaxIdleConns(maxIdleConnections)
	database.SetConnMaxLifetime(connectionLifetime)
	database.SetConnMaxIdleTime(connectionIdleTime)
	return database, nil
}

// Store implements identity.Store and workspace.Store.
type Store struct {
	database *sql.DB
}

func NewStore(database *sql.DB) *Store {
	return &Store{database: database}
}

func (s *Store) SaveLoginFlow(ctx context.Context, flow identity.LoginFlow) error {
	_, err := s.database.ExecContext(ctx, `
		insert into github_oauth_flow (
			state_digest, pkce_verifier, return_to, expires_at, created_at
		) values ($1, $2, $3, $4, $5)
	`, flow.StateDigest[:], flow.Verifier, flow.ReturnTo, flow.ExpiresAt, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert github oauth flow: %w", err)
	}
	return nil
}

func (s *Store) ConsumeLoginFlow(
	ctx context.Context,
	stateDigest [32]byte,
	now time.Time,
) (identity.LoginFlow, error) {
	var flow identity.LoginFlow
	flow.StateDigest = stateDigest
	err := s.database.QueryRowContext(ctx, `
		delete from github_oauth_flow
		where state_digest = $1 and expires_at > $2
		returning pkce_verifier, return_to, expires_at
	`, stateDigest[:], now).Scan(&flow.Verifier, &flow.ReturnTo, &flow.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.LoginFlow{}, identity.ErrUnauthorized
	}
	if err != nil {
		return identity.LoginFlow{}, fmt.Errorf("consume github oauth flow: %w", err)
	}
	return flow, nil
}

func (s *Store) CreateSession(ctx context.Context, user identity.GitHubUser, session identity.Session) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin identity session transaction: %w", err)
	}
	defer rollback(transaction)

	now := time.Now().UTC()
	_, err = transaction.ExecContext(ctx, `
		insert into web_identity (
			github_user_id, login, authorized_at, revoked_at, created_at, updated_at
		) values ($1, $2, $3, null, $3, $3)
		on conflict (github_user_id) do update
		set login = excluded.login,
			authorized_at = excluded.authorized_at,
			revoked_at = null,
			updated_at = excluded.updated_at
	`, user.ID, user.Login, now)
	if err != nil {
		return fmt.Errorf("upsert web identity: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into web_session (
			token_digest, csrf_digest, github_user_id, expires_at, revoked_at, created_at
		) values ($1, $2, $3, $4, null, $5)
	`, session.TokenDigest[:], session.CSRFDigest[:], user.ID, session.ExpiresAt, now)
	if err != nil {
		return fmt.Errorf("insert web session: %w", err)
	}
	if err := insertAudit(ctx, transaction, nil, user.ID, "identity.login", nil, map[string]any{}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit identity session transaction: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, tokenDigest [32]byte) (identity.Session, error) {
	var session identity.Session
	var csrfDigest []byte
	var revokedAt sql.NullTime
	err := s.database.QueryRowContext(ctx, `
		select s.csrf_digest, s.github_user_id, i.login, s.expires_at, s.revoked_at
		from web_session s
		join web_identity i on i.github_user_id = s.github_user_id
		where s.token_digest = $1 and i.revoked_at is null
	`, tokenDigest[:]).Scan(
		&csrfDigest,
		&session.Identity.GitHubUserID,
		&session.Identity.Login,
		&session.ExpiresAt,
		&revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Session{}, identity.ErrUnauthorized
	}
	if err != nil {
		return identity.Session{}, fmt.Errorf("query web session: %w", err)
	}
	if len(csrfDigest) != len(session.CSRFDigest) {
		return identity.Session{}, errors.New("stored csrf digest has invalid length")
	}
	copy(session.CSRFDigest[:], csrfDigest)
	session.TokenDigest = tokenDigest
	if revokedAt.Valid {
		session.RevokedAt = &revokedAt.Time
	}
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, tokenDigest [32]byte, now time.Time) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session revocation transaction: %w", err)
	}
	defer rollback(transaction)

	var githubUserID string
	err = transaction.QueryRowContext(ctx, `
		update web_session
		set revoked_at = coalesce(revoked_at, $2)
		where token_digest = $1
		returning github_user_id
	`, tokenDigest[:], now).Scan(&githubUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("revoke web session: %w", err)
	}
	if err := insertAudit(ctx, transaction, nil, githubUserID, "identity.logout", nil, map[string]any{}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit session revocation transaction: %w", err)
	}
	return nil
}

func (s *Store) Bootstrap(ctx context.Context, mutation workspace.BootstrapMutation) (workspace.Workspace, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("begin workspace bootstrap transaction: %w", err)
	}
	defer rollback(transaction)

	if err := lockIdentity(ctx, transaction, mutation.Actor.GitHubUserID); err != nil {
		return workspace.Workspace{}, err
	}
	if existing, found, err := lookupIdempotency(
		ctx,
		transaction,
		mutation.Actor.GitHubUserID,
		"workspace.bootstrap",
		"new",
		mutation.RequestKey,
		mutation.RequestDigest,
	); err != nil || found {
		return existing, err
	}
	now := time.Now().UTC()
	_, err = transaction.ExecContext(ctx, `
		insert into workspace (workspace_id, name, version, created_at, updated_at)
		values ($1, $2, $3, $4, $4)
	`, mutation.Workspace.ID, mutation.Workspace.Name, mutation.Workspace.Version, now)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("insert workspace: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into workspace_member (
			workspace_id, github_user_id, role, revoked_at, created_at, updated_at
		) values ($1, $2, $3, null, $4, $4)
	`, mutation.Workspace.ID, mutation.Actor.GitHubUserID, workspace.RoleAdmin, now)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("insert workspace admin: %w", err)
	}
	if err := insertAudit(
		ctx,
		transaction,
		&mutation.Workspace.ID,
		mutation.Actor.GitHubUserID,
		"workspace.bootstrap",
		&mutation.Workspace.Version,
		map[string]any{},
	); err != nil {
		return workspace.Workspace{}, err
	}
	if err := saveIdempotency(
		ctx,
		transaction,
		mutation.Actor.GitHubUserID,
		"workspace.bootstrap",
		"new",
		mutation.RequestKey,
		mutation.RequestDigest,
		mutation.Workspace,
	); err != nil {
		return workspace.Workspace{}, err
	}
	if err := transaction.Commit(); err != nil {
		return workspace.Workspace{}, fmt.Errorf("commit workspace bootstrap transaction: %w", err)
	}
	return mutation.Workspace, nil
}

func (s *Store) Get(ctx context.Context, workspaceID string) (workspace.Workspace, error) {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("begin workspace read transaction: %w", err)
	}
	defer rollback(transaction)
	result, err := loadWorkspace(ctx, transaction, workspaceID, false)
	if err != nil {
		return workspace.Workspace{}, err
	}
	if err := transaction.Commit(); err != nil {
		return workspace.Workspace{}, fmt.Errorf("commit workspace read transaction: %w", err)
	}
	return result, nil
}

func (s *Store) ConfigureRepositories(
	ctx context.Context,
	mutation workspace.ConfigureRepositoriesMutation,
) (workspace.Workspace, error) {
	return s.mutateWorkspace(
		ctx,
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"workspace.configure_repositories",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(transaction *sql.Tx, now time.Time) error {
			if err := upsertBinding(ctx, transaction, mutation.WorkspaceID, mutation.Project, now); err != nil {
				return err
			}
			return upsertBinding(ctx, transaction, mutation.WorkspaceID, mutation.Knowledge, now)
		},
	)
}

func (s *Store) AddRepositoryBinding(
	ctx context.Context,
	mutation workspace.AddRepositoryBindingMutation,
) (workspace.Workspace, error) {
	return s.mutateWorkspace(
		ctx,
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"workspace.add_repository",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(transaction *sql.Tx, now time.Time) error {
			_, err := transaction.ExecContext(ctx, `
				insert into repository_binding (
					workspace_id, role, github_repository_id, installation_id,
					display_name, access_mode, created_at, updated_at
				) values ($1, $2, $3, $4, $5, $6, $7, $7)
			`,
				mutation.WorkspaceID,
				mutation.Binding.Role,
				mutation.Binding.GitHubRepositoryID,
				mutation.Binding.InstallationID,
				mutation.Binding.DisplayName,
				mutation.Binding.Access,
				now,
			)
			if err != nil {
				return fmt.Errorf("insert repository binding: %w", workspace.ErrPolicyBlocked)
			}
			return nil
		},
	)
}

func (s *Store) RevokeMember(
	ctx context.Context,
	mutation workspace.RevokeMemberMutation,
) (workspace.Workspace, error) {
	return s.mutateWorkspace(
		ctx,
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"workspace.revoke_member",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(transaction *sql.Tx, now time.Time) error {
			result, err := transaction.ExecContext(ctx, `
				update workspace_member
				set revoked_at = $3, updated_at = $3
				where workspace_id = $1 and github_user_id = $2 and revoked_at is null
			`, mutation.WorkspaceID, mutation.GitHubUserID, now)
			if err != nil {
				return fmt.Errorf("revoke workspace member: %w", err)
			}
			rows, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("read revoked member count: %w", err)
			}
			if rows != 1 {
				return workspace.ErrValidation
			}
			return nil
		},
	)
}

func (s *Store) UpsertMember(
	ctx context.Context,
	mutation workspace.UpsertMemberMutation,
) (workspace.Workspace, error) {
	return s.mutateWorkspace(
		ctx,
		mutation.Actor,
		mutation.WorkspaceID,
		mutation.ExpectedVersion,
		"workspace.upsert_member",
		mutation.RequestKey,
		mutation.RequestDigest,
		func(transaction *sql.Tx, now time.Time) error {
			_, err := transaction.ExecContext(ctx, `
				insert into workspace_member (
					workspace_id, github_user_id, role, revoked_at, created_at, updated_at
				) values ($1, $2, $3, null, $4, $4)
				on conflict (workspace_id, github_user_id) do update
				set role = excluded.role,
					revoked_at = null,
					updated_at = excluded.updated_at
			`, mutation.WorkspaceID, mutation.GitHubUserID, mutation.Role, now)
			if err != nil {
				return fmt.Errorf("upsert workspace member: %w", err)
			}
			return nil
		},
	)
}

type mutationFunc func(*sql.Tx, time.Time) error

func (s *Store) mutateWorkspace(
	ctx context.Context,
	actor workspace.Actor,
	workspaceID string,
	expectedVersion int64,
	commandType string,
	requestKey string,
	requestDigest string,
	apply mutationFunc,
) (workspace.Workspace, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("begin workspace mutation transaction: %w", err)
	}
	defer rollback(transaction)

	if existing, found, err := lookupIdempotency(
		ctx,
		transaction,
		actor.GitHubUserID,
		commandType,
		workspaceID,
		requestKey,
		requestDigest,
	); err != nil || found {
		return existing, err
	}
	current, err := loadWorkspace(ctx, transaction, workspaceID, true)
	if err != nil {
		return workspace.Workspace{}, err
	}
	if existing, found, err := lookupIdempotency(
		ctx,
		transaction,
		actor.GitHubUserID,
		commandType,
		workspaceID,
		requestKey,
		requestDigest,
	); err != nil || found {
		return existing, err
	}
	if !isAdmin(current, actor.GitHubUserID) {
		return workspace.Workspace{}, workspace.ErrForbidden
	}
	if current.Version != expectedVersion {
		return workspace.Workspace{}, workspace.ErrVersionConflict
	}
	now := time.Now().UTC()
	if err := apply(transaction, now); err != nil {
		return workspace.Workspace{}, err
	}
	result, err := transaction.ExecContext(ctx, `
		update workspace
		set version = version + 1, updated_at = $3
		where workspace_id = $1 and version = $2
	`, workspaceID, expectedVersion, now)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("update workspace version: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("read workspace update count: %w", err)
	}
	if rows != 1 {
		return workspace.Workspace{}, workspace.ErrVersionConflict
	}
	updated, err := loadWorkspace(ctx, transaction, workspaceID, false)
	if err != nil {
		return workspace.Workspace{}, err
	}
	if err := insertAudit(
		ctx,
		transaction,
		&workspaceID,
		actor.GitHubUserID,
		commandType,
		&updated.Version,
		map[string]any{},
	); err != nil {
		return workspace.Workspace{}, err
	}
	if err := saveIdempotency(
		ctx,
		transaction,
		actor.GitHubUserID,
		commandType,
		workspaceID,
		requestKey,
		requestDigest,
		updated,
	); err != nil {
		return workspace.Workspace{}, err
	}
	if err := transaction.Commit(); err != nil {
		return workspace.Workspace{}, fmt.Errorf("commit workspace mutation transaction: %w", err)
	}
	return updated, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadWorkspace(ctx context.Context, queries queryer, workspaceID string, forUpdate bool) (workspace.Workspace, error) {
	query := `select workspace_id, name, version from workspace where workspace_id = $1`
	if forUpdate {
		query += ` for update`
	}
	var result workspace.Workspace
	err := queries.QueryRowContext(ctx, query, workspaceID).Scan(&result.ID, &result.Name, &result.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.Workspace{}, workspace.ErrForbidden
	}
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("query workspace: %w", err)
	}

	memberRows, err := queries.QueryContext(ctx, `
		select github_user_id, role, revoked_at
		from workspace_member
		where workspace_id = $1
		order by github_user_id
	`, workspaceID)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("query workspace members: %w", err)
	}
	members, err := collectMembers(memberRows)
	if err != nil {
		return workspace.Workspace{}, err
	}
	result.Members = members

	bindingRows, err := queries.QueryContext(ctx, `
		select github_repository_id, installation_id, display_name, role, access_mode
		from repository_binding
		where workspace_id = $1
		order by role
	`, workspaceID)
	if err != nil {
		return workspace.Workspace{}, fmt.Errorf("query repository bindings: %w", err)
	}
	project, knowledge, err := collectBindings(bindingRows)
	if err != nil {
		return workspace.Workspace{}, err
	}
	result.Project = project
	result.Knowledge = knowledge
	return result, nil
}

func collectMembers(memberRows *sql.Rows) (members []workspace.Member, err error) {
	defer func() {
		err = errors.Join(err, memberRows.Close())
	}()
	for memberRows.Next() {
		var member workspace.Member
		var revokedAt sql.NullTime
		if scanErr := memberRows.Scan(&member.GitHubUserID, &member.Role, &revokedAt); scanErr != nil {
			return nil, fmt.Errorf("scan workspace member: %w", scanErr)
		}
		if revokedAt.Valid {
			member.RevokedAt = &revokedAt.Time
		}
		members = append(members, member)
	}
	if rowsErr := memberRows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate workspace members: %w", rowsErr)
	}
	return members, nil
}

func collectBindings(
	bindingRows *sql.Rows,
) (project *workspace.RepositoryBinding, knowledge *workspace.RepositoryBinding, err error) {
	defer func() {
		err = errors.Join(err, bindingRows.Close())
	}()
	for bindingRows.Next() {
		var binding workspace.RepositoryBinding
		if scanErr := bindingRows.Scan(
			&binding.GitHubRepositoryID,
			&binding.InstallationID,
			&binding.DisplayName,
			&binding.Role,
			&binding.Access,
		); scanErr != nil {
			return nil, nil, fmt.Errorf("scan repository binding: %w", scanErr)
		}
		switch binding.Role {
		case workspace.RepositoryRoleProject:
			project = &binding
		case workspace.RepositoryRoleKnowledge:
			knowledge = &binding
		}
	}
	if rowsErr := bindingRows.Err(); rowsErr != nil {
		return nil, nil, fmt.Errorf("iterate repository bindings: %w", rowsErr)
	}
	return project, knowledge, nil
}

func upsertBinding(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID string,
	binding workspace.RepositoryBinding,
	now time.Time,
) error {
	_, err := transaction.ExecContext(ctx, `
		insert into repository_binding (
			workspace_id, role, github_repository_id, installation_id,
			display_name, access_mode, created_at, updated_at
		) values ($1, $2, $3, $4, $5, $6, $7, $7)
		on conflict (workspace_id, role) do update
		set github_repository_id = excluded.github_repository_id,
			installation_id = excluded.installation_id,
			display_name = excluded.display_name,
			access_mode = excluded.access_mode,
			updated_at = excluded.updated_at
	`,
		workspaceID,
		binding.Role,
		binding.GitHubRepositoryID,
		binding.InstallationID,
		binding.DisplayName,
		binding.Access,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert repository binding: %w", err)
	}
	return nil
}

func lookupIdempotency(
	ctx context.Context,
	transaction *sql.Tx,
	actor string,
	commandType string,
	targetID string,
	requestKey string,
	requestDigest string,
) (workspace.Workspace, bool, error) {
	var storedDigest string
	var response []byte
	err := transaction.QueryRowContext(ctx, `
		select request_digest, response
		from command_idempotency
		where actor_github_user_id = $1
			and command_type = $2
			and target_id = $3
			and request_key = $4
	`, actor, commandType, targetID, requestKey).Scan(&storedDigest, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.Workspace{}, false, nil
	}
	if err != nil {
		return workspace.Workspace{}, false, fmt.Errorf("query command idempotency: %w", err)
	}
	if storedDigest != requestDigest {
		return workspace.Workspace{}, false, workspace.ErrVersionConflict
	}
	var result workspace.Workspace
	if err := json.Unmarshal(response, &result); err != nil {
		return workspace.Workspace{}, false, fmt.Errorf("decode idempotent response: %w", err)
	}
	return result, true, nil
}

func lockIdentity(ctx context.Context, transaction *sql.Tx, githubUserID string) error {
	var lockedID string
	err := transaction.QueryRowContext(ctx, `
		select github_user_id
		from web_identity
		where github_user_id = $1 and revoked_at is null
		for update
	`, githubUserID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.ErrForbidden
	}
	if err != nil {
		return fmt.Errorf("lock bootstrap identity: %w", err)
	}
	return nil
}

func saveIdempotency(
	ctx context.Context,
	transaction *sql.Tx,
	actor string,
	commandType string,
	targetID string,
	requestKey string,
	requestDigest string,
	response workspace.Workspace,
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
	`, actor, commandType, targetID, requestKey, requestDigest, encoded, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert command idempotency: %w", err)
	}
	return nil
}

func insertAudit(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID *string,
	actor string,
	action string,
	version *int64,
	details map[string]any,
) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		insert into audit_event (
			workspace_id, actor_github_user_id, action, object_version, details, created_at
		) values ($1, $2, $3, $4, $5, $6)
	`, workspaceID, actor, action, version, encoded, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func isAdmin(value workspace.Workspace, githubUserID string) bool {
	for _, member := range value.Members {
		if member.GitHubUserID == githubUserID && member.Role == workspace.RoleAdmin && member.RevokedAt == nil {
			return true
		}
	}
	return false
}

func rollback(transaction *sql.Tx) {
	_ = transaction.Rollback()
}

var (
	_ identity.Store  = (*Store)(nil)
	_ workspace.Store = (*Store)(nil)
)
