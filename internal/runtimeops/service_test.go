package runtimeops_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/runtimeops"
)

type fakeSource map[string]execution.RuntimeStatus

func (f fakeSource) RuntimeStatus(_ context.Context, runtimeID string) (execution.RuntimeStatus, error) {
	status, ok := f[runtimeID]
	if !ok {
		return execution.RuntimeStatus{}, execution.ErrNotFound
	}
	return status, nil
}

type fixture struct {
	t     *testing.T
	store *runtimeops.MemoryStore
	now   time.Time
	svc   *runtimeops.Service
}

func newFixture(t *testing.T, source runtimeops.RuntimeSource) *fixture {
	t.Helper()
	f := &fixture{t: t, store: runtimeops.NewMemoryStore(), now: time.Date(2026, 10, 14, 2, 0, 0, 0, time.UTC)}
	f.svc = runtimeops.NewService(f.store, source, func() time.Time { return f.now })
	return f
}

func statusFor(runtimeID, epoch string, capabilities ...execution.Capability) execution.RuntimeStatus {
	return execution.RuntimeStatus{
		ID: runtimeID, Epoch: epoch, GitHubUserID: "9001",
		Provider:     execution.ProviderSelection{ID: "codex", Version: "1.0"},
		Capabilities: capabilities, RegisteredAt: time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC),
	}
}

func observationFor(runtimeID, epoch string, observedAt time.Time) runtimeops.Observation {
	return runtimeops.Observation{
		RuntimeID: runtimeID, RuntimeEpoch: epoch, ObservedAt: observedAt,
		Executable: true, CLIVersion: "1.2.3", VersionCompatible: true,
		Authenticated: true, CanExecute: true,
	}
}

func TestObservationIdempotencyAndConflicts(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t, fakeSource{})
	observation := observationFor("runtime-1", "epoch-1", f.now.Add(-time.Minute))
	if err := f.svc.ReportObservation(ctx, observation); err != nil {
		t.Fatal(err)
	}
	// The identical re-delivery is idempotent.
	if err := f.svc.ReportObservation(ctx, observation); err != nil {
		t.Fatalf("idempotent report = %v", err)
	}
	// The same identity with different content conflicts, for every content
	// field: canExecute and detail must behave identically in both adapters.
	conflict := observation
	conflict.CanExecute = false
	if err := f.svc.ReportObservation(ctx, conflict); !errors.Is(err, runtimeops.ErrRequestConflict) {
		t.Fatalf("conflicting report = %v", err)
	}
	detailConflict := observation
	detailConflict.Detail = "different detail"
	if err := f.svc.ReportObservation(ctx, detailConflict); !errors.Is(err, runtimeops.ErrRequestConflict) {
		t.Fatalf("detail-conflicting report = %v", err)
	}
	// A CLI version beyond the frozen 256 bound is rejected by the service
	// itself, so both adapters agree (F3b).
	tooLong := observationFor("runtime-1", "epoch-1", f.now.Add(time.Minute))
	tooLong.CLIVersion = strings.Repeat("v", 257)
	if err := f.svc.ReportObservation(ctx, tooLong); !errors.Is(err, runtimeops.ErrValidation) {
		t.Fatalf("over-long cliVersion = %v, want validation", err)
	}

	// Invalid shapes are rejected before touching the store.
	invalid := observationFor("runtime-1", "epoch-1", f.now)
	invalid.ObservedAt = time.Time{}
	if err := f.svc.ReportObservation(ctx, invalid); !errors.Is(err, runtimeops.ErrValidation) {
		t.Fatalf("missing observedAt = %v", err)
	}
}

func TestRuntimeProjectionFreshnessAndAuthorization(t *testing.T) {
	ctx := t.Context()
	source := fakeSource{
		"runtime-1": statusFor("runtime-1", "epoch-2", execution.Capability{ID: "plan.create", Version: 1}),
		"runtime-2": statusFor("runtime-2", "epoch-9"),
	}
	f := newFixture(t, source)
	if _, err := f.svc.UpdateExecutionConfig(ctx, runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: "workspace-a", ActorID: "101", RequestKey: "enable-1", ExpectedVersion: 0,
		Runtimes: []string{"runtime-1", "runtime-2"},
	}); err != nil {
		t.Fatal(err)
	}

	// No observation yet: unknown, never healthy.
	views, err := f.svc.WorkspaceRuntimes(ctx, "workspace-a")
	if err != nil || len(views) != 2 {
		t.Fatalf("views = %+v, %v", views, err)
	}
	for _, view := range views {
		if view.Freshness != runtimeops.FreshnessUnknown {
			t.Fatalf("unobserved freshness = %+v", view)
		}
	}

	// A current-generation observation inside the TTL is fresh.
	if err := f.svc.ReportObservation(ctx, observationFor("runtime-1", "epoch-2", f.now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	view, err := f.svc.WorkspaceRuntime(ctx, "workspace-a", "runtime-1")
	if err != nil || view.Freshness != runtimeops.FreshnessFresh {
		t.Fatalf("fresh view = %+v, %v", view, err)
	}
	if view.Observation == nil || !view.Observation.CanExecute || view.ProviderID != "codex" ||
		len(view.Capabilities) != 1 || view.Capabilities[0].ID != "plan.create" {
		t.Fatalf("fresh projection = %+v", view)
	}

	// Exceeding the TTL turns stale with the expired reason.
	f.now = f.now.Add(runtimeops.DefaultObservationTTL + time.Minute)
	view, err = f.svc.WorkspaceRuntime(ctx, "workspace-a", "runtime-1")
	if err != nil || view.Freshness != runtimeops.FreshnessStale || view.StaleReason != runtimeops.StaleExpired {
		t.Fatalf("stale view = %+v, %v", view, err)
	}

	// An observation of a superseded epoch is stale, not lost and not fresh.
	if err := f.svc.ReportObservation(ctx, observationFor("runtime-2", "epoch-old", f.now)); err != nil {
		t.Fatal(err)
	}
	view, err = f.svc.WorkspaceRuntime(ctx, "workspace-a", "runtime-2")
	if err != nil || view.Freshness != runtimeops.FreshnessStale ||
		view.StaleReason != runtimeops.StaleSupersededEpoch || view.Observation == nil {
		t.Fatalf("superseded view = %+v, %v", view, err)
	}

	// Workspace IDs follow the S01 base64url alphabet, which may start with a
	// dash or underscore; the local service must never re-impose the runtime
	// ID grammar on them.
	oddWorkspace := "-_base64url-styled-workspace"
	if _, err := f.svc.ExecutionConfig(ctx, oddWorkspace); err != nil {
		t.Fatalf("odd but valid workspace id rejected: %v", err)
	}
	if _, err := f.svc.UpdateExecutionConfig(ctx, runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: oddWorkspace, ActorID: "101", RequestKey: "odd-1", ExpectedVersion: 0,
	}); err != nil {
		t.Fatalf("config write for odd workspace id = %v", err)
	}

	// Cross-workspace and unauthorized runtimes are refused.
	if _, err := f.svc.WorkspaceRuntime(ctx, "workspace-b", "runtime-1"); !errors.Is(err, runtimeops.ErrForbidden) {
		t.Fatalf("cross workspace = %v", err)
	}
	if _, err := f.svc.WorkspaceRuntime(ctx, "workspace-a", "runtime-3"); !errors.Is(err, runtimeops.ErrForbidden) {
		t.Fatalf("unauthorized runtime = %v", err)
	}
}

func TestExecutionConfigIdempotencyAndVersions(t *testing.T) {
	ctx := t.Context()
	f := newFixture(t, fakeSource{})
	empty, err := f.svc.ExecutionConfig(ctx, "workspace-a")
	if err != nil || empty.Version != 0 || empty.Capabilities == nil || empty.Runtimes == nil {
		t.Fatalf("empty config = %+v, %v", empty, err)
	}
	command := runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: "workspace-a", ActorID: "101", RequestKey: "cfg-1", ExpectedVersion: 0,
		Capabilities: []runtimeops.Capability{{ID: "plan.create", Version: 1}},
		Runtimes:     []string{"runtime-1"},
	}
	created, err := f.svc.UpdateExecutionConfig(ctx, command)
	if err != nil || created.Version != 1 || created.UpdatedByGitHubUserID != "101" {
		t.Fatalf("created config = %+v, %v", created, err)
	}
	// The same request key with the same content is idempotent.
	again, err := f.svc.UpdateExecutionConfig(ctx, command)
	if err != nil || again.Version != 1 || !again.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("idempotent config = %+v, %v", again, err)
	}
	// The same request key with different content conflicts.
	different := command
	different.Runtimes = []string{"runtime-1", "runtime-2"}
	if _, err := f.svc.UpdateExecutionConfig(ctx, different); !errors.Is(err, runtimeops.ErrRequestConflict) {
		t.Fatalf("request conflict = %v", err)
	}
	// Version drift conflicts; the frozen version is untouched.
	drift := runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: "workspace-a", ActorID: "101", RequestKey: "cfg-2", ExpectedVersion: 7,
	}
	if _, err := f.svc.UpdateExecutionConfig(ctx, drift); !errors.Is(err, runtimeops.ErrVersionConflict) {
		t.Fatalf("version drift = %v", err)
	}
	current, err := f.svc.ExecutionConfig(ctx, "workspace-a")
	if err != nil || current.Version != 1 {
		t.Fatalf("config after conflicts = %+v, %v", current, err)
	}
	// A valid next version increments monotonically.
	next := runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: "workspace-a", ActorID: "101", RequestKey: "cfg-3", ExpectedVersion: 1,
		Capabilities: []runtimeops.Capability{{ID: "code.change", Version: 2}, {ID: "plan.create", Version: 1}},
		Runtimes:     []string{"runtime-2"},
	}
	updated, err := f.svc.UpdateExecutionConfig(ctx, next)
	if err != nil || updated.Version != 2 || len(updated.Capabilities) != 2 ||
		updated.Capabilities[0].ID != "code.change" || updated.Runtimes[0] != "runtime-2" {
		t.Fatalf("updated config = %+v, %v", updated, err)
	}
	// Invalid shapes are rejected.
	invalid := next
	invalid.Capabilities = []runtimeops.Capability{{ID: "Bad ID", Version: 1}}
	if _, err := f.svc.UpdateExecutionConfig(ctx, invalid); !errors.Is(err, runtimeops.ErrValidation) {
		t.Fatalf("invalid capability = %v", err)
	}
	duplicate := next
	duplicate.Capabilities = []runtimeops.Capability{{ID: "plan.create", Version: 1}, {ID: "plan.create", Version: 1}}
	if _, err := f.svc.UpdateExecutionConfig(ctx, duplicate); !errors.Is(err, runtimeops.ErrValidation) {
		t.Fatalf("duplicate capability = %v", err)
	}
}
