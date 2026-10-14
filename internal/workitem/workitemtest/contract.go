// Package workitemtest holds the shared WorkItem store contract. Both the
// in-memory and the PostgreSQL adapters must pass exactly these cases, so the
// two adapters cannot drift apart.
package workitemtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/workitem"
)

// Seed gives the adapter a chance to create any rows the store references. The
// memory adapter has no foreign keys and seeds nothing.
type Seed interface {
	Workspace(t *testing.T, workspaceID string)
	Run(t *testing.T, workspaceID, workItemID, runID string)
}

type noSeed struct{}

func (noSeed) Workspace(*testing.T, string)           {}
func (noSeed) Run(*testing.T, string, string, string) {}

// NoSeed is the seed used by adapters without referential constraints.
var NoSeed Seed = noSeed{}

func baseItem(workspaceID, workItemID string) workitem.WorkItem {
	created := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	return workitem.WorkItem{
		ID: workItemID, WorkspaceID: workspaceID, Title: "Fix the failing check",
		TargetRepositoryID: "1401511579",
		Requirement: workitem.VersionedContent{
			Version: 1, Content: "requirement text", Digest: workitem.ContentDigest("requirement text"),
		},
		Acceptance: workitem.VersionedContent{
			Version: 1, Content: "acceptance text", Digest: workitem.ContentDigest("acceptance text"),
		},
		State: workitem.StateOpen, Version: 1, CreatedByUserID: "101",
		CreatedAt: created, UpdatedAt: created,
	}
}

// RunStoreContract exercises the store contract both adapters must satisfy.
func RunStoreContract(t *testing.T, store workitem.Store, seed Seed) {
	ctx := context.Background()
	workspaceA, workspaceB := "ws_contract_a", "ws_contract_b"
	seed.Workspace(t, workspaceA)
	seed.Workspace(t, workspaceB)

	t.Run("create is idempotent per request key and scoped per workspace", func(t *testing.T) {
		item := baseItem(workspaceA, "wi_contract_1")
		seed.Run(t, workspaceA, item.ID, "run_contract_1")
		created, err := store.Create(ctx, workitem.CreateMutation{
			WorkItem: item, Actor: workitem.Actor{GitHubUserID: "101"},
			RequestKey: "req-1", RequestDigest: workitem.Digest("content-1"),
			TargetProjectID: item.TargetRepositoryID,
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if created.State != workitem.StateOpen || created.Version != 1 {
			t.Fatalf("Create() = %#v", created)
		}
		replay, err := store.Create(ctx, workitem.CreateMutation{
			WorkItem: baseItem(workspaceA, "wi_contract_other"), Actor: workitem.Actor{GitHubUserID: "101"},
			RequestKey: "req-1", RequestDigest: workitem.Digest("content-1"),
			TargetProjectID: item.TargetRepositoryID,
		})
		if err != nil || replay.ID != item.ID || replay.Version != 1 {
			t.Fatalf("idempotent replay = %#v, %v", replay, err)
		}
		if _, err := store.Create(ctx, workitem.CreateMutation{
			WorkItem: baseItem(workspaceA, "wi_contract_other"), Actor: workitem.Actor{GitHubUserID: "101"},
			RequestKey: "req-1", RequestDigest: workitem.Digest("content-2"),
			TargetProjectID: item.TargetRepositoryID,
		}); !errors.Is(err, workitem.ErrVersionConflict) {
			t.Fatalf("same key different content error = %v, want ErrVersionConflict", err)
		}
		if _, err := store.Get(ctx, workspaceB, item.ID); !errors.Is(err, workitem.ErrNotFound) { //nolint:staticcheck // cross-workspace isolation is the assertion
			t.Fatalf("cross workspace Get() error = %v, want ErrNotFound", err)
		}
		found, err := store.Get(ctx, workspaceA, item.ID)
		if err != nil || found.ID != item.ID {
			t.Fatalf("Get() = %#v, %v", found, err)
		}
	})

	t.Run("cancel is versioned, idempotent and terminal", func(t *testing.T) {
		item := baseItem(workspaceA, "wi_contract_cancel")
		if _, err := store.Create(ctx, workitem.CreateMutation{
			WorkItem: item, Actor: workitem.Actor{GitHubUserID: "101"},
			RequestKey: "req-cancel-create", RequestDigest: workitem.Digest("cancel-create"),
			TargetProjectID: item.TargetRepositoryID,
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		canceledAt := time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)
		mutation := workitem.CancelMutation{
			WorkspaceID: workspaceA, WorkItemID: item.ID, ExpectedVersion: 1,
			Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "req-cancel",
			RequestDigest: workitem.Digest("cancel"), Reason: "goal withdrawn", CanceledAt: canceledAt,
		}
		if _, err := store.Cancel(ctx, workitem.CancelMutation{
			WorkspaceID: workspaceA, WorkItemID: item.ID, ExpectedVersion: 99,
			Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "req-stale",
			RequestDigest: workitem.Digest("stale"), CanceledAt: canceledAt,
		}); !errors.Is(err, workitem.ErrVersionConflict) {
			t.Fatalf("stale cancel error = %v, want ErrVersionConflict", err)
		}
		canceled, err := store.Cancel(ctx, mutation)
		if err != nil || canceled.State != workitem.StateCanceled || canceled.Version != 2 {
			t.Fatalf("Cancel() = %#v, %v", canceled, err)
		}
		replay, err := store.Cancel(ctx, mutation)
		if err != nil || replay.Version != 2 || replay.State != workitem.StateCanceled {
			t.Fatalf("idempotent cancel = %#v, %v", replay, err)
		}
		if _, err := store.Cancel(ctx, workitem.CancelMutation{
			WorkspaceID: workspaceA, WorkItemID: item.ID, ExpectedVersion: 2,
			Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "req-cancel-again",
			RequestDigest: workitem.Digest("cancel-again"), CanceledAt: canceledAt,
		}); !errors.Is(err, workitem.ErrPolicyBlocked) {
			t.Fatalf("second cancel error = %v, want ErrPolicyBlocked", err)
		}
	})

	t.Run("advancement authority moves only through a settled repair", func(t *testing.T) {
		item := baseItem(workspaceA, "wi_contract_run")
		seed.Run(t, workspaceA, item.ID, "run_contract_first")
		seed.Run(t, workspaceA, item.ID, "run_contract_repair")
		if _, err := store.Create(ctx, workitem.CreateMutation{
			WorkItem: item, Actor: workitem.Actor{GitHubUserID: "101"},
			RequestKey: "req-run-create", RequestDigest: workitem.Digest("run-create"),
			TargetProjectID: item.TargetRepositoryID,
		}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		boundAt := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
		initial, err := store.AssociateRun(ctx, workitem.AssociateRunMutation{
			WorkItemID: item.ID, ExpectedVersion: 1, BoundAt: boundAt,
			RequestKey: "req-bind", RequestDigest: workitem.Digest("bind"),
			Binding: workitem.RunBinding{
				WorkItemID: item.ID, RunID: "run_contract_first", Kind: workitem.BindingInitial,
				BoundByUserID: "101", BoundAt: boundAt,
			},
		})
		if err != nil || initial.CurrentRunID != "run_contract_first" || initial.Version != 2 {
			t.Fatalf("AssociateRun(initial) = %#v, %v", initial, err)
		}
		if _, err := store.AssociateRun(ctx, workitem.AssociateRunMutation{
			WorkItemID: item.ID, ExpectedVersion: 2, BoundAt: boundAt,
			RequestKey: "req-bind-second", RequestDigest: workitem.Digest("bind-second"),
			Binding: workitem.RunBinding{
				WorkItemID: item.ID, RunID: "run_contract_repair", Kind: workitem.BindingInitial,
				BoundByUserID: "101", BoundAt: boundAt,
			},
		}); !errors.Is(err, workitem.ErrPolicyBlocked) {
			t.Fatalf("second initial bind error = %v, want ErrPolicyBlocked", err)
		}
		repair, err := store.AssociateRun(ctx, workitem.AssociateRunMutation{
			WorkItemID: item.ID, ExpectedVersion: 2, BoundAt: boundAt,
			RequestKey: "req-bind-repair", RequestDigest: workitem.Digest("bind-repair"),
			Binding: workitem.RunBinding{
				WorkItemID: item.ID, RunID: "run_contract_repair", Kind: workitem.BindingRepair,
				SourceRunID: "run_contract_first", Reason: "post-merge verification failed",
				MergedBaselineOID: "b99754c271e7c8b759e9affa7d9fc875625f6d8c",
				BoundByUserID:     "101", BoundAt: boundAt,
			},
		})
		if err != nil || repair.CurrentRunID != "run_contract_repair" || repair.Version != 3 {
			t.Fatalf("AssociateRun(repair) = %#v, %v", repair, err)
		}
		bindings, err := store.RunBindings(ctx, workspaceA, item.ID)
		if err != nil || len(bindings) != 2 || bindings[0].Kind != workitem.BindingInitial ||
			bindings[1].SourceRunID != "run_contract_first" {
			t.Fatalf("RunBindings() = %#v, %v", bindings, err)
		}
	})

}
