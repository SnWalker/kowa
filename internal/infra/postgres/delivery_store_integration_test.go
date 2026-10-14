//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
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
	}); !errors.Is(err, workitem.ErrVersionConflict) {
		t.Fatalf("same key different content = %v, want ErrVersionConflict", err)
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
		"pull_request_review", "42", integrationMergeOID, "the pull request head moved", time.Now().UTC())
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
			State:       workitem.StateOpen, Version: 1, CreatedByUserID: "101",
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
		MergeRepositoryID: "1401511579", MergeCommitOID: integrationMergeOID, PullRequestNumber: "42",
		MergeObservationID: "obs_merge", ReviewObservationID: "obs_review",
		ReviewHeadOID: integrationHeadOID, ReviewerGitHubUserID: "277460407",
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
