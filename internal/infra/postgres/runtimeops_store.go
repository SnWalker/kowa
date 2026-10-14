package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SnWalker/kowa/internal/runtimeops"
)

// ReportObservation persists one ladder observation; the (runtime, epoch,
// observedAt) primary key makes re-delivery idempotent, and a conflicting
// re-use of the same identity is refused.
func (s *Store) ReportObservation(ctx context.Context, observation runtimeops.Observation) error {
	result, err := s.database.ExecContext(ctx, `
		insert into runtime_observation (
			runtime_id, runtime_epoch, observed_at, received_at,
			executable, cli_version, version_compatible, authenticated, can_execute, detail
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		on conflict (runtime_id, runtime_epoch, observed_at) do nothing
	`, observation.RuntimeID, observation.RuntimeEpoch, observation.ObservedAt, observation.ReceivedAt,
		observation.Executable, observation.CLIVersion, observation.VersionCompatible,
		observation.Authenticated, observation.CanExecute, observation.Detail)
	if err != nil {
		return fmt.Errorf("insert runtime observation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read observation insert count: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var existing runtimeops.Observation
	var existingDetail sql.NullString
	err = s.database.QueryRowContext(ctx, `
		select runtime_id, runtime_epoch, observed_at, received_at,
			executable, cli_version, version_compatible, authenticated, can_execute, detail
		from runtime_observation
		where runtime_id = $1 and runtime_epoch = $2 and observed_at = $3
	`, observation.RuntimeID, observation.RuntimeEpoch, observation.ObservedAt).Scan(
		&existing.RuntimeID, &existing.RuntimeEpoch, &existing.ObservedAt, &existing.ReceivedAt,
		&existing.Executable, &existing.CLIVersion, &existing.VersionCompatible,
		&existing.Authenticated, &existing.CanExecute, &existingDetail)
	if err != nil {
		return fmt.Errorf("read existing observation: %w", err)
	}
	existing.Detail = existingDetail.String
	if existing.CLIVersion != observation.CLIVersion || existing.Executable != observation.Executable ||
		existing.VersionCompatible != observation.VersionCompatible ||
		existing.Authenticated != observation.Authenticated ||
		existing.CanExecute != observation.CanExecute || existing.Detail != observation.Detail {
		return runtimeops.ErrRequestConflict
	}
	return nil
}

// LatestObservation returns the newest received observation for a runtime; an
// empty epoch matches any generation.
func (s *Store) LatestObservation(
	ctx context.Context,
	runtimeID string,
	epoch string,
) (runtimeops.Observation, bool, error) {
	query := `
		select runtime_id, runtime_epoch, observed_at, received_at,
			executable, cli_version, version_compatible, authenticated, can_execute, detail
		from runtime_observation
		where runtime_id = $1
	`
	arguments := []any{runtimeID}
	if epoch != "" {
		query += " and runtime_epoch = $2"
		arguments = append(arguments, epoch)
	}
	query += " order by received_at desc, observed_at desc limit 1"
	var observation runtimeops.Observation
	var detail sql.NullString
	err := s.database.QueryRowContext(ctx, query, arguments...).Scan(
		&observation.RuntimeID, &observation.RuntimeEpoch, &observation.ObservedAt, &observation.ReceivedAt,
		&observation.Executable, &observation.CLIVersion, &observation.VersionCompatible,
		&observation.Authenticated, &observation.CanExecute, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return runtimeops.Observation{}, false, nil
	}
	if err != nil {
		return runtimeops.Observation{}, false, fmt.Errorf("read latest observation: %w", err)
	}
	observation.Detail = detail.String
	return observation, true, nil
}

func (s *Store) ExecutionConfig(ctx context.Context, workspaceID string) (runtimeops.ExecutionConfig, bool, error) {
	var config runtimeops.ExecutionConfig
	var capabilities, runtimes []byte
	err := s.database.QueryRowContext(ctx, `
		select workspace_id, version, capabilities, runtimes, request_key, request_digest,
			updated_by_github_user_id, updated_at
		from workspace_execution_config where workspace_id = $1
	`, workspaceID).Scan(&config.WorkspaceID, &config.Version, &capabilities, &runtimes,
		&config.RequestKey, &config.RequestDigest, &config.UpdatedByGitHubUserID, &config.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runtimeops.ExecutionConfig{}, false, nil
	}
	if err != nil {
		return runtimeops.ExecutionConfig{}, false, fmt.Errorf("read execution config: %w", err)
	}
	if err := json.Unmarshal(capabilities, &config.Capabilities); err != nil {
		return runtimeops.ExecutionConfig{}, false, fmt.Errorf("decode execution capabilities: %w", err)
	}
	if err := json.Unmarshal(runtimes, &config.Runtimes); err != nil {
		return runtimeops.ExecutionConfig{}, false, fmt.Errorf("decode execution runtimes: %w", err)
	}
	return config, true, nil
}

// SaveExecutionConfig applies one admin mutation atomically: idempotent per
// (workspace, request key) with an identical digest, conflicting otherwise and
// on version drift.
func (s *Store) SaveExecutionConfig(
	ctx context.Context,
	command runtimeops.UpdateExecutionConfigCommand,
	digest string,
	now time.Time,
) (runtimeops.ExecutionConfig, error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("begin execution config transaction: %w", err)
	}
	defer rollback(transaction)
	var existing runtimeops.ExecutionConfig
	var existingCapabilities, existingRuntimes []byte
	err = transaction.QueryRowContext(ctx, `
		select workspace_id, version, capabilities, runtimes, request_key, request_digest,
			updated_by_github_user_id, updated_at
		from workspace_execution_config where workspace_id = $1
		for update
	`, command.WorkspaceID).Scan(&existing.WorkspaceID, &existing.Version, &existingCapabilities,
		&existingRuntimes, &existing.RequestKey, &existing.RequestDigest,
		&existing.UpdatedByGitHubUserID, &existing.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if command.ExpectedVersion != 0 {
			return runtimeops.ExecutionConfig{}, runtimeops.ErrVersionConflict
		}
		capabilities, marshalErr := json.Marshal(command.Capabilities)
		if marshalErr != nil {
			return runtimeops.ExecutionConfig{}, fmt.Errorf("encode execution capabilities: %w", marshalErr)
		}
		runtimes, marshalErr := json.Marshal(command.Runtimes)
		if marshalErr != nil {
			return runtimeops.ExecutionConfig{}, fmt.Errorf("encode execution runtimes: %w", marshalErr)
		}
		if _, err := transaction.ExecContext(ctx, `
			insert into workspace_execution_config (
				workspace_id, version, capabilities, runtimes, request_key, request_digest,
				updated_by_github_user_id, updated_at
			) values ($1, 1, $2, $3, $4, $5, $6, $7)
		`, command.WorkspaceID, capabilities, runtimes, command.RequestKey, digest,
			command.ActorID, now.UTC()); err != nil {
			return runtimeops.ExecutionConfig{}, fmt.Errorf("insert execution config: %w", err)
		}
		if err := transaction.Commit(); err != nil {
			return runtimeops.ExecutionConfig{}, fmt.Errorf("commit execution config: %w", err)
		}
		return runtimeops.ExecutionConfig{
			WorkspaceID: command.WorkspaceID, Version: 1,
			Capabilities: command.Capabilities, Runtimes: command.Runtimes,
			RequestKey: command.RequestKey, RequestDigest: digest,
			UpdatedByGitHubUserID: command.ActorID, UpdatedAt: now.UTC(),
		}, nil
	case err != nil:
		return runtimeops.ExecutionConfig{}, fmt.Errorf("lock execution config: %w", err)
	}
	if err := json.Unmarshal(existingCapabilities, &existing.Capabilities); err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("decode existing capabilities: %w", err)
	}
	if err := json.Unmarshal(existingRuntimes, &existing.Runtimes); err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("decode existing runtimes: %w", err)
	}
	if existing.RequestKey == command.RequestKey {
		if existing.RequestDigest == digest {
			return existing, nil
		}
		return runtimeops.ExecutionConfig{}, runtimeops.ErrRequestConflict
	}
	if existing.Version != command.ExpectedVersion {
		return runtimeops.ExecutionConfig{}, runtimeops.ErrVersionConflict
	}
	capabilities, err := json.Marshal(command.Capabilities)
	if err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("encode execution capabilities: %w", err)
	}
	runtimes, err := json.Marshal(command.Runtimes)
	if err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("encode execution runtimes: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		update workspace_execution_config
		set version = version + 1, capabilities = $2, runtimes = $3,
			request_key = $4, request_digest = $5,
			updated_by_github_user_id = $6, updated_at = $7
		where workspace_id = $1
	`, command.WorkspaceID, capabilities, runtimes, command.RequestKey, digest,
		command.ActorID, now.UTC()); err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("update execution config: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return runtimeops.ExecutionConfig{}, fmt.Errorf("commit execution config: %w", err)
	}
	return runtimeops.ExecutionConfig{
		WorkspaceID: command.WorkspaceID, Version: existing.Version + 1,
		Capabilities: command.Capabilities, Runtimes: command.Runtimes,
		RequestKey: command.RequestKey, RequestDigest: digest,
		UpdatedByGitHubUserID: command.ActorID, UpdatedAt: now.UTC(),
	}, nil
}
