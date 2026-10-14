package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/workitem"
)

const schemaHeadOID = "de62a4324cad57908376f8c5f3e14bd3bd3dab95"

// TestWorkItemSurfaceMatchesTheFrozenSchema validates every live work item
// response against api/workitem/v1/schema.json.
func TestWorkItemSurfaceMatchesTheFrozenSchema(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{})
	bodies := map[string]string{}

	created := createWorkItem(t, harness, "schema-intake")
	encoded, _ := json.Marshal(created)
	body := urlSafe(t, harness, http.MethodGet, "/api/v1/workspaces/workspace-a/work-items", "")
	bodies["workItemList"] = body
	workItemID, _ := created["workItem"].(map[string]any)["workItemId"].(string)
	detail := urlSafe(t, harness, http.MethodGet, "/api/v1/workspaces/workspace-a/work-items/"+workItemID, "")
	bodies["workItemDetail"] = detail

	cancelRequest := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/work-items/"+workItemID+"/cancel",
		`{"requestKey":"schema-cancel","expectedVersion":1,"reason":"schema validation"}`)
	cancelRequest.Header.Set("Origin", trustedOriginForDelivery)
	cancelRequest.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	recorder := httptest.NewRecorder()
	harness.workItems.ServeHTTP(recorder, cancelRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel = %d (%s)", recorder.Code, recorder.Body.String())
	}
	bodies["workItemCanceled"] = recorder.Body.String()
	_ = encoded

	root := contractRepoRoot(t)
	validateAgainstSchema(t, root, "api/workitem/v1/schema.json", bodies)
}

// TestDeliverySurfaceMatchesTheFrozenSchema validates every live
// delivery-governance response against api/delivery-governance/v1/schema.json.
// The delivery view and the human task responses are produced by the real
// services over their in-memory stores; only the external GitHub adapter is
// scripted.
func TestDeliverySurfaceMatchesTheFrozenSchema(t *testing.T) {
	projectRepository := "1401511579"
	workItem := workitem.WorkItem{
		ID: "wi_schema", WorkspaceID: "workspace-a", Title: "Fix", TargetRepositoryID: projectRepository,
		Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
		Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
		State:       workitem.StateOpen, Version: 1, CurrentRunID: "run_schema", CreatedByUserID: "101",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	run := execution.Run{
		ID: "run_schema", WorkspaceID: "workspace-a", WorkItemID: workItem.ID,
		StartedByGitHubUserID: "101", GitExecutionUserID: "135722937",
		DefinitionName: "mvp-delivery", DefinitionVersion: 1,
		DefinitionDigest: "sha256:" + workitem.Digest("definition"), State: execution.RunActive, Version: 4,
	}
	verification := evaluation.Verification{
		VerificationID: "ve_tests", WorkspaceID: "workspace-a", WorkItemID: workItem.ID,
		Kind: evaluation.KindRequiredTests, SubjectKind: "commit", RepositoryID: projectRepository,
		CommitOID: schemaHeadOID, ConfigurationVersion: "build-v1", ProducerKind: evaluation.ProducerTask,
		ProducerID: "task_1", Source: evaluation.SourceReal, Result: evaluation.ResultPassed,
		ObservedAt: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}

	facts := schemaFactSource{pullRequest: schemaPullRequest()}
	delivery := githubdelivery.NewService(
		githubdelivery.NewMemoryStore(), facts, schemaEvidence{verification: verification},
		schemaRuns{run: run}, schemaWorkItems{item: workItem}, &fakeSettler{}, &sequenceIDs{}, time.Now)
	humans := humantask.NewService(humantask.NewMemoryStore(),
		fakeObservationSource{matches: map[string]bool{}}, anyWorkItem{}, &sequenceIDs{}, time.Now)
	status := schemaGitRemoteStatus()

	handler := httpapi.NewDeliveryHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: "101"}},
		&fakeWorkspaces{members: members()}, humans, fakeDelivery{status: status}, trustedOriginForDelivery)

	bodies := map[string]string{}
	listed := urlSafeHandler(t, handler, http.MethodGet, "/api/v1/workspaces/workspace-a/human-tasks", "")
	bodies["humanTaskList"] = listed

	// A real kowa_answer through the real service and the real route.
	openTask, err := humans.Create(context.Background(), humantask.CreateCommand{
		WorkspaceID: "workspace-a", WorkItemID: workItem.ID, RunID: run.ID, NodeRunID: "nr_1",
		ActorID: "101", RequestKey: "schema-create", Title: "Approve the plan",
		Kind: "plan_approval", Mode: humantask.ModeKowaAnswer,
		Fields:     []humantask.Field{{Name: "decision", Type: "decision", Required: true, Options: []string{"approved", "rejected"}}},
		Object:     humantask.ObjectBinding{Kind: "plan_artifact", ID: "art_1", Version: workitem.ContentDigest("plan")},
		Candidates: []string{"101"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	answer := `{"requestKey":"schema-answer","requestVersion":1,
	   "object":{"kind":"plan_artifact","id":"art_1","version":"` + workitem.ContentDigest("plan") + `"},
	   "decision":"approved","values":{"decision":"approved"}}`
	answerRequest := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/human-tasks/"+openTask.ID+"/responses", answer)
	answerRequest.Header.Set("Origin", trustedOriginForDelivery)
	answerRequest.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, answerRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("answer = %d (%s)", recorder.Code, recorder.Body.String())
	}
	bodies["humanTaskResolved"] = recorder.Body.String()

	// A pull-request fact reconciliation through the real delivery service.
	projection, err := delivery.ObservePullRequest(context.Background(), githubdelivery.ObservePullRequestCommand{
		WorkspaceID: "workspace-a", RepositoryID: projectRepository, Number: "42",
	})
	if err != nil {
		t.Fatalf("ObservePullRequest() error = %v", err)
	}
	if projection.Unknown {
		t.Fatalf("scripted pull request is unknown")
	}
	view, err := delivery.DeliveryView(context.Background(), "workspace-a", workItem.ID)
	if err != nil {
		t.Fatalf("DeliveryView() error = %v", err)
	}
	if len(view.Facts) != 3 || len(view.Verification) != 1 {
		t.Fatalf("the real delivery view is incomplete: %d facts, %d verification rows",
			len(view.Facts), len(view.Verification))
	}
	// The closure branch of this response is produced and validated in the
	// PostgreSQL composition-root test: closing needs the atomic transaction
	// that only that adapter provides.
	if view.Closure != nil {
		t.Fatalf("an adapter without an atomic closer reported a closure: %#v", view.Closure)
	}

	root := contractRepoRoot(t)
	validateAgainstSchema(t, root, "api/delivery-governance/v1/schema.json", bodies)

	// The projected HTTP delivery view and the git remote status are validated
	// through their routes with the same frozen schema.
	httpHandler := httpapi.NewDeliveryHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: "101"}},
		&fakeWorkspaces{members: members()}, humans, deliveryTargetsFor{view: view, status: status},
		trustedOriginForDelivery)
	routeBodies := map[string]string{
		"deliveryViewRoute":    urlSafeHandler(t, httpHandler, http.MethodGet, "/api/v1/workspaces/workspace-a/work-items/wi_schema/delivery", ""),
		"gitRemoteStatusRoute": urlSafeHandler(t, httpHandler, http.MethodGet, "/api/v1/workspaces/workspace-a/git-remote-status", ""),
	}
	validateAgainstSchema(t, root, "api/delivery-governance/v1/schema.json", routeBodies)
}

func schemaPullRequest() githubdelivery.PullRequestProjection {
	fact := githubdelivery.PullRequestFact{
		Number: "42", AuthorID: "135722937", HeadOID: schemaHeadOID, HeadRef: "task/s05-fix",
		BaseRef: "refs/heads/main", State: "closed", Merged: true,
		MergeCommitOID: "ca6c6ec8446ec687213d58064a7fd8363050321a", MergedByID: "277460407",
		MergedByType: "User",
		MergedAt:     time.Now().UTC(),
	}
	return githubdelivery.PullRequestProjection{
		RepositoryID: "1401511579", Number: "42", ActorID: "135722937", PullRequest: fact,
		Reviews: []githubdelivery.ReviewFact{{
			ReviewID: "5001", ReviewerID: "277460407", ReviewerLogin: "reviewer-a",
			ReviewerType: "User", ReviewerPermission: "write", AuthorAssociation: "MEMBER",
			State: "APPROVED", CommitOID: schemaHeadOID, Submitted: time.Now().UTC(),
		}},
		Checks: &githubdelivery.ChecksFact{
			CommitOID: schemaHeadOID, State: githubdelivery.ChecksPassed, Total: 2,
		},
	}
}

func schemaGitRemoteStatus() gitremote.Status {
	return gitremote.Status{
		SchemaVersion: gitremote.SchemaVersion, Kind: gitremote.GitRemoteStatusKind,
		WorkspaceID: "workspace-a", RequestedByGitHubUserID: "101", ProbeOutcome: "completed",
		ObservedAt: time.Now().UTC(),
		MachineIdentity: gitremote.MachineIdentity{
			RuntimeIDs: []string{"rt_1"}, RegisteredGitHubUserIDs: []string{"135722937"},
			ObservedGitHubUserID: "135722937", State: gitremote.IdentityMatched,
		},
		Repositories: []gitremote.RepositoryStatus{{
			RepositoryID: "1401511579", Role: gitremote.RoleProject,
			RemoteRead:       gitremote.RemoteRead{State: gitremote.ReadReadable, Transport: "https"},
			PushPermission:   gitremote.PushPermission{State: gitremote.PermissionGranted},
			WriteObservation: gitremote.WriteObservationNone, ObservedAt: time.Now().UTC(),
			ObservedByGitHubUserID: "135722937",
		}},
	}
}

// schemaFactSource scripts the external GitHub reads of the delivery service.
type schemaFactSource struct {
	pullRequest githubdelivery.PullRequestProjection
}

func (s schemaFactSource) PullRequest(context.Context, string, string) (githubdelivery.PullRequestProjection, error) {
	return s.pullRequest, nil
}

func (s schemaFactSource) PullRequestsByHead(context.Context, string, string) ([]githubdelivery.PullRequestProjection, error) {
	return []githubdelivery.PullRequestProjection{s.pullRequest}, nil
}

func (s schemaFactSource) BranchHead(context.Context, string, string) (string, bool, error) {
	return schemaHeadOID, true, nil
}

type schemaEvidence struct {
	verification evaluation.Verification
	closure      *evaluation.ClosureRecord
}

func (s schemaEvidence) Verification(_ context.Context, _, verificationID string) (evaluation.Verification, bool, error) {
	if verificationID != s.verification.VerificationID {
		return evaluation.Verification{}, false, nil
	}
	return s.verification, true, nil
}

func (s schemaEvidence) VerificationsFor(context.Context, string, string) ([]evaluation.Verification, error) {
	return []evaluation.Verification{s.verification}, nil
}

func (s schemaEvidence) Closure(context.Context, string, string) (evaluation.ClosureRecord, bool, error) {
	if s.closure == nil {
		return evaluation.ClosureRecord{}, false, nil
	}
	return *s.closure, true, nil
}

type schemaRuns struct {
	run execution.Run
}

func (s schemaRuns) RunView(context.Context, string) (execution.RunView, error) {
	return execution.RunView{ID: s.run.ID, WorkItemID: s.run.WorkItemID, State: s.run.State}, nil
}

func (s schemaRuns) Run(context.Context, string) (execution.Run, error) { return s.run, nil }

type schemaWorkItems struct {
	item workitem.WorkItem
}

func (s schemaWorkItems) Get(context.Context, string, string) (workitem.WorkItem, error) {
	return s.item, nil
}

func (s schemaWorkItems) RunBindings(context.Context, string, string) ([]workitem.RunBinding, error) {
	return []workitem.RunBinding{{
		WorkItemID: s.item.ID, RunID: s.item.CurrentRunID, Kind: workitem.BindingInitial,
		BoundByUserID: "101", BoundAt: time.Now().UTC(),
	}}, nil
}

type fakeSettler struct{}

func (fakeSettler) ResolveForObject(context.Context, string, string, string, string, string, string) (bool, error) {
	return false, nil
}

func (fakeSettler) ExpireBoundTo(context.Context, string, string, string, string, string, string) (int, error) {
	return 0, nil
}

// deliveryTargetsFor adapts the real delivery view and a scripted git remote
// status to the handler's projection interface.
type deliveryTargetsFor struct {
	view   githubdelivery.DeliveryView
	status gitremote.Status
}

func (d deliveryTargetsFor) DeliveryView(context.Context, string, string) (githubdelivery.DeliveryView, error) {
	return d.view, nil
}

func (d deliveryTargetsFor) Status(context.Context, gitremote.StatusCommand) (gitremote.Status, error) {
	return d.status, nil
}

func urlSafe(t *testing.T, harness surfaceHarness, method, path, body string) string {
	t.Helper()
	return urlSafeHandler(t, harness.workItems, method, path, body)
}

func urlSafeHandler(t *testing.T, handler http.Handler, method, path, body string) string {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s %s = %d (%s)", method, path, recorder.Code, recorder.Body.String())
	}
	return recorder.Body.String()
}

func validateAgainstSchema(t *testing.T, root, schemaPath string, bodies map[string]string) {
	t.Helper()
	schema := filepath.Join(root, filepath.FromSlash(schemaPath))
	for name, payload := range bodies {
		t.Run(name, func(t *testing.T) {
			instance := filepath.Join(t.TempDir(), name+".json")
			if err := os.WriteFile(instance, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			goBinary, err := exec.LookPath("go")
			if err != nil {
				t.Skipf("go binary unavailable for the pinned schema validator: %v", err)
			}
			command := exec.Command(goBinary, "run",
				"github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0", "-f", "-c", schema, instance)
			command.Dir = root
			command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("live response violates the frozen schema: %v\n%s\n%s", err, output, payload)
			}
		})
	}
}
