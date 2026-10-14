package workitem_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workitem/workitemtest"
)

type fixedProjects struct {
	repositoryID string
	err          error
}

func (f fixedProjects) ProjectRepositoryID(context.Context, string) (string, error) {
	return f.repositoryID, f.err
}

type fixedRuns struct {
	settled map[string]bool
}

func (f fixedRuns) RunSettled(_ context.Context, runID string) (bool, error) {
	return f.settled[runID], nil
}

type fixedOperations struct {
	unsettled int
}

func (f fixedOperations) UnsettledOperations(context.Context, string, string) (int, error) {
	return f.unsettled, nil
}

type sequenceIDs struct{ next int }

func (s *sequenceIDs) New() (string, error) {
	s.next++
	return "wi_test_" + strconv.Itoa(s.next), nil
}

func newService(t *testing.T, operations workitem.ExternalOperationGuard, settled ...string) *workitem.Service {
	t.Helper()
	settledRuns := map[string]bool{}
	for _, runID := range settled {
		settledRuns[runID] = true
	}
	return workitem.NewService(
		workitem.NewMemoryStore(),
		fixedProjects{repositoryID: "1401511579"},
		fixedRuns{settled: settledRuns},
		operations,
		&sequenceIDs{},
		func() time.Time { return time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) },
	)
}

func createCommand() workitem.CreateCommand {
	return workitem.CreateCommand{
		WorkspaceID: "ws_1", RequestKey: "req-1", Title: "Fix the failing test",
		Requirement: workitem.VersionedContent{Version: 1, Content: "make calc add correctly"},
		Acceptance:  workitem.VersionedContent{Version: 1, Content: "tests pass and PR is merged"},
	}
}

func mustCreate(t *testing.T, service *workitem.Service) workitem.WorkItem {
	t.Helper()
	created, err := service.Create(context.Background(), workitem.Actor{GitHubUserID: "101"}, createCommand())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}

func TestCreateFreezesIntakeWithoutStartingARun(t *testing.T) {
	service := newService(t, fixedOperations{})
	created := mustCreate(t, service)
	if created.State != workitem.StateOpen || created.Version != 1 {
		t.Fatalf("Create() = %#v", created)
	}
	if created.CurrentRunID != "" {
		t.Fatalf("Create() bound a run: %#v", created)
	}
	if created.TargetRepositoryID != "1401511579" {
		t.Fatalf("Create() target = %q", created.TargetRepositoryID)
	}
	if created.Requirement.Digest != workitem.ContentDigest("make calc add correctly") ||
		created.Acceptance.Digest != workitem.ContentDigest("tests pass and PR is merged") {
		t.Fatalf("Create() digests = %#v", created)
	}
	if created.CreatedByUserID != "101" {
		t.Fatalf("Create() creator = %q", created.CreatedByUserID)
	}
}

func TestCreateRequiresAProjectBinding(t *testing.T) {
	service := workitem.NewService(workitem.NewMemoryStore(), fixedProjects{err: errors.New("no binding")},
		fixedRuns{}, fixedOperations{}, &sequenceIDs{}, time.Now)
	if _, err := service.Create(context.Background(), workitem.Actor{GitHubUserID: "101"}, createCommand()); err == nil {
		t.Fatal("Create() without a project binding succeeded")
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	service := newService(t, fixedOperations{})
	cases := map[string]func(*workitem.CreateCommand){
		"empty title":       func(c *workitem.CreateCommand) { c.Title = "   " },
		"empty requirement": func(c *workitem.CreateCommand) { c.Requirement.Content = "" },
		"zero version":      func(c *workitem.CreateCommand) { c.Acceptance.Version = 0 },
		"bad request key":   func(c *workitem.CreateCommand) { c.RequestKey = "has space" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			command := createCommand()
			mutate(&command)
			if _, err := service.Create(context.Background(), workitem.Actor{GitHubUserID: "101"}, command); !errors.Is(err, workitem.ErrValidation) {
				t.Fatalf("Create() error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestCreateRejectsAnUnauthenticatedActor(t *testing.T) {
	service := newService(t, fixedOperations{})
	if _, err := service.Create(context.Background(), workitem.Actor{}, createCommand()); !errors.Is(err, workitem.ErrValidation) {
		t.Fatalf("Create() without an authenticated actor = %v, want ErrValidation", err)
	}
}

func TestCreateIsIdempotentAndConflictsOnDifferentContent(t *testing.T) {
	service := newService(t, fixedOperations{})
	first := mustCreate(t, service)
	replay, err := service.Create(context.Background(), workitem.Actor{GitHubUserID: "101"}, createCommand())
	if err != nil || replay.ID != first.ID {
		t.Fatalf("idempotent Create() = %#v, %v", replay, err)
	}
	changed := createCommand()
	changed.Requirement.Content = "different requirement"
	if _, err := service.Create(context.Background(), workitem.Actor{GitHubUserID: "101"}, changed); !errors.Is(err, workitem.ErrVersionConflict) {
		t.Fatalf("same key different content error = %v, want ErrVersionConflict", err)
	}
}

func TestCancelRequiresReconciledExternalOperations(t *testing.T) {
	blocked := newService(t, fixedOperations{unsettled: 1})
	created := mustCreate(t, blocked)
	cancel := workitem.CancelCommand{
		WorkspaceID: "ws_1", WorkItemID: created.ID,
		RequestKey: "req-cancel", ExpectedVersion: 1, Reason: "goal withdrawn",
	}
	if _, err := blocked.Cancel(context.Background(), workitem.Actor{GitHubUserID: "101"}, cancel); !errors.Is(err, workitem.ErrExternalUnknown) {
		t.Fatalf("Cancel() with unsettled operations = %v, want ErrExternalUnknown", err)
	}
	item, err := blocked.Get(context.Background(), "ws_1", created.ID)
	if err != nil || item.State != workitem.StateOpen || item.Version != 1 {
		t.Fatalf("rejected cancel changed facts: %#v, %v", item, err)
	}

	reconciled := newService(t, fixedOperations{})
	created2 := mustCreate(t, reconciled)
	canceled, err := reconciled.Cancel(context.Background(), workitem.Actor{GitHubUserID: "101"}, workitem.CancelCommand{
		WorkspaceID: "ws_1", WorkItemID: created2.ID,
		RequestKey: "req-cancel", ExpectedVersion: 1, Reason: "goal withdrawn",
	})
	if err != nil || canceled.State != workitem.StateCanceled {
		t.Fatalf("Cancel() after reconciliation = %#v, %v", canceled, err)
	}
	if _, err := reconciled.Cancel(context.Background(), workitem.Actor{GitHubUserID: "101"}, workitem.CancelCommand{
		WorkspaceID: "ws_1", WorkItemID: created2.ID,
		RequestKey: "req-cancel-again", ExpectedVersion: canceled.Version, Reason: "again",
	}); !errors.Is(err, workitem.ErrPolicyBlocked) {
		t.Fatalf("cancel of a CANCELED item = %v, want ErrPolicyBlocked", err)
	}
}

func TestAssociateRunKeepsOneAdvancementAuthority(t *testing.T) {
	service := newService(t, fixedOperations{}, "run_initial")
	created := mustCreate(t, service)
	bound, err := service.AssociateRun(context.Background(), workitem.AssociateRunCommand{
		WorkspaceID: "ws_1", WorkItemID: created.ID, RunID: "run_initial",
		Kind: workitem.BindingInitial, ActorID: "101", RequestKey: "bind-1", ExpectedVersion: 1,
	})
	if err != nil || bound.CurrentRunID != "run_initial" || bound.Version != 2 {
		t.Fatalf("AssociateRun(initial) = %#v, %v", bound, err)
	}
	if _, err := service.AssociateRun(context.Background(), workitem.AssociateRunCommand{
		WorkspaceID: "ws_1", WorkItemID: created.ID, RunID: "run_other",
		Kind: workitem.BindingInitial, ActorID: "101", RequestKey: "bind-2", ExpectedVersion: 2,
	}); !errors.Is(err, workitem.ErrPolicyBlocked) {
		t.Fatalf("second initial bind = %v, want ErrPolicyBlocked", err)
	}
	if _, err := service.AssociateRun(context.Background(), workitem.AssociateRunCommand{
		WorkspaceID: "ws_1", WorkItemID: created.ID, RunID: "run_repair",
		Kind: workitem.BindingRepair, SourceRunID: "run_other", Reason: "wrong source",
		ActorID: "101", RequestKey: "bind-3", ExpectedVersion: 2,
	}); !errors.Is(err, workitem.ErrPolicyBlocked) {
		t.Fatalf("repair from a foreign run = %v, want ErrPolicyBlocked", err)
	}
	item, err := service.Get(context.Background(), "ws_1", created.ID)
	if err != nil || item.CurrentRunID != "run_initial" || item.Version != 2 {
		t.Fatalf("rejected repair changed facts: %#v, %v", item, err)
	}

	live := newService(t, fixedOperations{})
	createdLive := mustCreate(t, live)
	boundLive, err := live.AssociateRun(context.Background(), workitem.AssociateRunCommand{
		WorkspaceID: "ws_1", WorkItemID: createdLive.ID, RunID: "run_live",
		Kind: workitem.BindingInitial, ActorID: "101", RequestKey: "bind-live", ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("AssociateRun(initial) error = %v", err)
	}
	if _, err := live.AssociateRun(context.Background(), workitem.AssociateRunCommand{
		WorkspaceID: "ws_1", WorkItemID: createdLive.ID, RunID: "run_repair_live",
		Kind: workitem.BindingRepair, SourceRunID: "run_live", Reason: "post-merge failed",
		ActorID: "101", RequestKey: "bind-repair-live", ExpectedVersion: boundLive.Version,
	}); !errors.Is(err, workitem.ErrPolicyBlocked) {
		t.Fatalf("repair over a live run = %v, want ErrPolicyBlocked", err)
	}

	repair, err := service.AssociateRun(context.Background(), workitem.AssociateRunCommand{
		WorkspaceID: "ws_1", WorkItemID: created.ID, RunID: "run_repair",
		Kind: workitem.BindingRepair, SourceRunID: "run_initial", Reason: "post-merge failed",
		MergedBaselineOID: "b99754c271e7c8b759e9affa7d9fc875625f6d8c",
		ActorID:           "101", RequestKey: "bind-4", ExpectedVersion: 2,
	})
	if err != nil || repair.CurrentRunID != "run_repair" || repair.Version != 3 {
		t.Fatalf("AssociateRun(repair) = %#v, %v", repair, err)
	}
	bindings, err := service.RunBindings(context.Background(), "ws_1", created.ID)
	if err != nil || len(bindings) != 2 || bindings[0].Kind != workitem.BindingInitial ||
		bindings[1].SourceRunID != "run_initial" || bindings[1].MergedBaselineOID == "" {
		t.Fatalf("RunBindings() = %#v, %v", bindings, err)
	}
}

func TestListIsScopedToTheWorkspace(t *testing.T) {
	service := newService(t, fixedOperations{})
	mustCreate(t, service)
	other := createCommand()
	other.WorkspaceID = "ws_2"
	other.RequestKey = "req-2"
	if _, err := service.Create(context.Background(), workitem.Actor{GitHubUserID: "101"}, other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	items, err := service.List(context.Background(), "ws_1")
	if err != nil || len(items) != 1 || items[0].WorkspaceID != "ws_1" {
		t.Fatalf("List() = %#v, %v", items, err)
	}
	if _, err := service.Get(context.Background(), "ws_1", "unknown"); !errors.Is(err, workitem.ErrNotFound) {
		t.Fatalf("Get(unknown) = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreSatisfiesTheStoreContract(t *testing.T) {
	workitemtest.RunStoreContract(t, workitem.NewMemoryStore(), workitemtest.NoSeed)
}
