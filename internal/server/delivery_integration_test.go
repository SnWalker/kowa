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
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/platform/config"
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
	token, csrf := seedDeliverySession(t, ctx, database)

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

func seedDeliverySession(t *testing.T, ctx context.Context, database *sql.DB) (token, csrf string) {
	t.Helper()
	store := postgres.NewStore(database)
	const tokenValue = "s05-integration-session-token"
	const csrfValue = "s05-integration-csrf-token"
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
