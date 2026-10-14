//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/workflow"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workitem/workitemtest"
	"github.com/SnWalker/kowa/internal/workspace"
)

const integrationDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// integrationCommandDigest builds the bare request digest the command
// idempotency table requires (the display prefix belongs to domain digests).
func integrationCommandDigest(fill string) string {
	return strings.Repeat(fill, 64)
}

// integrationDomainDigest builds a frozen domain digest with the sha256 prefix.
func integrationDomainDigest(fill string) string {
	return "sha256:" + strings.Repeat(fill, 64)
}

const integrationHeadOID = "de62a4324cad57908376f8c5f3e14bd3bd3dab95"
const integrationMergeOID = "ca6c6ec8446ec687213d58064a7fd8363050321a"

type deliveryFixture struct {
	store    *postgres.Store
	database *sql.DB
	service  *execution.Service
	plan     string
}

func newDeliveryFixture(t *testing.T) *deliveryFixture {
	t.Helper()
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	database, err := postgres.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database.Close() error = %v", err)
		}
	})
	store := postgres.NewStore(database)
	service := execution.NewService(store, time.Now)
	return &deliveryFixture{store: store, database: database, service: service}
}

func (f *deliveryFixture) workspace(t *testing.T, workspaceID string) {
	t.Helper()
	// The workspace bootstrap requires the acting identity to exist; the
	// identity service creates it during login, so seed it the same way here.
	if _, err := f.database.ExecContext(t.Context(), `
		insert into web_identity (github_user_id, login, authorized_at, revoked_at, created_at, updated_at)
		values ('101', 'member-a', now(), null, now(), now())
		on conflict (github_user_id) do nothing
	`); err != nil {
		t.Fatalf("seed identity error = %v", err)
	}
	_, err := f.store.Bootstrap(t.Context(), workspace.BootstrapMutation{
		Actor:      workspace.Actor{GitHubUserID: "101"},
		RequestKey: "bootstrap-" + workspaceID,
		Workspace: workspace.Workspace{
			ID: workspaceID, Name: "delivery " + workspaceID, Version: 1,
			Members: []workspace.Member{{GitHubUserID: "101", Role: workspace.RoleAdmin}},
		},
		RequestDigest: integrationDigest[len("sha256:"):],
	})
	if err != nil && !errors.Is(err, workspace.ErrVersionConflict) {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if _, err := f.database.ExecContext(t.Context(), `
		insert into repository_binding (workspace_id, role, github_repository_id, installation_id,
			display_name, access_mode, created_at, updated_at)
		values ($1, 'project', '1401511579', '12345', 'kowa-sandbox', 'read_write', now(), now())
		on conflict (workspace_id, role) do nothing
	`, workspaceID); err != nil {
		t.Fatalf("seed project binding error = %v", err)
	}
}

func (f *deliveryFixture) publishPlan(t *testing.T) {
	t.Helper()
	definition := workflow.Definition{
		SchemaVersion: workflow.SchemaVersion, Kind: workflow.DefinitionKind,
		Name: "delivery", Version: 1,
		Nodes: []workflow.Node{{
			ID: "plan", Type: workflow.NodeCapability, Mandatory: true,
			Capability: workflow.CapabilityRef{ID: "plan.create", Version: 1},
		}},
	}
	plan, err := workflow.Compile(definition, workflow.Catalog{
		{ID: "plan.create", Version: 1}: {
			Input:  workflow.Schema{Fields: map[string]workflow.ValueType{}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{"result_ref": workflow.ValueArtifactRef}},
		},
	})
	if err != nil {
		t.Fatalf("workflow.Compile() error = %v", err)
	}
	if err := f.store.PublishPlan(t.Context(), plan); err != nil {
		// A previous test in the same database may have published the plan.
		t.Logf("PublishPlan() returned %v; continuing with digest %s", err, plan.Digest)
	}
	f.plan = plan.Digest
}

// nodeRunIDFor derives the node run id of one seeded delivery run.
func nodeRunIDFor(runID string) string { return runID + "-plan-1" }

func (f *deliveryFixture) run(t *testing.T, workspaceID, workItemID, runID string, machineUserID string) {
	t.Helper()
	if f.plan == "" {
		f.publishPlan(t)
	}
	err := f.service.CreateRun(t.Context(), execution.CreateRunCommand{
		Run: execution.Run{
			ID: runID, WorkspaceID: workspaceID, WorkItemID: workItemID,
			StartedByGitHubUserID: "101", GitExecutionUserID: machineUserID,
			DefinitionName: "delivery", DefinitionVersion: 1, DefinitionDigest: f.plan,
			State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: nodeRunIDFor(runID), RunID: runID, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: integrationDigest, Version: 1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
}

// pgWorkItemStore adapts the shared store to the WorkItem port for the contract.
type pgWorkItemStore struct{ store *postgres.Store }

func (a pgWorkItemStore) Create(ctx context.Context, m workitem.CreateMutation) (workitem.WorkItem, error) {
	return a.store.CreateWorkItem(ctx, m)
}
func (a pgWorkItemStore) Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error) {
	return a.store.WorkItem(ctx, workspaceID, workItemID)
}
func (a pgWorkItemStore) List(ctx context.Context, workspaceID string) ([]workitem.WorkItem, error) {
	return a.store.WorkItems(ctx, workspaceID)
}
func (a pgWorkItemStore) Cancel(ctx context.Context, m workitem.CancelMutation) (workitem.WorkItem, error) {
	return a.store.CancelWorkItem(ctx, m)
}
func (a pgWorkItemStore) AssociateRun(ctx context.Context, m workitem.AssociateRunMutation) (workitem.WorkItem, error) {
	return a.store.AssociateRun(ctx, m)
}
func (a pgWorkItemStore) RunBindings(ctx context.Context, workspaceID, workItemID string) ([]workitem.RunBinding, error) {
	return a.store.WorkItemRunBindings(ctx, workspaceID, workItemID)
}

// pgSeed satisfies the shared contract's seeding port.
type pgSeed struct{ fixture *deliveryFixture }

func (s pgSeed) Workspace(t *testing.T, workspaceID string) { s.fixture.workspace(t, workspaceID) }
func (s pgSeed) Run(t *testing.T, workspaceID, workItemID, runID string) {
	s.fixture.run(t, workspaceID, workItemID, runID, "135722937")
}

func TestWorkItemStorePostgresContract(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workitemtest.RunStoreContract(t, pgWorkItemStore{fixture.store}, pgSeed{fixture})
}

func TestHumanTaskStorePostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_ht", "wi_ht", "run_ht"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Fix", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-create",
		RequestDigest: workitem.Digest("wi-create"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}

	object := humantask.ObjectBinding{Kind: "pull_request_review", ID: "42", Version: integrationHeadOID}
	task := humantask.HumanTask{
		ID: "ht_1", WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID, NodeRunID: "",
		Request: humantask.Request{
			Version: 1, Title: "Wait for review", Kind: "github_review",
			Mode: humantask.ModeExternalFact, Fields: []humantask.Field{}, Object: object,
		},
		RequestDigest: integrationDomainDigest("1"), State: humantask.StateOpen, Version: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if _, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: task, ActorID: "101", RequestKey: "ht-create",
		RequestDigest: integrationCommandDigest("2"),
	}); err != nil {
		t.Fatalf("CreateHumanTask() error = %v", err)
	}
	replay, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: humantask.HumanTask{ID: "ht_other", WorkspaceID: workspaceID, WorkItemID: workItemID,
			Request: task.Request, RequestDigest: task.RequestDigest, State: humantask.StateOpen, Version: 1},
		ActorID: "101", RequestKey: "ht-create", RequestDigest: integrationCommandDigest("2"),
	})
	if err != nil || replay.ID != task.ID {
		t.Fatalf("idempotent CreateHumanTask() = %#v, %v", replay, err)
	}
	if _, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: task, ActorID: "101", RequestKey: "ht-create",
		RequestDigest: integrationCommandDigest("3"),
	}); !errors.Is(err, humantask.ErrVersionConflict) {
		t.Fatalf("same key different content = %v, want ErrVersionConflict", err)
	}

	// A task whose work item belongs to another workspace cannot be persisted:
	// the composite foreign key makes the ownership rule a database invariant.
	foreign := task
	foreign.ID = "ht_foreign"
	foreign.WorkItemID = "wi_somewhere_else"
	if _, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: foreign, ActorID: "101", RequestKey: "ht-foreign",
		RequestDigest: integrationCommandDigest("9"),
	}); err == nil {
		t.Fatal("CreateHumanTask() accepted a work item of another workspace")
	}

	stored, err := fixture.store.HumanTask(t.Context(), workspaceID, task.ID)
	if err != nil || stored.State != humantask.StateOpen || stored.Request.Object.Kind != "pull_request_review" {
		t.Fatalf("HumanTask() = %#v, %v", stored, err)
	}

	resolved, err := fixture.store.ResolveHumanTaskFromFact(t.Context(), humantask.ResolveExternalFactMutation{
		TaskID: task.ID, WorkspaceID: workspaceID, ActorID: "135722937",
		RequestKey:    "ht-resolve",
		RequestDigest: integrationCommandDigest("4"),
		ObservationID: "obs_merge", ResolvedAt: time.Now().UTC(),
	})
	if err != nil || resolved.State != humantask.StateResolved || resolved.ExternalFactID != "obs_merge" {
		t.Fatalf("ResolveHumanTaskFromFact() = %#v, %v", resolved, err)
	}
	replayed, err := fixture.store.ResolveHumanTaskFromFact(t.Context(), humantask.ResolveExternalFactMutation{
		TaskID: task.ID, WorkspaceID: workspaceID, ActorID: "135722937",
		RequestKey:    "ht-resolve",
		RequestDigest: integrationCommandDigest("4"),
		ObservationID: "obs_merge", ResolvedAt: time.Now().UTC(),
	})
	if err != nil || replayed.State != humantask.StateResolved {
		t.Fatalf("idempotent ResolveHumanTaskFromFact() = %#v, %v", replayed, err)
	}

	// Two OPEN tasks on the same pull request: the one bound to the superseded
	// head expires when the head moves on, the one bound to the current head
	// stays open.
	current := task
	current.ID = "ht_current"
	current.Request.Object.Version = integrationMergeOID
	current.RequestDigest = integrationDomainDigest("6")
	if _, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: current, ActorID: "101", RequestKey: "ht-create-current",
		RequestDigest: integrationCommandDigest("6"),
	}); err != nil {
		t.Fatalf("CreateHumanTask() error = %v", err)
	}
	superseded := task
	superseded.ID = "ht_superseded"
	superseded.RequestDigest = integrationDomainDigest("7")
	if _, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: superseded, ActorID: "101", RequestKey: "ht-create-superseded",
		RequestDigest: integrationCommandDigest("7"),
	}); err != nil {
		t.Fatalf("CreateHumanTask() error = %v", err)
	}
	expired, err := fixture.store.ExpireHumanTasksBoundTo(t.Context(), workspaceID,
		"pull_request_review", "42", integrationMergeOID, "the pull request head moved", "135722937", time.Now().UTC())
	if err != nil || expired != 1 {
		t.Fatalf("ExpireHumanTasksBoundTo() = %d, %v", expired, err)
	}
	stillOpen, err := fixture.store.HumanTask(t.Context(), workspaceID, "ht_current")
	if err != nil || stillOpen.State != humantask.StateOpen {
		t.Fatalf("current task after expiry = %#v, %v", stillOpen, err)
	}
	expiredTask, err := fixture.store.HumanTask(t.Context(), workspaceID, "ht_superseded")
	if err != nil || expiredTask.State != humantask.StateExpired || expiredTask.ExpiredReason == "" {
		t.Fatalf("superseded task after expiry = %#v, %v", expiredTask, err)
	}
	if _, err := fixture.store.SetHumanTaskState(t.Context(), humantask.StateMutation{
		TaskID: "ht_current", WorkspaceID: workspaceID, State: humantask.StateCanceled,
		Reason: "target canceled", ActorID: "135722937", At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SetHumanTaskState() error = %v", err)
	}
}

func TestExternalOperationPostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_op", "wi_op", "run_op"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Fix", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-op",
		RequestDigest: workitem.Digest("wi-op"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}

	operation := githubdelivery.ExternalOperation{
		OperationID: "op_1", WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		Kind: githubdelivery.OperationPublishPR, State: githubdelivery.StatePending,
		Request: githubdelivery.OperationRequest{
			RepositoryID: "1401511579", Role: "project", Operation: "publish_pr",
			Ref: "refs/heads/task/s05", BaseRef: "refs/heads/main", CommitOID: integrationHeadOID,
			ExpectedGitHubUserID: "135722937", RequestedByGitHubUserID: "101",
		},
		RequestDigest: integrationDigest[len("sha256:"):], Version: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if _, err := fixture.store.CreateExternalOperation(t.Context(), githubdelivery.AdmitMutation{
		Operation: operation, RequestKey: "admit-1", RequestDigest: integrationDigest[len("sha256:"):],
	}); err != nil {
		t.Fatalf("CreateExternalOperation() error = %v", err)
	}
	found, exists, err := fixture.store.ExternalOperationForRequest(t.Context(), workspaceID,
		githubdelivery.OperationPublishPR, "1401511579", "refs/heads/task/s05", integrationHeadOID)
	if err != nil || !exists || found.OperationID != operation.OperationID || found.State != githubdelivery.StatePending {
		t.Fatalf("ExternalOperationForRequest() = %#v, %v, %v", found, exists, err)
	}
	if _, err := fixture.store.UpdateExternalOperation(t.Context(), githubdelivery.UpdateMutation{
		WorkspaceID: workspaceID, OperationID: operation.OperationID, ExpectedVersion: 1,
		State: githubdelivery.StateAttempted, At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpdateExternalOperation() error = %v", err)
	}
	if _, err := fixture.store.UpdateExternalOperation(t.Context(), githubdelivery.UpdateMutation{
		WorkspaceID: workspaceID, OperationID: operation.OperationID, ExpectedVersion: 1,
		State: githubdelivery.StateSucceeded, At: time.Now().UTC(),
	}); !errors.Is(err, githubdelivery.ErrVersionConflict) {
		t.Fatalf("stale operation transition = %v, want ErrVersionConflict", err)
	}
	unsettled, err := fixture.store.UnsettledExternalOperations(t.Context(), workspaceID, workItemID)
	if err != nil || unsettled != 1 {
		t.Fatalf("UnsettledExternalOperations() = %d, %v", unsettled, err)
	}
	// A concurrent admission for the same delivery object loses on the partial
	// unique index instead of creating a second live write intent.
	second := operation
	second.OperationID = "op_2"
	second.RequestDigest = integrationDomainDigest("b")
	if _, err := fixture.store.CreateExternalOperation(t.Context(), githubdelivery.AdmitMutation{
		Operation: second, RequestKey: "admit-2", RequestDigest: integrationCommandDigest("b"),
	}); !errors.Is(err, githubdelivery.ErrPolicyBlocked) {
		t.Fatalf("duplicate delivery intent = %v, want ErrPolicyBlocked", err)
	}
	// The work item cannot be canceled while that operation is unsettled, and
	// the guard runs inside the cancel transaction.
	if _, err := fixture.store.CancelWorkItem(t.Context(), workitem.CancelMutation{
		WorkspaceID: workspaceID, WorkItemID: workItemID, ExpectedVersion: 1,
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "cancel-blocked",
		RequestDigest: integrationCommandDigest("c"), Reason: "stop", CanceledAt: time.Now().UTC(),
	}); !errors.Is(err, workitem.ErrExternalUnknown) {
		t.Fatalf("cancel with a live operation = %v, want ErrExternalUnknown", err)
	}
	if _, err := fixture.store.UpdateExternalOperation(t.Context(), githubdelivery.UpdateMutation{
		WorkspaceID: workspaceID, OperationID: operation.OperationID, ExpectedVersion: 2,
		State: githubdelivery.StateFailed, UnknownReason: "reconciled as never created", At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("settle operation error = %v", err)
	}
	if _, err := fixture.store.CancelWorkItem(t.Context(), workitem.CancelMutation{
		WorkspaceID: workspaceID, WorkItemID: workItemID, ExpectedVersion: 1,
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "cancel-legal",
		RequestDigest: integrationCommandDigest("d"), Reason: "stop", CanceledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("cancel after reconciliation error = %v", err)
	}

	observation := githubdelivery.Observation{
		ObservationID: "obs_1", WorkspaceID: workspaceID, RepositoryID: "1401511579",
		SubjectKind: githubdelivery.SubjectPullRequest, SubjectKey: "42",
		ObservedAt: time.Now().UTC(), ActorID: "135722937",
		PullRequest: &githubdelivery.PullRequestFact{
			Number: "42", AuthorID: "135722937", HeadOID: integrationHeadOID,
			HeadRef: "task/s05", BaseRef: "refs/heads/main", State: "open",
		},
	}
	if _, err := fixture.store.RecordGitHubObservation(t.Context(), githubdelivery.RecordObservationMutation{
		Observation: observation,
	}); err != nil {
		t.Fatalf("RecordGitHubObservation() error = %v", err)
	}
	unknown := githubdelivery.Observation{
		ObservationID: "obs_2", WorkspaceID: workspaceID, RepositoryID: "1401511579",
		SubjectKind: githubdelivery.SubjectPullRequest, SubjectKey: "42",
		ObservedAt: time.Now().UTC().Add(time.Second), Unknown: true, UnknownReason: "gh unavailable",
	}
	if _, err := fixture.store.RecordGitHubObservation(t.Context(), githubdelivery.RecordObservationMutation{
		Observation: unknown,
	}); err != nil {
		t.Fatalf("RecordGitHubObservation(unknown) error = %v", err)
	}
	latest, err := fixture.store.LatestGitHubObservations(t.Context(), workspaceID, "1401511579")
	if err != nil || len(latest) != 1 {
		t.Fatalf("LatestGitHubObservations() = %#v, %v", latest, err)
	}
	if !latest[0].Unknown || latest[0].PullRequest != nil {
		t.Fatalf("an unknown attempt replaced the known fact: %#v", latest[0])
	}
	known, knownFound, err := fixture.store.GitHubObservation(t.Context(), workspaceID, "obs_1")
	if err != nil || !knownFound || known.PullRequest == nil || known.PullRequest.HeadOID != integrationHeadOID {
		t.Fatalf("GitHubObservation(obs_1) = %#v, %v, %v", known, knownFound, err)
	}
}

// TestCancellationAndAdmissionAreMutuallyExclusivePostgres proves the cancel
// guard and the admission guard share the work item row lock: exactly one of a
// concurrent admission and cancellation may proceed, so a canceled work item
// never carries a live external operation.
func TestCancellationAndAdmissionAreMutuallyExclusivePostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_race", "wi_race", "run_race"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Race", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-race",
		RequestDigest: workitem.Digest("wi-race"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	operation := githubdelivery.ExternalOperation{
		OperationID: "op_race", WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		Kind: githubdelivery.OperationVerifyChange, State: githubdelivery.StatePending,
		Request: githubdelivery.OperationRequest{
			RepositoryID: "1401511579", Role: "project", Operation: "verify_change",
			Ref: "refs/heads/task/race", CommitOID: integrationHeadOID,
			ExpectedGitHubUserID: "135722937", RequestedByGitHubUserID: "101",
		},
		RequestDigest: integrationDomainDigest("e"), Version: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	type outcome struct {
		admitted bool
		canceled bool
		err      error
	}
	results := make(chan outcome, 2)
	go func() {
		_, err := fixture.store.CreateExternalOperation(context.Background(), githubdelivery.AdmitMutation{
			Operation: operation, RequestKey: "admit-race", RequestDigest: integrationCommandDigest("e"),
		})
		results <- outcome{admitted: err == nil, err: err}
	}()
	go func() {
		_, err := fixture.store.CancelWorkItem(context.Background(), workitem.CancelMutation{
			WorkspaceID: workspaceID, WorkItemID: workItemID, ExpectedVersion: 1,
			Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "cancel-race",
			RequestDigest: integrationCommandDigest("f"), Reason: "race", CanceledAt: time.Now().UTC(),
		})
		results <- outcome{canceled: err == nil, err: err}
	}()
	first, second := <-results, <-results
	admitted := first.admitted || second.admitted
	canceled := first.canceled || second.canceled
	if admitted && canceled {
		t.Fatalf("admission and cancellation both succeeded: %#v %#v", first, second)
	}
	if !admitted && !canceled {
		t.Fatalf("neither side made progress: %#v %#v", first, second)
	}
	item, err := fixture.store.WorkItem(t.Context(), workspaceID, workItemID)
	if err != nil {
		t.Fatalf("read work item: %v", err)
	}
	unsettled, err := fixture.store.UnsettledExternalOperations(t.Context(), workspaceID, workItemID)
	if err != nil {
		t.Fatalf("count unsettled operations: %v", err)
	}
	if canceled && unsettled > 0 {
		t.Fatalf("a canceled work item carries %d live external operations", unsettled)
	}
	if item.State == workitem.StateCanceled && unsettled > 0 {
		t.Fatal("canceled work item with a live operation")
	}
}

// TestCancellationPropagatesToHumanTasksPostgres proves the same transaction
// cancels the work item's open human requests, so a canceled goal cannot keep
// answering and a late answer is refused.
func TestCancellationPropagatesToHumanTasksPostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_cancel", "wi_cancel", "run_cancel"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Cancel", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-cancel",
		RequestDigest: workitem.Digest("wi-cancel"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	task := humantask.HumanTask{
		ID: "ht_cancel", WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		Request: humantask.Request{
			Version: 1, Title: "Approve", Kind: "plan_approval", Mode: humantask.ModeKowaAnswer,
			Fields:     []humantask.Field{{Name: "decision", Type: "decision", Required: true}},
			Object:     humantask.ObjectBinding{Kind: "plan_artifact", ID: "art_1", Version: integrationDigest},
			Candidates: []string{"101"},
		},
		RequestDigest: integrationDigest, State: humantask.StateOpen, Version: 1,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if _, err := fixture.store.CreateHumanTask(t.Context(), humantask.CreateMutation{
		Task: task, ActorID: "101", RequestKey: "ht-cancel",
		RequestDigest: integrationCommandDigest("7"),
	}); err != nil {
		t.Fatalf("CreateHumanTask() error = %v", err)
	}
	if _, err := fixture.store.CancelWorkItem(t.Context(), workitem.CancelMutation{
		WorkspaceID: workspaceID, WorkItemID: workItemID, ExpectedVersion: 1,
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "cancel-goal",
		RequestDigest: integrationCommandDigest("8"), Reason: "goal withdrawn", CanceledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CancelWorkItem() error = %v", err)
	}
	requests, err := fixture.store.ListHumanTasks(t.Context(), workspaceID, false)
	if err != nil || len(requests) != 1 {
		t.Fatalf("ListHumanTasks() = %#v, %v", requests, err)
	}
	if requests[0].State != humantask.StateCanceled || requests[0].ExpiredReason == "" {
		t.Fatalf("the canceled goal left its request %s: %#v", requests[0].State, requests[0])
	}
	// The real answer path refuses a late structured answer.
	humans := humantask.NewService(pgHumanTaskAnswers{fixture.store},
		noObservations{}, pgWorkItemExistence{fixture.store}, &evaluationSeqIDs{}, time.Now)
	if _, err := humans.Answer(t.Context(), humantask.Actor{GitHubUserID: "101"}, humantask.AnswerCommand{
		WorkspaceID: workspaceID, TaskID: task.ID, RequestKey: "late-answer", RequestVersion: 1,
		Object: task.Request.Object, Decision: humantask.DecisionApproved,
		Values: map[string]string{"decision": "approved"},
	}); !errors.Is(err, humantask.ErrPolicyBlocked) {
		t.Fatalf("a late answer on the canceled request = %v, want ErrPolicyBlocked", err)
	}
	if _, err := fixture.store.SetHumanTaskState(t.Context(), humantask.StateMutation{
		TaskID: task.ID, WorkspaceID: workspaceID, State: humantask.StateExpired,
		Reason: "late", ActorID: "135722937", At: time.Now().UTC(),
	}); !errors.Is(err, humantask.ErrPolicyBlocked) {
		t.Fatalf("a late transition on the canceled request = %v, want ErrPolicyBlocked", err)
	}
}

func TestClosurePostgresIsAtomicAndRaceSafe(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_close", "wi_close", "run_close"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	item := workitem.WorkItem{
		ID: workItemID, WorkspaceID: workspaceID, Title: "Fix", TargetRepositoryID: "1401511579",
		Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
		Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
		State:       workitem.StateOpen, Version: 1, CreatedByUserID: "101",
		CurrentRunID: runID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: item, Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-close",
		RequestDigest: workitem.Digest("wi-close"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	record := evaluation.ClosureRecord{
		ClosureID: "cl_1", WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		DefinitionName: "delivery", DefinitionVersion: 1, DefinitionDigest: fixture.plan,
		PolicyVersion: "delivery-policy-v1", RequirementDigest: item.Requirement.Digest,
		KnowledgeSnapshotID: "ks_red", KnowledgeBindingID: "kbe_red",
		MergeRepositoryID: "1401511579", MergeCommitOID: integrationMergeOID, PullRequestNumber: "42",
		MergeObservationID: "obs_merge", ReviewObservationID: "obs_review",
		ReviewHeadOID: integrationHeadOID, ReviewerGitHubUserID: "277460407",
		MergedByGitHubUserID: "277460407", MergedByType: "User",
		RequiredTestIDs: []string{"ve_tests"}, IndependentReviewID: "ve_review",
		PostMergeVerificationID: "ve_post",
		ChecklistDigest:         integrationDigest, CreatedByGitHubUserID: "101", CreatedAt: time.Now().UTC(),
	}
	// The closure store requires the referenced rows to exist.
	dataset := fixture
	for _, row := range []struct {
		statement string
		arguments []any
	}{
		{statement: `insert into github_observation (observation_id, workspace_id, repository_id, subject_kind, subject_key,
			observed_at, actor_github_user_id, unknown, fact)
		 values ('obs_merge', $1, '1401511579', 'pull_request', '42', now(), '135722937', false, '{}'),
		        ('obs_review', $1, '1401511579', 'pull_request_review', '42/5001', now(), '135722937', false, '{}')
		 on conflict (observation_id) do nothing`, arguments: []any{workspaceID}},
		{statement: `insert into verification_evidence (verification_id, workspace_id, work_item_id, kind, subject_kind,
			repository_id, commit_oid, configuration_version, producer_kind, producer_id, source, result,
			observed_at, created_at)
		 values ('ve_tests', $1, $2, 'required_tests', 'commit', '1401511579', '` + integrationHeadOID + `',
		         'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		        ('ve_review', $1, $2, 'independent_review', 'commit', '1401511579', '` + integrationHeadOID + `',
		         'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		        ('ve_post', $1, $2, 'post_merge_integration', 'commit', '1401511579', '` + integrationMergeOID + `',
		         'build-v1', 'task', 'task_1', 'real', 'passed', now(), now())
		 on conflict (verification_id) do nothing`, arguments: []any{workspaceID, workItemID}},
	} {
		if _, err := dataset.database.ExecContext(t.Context(), row.statement, row.arguments...); err != nil {
			t.Fatalf("fixture statement error = %v", err)
		}
	}

	if _, err := fixture.store.CloseDeliveryWorkItem(t.Context(), evaluation.CloseMutation{
		Record: record, ExpectedRunVersion: 1, ExpectedWorkItemVersion: 1,
		ActorID: "101", RequestKey: "close-1", RequestDigest: evaluation.Digest("close-1"),
		ClosedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CloseDeliveryWorkItem() error = %v", err)
	}
	closed, err := fixture.store.WorkItem(t.Context(), workspaceID, workItemID)
	if err != nil || closed.State != workitem.StateClosed || closed.Version != 2 {
		t.Fatalf("work item after closure = %#v, %v", closed, err)
	}
	run, err := fixture.service.Run(t.Context(), runID)
	if err != nil || run.State != execution.RunSucceeded {
		t.Fatalf("run after closure = %#v, %v", run, err)
	}
	stored, found, err := fixture.store.ClosureEvidenceFor(t.Context(), workspaceID, workItemID)
	if err != nil || !found || stored.ClosureID != "cl_1" {
		t.Fatalf("ClosureEvidenceFor() = %#v, %v, %v", stored, found, err)
	}
	if _, err := fixture.store.CloseDeliveryWorkItem(t.Context(), evaluation.CloseMutation{
		Record:  record,
		ActorID: "101", RequestKey: "close-1", RequestDigest: evaluation.Digest("close-1"),
		ClosedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("idempotent closure error = %v", err)
	}
	if _, err := fixture.store.CloseDeliveryWorkItem(t.Context(), evaluation.CloseMutation{
		Record: record, ExpectedRunVersion: 1, ExpectedWorkItemVersion: 2,
		ActorID: "101", RequestKey: "close-race", RequestDigest: evaluation.Digest("close-race"),
		ClosedAt: time.Now().UTC(),
	}); !errors.Is(err, evaluation.ErrPolicyBlocked) {
		t.Fatalf("racing closure = %v, want ErrPolicyBlocked", err)
	}
}

// ---- closure through the production service -------------------------------

// pgClosureStore adapts the shared store to the evaluation ports exactly as
// the composition root does, so this test exercises the real Service.Close
// path (including the knowledge and publish_pr binding queries) rather than
// the store method directly.
type pgClosureStore struct{ store *postgres.Store }

func (a pgClosureStore) RecordVerification(ctx context.Context, verification evaluation.Verification, actorID, requestKey, requestDigest string) (evaluation.Verification, error) {
	return a.store.RecordVerificationEvidence(ctx, verification, actorID, requestKey, requestDigest)
}

func (a pgClosureStore) Verification(ctx context.Context, workspaceID, verificationID string) (evaluation.Verification, bool, error) {
	return a.store.VerificationEvidence(ctx, workspaceID, verificationID)
}

func (a pgClosureStore) VerificationsFor(ctx context.Context, workspaceID, workItemID string) ([]evaluation.Verification, error) {
	return a.store.VerificationsForWorkItem(ctx, workspaceID, workItemID)
}

func (a pgClosureStore) Closure(ctx context.Context, workspaceID, workItemID string) (evaluation.ClosureRecord, bool, error) {
	return a.store.ClosureEvidenceFor(ctx, workspaceID, workItemID)
}

func (a pgClosureStore) ReplayedClose(ctx context.Context, actor, workItemID, requestKey, digest string) (evaluation.ClosureRecord, bool, error) {
	return a.store.ReplayedClose(ctx, actor, workItemID, requestKey, digest)
}

func (a pgClosureStore) Close(ctx context.Context, mutation evaluation.CloseMutation) (evaluation.ClosureRecord, error) {
	return a.store.CloseDeliveryWorkItem(ctx, mutation)
}

func (a pgClosureStore) KnowledgeBindingExists(ctx context.Context, workspaceID, workItemID, snapshotID, bindingEventID string) (bool, error) {
	return a.store.KnowledgeBindingExists(ctx, workspaceID, workItemID, snapshotID, bindingEventID)
}

func (a pgClosureStore) SettledPublishPullRequest(ctx context.Context, workspaceID, workItemID, runID, repositoryID, commitOID string) (string, bool, error) {
	return a.store.SettledPublishPullRequest(ctx, workspaceID, workItemID, runID, repositoryID, commitOID)
}

// seedingClosureWorkItems and seedingClosureRuns read the seeded rows back.
type seedingClosureWorkItems struct{ store *postgres.Store }

func (s seedingClosureWorkItems) Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error) {
	return s.store.WorkItem(ctx, workspaceID, workItemID)
}

type seedingClosureRuns struct{ store *postgres.Store }

func (s seedingClosureRuns) Run(ctx context.Context, runID string) (execution.Run, error) {
	return s.store.Run(ctx, runID)
}

func TestClosureThroughTheProductionServicePostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_svc", "wi_svc", "run_svc"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	store := fixture.store
	if _, err := store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Service close", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-svc",
		RequestDigest: workitem.Digest("wi-svc"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	// The knowledge confirmation belongs to this work item; another work item's
	// event must not satisfy the closure.
	statements := []struct {
		statement string
		arguments []any
	}{
		{`insert into knowledge_snapshot (workspace_id, snapshot_id, repository_id, commit_oid, digest, canonical)
		  values ($1, 'ks_svc', '1413481670', 'bd2d5073a6001085d33d91d251cd082deb5676fe', $2, 'x'::bytea)`,
			[]any{workspaceID, integrationDigest}},
		{`insert into knowledge_binding_event (workspace_id, event_id, subject_id, snapshot_id,
			human_response_id, actor_id, version, request_digest, confirmed_at)
		  values ($1, 'kbe_svc', $2, 'ks_svc', 'hr_svc', '101', 1, $3, now()),
		         ($1, 'kbe_other', 'wi_other', 'ks_svc', 'hr_other', '101', 1, $3, now())`,
			[]any{workspaceID, workItemID, integrationDigest}},
		{`insert into external_operation (operation_id, workspace_id, work_item_id, workflow_run_id,
			kind, state, repository_id, ref, base_ref, commit_oid, expected_github_user_id,
			requested_by_github_user_id, request, request_digest, request_key, result, version,
			created_at, updated_at, settled_at)
		  values ('op_svc', $1, $2, $3, 'publish_pr', 'SUCCEEDED', '1401511579', 'refs/heads/task/svc',
			'refs/heads/main', $4, '135722937', '101',
			'{"repositoryId":"1401511579","role":"project","operation":"publish_pr","ref":"refs/heads/task/svc","baseRef":"refs/heads/main","commitOid":"` + integrationHeadOID + `","expectedGitHubUserId":"135722937","requestedByGitHubUserId":"101"}',
			$5, 'op-svc', '{"repositoryId":"1401511579","ref":"refs/heads/task/svc","commitOid":"` + integrationHeadOID + `","pullRequestNumber":"42","pullRequestAuthorId":"135722937","baseRef":"refs/heads/main","reconciled":true,"observationId":"obs_pr_svc","observedAt":"2026-10-14T12:00:00Z","observedByGitHubUserId":"135722937"}', 2,
			now(), now(), now())`,
			[]any{workspaceID, workItemID, runID, integrationHeadOID, integrationDigest}},
	}
	for _, row := range statements {
		if _, err := fixture.database.ExecContext(t.Context(), row.statement, row.arguments...); err != nil {
			t.Fatalf("seed statement error = %v", err)
		}
	}
	facts := observationFacts{store: store}
	adapter := pgClosureStore{store}
	service := evaluation.NewService(adapter, adapter, adapter,
		seedingClosureWorkItems{store}, seedingClosureRuns{store}, facts, adapter, adapter,
		&evaluationSeqIDs{}, time.Now)
	closeCommand := func(snapshotID, eventID, requestKey string) evaluation.CloseCommand {
		return evaluation.CloseCommand{
			WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
			ExpectedWorkItemVersion: 1, ExpectedRunVersion: 1, ActorID: "101", RequestKey: requestKey,
			PolicyVersion: "delivery-policy-v1", PullRequestNumber: "42",
			KnowledgeSnapshotID: snapshotID, KnowledgeBindingEventID: eventID,
			MergeObservationID: "obs_svc_merge", ReviewObservationID: "obs_svc_review",
			RequiredTestIDs: []string{"ve_svc_tests"}, IndependentReviewID: "ve_svc_review",
			PostMergeVerificationID: "ve_svc_post",
		}
	}
	seedClosureFacts(t, fixture, workspaceID, workItemID, runID)

	if _, err := service.Close(t.Context(), closeCommand("ks_svc", "kbe_other", "close-svc-wrong-event")); !errors.Is(err, evaluation.ErrPolicyBlocked) {
		t.Fatalf("closure with another work item's confirmation = %v, want ErrPolicyBlocked", err)
	}
	if _, err := service.Close(t.Context(), closeCommand("ks_missing", "kbe_svc", "close-svc-wrong-snapshot")); !errors.Is(err, evaluation.ErrPolicyBlocked) {
		t.Fatalf("closure with an unknown snapshot = %v, want ErrPolicyBlocked", err)
	}
	record, err := service.Close(t.Context(), closeCommand("ks_svc", "kbe_svc", "close-svc"))
	if err != nil {
		t.Fatalf("the production service cannot close: %v", err)
	}
	if record.MergedByType != "User" || record.MergedByGitHubUserID != "277460407" ||
		len(record.RequiredTestIDs) != 1 || record.KnowledgeSnapshotID != "ks_svc" {
		t.Fatalf("closure record = %#v", record)
	}
	item, err := store.WorkItem(t.Context(), workspaceID, workItemID)
	if err != nil || item.State != workitem.StateClosed {
		t.Fatalf("work item after the service closure = %#v, %v", item, err)
	}
	run, err := fixture.service.Run(t.Context(), runID)
	if err != nil || run.State != execution.RunSucceeded {
		t.Fatalf("run after the service closure = %#v, %v", run, err)
	}
	read, found, err := service.Closure(t.Context(), workspaceID, workItemID)
	if err != nil || !found || read.ClosureID != record.ClosureID || len(read.RequiredTestIDs) != 1 {
		t.Fatalf("Closure() = %#v, %v, %v", read, found, err)
	}
	replay, err := service.Close(t.Context(), closeCommand("ks_svc", "kbe_svc", "close-svc"))
	if err != nil || replay.ClosureID != record.ClosureID {
		t.Fatalf("idempotent Close() = %#v, %v", replay, err)
	}
	// A settled operation answers only for its own run, repository and commit.
	if number, found, err := store.SettledPublishPullRequest(t.Context(), workspaceID, workItemID,
		runID, "1401511579", integrationHeadOID); err != nil || !found || number != "42" {
		t.Fatalf("SettledPublishPullRequest() = %q, %v, %v", number, found, err)
	}
	for name, probe := range map[string]struct {
		runID, repositoryID, commitOID string
	}{
		"another run":        {runID: "run_other", repositoryID: "1401511579", commitOID: integrationHeadOID},
		"another commit":     {runID: runID, repositoryID: "1401511579", commitOID: "19a68257314d32152f520c698b6b38b00cb6f17e"},
		"another repository": {runID: runID, repositoryID: "1413481670", commitOID: integrationHeadOID},
	} {
		t.Run(name, func(t *testing.T) {
			if number, found, err := store.SettledPublishPullRequest(t.Context(), workspaceID, workItemID,
				probe.runID, probe.repositoryID, probe.commitOID); err != nil || found {
				t.Fatalf("a settled operation answered for %s: %q, %v, %v", name, number, found, err)
			}
		})
	}
}

// TestVerificationRecordingSeparatesActorFromProducerPostgres proves that a
// task-produced verification fact records the authenticated actor for
// idempotency and audit while the producer id stays a data field: using the
// producer id as an actor violates the decimal-identity constraints.
func TestVerificationRecordingSeparatesActorFromProducerPostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_verify", "wi_verify", "run_verify"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Verify", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-verify",
		RequestDigest: workitem.Digest("wi-verify"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
	adapter := pgClosureStore{fixture.store}
	service := evaluation.NewService(adapter, adapter, adapter,
		seedingClosureWorkItems{fixture.store}, seedingClosureRuns{fixture.store},
		observationFacts{store: fixture.store}, adapter, adapter, &evaluationSeqIDs{}, time.Now)
	command := evaluation.RecordVerificationCommand{
		WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		Kind: evaluation.KindRequiredTests, SubjectKind: "commit", RepositoryID: "1401511579",
		CommitOID: integrationHeadOID, ConfigurationVersion: "build-v1",
		ProducerKind: evaluation.ProducerTask, ProducerID: "task_1",
		Source: evaluation.SourceReal, Result: evaluation.ResultPassed,
		RequestKey: "record-verify", ActorID: "101",
	}
	recorded, err := service.RecordVerification(t.Context(), command)
	if err != nil {
		t.Fatalf("RecordVerification() with a task producer error = %v", err)
	}
	if recorded.ProducerID != "task_1" {
		t.Fatalf("the producer id was not preserved: %#v", recorded)
	}
	replay, err := service.RecordVerification(t.Context(), command)
	if err != nil || replay.VerificationID != recorded.VerificationID {
		t.Fatalf("idempotent RecordVerification() = %#v, %v", replay, err)
	}
	conflict := command
	conflict.Result = evaluation.ResultFailed
	if _, err := service.RecordVerification(t.Context(), conflict); !errors.Is(err, evaluation.ErrVersionConflict) {
		t.Fatalf("same key different content = %v, want ErrVersionConflict", err)
	}
}

// evaluationSeqIDs yields deterministic ids for the service-level closure test.
type evaluationSeqIDs struct{ next int }

func (s *evaluationSeqIDs) New() (string, error) { s.next++; return "id_" + strconv.Itoa(s.next), nil }

// observationFacts reads the seeded observations for the evaluation service.
type observationFacts struct{ store *postgres.Store }

func (f observationFacts) Observation(ctx context.Context, workspaceID, observationID string) (evaluation.ObservationFact, bool, error) {
	observation, found, err := f.store.GitHubObservation(ctx, workspaceID, observationID)
	if err != nil || !found {
		return evaluation.ObservationFact{}, found, err
	}
	fact := evaluation.ObservationFact{
		ObservationID: observation.ObservationID, RepositoryID: observation.RepositoryID,
		SubjectKind: observation.SubjectKind, SubjectKey: observation.SubjectKey, Unknown: observation.Unknown,
	}
	if observation.PullRequest != nil {
		fact.PullRequestAuthorID = observation.PullRequest.AuthorID
		fact.HeadOID = observation.PullRequest.HeadOID
		fact.Merged = observation.PullRequest.Merged
		fact.MergeCommitOID = observation.PullRequest.MergeCommitOID
		fact.MergedByID = observation.PullRequest.MergedByID
		fact.MergedByType = observation.PullRequest.MergedByType
	}
	if observation.Review != nil {
		fact.ReviewID = observation.Review.ReviewID
		fact.ReviewerID = observation.Review.ReviewerID
		fact.ReviewerType = observation.Review.ReviewerType
		fact.ReviewerPermission = observation.Review.ReviewerPermission
		fact.ReviewState = observation.Review.State
		fact.ReviewCommitOID = observation.Review.CommitOID
	}
	return fact, true, nil
}

// seedClosureFacts writes the merge/review observations and verification rows
// the service-level closure needs.
func seedClosureFacts(t *testing.T, fixture *deliveryFixture, workspaceID, workItemID, runID string) {
	t.Helper()
	statements := []struct {
		statement string
		arguments []any
	}{
		{`insert into github_observation (observation_id, workspace_id, repository_id, subject_kind, subject_key,
			observed_at, actor_github_user_id, unknown, fact)
		  values ('obs_svc_merge', $1, '1401511579', 'pull_request', '42', now(), '135722937', false,
			'{"pullRequest":{"number":"42","authorId":"135722937","headOid":"` + integrationHeadOID + `","headRef":"task/svc","baseRef":"refs/heads/main","state":"closed","merged":true,"mergeCommitOid":"` + integrationMergeOID + `","mergedById":"277460407","mergedByType":"User"}}'),
		         ('obs_svc_review', $1, '1401511579', 'pull_request_review', '42/5001', now(), '135722937', false,
			'{"review":{"reviewId":"5001","reviewerId":"277460407","reviewerType":"User","reviewerPermission":"write","state":"APPROVED","commitOid":"` + integrationHeadOID + `"}}')`,
			[]any{workspaceID}},
		{`insert into verification_evidence (verification_id, workspace_id, work_item_id, workflow_run_id,
			kind, subject_kind, repository_id, commit_oid, configuration_version, producer_kind, producer_id,
			source, result, observed_at, created_at)
		  values ('ve_svc_tests', $1, $2, $3, 'required_tests', 'commit', '1401511579', $4,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		 ('ve_svc_review', $1, $2, $3, 'independent_review', 'commit', '1401511579', $4,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		 ('ve_svc_post', $1, $2, $3, 'post_merge_integration', 'commit', '1401511579', $5,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now())`,
			[]any{workspaceID, workItemID, runID, integrationHeadOID, integrationMergeOID}},
	}
	for _, row := range statements {
		if _, err := fixture.database.ExecContext(t.Context(), row.statement, row.arguments...); err != nil {
			t.Fatalf("seed closure fact error = %v", err)
		}
	}
}

// ---- closure failure invariants and concurrency (permanent) ----------------

// TestClosureRollsBackOnLateFailureAndRetriesPostgres proves the closure
// transaction leaves no half state: a failure after the run completion (the
// required-test relation foreign key, and the idempotency bookkeeping that
// precedes the audit row) rolls everything back, and a legal retry then
// succeeds.
func TestClosureRollsBackOnLateFailureAndRetriesPostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_rollback", "wi_rollback", "run_rollback"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	fixture.run(t, workspaceID, workItemID, "run_rollback_old", "135722937")
	redSeedWorkItemForBarrier(t, fixture, workspaceID, workItemID, runID)
	seedRollbackFixture(t, fixture, workspaceID, workItemID, runID)

	base := evaluation.ClosureRecord{
		ClosureID: "cl_rollback", WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		DefinitionName: "delivery", DefinitionVersion: 1,
		DefinitionDigest: "sha256:" + strings.Repeat("1", 64),
		PolicyVersion:    "delivery-policy-v1", RequirementDigest: workitem.ContentDigest("r"),
		KnowledgeSnapshotID: "ks_rb", KnowledgeBindingID: "kbe_rb",
		MergeRepositoryID: "1401511579", MergeCommitOID: integrationMergeOID, PullRequestNumber: "42",
		MergeObservationID: "obs_merge_rb", ReviewObservationID: "obs_review_rb",
		ReviewHeadOID: integrationHeadOID, ReviewerGitHubUserID: "277460407",
		MergedByGitHubUserID: "277460407", MergedByType: "User",
		IndependentReviewID: "ve_rb_review", PostMergeVerificationID: "ve_rb_post",
		ChecklistDigest: "sha256:" + strings.Repeat("2", 64), CreatedByGitHubUserID: "101",
		CreatedAt: time.Now().UTC(),
	}
	assertNoHalfState := func(t *testing.T) {
		t.Helper()
		run, err := fixture.service.Run(t.Context(), runID)
		if err != nil || run.State != execution.RunActive {
			t.Fatalf("a failed closure advanced the run: %#v, %v", run, err)
		}
		item, err := fixture.store.WorkItem(t.Context(), workspaceID, workItemID)
		if err != nil || item.State != workitem.StateOpen {
			t.Fatalf("a failed closure closed the work item: %#v, %v", item, err)
		}
		if _, found, err := fixture.store.ClosureEvidenceFor(t.Context(), workspaceID, workItemID); err != nil || found {
			t.Fatalf("a failed closure left a closure row: %v, %v", found, err)
		}
	}

	// (a) a required test that does not exist fails at the relation insert,
	// after the run completion and the closure row insert.
	missingTest := base
	missingTest.RequiredTestIDs = []string{"ve_rb_missing"}
	if _, err := fixture.store.CloseDeliveryWorkItem(t.Context(), evaluation.CloseMutation{
		Record: missingTest, ExpectedRunVersion: 1, ExpectedWorkItemVersion: 1,
		ActorID: "101", RequestKey: "rollback-missing-test",
		RequestDigest: evaluation.Digest("rollback-missing-test"), ClosedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("a closure with a missing required test succeeded")
	}
	assertNoHalfState(t)

	// (b) idempotency bookkeeping is the last write before the audit row; a
	// conflicting recorded command makes it fail and rolls everything back.
	if _, err := fixture.database.ExecContext(t.Context(), `
		insert into command_idempotency (actor_github_user_id, command_type, target_id, request_key,
			request_digest, response, created_at)
		values ('101', 'evaluation.close', $1, 'rollback-conflict', $2, '{}', now())
	`, workItemID, strings.Repeat("9", 64)); err != nil {
		t.Fatalf("seed idempotency conflict: %v", err)
	}
	conflicting := base
	conflicting.RequiredTestIDs = []string{"ve_rb_tests"}
	if _, err := fixture.store.CloseDeliveryWorkItem(t.Context(), evaluation.CloseMutation{
		Record: conflicting, ExpectedRunVersion: 1, ExpectedWorkItemVersion: 1,
		ActorID: "101", RequestKey: "rollback-conflict",
		RequestDigest: evaluation.Digest("rollback-conflict"), ClosedAt: time.Now().UTC(),
	}); err == nil {
		t.Fatal("a closure whose idempotency bookkeeping conflicts succeeded")
	}
	assertNoHalfState(t)

	// (c) the legal retry closes in one transaction.
	legal := base
	legal.RequiredTestIDs = []string{"ve_rb_tests"}
	if _, err := fixture.store.CloseDeliveryWorkItem(t.Context(), evaluation.CloseMutation{
		Record: legal, ExpectedRunVersion: 1, ExpectedWorkItemVersion: 1,
		ActorID: "101", RequestKey: "rollback-legal",
		RequestDigest: evaluation.Digest("rollback-legal"), ClosedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("the legal retry failed: %v", err)
	}
	run, err := fixture.service.Run(t.Context(), runID)
	if err != nil || run.State != execution.RunSucceeded {
		t.Fatalf("run after the legal closure = %#v, %v", run, err)
	}
	item, err := fixture.store.WorkItem(t.Context(), workspaceID, workItemID)
	if err != nil || item.State != workitem.StateClosed {
		t.Fatalf("work item after the legal closure = %#v, %v", item, err)
	}
}

// seedRollbackFixture writes the observations, knowledge and verification rows
// the rollback cases reference.
func seedRollbackFixture(t *testing.T, fixture *deliveryFixture, workspaceID, workItemID, runID string) {
	t.Helper()
	statements := []struct {
		statement string
		arguments []any
	}{
		{`insert into knowledge_snapshot (workspace_id, snapshot_id, repository_id, commit_oid, digest, canonical)
		  values ($1, 'ks_rb', '1413481670', 'bd2d5073a6001085d33d91d251cd082deb5676fe', $2, 'x'::bytea)`,
			[]any{workspaceID, integrationDigest}},
		{`insert into knowledge_binding_event (workspace_id, event_id, subject_id, snapshot_id,
			human_response_id, actor_id, version, request_digest, confirmed_at)
		  values ($1, 'kbe_rb', $2, 'ks_rb', 'hr_rb', '101', 1, $3, now())`,
			[]any{workspaceID, workItemID, integrationDigest}},
		{`insert into github_observation (observation_id, workspace_id, repository_id, subject_kind, subject_key,
			observed_at, actor_github_user_id, unknown, fact)
		  values ('obs_merge_rb', $1, '1401511579', 'pull_request', '42', now(), '135722937', false, '{}'),
		         ('obs_review_rb', $1, '1401511579', 'pull_request_review', '42/5001', now(), '135722937', false, '{}')`,
			[]any{workspaceID}},
		{`insert into verification_evidence (verification_id, workspace_id, work_item_id, workflow_run_id,
			kind, subject_kind, repository_id, commit_oid, configuration_version, producer_kind, producer_id,
			source, result, observed_at, created_at)
		  values ('ve_rb_tests', $1, $2, $3, 'required_tests', 'commit', '1401511579', $4,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		 ('ve_rb_review', $1, $2, $3, 'independent_review', 'commit', '1401511579', $4,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		 ('ve_rb_post', $1, $2, $3, 'post_merge_integration', 'commit', '1401511579', $5,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now())`,
			[]any{workspaceID, workItemID, runID, integrationHeadOID, integrationMergeOID}},
	}
	for _, row := range statements {
		if _, err := fixture.database.ExecContext(t.Context(), row.statement, row.arguments...); err != nil {
			t.Fatalf("seed rollback fixture error = %v", err)
		}
	}
}

// TestConcurrentDualAdmissionAcceptsExactlyOnePostgres starts two admissions
// for the same delivery object behind a barrier: the database-level invariant
// admits exactly one.
func TestConcurrentDualAdmissionAcceptsExactlyOnePostgres(t *testing.T) {
	fixture := newDeliveryFixture(t)
	workspaceID, workItemID, runID := "ws_barrier", "wi_barrier", "run_barrier"
	fixture.workspace(t, workspaceID)
	fixture.run(t, workspaceID, workItemID, runID, "135722937")
	redSeedWorkItemForBarrier(t, fixture, workspaceID, workItemID, runID)

	start := make(chan struct{})
	results := make(chan error, 2)
	for index, operationID := range []string{"op_barrier_a", "op_barrier_b"} {
		go func(index int, operationID string) {
			<-start
			operation := githubdelivery.ExternalOperation{
				OperationID: operationID, WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
				Kind: githubdelivery.OperationVerifyChange, State: githubdelivery.StatePending,
				Request: githubdelivery.OperationRequest{
					RepositoryID: "1401511579", Role: "project", Operation: "verify_change",
					Ref: "refs/heads/task/barrier", CommitOID: integrationHeadOID,
					ExpectedGitHubUserID: "135722937", RequestedByGitHubUserID: "101",
				},
				RequestDigest: integrationDomainDigest("a"), Version: 1,
				CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			}
			_, err := fixture.store.CreateExternalOperation(context.Background(), githubdelivery.AdmitMutation{
				Operation: operation, RequestKey: "barrier-" + operationID,
				RequestDigest: integrationCommandDigest(string(rune('a' + index))),
			})
			results <- err
		}(index, operationID)
	}
	close(start)
	accepted, rejected := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, githubdelivery.ErrPolicyBlocked):
			rejected++
		default:
			t.Fatalf("unexpected concurrent admission error = %v", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("concurrent admissions accepted=%d rejected=%d, want exactly one winner", accepted, rejected)
	}
}

// pgHumanTaskAnswers adapts the shared store to the HumanTask store port for
// the answer path, exactly as the composition root does.
type pgHumanTaskAnswers struct{ store *postgres.Store }

func (a pgHumanTaskAnswers) ReplayTask(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (humantask.HumanTask, bool, error) {
	return a.store.ReplayHumanTaskCommand(ctx, actor, commandType, targetID, requestKey, digest)
}

func (a pgHumanTaskAnswers) ReplayContinuation(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (humantask.Continuation, bool, error) {
	return a.store.ReplayHumanContinuation(ctx, actor, commandType, targetID, requestKey, digest)
}

func (a pgHumanTaskAnswers) Create(ctx context.Context, mutation humantask.CreateMutation) (humantask.HumanTask, error) {
	return a.store.CreateHumanTask(ctx, mutation)
}

func (a pgHumanTaskAnswers) Get(ctx context.Context, workspaceID, taskID string) (humantask.HumanTask, error) {
	return a.store.HumanTask(ctx, workspaceID, taskID)
}

func (a pgHumanTaskAnswers) List(ctx context.Context, workspaceID string, openOnly bool) ([]humantask.HumanTask, error) {
	return a.store.ListHumanTasks(ctx, workspaceID, openOnly)
}

func (a pgHumanTaskAnswers) Answer(ctx context.Context, mutation humantask.AnswerMutation) (humantask.HumanTask, error) {
	return a.store.AnswerHumanTask(ctx, mutation)
}

func (a pgHumanTaskAnswers) ResolveExternalFact(ctx context.Context, mutation humantask.ResolveExternalFactMutation) (humantask.HumanTask, error) {
	return a.store.ResolveHumanTaskFromFact(ctx, mutation)
}

func (a pgHumanTaskAnswers) SetState(ctx context.Context, mutation humantask.StateMutation) (humantask.HumanTask, error) {
	return a.store.SetHumanTaskState(ctx, mutation)
}

func (a pgHumanTaskAnswers) ExpireBoundTo(ctx context.Context, workspaceID, objectKind, objectID, exceptVersion, reason, actorID string, at time.Time) (int, error) {
	return a.store.ExpireHumanTasksBoundTo(ctx, workspaceID, objectKind, objectID, exceptVersion, reason, actorID, at)
}

func (a pgHumanTaskAnswers) PendingContinuations(ctx context.Context, workspaceID, runID string) ([]humantask.Continuation, error) {
	return a.store.PendingHumanContinuations(ctx, workspaceID, runID)
}

func (a pgHumanTaskAnswers) ConsumeContinuation(ctx context.Context, mutation humantask.ConsumeContinuationMutation) (humantask.Continuation, error) {
	return a.store.ConsumeHumanContinuation(ctx, mutation)
}

// noObservations stands in for the fact source of the answer path.
type noObservations struct{}

func (noObservations) FactMatches(context.Context, string, string, humantask.ObjectBinding) (bool, error) {
	return false, nil
}

// pgWorkItemExistence adapts the store to the HumanTask ownership port.
type pgWorkItemExistence struct{ store *postgres.Store }

func (p pgWorkItemExistence) WorkItemExists(ctx context.Context, workspaceID, workItemID string) (bool, error) {
	if _, err := p.store.WorkItem(ctx, workspaceID, workItemID); err != nil {
		if errors.Is(err, workitem.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// redSeedWorkItemForBarrier seeds one open work item bound to its current run.
func redSeedWorkItemForBarrier(t *testing.T, fixture *deliveryFixture, workspaceID, workItemID, runID string) {
	t.Helper()
	if _, err := fixture.store.CreateWorkItem(t.Context(), workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Barrier", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-barrier",
		RequestDigest: workitem.Digest("wi-barrier"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("CreateWorkItem() error = %v", err)
	}
}
