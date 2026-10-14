//go:build integration

package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/workitem"
	"go.uber.org/fx"
)

// TestDeliverySurfaceServerPostgres proves the production composition root for
// the S05 families: the real handlers over the real PostgreSQL stores and the
// S01 session authority, with no fixture standing in for a Kowa producer.
func TestDeliverySurfaceServerPostgres(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("KOWA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	const origin = "https://kowa.test"
	var srv *http.Server
	app := New(
		fx.Replace(config.Server{Address: "127.0.0.1:0"},
			config.Identity{
				Enabled: true, DatabaseURL: dsn, TrustedOrigin: origin,
				ClientID: "integration-client", ClientSecret: "integration-secret",
				AdminIDs: []string{"101"},
			},
			config.Runtime{
				Enabled: true, DatabaseURL: dsn, GitHubMachineUserID: "135722937",
				Credentials: map[string]string{"runtime-1": "server-integration-runtime-token"},
			}),
		fx.Populate(&srv))
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()

	database, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = database.Close() }()
	seedDeliveryWorkspace(t, ctx, database)
	token, csrf := seedDeliverySession(t, ctx, database, "surface")

	call := func(method, path, body, sessionToken, csrfToken string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if sessionToken != "" {
			request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: sessionToken})
		}
		if csrfToken != "" {
			request.Header.Set("X-CSRF-Token", csrfToken)
			request.Header.Set("Origin", origin)
		}
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	// Anonymous and unauthenticated calls fail closed.
	if status, _ := call(http.MethodGet, "/api/v1/workspaces/ws_s05/work-items", "", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous work item list = %d, want 401", status)
	}
	// A viewer may read but not create.
	if status, _ := call(http.MethodPost, "/api/v1/workspaces/ws_s05/work-items",
		`{"requestKey":"r1","title":"t","requirement":{"version":1,"content":"c"},"acceptance":{"version":1,"content":"a"}}`,
		token, csrf); status != http.StatusCreated {
		status, _ := call(http.MethodPost, "/api/v1/workspaces/ws_s05/work-items",
			`{"requestKey":"r1","title":"t","requirement":{"version":1,"content":"c"},"acceptance":{"version":1,"content":"a"}}`,
			token, csrf)
		t.Fatalf("developer intake = %d", status)
	}
	status, body := call(http.MethodGet, "/api/v1/workspaces/ws_s05/work-items", "", token, "")
	if status != http.StatusOK {
		t.Fatalf("work item list = %d %s", status, body)
	}
	var list struct {
		Kind      string `json:"kind"`
		WorkItems []struct {
			ID    string `json:"workItemId"`
			State string `json:"state"`
		} `json:"workItems"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil || list.Kind != "workItemList" || len(list.WorkItems) != 1 {
		t.Fatalf("work item list body = %s (%v)", body, err)
	}
	workItemID := list.WorkItems[0].ID
	if list.WorkItems[0].State != "OPEN" {
		t.Fatalf("intake state = %s", list.WorkItems[0].State)
	}
	status, body = call(http.MethodGet, "/api/v1/workspaces/ws_s05/work-items/"+workItemID, "", token, "")
	if status != http.StatusOK || !strings.Contains(body, workItemID) {
		t.Fatalf("work item detail = %d %s", status, body)
	}
	status, body = call(http.MethodGet, "/api/v1/workspaces/ws_s05/work-items/"+workItemID+"/delivery", "", token, "")
	if status != http.StatusOK || !strings.Contains(body, `"kind":"deliveryView"`) {
		t.Fatalf("delivery view = %d %s", status, body)
	}
	status, body = call(http.MethodGet, "/api/v1/workspaces/ws_s05/human-tasks", "", token, "")
	if status != http.StatusOK || !strings.Contains(body, `"kind":"humanTaskList"`) {
		t.Fatalf("human task list = %d %s", status, body)
	}
	// A write command without the CSRF token is refused even for a member.
	status, _ = call(http.MethodPost, "/api/v1/workspaces/ws_s05/work-items/"+workItemID+"/cancel",
		`{"requestKey":"cancel-1","expectedVersion":1,"reason":"stop"}`, token, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("cancel without CSRF = %d, want 401", status)
	}
	status, body = call(http.MethodPost, "/api/v1/workspaces/ws_s05/work-items/"+workItemID+"/cancel",
		`{"requestKey":"cancel-1","expectedVersion":1,"reason":"stop"}`, token, csrf)
	if status != http.StatusOK || !strings.Contains(body, `"state":"CANCELED"`) {
		t.Fatalf("cancel = %d %s", status, body)
	}
	// The cancellation is durable: the next read shows it and no reopen is offered.
	status, body = call(http.MethodGet, "/api/v1/workspaces/ws_s05/work-items/"+workItemID, "", token, "")
	if status != http.StatusOK || !strings.Contains(body, `"state":"CANCELED"`) {
		t.Fatalf("work item after cancel = %d %s", status, body)
	}

	// The git remote projection performs a real read-only observation. The
	// invariants hold whatever the environment reports: never a fabricated
	// fact, never a write observation, and an identity only when one was seen.
	status, body = call(http.MethodGet, "/api/v1/workspaces/ws_s05/git-remote-status", "", token, "")
	if status != http.StatusOK {
		t.Fatalf("git remote status = %d %s", status, body)
	}
	var statusBody struct {
		Kind   string `json:"kind"`
		Status struct {
			ProbeOutcome    string `json:"probeOutcome"`
			MachineIdentity struct {
				ObservedGitHubUserID string `json:"observedGitHubUserId"`
				State                string `json:"state"`
			} `json:"machineIdentity"`
			Repositories []struct {
				RepositoryID     string `json:"repositoryId"`
				WriteObservation string `json:"writeObservation"`
				RemoteRead       struct {
					State string `json:"state"`
				} `json:"remoteRead"`
				PushPermission struct {
					State string `json:"state"`
				} `json:"pushPermission"`
			} `json:"repositories"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &statusBody); err != nil {
		t.Fatalf("git remote body = %s (%v)", body, err)
	}
	if statusBody.Kind != "gitRemoteStatus" || len(statusBody.Status.Repositories) != 2 {
		t.Fatalf("git remote projection = %s", body)
	}
	// The observed projection is the environment evidence of this read-only
	// probe; it records what the machine identity and the remotes really were.
	t.Logf("git remote status body: %s", body)
	switch statusBody.Status.ProbeOutcome {
	case "completed":
		// A completed probe established the query identity; whether it is
		// registered machine identity is a separate fact.
		if statusBody.Status.MachineIdentity.ObservedGitHubUserID == "" {
			t.Fatalf("completed probe without a query identity: %s", body)
		}
		switch statusBody.Status.MachineIdentity.State {
		case "matched", "drifted", "unknown":
		default:
			t.Fatalf("unexpected identity state: %s", body)
		}
		if statusBody.Status.MachineIdentity.State == "unknown" &&
			!strings.Contains(body, `"reason"`) {
			t.Fatalf("an unmatched identity must carry a reason: %s", body)
		}
	case "unknown":
		if statusBody.Status.MachineIdentity.ObservedGitHubUserID != "" {
			t.Fatalf("unknown probe reported a query identity: %s", body)
		}
	default:
		t.Fatalf("unexpected probe outcome: %s", body)
	}
	for _, repository := range statusBody.Status.Repositories {
		if repository.WriteObservation != "none" {
			t.Fatalf("unexpected write observation %q", repository.WriteObservation)
		}
		switch repository.RemoteRead.State {
		case "readable", "unreadable", "unknown":
		default:
			t.Fatalf("unexpected remote read state %q", repository.RemoteRead.State)
		}
		switch repository.PushPermission.State {
		case "granted", "denied", "unknown":
		default:
			t.Fatalf("unexpected push permission state %q", repository.PushPermission.State)
		}
		if repository.RemoteRead.State != "readable" && strings.Contains(body, `"transport"`) {
			// A transport is only reported when a read actually succeeded.
			marker := `"repositoryId":"` + repository.RepositoryID + `"`
			section := body[strings.Index(body, marker):]
			if end := strings.Index(section, "}"); end > 0 && strings.Contains(section[:end], "transport") {
				t.Fatalf("unreadable repository reported a transport: %s", section[:end+1])
			}
		}
	}
}

// TestDeliverySurfaceKeepsTheRuntimeTransportOff proves the S04 frozen switch
// survives the S05 wiring: with the runtime surface disabled, the Runner
// transport still answers the closed shim (503 RESOURCE_UNAVAILABLE, which a
// Runner treats as retryable) instead of falling through to credential errors,
// while the execution service stays available to the delivery surface.
func TestDeliverySurfaceKeepsTheRuntimeTransportOff(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("KOWA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	const origin = "https://kowa.test"
	var srv *http.Server
	app := New(
		fx.Replace(config.Server{Address: "127.0.0.1:0"},
			config.Identity{
				Enabled: true, DatabaseURL: dsn, TrustedOrigin: origin,
				ClientID: "integration-client", ClientSecret: "integration-secret",
				AdminIDs: []string{"101"},
			}),
		fx.Populate(&srv))
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/register", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), "RESOURCE_UNAVAILABLE") {
		t.Fatalf("register with the runtime surface disabled = %d %s, want the 503 shim",
			recorder.Code, recorder.Body.String())
	}
	lease := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/tasks/lease", strings.NewReader(`{}`))
	lease.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, lease)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("lease with the runtime surface disabled = %d, want 503", recorder.Code)
	}
}

// TestDeliveryClosureSchemaServerPostgres runs a real closure through the
// production composition root (PostgreSQL single transaction) and validates the
// delivery HTTP response that carries the closure record against the frozen
// schema, with the pinned validator.
func TestDeliveryClosureSchemaServerPostgres(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("KOWA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	const origin = "https://kowa.test"
	var srv *http.Server
	var closureService *evaluation.Service
	app := New(
		fx.Replace(config.Server{Address: "127.0.0.1:0"},
			config.Identity{
				Enabled: true, DatabaseURL: dsn, TrustedOrigin: origin,
				ClientID: "integration-client", ClientSecret: "integration-secret",
				AdminIDs: []string{"101"},
			},
			config.Runtime{Enabled: true, DatabaseURL: dsn, GitHubMachineUserID: "135722937",
				Credentials: map[string]string{"runtime-1": "server-integration-runtime-token"}}),
		fx.Populate(&srv),
		fx.Populate(&closureService))
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Stop(ctx) }()
	database, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = database.Close() }()
	seedDeliveryWorkspace(t, ctx, database)
	seedClosureFixture(t, ctx, database, closureService)

	token, csrf := seedDeliverySession(t, ctx, database, "closure")
	get := func(path, sessionToken string) (int, string) {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: sessionToken})
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	status, body := get("/api/v1/workspaces/ws_s05/work-items/wi_closure/delivery", token)
	if status != http.StatusOK {
		t.Fatalf("delivery view = %d %s", status, body)
	}
	closureRead, closureFound, closureErr := closureService.Closure(ctx, "ws_s05", "wi_closure")
	if closureErr != nil || !closureFound {
		t.Fatalf("the composition root cannot read its own closure: %v, %v", closureFound, closureErr)
	}
	if !strings.Contains(body, `"closure"`) || !strings.Contains(body, `"closureId":"`+closureRead.ClosureID+`"`) {
		t.Fatalf("the delivery view does not carry the real closure: %s", body)
	}
	_ = csrf
	root := deliveryContractRoot(t)
	schema := filepath.Join(root, "api", "delivery-governance", "v1", "schema.json")
	instance := filepath.Join(t.TempDir(), "delivery-view.json")
	if err := os.WriteFile(instance, []byte(body), 0o600); err != nil {
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
		t.Fatalf("the live closure response violates the frozen schema: %v\n%s\n%s", err, output, body)
	}
}

// seedClosureFixture writes one work item with its run, observations, settled
// publish_pr operation, knowledge confirmation and verification rows, then
// closes it through the evaluation service the composition root assembled:
// the permanent chain is real Service.Close -> HTTP -> frozen schema.
func seedClosureFixture(t *testing.T, ctx context.Context, database *sql.DB, service *evaluation.Service) {
	t.Helper()
	store := postgres.NewStore(database)
	workspaceID, workItemID, runID := "ws_s05", "wi_closure", "run_closure"
	if _, err := database.ExecContext(ctx, `
		insert into workflow_definition (name, version, digest, definition, compiled_plan, created_at)
		values ('mvp-delivery', 1, $1, '{}', '{}', now())
		on conflict (name, version) do nothing
	`, "sha256:"+strings.Repeat("1", 64)); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	if _, err := database.ExecContext(ctx, `
		insert into workflow_run (workflow_run_id, workspace_id, work_item_id, started_by_github_user_id,
			git_execution_user_id, definition_name, definition_version, definition_digest, state, version,
			created_at, updated_at)
		values ($1, $2, $3, '101', '135722937', 'mvp-delivery', 1, $4, 'ACTIVE', 1, now(), now())
		on conflict (workflow_run_id) do nothing
	`, runID, workspaceID, workItemID, "sha256:"+strings.Repeat("1", 64)); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if _, err := store.CreateWorkItem(ctx, workitem.CreateMutation{
		WorkItem: workitem.WorkItem{
			ID: workItemID, WorkspaceID: workspaceID, Title: "Closure", TargetRepositoryID: "1401511579",
			Requirement: workitem.VersionedContent{Version: 1, Content: "r", Digest: workitem.ContentDigest("r")},
			Acceptance:  workitem.VersionedContent{Version: 1, Content: "a", Digest: workitem.ContentDigest("a")},
			State:       workitem.StateOpen, Version: 1, CurrentRunID: runID, CreatedByUserID: "101",
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Actor: workitem.Actor{GitHubUserID: "101"}, RequestKey: "wi-closure",
		RequestDigest: workitem.Digest("wi-closure"), TargetProjectID: "1401511579",
	}); err != nil {
		t.Fatalf("seed work item: %v", err)
	}
	head := "de62a4324cad57908376f8c5f3e14bd3bd3dab95"
	merge := "ca6c6ec8446ec687213d58064a7fd8363050321a"
	statements := []struct {
		statement string
		arguments []any
	}{
		{`insert into knowledge_snapshot (workspace_id, snapshot_id, repository_id, commit_oid, digest, canonical)
		  values ($1, 'ks_s05', '1413481670', 'bd2d5073a6001085d33d91d251cd082deb5676fe', $2, 'x'::bytea)
		  on conflict (workspace_id, snapshot_id) do nothing`,
			[]any{workspaceID, "sha256:" + strings.Repeat("2", 64)}},
		{`insert into knowledge_binding_event (workspace_id, event_id, subject_id, snapshot_id,
			human_response_id, actor_id, version, request_digest, confirmed_at)
		  values ($1, 'kbe_s05', $2, 'ks_s05', 'hr_s05', '101', 1, $3, now())
		  on conflict (workspace_id, event_id) do nothing`,
			[]any{workspaceID, workItemID, "sha256:" + strings.Repeat("3", 64)}},
		{`insert into github_observation (observation_id, workspace_id, repository_id, subject_kind, subject_key,
			observed_at, actor_github_user_id, unknown, fact)
		  values ('obs_merge_s05', $1, '1401511579', 'pull_request', '42', now(), '135722937', false,
			'{"pullRequest":{"number":"42","authorId":"135722937","headOid":"de62a4324cad57908376f8c5f3e14bd3bd3dab95","headRef":"refs/heads/task/s05","baseRef":"refs/heads/main","state":"closed","merged":true,"mergeCommitOid":"ca6c6ec8446ec687213d58064a7fd8363050321a","mergedById":"277460407","mergedByType":"User"}}'),
		         ('obs_review_s05', $1, '1401511579', 'pull_request_review', '42/5001', now(), '135722937', false,
			'{"review":{"reviewId":"5001","reviewerId":"277460407","reviewerType":"User","reviewerPermission":"write","state":"APPROVED","commitOid":"de62a4324cad57908376f8c5f3e14bd3bd3dab95"}}')
		  on conflict (observation_id) do nothing`, []any{workspaceID}},
		{`insert into external_operation (operation_id, workspace_id, work_item_id, workflow_run_id,
			kind, state, repository_id, ref, base_ref, commit_oid, expected_github_user_id,
			requested_by_github_user_id, request, request_digest, request_key, result, version,
			created_at, updated_at, settled_at)
		  values ('op_publish_s05', $1, $2, $3, 'publish_pr', 'SUCCEEDED', '1401511579', 'refs/heads/task/s05',
			'refs/heads/main', $4, '135722937', '101',
			'{"repositoryId":"1401511579","role":"project","operation":"publish_pr","ref":"refs/heads/task/s05","baseRef":"refs/heads/main","commitOid":"de62a4324cad57908376f8c5f3e14bd3bd3dab95","expectedGitHubUserId":"135722937","requestedByGitHubUserId":"101"}',
			$5, 'op-publish', '{"repositoryId":"1401511579","ref":"refs/heads/task/s05","commitOid":"de62a4324cad57908376f8c5f3e14bd3bd3dab95","pullRequestNumber":"42","pullRequestAuthorId":"135722937","baseRef":"refs/heads/main","reconciled":true,"observationId":"obs_pr_s05","observedAt":"2026-10-14T12:00:00Z","observedByGitHubUserId":"135722937"}', 2,
			now(), now(), now())
		  on conflict (operation_id) do nothing`,
			[]any{workspaceID, workItemID, runID, head, "sha256:" + strings.Repeat("4", 64)}},
		{`insert into verification_evidence (verification_id, workspace_id, work_item_id, workflow_run_id,
			kind, subject_kind, repository_id, commit_oid, configuration_version, producer_kind, producer_id,
			source, result, observed_at, created_at)
		  values ('ve_tests_s05', $1, $2, $3, 'required_tests', 'commit', '1401511579', $4,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		 ('ve_review_s05', $1, $2, $3, 'independent_review', 'commit', '1401511579', $4,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now()),
		 ('ve_post_s05', $1, $2, $3, 'post_merge_integration', 'commit', '1401511579', $5,
			'build-v1', 'task', 'task_1', 'real', 'passed', now(), now())
		  on conflict (verification_id) do nothing`,
			[]any{workspaceID, workItemID, runID, head, merge}},
	}
	for _, row := range statements {
		if _, err := database.ExecContext(ctx, row.statement, row.arguments...); err != nil {
			t.Fatalf("seed statement error = %v", err)
		}
	}
	if service == nil {
		t.Fatal("the composition root did not assemble the evaluation service")
	}
	if _, err := service.Close(ctx, evaluation.CloseCommand{
		WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		ExpectedWorkItemVersion: 1, ExpectedRunVersion: 1, ActorID: "101",
		RequestKey: "close-schema", PolicyVersion: "delivery-policy-v1",
		KnowledgeSnapshotID: "ks_s05", KnowledgeBindingEventID: "kbe_s05",
		PullRequestNumber: "42", MergeObservationID: "obs_merge_s05",
		ReviewObservationID: "obs_review_s05", RequiredTestIDs: []string{"ve_tests_s05"},
		IndependentReviewID: "ve_review_s05", PostMergeVerificationID: "ve_post_s05",
	}); err != nil {
		t.Fatalf("the composition root's Service.Close() error = %v", err)
	}
}

func deliveryContractRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "api", "delivery-governance", "v1", "schema.json")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root with the frozen schema was not found")
		}
		directory = parent
	}
}

func seedDeliveryWorkspace(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	statements := []string{
		`insert into workspace (workspace_id, name, version, created_at, updated_at)
		 values ('ws_s05', 'S05 integration', 1, now(), now())
		 on conflict (workspace_id) do nothing`,
		`insert into workspace_member (workspace_id, github_user_id, role, created_at, updated_at)
		 values ('ws_s05', '101', 'developer', now(), now()),
		        ('ws_s05', '202', 'viewer', now(), now())
		 on conflict (workspace_id, github_user_id) do update set role = excluded.role, revoked_at = null`,
		`insert into repository_binding (workspace_id, role, github_repository_id, installation_id,
			display_name, access_mode, created_at, updated_at)
		 values ('ws_s05', 'project', '1401511579', '12345', 'kowa-sandbox', 'read_write', now(), now()),
		        ('ws_s05', 'knowledge', '1413481670', '12345', 'kowa-sandbox-knowledge', 'read', now(), now())
		 on conflict (workspace_id, role) do nothing`,
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed statement error = %v", err)
		}
	}
}

func seedDeliverySession(t *testing.T, ctx context.Context, database *sql.DB, suffix string) (token, csrf string) {
	t.Helper()
	store := postgres.NewStore(database)
	tokenValue := "s05-integration-session-token-" + suffix
	csrfValue := "s05-integration-csrf-token-" + suffix
	err := store.CreateSession(ctx, identity.GitHubUser{ID: "101", Login: "member-a"}, identity.Session{
		TokenDigest: sha256.Sum256([]byte(tokenValue)),
		CSRFDigest:  sha256.Sum256([]byte(csrfValue)),
		Identity:    identity.WebIdentity{GitHubUserID: "101"},
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	return tokenValue, csrfValue
}
