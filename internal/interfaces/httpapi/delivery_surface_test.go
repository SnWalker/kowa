package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

const trustedOriginForDelivery = "https://kowa.local"

type fakeProjects struct{ repositoryID string }

func (f fakeProjects) ProjectRepositoryID(context.Context, string) (string, error) {
	return f.repositoryID, nil
}

type fakeRunSource struct {
	settled map[string]bool
}

func (f fakeRunSource) RunSettled(_ context.Context, runID string) (bool, error) {
	return f.settled[runID], nil
}

type fakeOperationGuard struct{ unsettled int }

func (f fakeOperationGuard) UnsettledOperations(context.Context, string, string) (int, error) {
	return f.unsettled, nil
}

type fakeObservationSource struct{ matches map[string]bool }

func (f fakeObservationSource) FactMatches(_ context.Context, _, observationID string, _ humantask.ObjectBinding) (bool, error) {
	return f.matches[observationID], nil
}

type sequenceIDs struct{ next int }

func (s *sequenceIDs) New() (string, error) {
	s.next++
	return "id_" + strconv.Itoa(s.next), nil
}

type fakeDelivery struct {
	view   githubdelivery.DeliveryView
	status gitremote.Status
	err    error
}

func (f fakeDelivery) DeliveryView(context.Context, string, string) (githubdelivery.DeliveryView, error) {
	if f.err != nil {
		return githubdelivery.DeliveryView{}, f.err
	}
	return f.view, nil
}

func (f fakeDelivery) Status(context.Context, gitremote.StatusCommand) (gitremote.Status, error) {
	return f.status, nil
}

type surfaceHarness struct {
	workItems *httpapi.WorkItemHandler
	delivery  *httpapi.DeliveryHandler
	items     *workitem.Service
	humans    *humantask.Service
	workStore *workitem.MemoryStore
	humanTask *humantask.MemoryStore
}

func newSurface(t *testing.T, guard workitem.ExternalOperationGuard, delivery fakeDelivery) surfaceHarness {
	t.Helper()
	workStore := workitem.NewMemoryStore()
	projectRepository := "1401511579"
	workItems := workitem.NewService(workStore, fakeProjects{repositoryID: projectRepository},
		fakeRunSource{settled: map[string]bool{}}, guard, &sequenceIDs{},
		func() time.Time { return time.Date(2026, 10, 14, 17, 0, 0, 0, time.UTC) })

	humanStore := humantask.NewMemoryStore()
	humans := humantask.NewService(humanStore, fakeObservationSource{matches: map[string]bool{"obs_1": true}},
		&sequenceIDs{}, func() time.Time { return time.Date(2026, 10, 14, 17, 0, 0, 0, time.UTC) })

	return surfaceHarness{
		workItems: workItemHandlerFor("101", workItems),
		delivery:  deliveryHandlerFor("101", humans, delivery),
		items:     workItems, humans: humans,
		workStore: workStore, humanTask: humanStore,
	}
}

// handlerFor builds the surface for one authenticated member.
func members() map[string]workspace.Member {
	return map[string]workspace.Member{
		"101": {GitHubUserID: "101", Role: workspace.RoleDeveloper},
		"202": {GitHubUserID: "202", Role: workspace.RoleViewer, RevokedAt: nil},
		"303": {GitHubUserID: "303", Role: workspace.RoleDeveloper, RevokedAt: timePointer()},
	}
}

func workItemHandlerFor(githubUserID string, service *workitem.Service) *httpapi.WorkItemHandler {
	return httpapi.NewWorkItemHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: githubUserID}},
		&fakeWorkspaces{members: members()}, service, trustedOriginForDelivery)
}

func deliveryHandlerFor(
	githubUserID string,
	service *humantask.Service,
	delivery fakeDelivery,
) *httpapi.DeliveryHandler {
	return httpapi.NewDeliveryHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: githubUserID}},
		&fakeWorkspaces{members: members()}, service, delivery, trustedOriginForDelivery)
}

func timePointer() *time.Time {
	value := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return &value
}

func doRequest(t *testing.T, handler http.Handler, request *http.Request) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	if request.Header.Get("Cookie") == "" {
		request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	body := map[string]any{}
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not JSON: %v (%s)", err, recorder.Body.String())
		}
	}
	return recorder, body
}

func jsonRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func createWorkItem(t *testing.T, harness surfaceHarness, requestKey string) map[string]any {
	t.Helper()
	request := jsonRequest(http.MethodPost, "/api/v1/workspaces/workspace-a/work-items",
		`{"requestKey":"`+requestKey+`","title":"Fix the failing test",
		  "requirement":{"version":1,"content":"make calc add correctly"},
		  "acceptance":{"version":1,"content":"tests pass and the PR is merged"}}`)
	request.Header.Set("Origin", trustedOriginForDelivery)
	request.Header.Set("X-CSRF-Token", "")
	recorder, body := doRequest(t, harness.workItems, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create work item = %d (%s)", recorder.Code, recorder.Body.String())
	}
	return body
}

func TestWorkItemSurfaceIsAuthorizedPerRequest(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{})
	anonymous := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/workspace-a/work-items", nil)
	anonymous.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "stolen"})
	recorder := httptest.NewRecorder()
	httpapi.NewWorkItemHandler(&fakeIdentity{err: identity.ErrUnauthorized},
		&fakeWorkspaces{members: map[string]workspace.Member{}}, harness.items, trustedOriginForDelivery).
		ServeHTTP(recorder, anonymous)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d, want 401", recorder.Code)
	}

	crossWorkspace := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/workspace-b/work-items", nil)
	crossWorkspace.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	recorder = httptest.NewRecorder()
	harness.workItems.ServeHTTP(recorder, crossWorkspace)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-workspace list = %d, want 403", recorder.Code)
	}

	viewerRequest := jsonRequest(http.MethodPost, "/api/v1/workspaces/workspace-a/work-items",
		`{"requestKey":"r1","title":"t","requirement":{"version":1,"content":"c"},"acceptance":{"version":1,"content":"a"}}`)
	viewerRequest.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	viewerRequest.Header.Set("Origin", trustedOriginForDelivery)
	recorder = httptest.NewRecorder()
	workItemHandlerFor("202", harness.items).ServeHTTP(recorder, viewerRequest)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("viewer create = %d, want 403", recorder.Code)
	}

	revokedRequest := jsonRequest(http.MethodPost, "/api/v1/workspaces/workspace-a/work-items",
		`{"requestKey":"r2","title":"t","requirement":{"version":1,"content":"c"},"acceptance":{"version":1,"content":"a"}}`)
	revokedRequest.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: "session-token"})
	revokedRequest.Header.Set("Origin", trustedOriginForDelivery)
	recorder = httptest.NewRecorder()
	workItemHandlerFor("303", harness.items).ServeHTTP(recorder, revokedRequest)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("revoked member create = %d, want 403", recorder.Code)
	}
}

func TestWorkItemSurfaceRejectsUntrustedOriginAndSmuggledFields(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{})
	noOrigin := jsonRequest(http.MethodPost, "/api/v1/workspaces/workspace-a/work-items",
		`{"requestKey":"r1","title":"t","requirement":{"version":1,"content":"c"},"acceptance":{"version":1,"content":"a"}}`)
	recorder, _ := doRequest(t, harness.workItems, noOrigin)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("create without a trusted origin = %d, want 401", recorder.Code)
	}
	smuggled := jsonRequest(http.MethodPost, "/api/v1/workspaces/workspace-a/work-items",
		`{"requestKey":"r1","title":"t","requirement":{"version":1,"content":"c"},"acceptance":{"version":1,"content":"a"},"actorId":"999"}`)
	smuggled.Header.Set("Origin", trustedOriginForDelivery)
	recorder, _ = doRequest(t, harness.workItems, smuggled)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("create with a smuggled field = %d, want 400", recorder.Code)
	}
}

func TestWorkItemIntakeDetailAndCancellation(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{})
	created := createWorkItem(t, harness, "intake-1")
	item, _ := created["workItem"].(map[string]any)
	if created["kind"] != "workItem" || item["state"] != "OPEN" || item["targetRepositoryId"] != "1401511579" {
		t.Fatalf("created work item = %#v", created)
	}
	if item["createdByGitHubUserId"] != "101" {
		t.Fatalf("creator = %#v", item["createdByGitHubUserId"])
	}
	workItemID, _ := item["workItemId"].(string)

	replayed := createWorkItem(t, harness, "intake-1")
	replayedItem, _ := replayed["workItem"].(map[string]any)
	if replayedItem["workItemId"] != workItemID {
		t.Fatalf("idempotent intake changed the identity: %#v", replayedItem)
	}

	detailRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/workspace-a/work-items/"+workItemID, nil)
	recorder, body := doRequest(t, harness.workItems, detailRequest)
	if recorder.Code != http.StatusOK || body["kind"] != "workItem" {
		t.Fatalf("detail = %d (%s)", recorder.Code, recorder.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/workspace-a/work-items", nil)
	recorder, body = doRequest(t, harness.workItems, listRequest)
	if recorder.Code != http.StatusOK || body["kind"] != "workItemList" {
		t.Fatalf("list = %d (%s)", recorder.Code, recorder.Body.String())
	}

	cancelRequest := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/work-items/"+workItemID+"/cancel",
		`{"requestKey":"cancel-1","expectedVersion":1,"reason":"goal withdrawn"}`)
	cancelRequest.Header.Set("Origin", trustedOriginForDelivery)
	recorder, body = doRequest(t, harness.workItems, cancelRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel = %d (%s)", recorder.Code, recorder.Body.String())
	}
	canceled, _ := body["workItem"].(map[string]any)
	if canceled["state"] != "CANCELED" {
		t.Fatalf("canceled work item = %#v", canceled)
	}

	blocked := newSurface(t, fakeOperationGuard{unsettled: 1}, fakeDelivery{})
	blockedBody := createWorkItem(t, blocked, "intake-2")
	blockedItem, _ := blockedBody["workItem"].(map[string]any)
	blockedRequest := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/work-items/"+blockedItem["workItemId"].(string)+"/cancel",
		`{"requestKey":"cancel-2","expectedVersion":1,"reason":"stop"}`)
	blockedRequest.Header.Set("Origin", trustedOriginForDelivery)
	recorder, body = doRequest(t, blocked.workItems, blockedRequest)
	if recorder.Code != http.StatusConflict || body["error"] != "EXTERNAL_UNKNOWN" {
		t.Fatalf("cancel with an unsettled operation = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestHumanTaskSurfaceAnswersWithServerSideActions(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{})
	task, err := humantask.NewService(harness.humanTask, fakeObservationSource{matches: map[string]bool{}},
		&sequenceIDs{}, time.Now).Create(context.Background(), humantask.CreateCommand{
		WorkspaceID: "workspace-a", WorkItemID: "wi_1", RunID: "run_1", NodeRunID: "nr_1",
		ActorID: "101", RequestKey: "create-1", Title: "Approve the plan",
		Kind: "plan_approval", Mode: humantask.ModeKowaAnswer,
		Fields:     []humantask.Field{{Name: "decision", Type: "decision", Required: true}},
		Object:     humantask.ObjectBinding{Kind: "plan_artifact", ID: "art_1", Version: "sha256:" + strings.Repeat("a", 64)},
		Candidates: []string{"101"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/workspace-a/human-tasks?state=open", nil)
	recorder, body := doRequest(t, harness.delivery, listRequest)
	if recorder.Code != http.StatusOK || body["kind"] != "humanTaskList" {
		t.Fatalf("list = %d (%s)", recorder.Code, recorder.Body.String())
	}
	tasks, _ := body["humanTasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("listed tasks = %#v", body["humanTasks"])
	}
	first, _ := tasks[0].(map[string]any)
	actions, _ := first["availableActions"].([]any)
	if len(actions) != 1 || actions[0] != "respond" {
		t.Fatalf("available actions = %#v", first["availableActions"])
	}

	viewerRequest := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/human-tasks/"+task.ID+"/responses",
		`{"requestKey":"answer-1","requestVersion":1,
		  "object":{"kind":"plan_artifact","id":"art_1","version":"`+task.Request.Object.Version+`"},
		  "decision":"approved","values":{"decision":"approved"}}`)
	viewerRequest.Header.Set("Origin", trustedOriginForDelivery)
	recorder = httptest.NewRecorder()
	deliveryHandlerFor("202", harness.humans, fakeDelivery{}).ServeHTTP(recorder, viewerRequest)
	if recorder.Code == http.StatusOK {
		t.Fatalf("a non-candidate answered the request: %d (%s)", recorder.Code, recorder.Body.String())
	}

	answerRequest := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/human-tasks/"+task.ID+"/responses",
		`{"requestKey":"answer-1","requestVersion":1,
		  "object":{"kind":"plan_artifact","id":"art_1","version":"`+task.Request.Object.Version+`"},
		  "decision":"approved","values":{"decision":"approved"}}`)
	answerRequest.Header.Set("Origin", trustedOriginForDelivery)
	recorder, body = doRequest(t, harness.delivery, answerRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("answer = %d (%s)", recorder.Code, recorder.Body.String())
	}
	resolved, _ := body["humanTask"].(map[string]any)
	if resolved["state"] != "RESOLVED" {
		t.Fatalf("answer did not resolve the request: %#v", resolved)
	}
	response, _ := resolved["response"].(map[string]any)
	if response["githubUserId"] != "101" {
		t.Fatalf("response actor = %#v", response["githubUserId"])
	}

	missingCSRF := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/human-tasks/"+task.ID+"/responses",
		`{"requestKey":"answer-2","requestVersion":1,
		  "object":{"kind":"plan_artifact","id":"art_1","version":"`+task.Request.Object.Version+`"},
		  "decision":"approved","values":{"decision":"approved"}}`)
	recorder, _ = doRequest(t, harness.delivery, missingCSRF)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("answer without a trusted origin = %d, want 401", recorder.Code)
	}
}

func TestExternalFactTasksExposeNoBrowserApproval(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{})
	humans := humantask.NewService(harness.humanTask, fakeObservationSource{matches: map[string]bool{}},
		&sequenceIDs{}, time.Now)
	task, err := humans.Create(context.Background(), humantask.CreateCommand{
		WorkspaceID: "workspace-a", WorkItemID: "wi_1", RunID: "run_1", NodeRunID: "nr_1",
		ActorID: "101", RequestKey: "create-fact", Title: "Wait for the GitHub review",
		Kind: "github_review", Mode: humantask.ModeExternalFact,
		Object: humantask.ObjectBinding{
			Kind: "pull_request_review", ID: "42", Version: strings.Repeat("b", 40),
		},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	detailRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/workspace-a/human-tasks/"+task.ID, nil)
	recorder, body := doRequest(t, harness.delivery, detailRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("detail = %d (%s)", recorder.Code, recorder.Body.String())
	}
	wire, _ := body["humanTask"].(map[string]any)
	actions, _ := wire["availableActions"].([]any)
	if len(actions) != 1 || actions[0] != "await-external-fact" {
		t.Fatalf("external fact actions = %#v", wire["availableActions"])
	}
	answer := jsonRequest(http.MethodPost,
		"/api/v1/workspaces/workspace-a/human-tasks/"+task.ID+"/responses",
		`{"requestKey":"answer-1","requestVersion":1,
		  "object":{"kind":"pull_request_review","id":"42","version":"`+strings.Repeat("b", 40)+`"},
		  "decision":"approved","values":{"decision":"approved"}}`)
	answer.Header.Set("Origin", trustedOriginForDelivery)
	recorder, body = doRequest(t, harness.delivery, answer)
	if recorder.Code != http.StatusUnprocessableEntity || body["error"] != "POLICY_BLOCKED" {
		t.Fatalf("browser approval of an external fact = %d (%s)", recorder.Code, recorder.Body.String())
	}
}

func TestDeliveryViewAndGitRemoteStatusProjection(t *testing.T) {
	view := githubdelivery.DeliveryView{
		WorkItem: workitem.WorkItem{
			ID: "wi_1", WorkspaceID: "workspace-a", Title: "Fix", TargetRepositoryID: "1401511579",
			State: workitem.StateOpen, Version: 2, CreatedByUserID: "101",
			Requirement: workitem.VersionedContent{Version: 1, Content: "c", Digest: workitem.ContentDigest("c")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
		},
		Verification: []evaluation.Verification{},
	}
	status := gitremote.Status{
		SchemaVersion: gitremote.SchemaVersion, Kind: gitremote.GitRemoteStatusKind,
		WorkspaceID: "workspace-a", RequestedByGitHubUserID: "101", ProbeOutcome: "completed",
		ObservedAt: time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC),
		MachineIdentity: gitremote.MachineIdentity{
			RuntimeIDs: []string{"rt_1"}, RegisteredGitHubUserIDs: []string{"135722937"},
			ObservedGitHubUserID: "135722937", State: gitremote.IdentityMatched,
		},
		Repositories: []gitremote.RepositoryStatus{{
			RepositoryID: "1401511579", Role: gitremote.RoleProject,
			RemoteRead:             gitremote.RemoteRead{State: gitremote.ReadReadable, Transport: "https"},
			PushPermission:         gitremote.PushPermission{State: gitremote.PermissionGranted},
			WriteObservation:       gitremote.WriteObservationNone,
			ObservedAt:             time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC),
			ObservedByGitHubUserID: "135722937",
		}},
	}
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{view: view, status: status})

	viewRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/workspace-a/work-items/wi_1/delivery", nil)
	recorder, body := doRequest(t, harness.delivery, viewRequest)
	if recorder.Code != http.StatusOK || body["kind"] != "deliveryView" {
		t.Fatalf("delivery view = %d (%s)", recorder.Code, recorder.Body.String())
	}
	workItemWire, _ := body["workItem"].(map[string]any)
	if workItemWire["workItemId"] != "wi_1" {
		t.Fatalf("delivery view work item = %#v", workItemWire)
	}

	statusRequest := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/workspace-a/git-remote-status", nil)
	recorder, body = doRequest(t, harness.delivery, statusRequest)
	if recorder.Code != http.StatusOK || body["kind"] != "gitRemoteStatus" {
		t.Fatalf("git remote status = %d (%s)", recorder.Code, recorder.Body.String())
	}
	projection, _ := body["status"].(map[string]any)
	if projection["requestedByGitHubUserId"] != "101" || projection["probeOutcome"] != "completed" {
		t.Fatalf("git remote projection = %#v", projection)
	}
	identityWire, _ := projection["machineIdentity"].(map[string]any)
	if identityWire["observedGitHubUserId"] != "135722937" || identityWire["state"] != "matched" {
		t.Fatalf("machine identity = %#v", identityWire)
	}
	repositories, _ := projection["repositories"].([]any)
	if len(repositories) != 1 {
		t.Fatalf("repositories = %#v", projection["repositories"])
	}
	first, _ := repositories[0].(map[string]any)
	readWire, _ := first["remoteRead"].(map[string]any)
	if readWire["state"] != "readable" || readWire["transport"] != "https" {
		t.Fatalf("remote read = %#v", readWire)
	}
	if first["writeObservation"] != "none" {
		t.Fatalf("write observation = %#v", first["writeObservation"])
	}
}

func TestDeliverySurfaceFailsClosedWhenDisabled(t *testing.T) {
	harness := newSurface(t, fakeOperationGuard{}, fakeDelivery{err: errors.New("delivery is not assembled")})
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/workspace-a/work-items/wi_1/delivery", nil)
	recorder, _ := doRequest(t, harness.delivery, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected delivery view status = %d", recorder.Code)
	}
}
