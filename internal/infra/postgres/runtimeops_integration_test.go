//go:build integration

package postgres_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/runtimeops"
)

func TestRuntimeOpsPostgres(t *testing.T) {
	ctx := t.Context()
	database, err := postgres.Open(ctx, os.Getenv("KOWA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	store := postgres.NewStore(database)

	if _, err := database.ExecContext(ctx, `
 insert into workspace(workspace_id,name,version,created_at,updated_at)
 values('runtimeops-workspace','runtime ops tests',1,now(),now()) on conflict do nothing
 `); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
 insert into runtime_registration(runtime_id,runtime_epoch,github_user_id,provider_id,provider_version,
   provider_authenticated,registered_at,last_heartbeat_at)
 values('runtimeops-runtime','epoch-1','9001','codex','1.0',true,now(),now())
 on conflict (runtime_id) do update set runtime_epoch = excluded.runtime_epoch
 `); err != nil {
		t.Fatal(err)
	}

	observedAt := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	observation := runtimeops.Observation{
		RuntimeID: "runtimeops-runtime", RuntimeEpoch: "epoch-1", ObservedAt: observedAt,
		ReceivedAt: observedAt.Add(time.Second), Executable: true, CLIVersion: "codex-cli 0.162.1",
		VersionCompatible: true, Authenticated: true, CanExecute: false, Detail: "quota",
	}
	if err := store.ReportObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	// Identical re-delivery is idempotent and keeps the original watermark.
	if err := store.ReportObservation(ctx, observation); err != nil {
		t.Fatalf("idempotent report = %v", err)
	}
	latest, found, err := store.LatestObservation(ctx, "runtimeops-runtime", "epoch-1")
	if err != nil || !found || !latest.ReceivedAt.Equal(observation.ReceivedAt) ||
		latest.CanExecute || latest.CLIVersion != "codex-cli 0.162.1" {
		t.Fatalf("latest = %+v, found=%v, %v", latest, found, err)
	}
	// The same identity with different content conflicts, including a
	// detail-only difference (the memory adapter must agree).
	conflict := observation
	conflict.CanExecute = true
	if err := store.ReportObservation(ctx, conflict); !errors.Is(err, runtimeops.ErrRequestConflict) {
		t.Fatalf("conflicting report = %v", err)
	}
	detailConflict := observation
	detailConflict.Detail = "different detail"
	if err := store.ReportObservation(ctx, detailConflict); !errors.Is(err, runtimeops.ErrRequestConflict) {
		t.Fatalf("detail-conflicting report = %v", err)
	}
	// A newer observation of a superseded epoch remains readable without an
	// epoch filter (for stale projections) but not under the current epoch.
	newer := observation
	newer.RuntimeEpoch = "epoch-0"
	newer.ObservedAt = observedAt.Add(time.Minute)
	newer.ReceivedAt = observedAt.Add(2 * time.Minute)
	if err := store.ReportObservation(ctx, newer); err != nil {
		t.Fatal(err)
	}
	filtered, found, err := store.LatestObservation(ctx, "runtimeops-runtime", "epoch-1")
	if err != nil || !found || !filtered.ReceivedAt.Equal(observation.ReceivedAt) {
		t.Fatalf("epoch-filtered latest = %+v, found=%v, %v", filtered, found, err)
	}
	anyEpoch, found, err := store.LatestObservation(ctx, "runtimeops-runtime", "")
	if err != nil || !found || anyEpoch.RuntimeEpoch != "epoch-0" {
		t.Fatalf("any-epoch latest = %+v, found=%v, %v", anyEpoch, found, err)
	}

	// Execution configuration: create, idempotent replay, request conflict,
	// version drift, and monotonic versions.
	command := runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: "runtimeops-workspace", ActorID: "9001", RequestKey: "cfg-1", ExpectedVersion: 0,
		Capabilities: []runtimeops.Capability{{ID: "plan.create", Version: 1}},
		Runtimes:     []string{"runtimeops-runtime"},
	}
	created, err := store.SaveExecutionConfig(ctx, command, "sha256:aaaa", observedAt)
	if err != nil || created.Version != 1 {
		t.Fatalf("created config = %+v, %v", created, err)
	}
	replayed, err := store.SaveExecutionConfig(ctx, command, "sha256:aaaa", observedAt.Add(time.Hour))
	if err != nil || replayed.Version != 1 || !replayed.UpdatedAt.Equal(observedAt) {
		t.Fatalf("replayed config = %+v, %v", replayed, err)
	}
	if _, err := store.SaveExecutionConfig(ctx, command, "sha256:bbbb", observedAt); !errors.Is(err, runtimeops.ErrRequestConflict) {
		t.Fatalf("request conflict = %v", err)
	}
	drift := command
	drift.RequestKey = "cfg-2"
	drift.ExpectedVersion = 5
	if _, err := store.SaveExecutionConfig(ctx, drift, "sha256:cccc", observedAt); !errors.Is(err, runtimeops.ErrVersionConflict) {
		t.Fatalf("version drift = %v", err)
	}
	next := drift
	next.ExpectedVersion = 1
	next.Capabilities = []runtimeops.Capability{}
	next.Runtimes = []string{}
	updated, err := store.SaveExecutionConfig(ctx, next, "sha256:dddd", observedAt)
	if err != nil || updated.Version != 2 || len(updated.Capabilities) != 0 || len(updated.Runtimes) != 0 {
		t.Fatalf("updated config = %+v, %v", updated, err)
	}
	readBack, found, err := store.ExecutionConfig(ctx, "runtimeops-workspace")
	if err != nil || !found || readBack.Version != 2 || readBack.RequestKey != "cfg-2" {
		t.Fatalf("read back config = %+v, found=%v, %v", readBack, found, err)
	}
}
