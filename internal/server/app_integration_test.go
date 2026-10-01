//go:build integration

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

func TestIdentityWorkspaceServerPostgres(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("KOWA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if r.Form.Get("code_verifier") == "" {
				t.Error("missing PKCE verifier")
			}
			if r.Form.Get("code") == "error" {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]string{"access_token": r.Form.Get("code")}); err != nil {
				t.Error(err)
			}
		case "/user":
			w.Header().Set("Content-Type", "application/json")
			if r.Header.Get("Authorization") == "Bearer member-b" {
				_, err := w.Write([]byte(`{"id":9202,"login":"member-b"}`))
				if err != nil {
					t.Error(err)
				}
			} else {
				_, err := w.Write([]byte(`{"id":9101,"login":"member-a"}`))
				if err != nil {
					t.Error(err)
				}
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer external.Close()
	clock := &integrationClock{}
	clock.nanos.Store(time.Now().UnixNano())
	var srv *http.Server
	app := New(fx.Replace(config.Server{Address: "127.0.0.1:0"},
		config.Identity{Enabled: true,
			DatabaseURL:   dsn,
			TrustedOrigin: "https://kowa.test",
			ClientID:      "test-id",
			ClientSecret:  "test-secret",
			AdminIDs:      []string{"9101"}}),

		fx.Decorate(func(identity.OAuthClient) (identity.OAuthClient, error) {
			return identity.NewGitHubOAuthClient(identity.GitHubOAuthConfig{ClientID: "test-id",
				ClientSecret: "test-secret",
				RedirectURL:  "https://kowa.test/api/v2/auth/github/callback",
				AuthorizeURL: external.URL + "/authorize",
				TokenURL:     external.URL + "/token",
				UserURL:      external.URL + "/user",
				HTTPClient:   external.Client()})
		}),
		fx.Decorate(func(workspace.RepositoryVisibility) workspace.RepositoryVisibility { return integrationVisibility{} }),
		fx.Decorate(func(identity.Clock) identity.Clock { return clock }), fx.Populate(&srv))
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	lateReady := make(chan struct{})
	lateRelease := make(chan struct{})
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("testDelay") != "1" {
			srv.Handler.ServeHTTP(w, r)
			return
		}
		snapshot := httptest.NewRecorder()
		srv.Handler.ServeHTTP(snapshot, r)
		close(lateReady)
		<-lateRelease
		for name, values := range snapshot.Header() {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(snapshot.Code)
		if _, err := w.Write(snapshot.Body.Bytes()); err != nil {
			t.Error(err)
		}
	}))
	defer tls.Close()
	defer func() {
		select {
		case <-lateRelease:
		default:
			close(lateRelease)
		}
	}()
	client := tls.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	call := func(method,
		path,
		body string,
		cookie *http.Cookie,
		csrf,
		origin,
		key string) (int,
		[]byte,
		http.Header,
		[]*http.Cookie) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, tls.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", key)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "test-secret") || strings.Contains(string(data), "access_token") {
			t.Fatal("external credentials leaked")
		}
		return res.StatusCode, data, res.Header, res.Cookies()
	}
	// diagBody renders a response body for failure messages only. Error
	// responses (status >= 400) are JSON without tokens and are truncated;
	// other statuses may carry session material, so the body is withheld.
	diagBody := func(status int, data []byte) string {
		if status < 400 {
			return "<withheld: non-error status>"
		}
		if len(data) > 512 {
			return string(data[:512]) + "...(truncated)"
		}
		return string(data)
	}
	login := func(code, version string) *http.Cookie {
		t.Helper()
		status, data, _, cookies := call("POST",
			"/api/"+version+"/auth/github/login",
			`{"returnTo":"/workspaces"}`,
			nil,
			"",
			"https://kowa.test",
			"")
		if status != 200 {
			t.Fatalf("login %d %s", status, data)
		}
		var begin struct {
			AuthorizationURL string `json:"authorizationUrl"`
		}
		if err := json.Unmarshal(data, &begin); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(begin.AuthorizationURL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("code_challenge_method") != "S256" {
			t.Fatal("missing PKCE")
		}
		var state *http.Cookie
		for _, c := range cookies {
			if c.Name == "__Host-kowa-oauth-state" {
				state = c
			}
		}
		if state == nil {
			names := make([]string, 0, len(cookies))
			for _, c := range cookies {
				names = append(names, c.Name)
			}
			t.Fatalf("missing state cookie: status=%d cookies=%v", status, names)
		}
		path := "/api/" + version + "/auth/github/callback?state=" + u.Query().Get("state") + "&code=" + code
		bad, badData, _, _ := call("GET", path, "", &http.Cookie{Name: state.Name, Value: "wrong"}, "", "", "")
		if bad != 401 {
			t.Fatalf("bad state accepted: status=%d body=%s", bad, diagBody(bad, badData))
		}
		status, data, headers, cookies := call("GET", path, "", state, "", "", "")
		if version == "v2" {
			if status != 303 || headers.Get("Location") != "https://kowa.test/workspaces" || len(data) != 0 {
				t.Fatalf("UI handoff %d %s %s", status, headers, data)
			}
		} else {
			if status != 200 {
				t.Fatalf("v1 callback failed: status=%d body=%s", status, diagBody(status, data))
			}
			assertResponseSchema(t, "identity-workspace", "LoginCallbackResponse", data)
		}
		if headers.Get("Referrer-Policy") != "no-referrer" || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("unsafe callback headers: status=%d referrer-policy=%q cache-control=%q",
				status, headers.Get("Referrer-Policy"), headers.Get("Cache-Control"))
		}
		replay, replayData, _, _ := call("GET", path, "", state, "", "", "")
		if replay != 401 {
			t.Fatalf("replayed callback succeeded: status=%d body=%s", replay, diagBody(replay, replayData))
		}
		for _, c := range cookies {
			if c.Name == "__Host-kowa-session" {
				if !c.HttpOnly || !c.Secure || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
					t.Fatal("unsafe session cookie")
				}
				return c
			}
		}
		t.Fatal("missing session cookie")
		return nil
	}
	// Exercise the real adapter, Store and handler through the Server composition root.
	a := login("member-a", "v1")
	recover := func(cookie *http.Cookie, id string) (int, []byte) {
		t.Helper()
		status, data, h, _ := call("POST",
			"/api/v2/auth/session",
			`{"requestId":"`+id+`"}`,
			cookie,
			"",
			"https://kowa.test",
			"")
		if h.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable recovery")
		}
		if status == 200 {
			assertResponseSchema(t, "web-session", "SessionBootstrapResponse", data)
		}
		return status, data
	}
	status, data := recover(a, "a-1")
	if status != 200 {
		t.Fatalf("recover %d %s", status, data)
	}
	var view struct {
		identity.SessionView
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		t.Fatal(err)
	}
	if view.Identity.GitHubUserID != "9101" || view.RequestID != "a-1" {
		t.Fatal("wrong recovered actor")
	}
	for _, id := range []string{"refresh", "tab-2"} {
		status, data := recover(a, id)
		var next identity.SessionView
		if err := json.Unmarshal(data, &next); err != nil {
			t.Fatal(err)
		}
		if status != 200 || next.CSRFToken != view.CSRFToken || next.SessionID != view.SessionID {
			t.Fatal("refresh/tab invalidated session")
		}
	}
	status, _, _, _ = call("POST", "/api/v2/auth/session", `{"requestId":"foreign"}`, a, "", "https://evil.test", "")
	if status != 401 {
		t.Fatal("foreign origin recovery accepted")
	}
	status, _, _, _ = call("POST",
		"/api/v1/workspaces",
		`{"name":"S01 rework"}`,
		a,
		"wrong",
		"https://kowa.test",
		"s01-server-bootstrap")
	if status != 401 {
		t.Fatal("wrong csrf write accepted")
	}
	status, data, _, _ = call("POST",
		"/api/v1/workspaces",
		`{"name":"S01 rework"}`,
		a,
		view.CSRFToken,
		"https://kowa.test",
		"s01-server-bootstrap")
	if status != 201 {
		t.Fatalf("bootstrap %d %s", status, data)
	}
	assertResponseSchema(t, "identity-workspace", "WorkspaceConfig", data)
	var ws workspace.Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatal(err)
	}
	repos := `{"expectedVersion":1,"project":{"githubRepositoryId":"3001","installationId":"4001",
		"displayName":"team/project"},"knowledge":{"githubRepositoryId":"3002","installationId":"4001",
		"displayName":"team/knowledge"}}`
	status, _, _, _ = call("PUT",
		"/api/v1/workspaces/"+ws.ID+"/repositories",
		strings.ReplaceAll(repos,
			"4001",
			"4999"),
		a,
		view.CSRFToken,
		"https://kowa.test",
		"s01-invisible-repos")
	if status != 503 {
		t.Fatal("unverified installation was accepted")
	}
	status, data, _, _ = call("GET", "/api/v1/workspaces/"+ws.ID, "", a, "", "", "")
	var unchanged workspace.Workspace
	if err := json.Unmarshal(data, &unchanged); err != nil {
		t.Fatal(err)
	}
	if status != 200 || unchanged.Version != 1 || unchanged.Project != nil {
		t.Fatal("refused repository mutation changed facts")
	}
	status, data, _, _ = call("PUT",
		"/api/v1/workspaces/"+ws.ID+"/repositories",
		repos,
		a,
		view.CSRFToken,
		"https://kowa.test",
		"s01-repos")
	if status != 200 {
		t.Fatalf("repo binding %d %s", status, data)
	}
	assertResponseSchema(t, "identity-workspace", "WorkspaceConfig", data)
	// Hold an actual A bootstrap response in transit while the shared browser
	// cookie switches to B. Correlation belongs to the request, not arrival time.
	type lateResult struct {
		body []byte
		err  error
	}
	lateDone := make(chan lateResult, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx,
			"POST",
			tls.URL+"/api/v2/auth/session?testDelay=1",
			strings.NewReader(`{"requestId":"late-a"}`))
		if err != nil {
			lateDone <- lateResult{err: err}
			return
		}
		req.Header.Set("Origin", "https://kowa.test")
		req.AddCookie(a)
		res, err := client.Do(req)
		if err != nil {
			lateDone <- lateResult{err: err}
			return
		}
		body, err := io.ReadAll(res.Body)
		closeErr := res.Body.Close()
		if err == nil {
			err = closeErr
		}
		lateDone <- lateResult{body: body, err: err}
	}()
	select {
	case <-lateReady:
	case <-time.After(10 * time.Second):
		t.Fatal("late response fixture not ready")
	}

	b := login("member-b", "v2")
	status, bData := recover(b, "b")
	if status != 200 {
		t.Fatal("identity switch recovery failed")
	}
	var bView identity.SessionView
	if err := json.Unmarshal(bData, &bView); err != nil {
		t.Fatal(err)
	}
	if bView.Identity.GitHubUserID != "9202" || bView.SessionID == view.SessionID {
		t.Fatal("identity switch not separated")
	}
	close(lateRelease)
	late := <-lateDone
	if late.err != nil {
		t.Fatal(late.err)
	}
	var lateView struct {
		identity.SessionView
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(late.body, &lateView); err != nil {
		t.Fatal(err)
	}
	if lateView.RequestID != "late-a" ||
		lateView.Identity.GitHubUserID != "9101" ||
		lateView.SessionID != view.SessionID ||
		lateView.SessionID == bView.SessionID {
		t.Fatal("late response lost original session/request correlation")
	}

	status, _, _, _ = call("GET", "/api/v1/workspaces/"+ws.ID, "", b, "", "", "")
	if status != 403 {
		t.Fatal("cross-workspace access accepted")
	}
	status, _, _, _ = call("PUT",
		"/api/v1/workspaces/"+ws.ID+"/members/9202",
		`{"expectedVersion":2,"role":"viewer"}`,
		a,
		view.CSRFToken,
		"https://kowa.test",
		"s01-grant-b")
	if status != 204 {
		t.Fatal("grant B failed")
	}
	status, _, _, _ = call("GET", "/api/v1/workspaces/"+ws.ID, "", b, "", "", "")
	if status != 200 {
		t.Fatal("granted member cannot read")
	}
	status, _, _, _ = call("DELETE",
		"/api/v1/workspaces/"+ws.ID+"/members/9202",
		`{"expectedVersion":3}`,
		a,
		view.CSRFToken,
		"https://kowa.test",
		"s01-revoke-b")
	if status != 204 {
		t.Fatal("revoke B failed")
	}
	status, _, _, _ = call("GET", "/api/v1/workspaces/"+ws.ID, "", b, "", "", "")
	if status != 403 {
		t.Fatal("revoked membership allowed read")
	}
	// A response from A arriving after switching to B cannot authorize any B write.
	status, _, _, _ = call("POST",
		"/api/v1/workspaces",
		`{"name":"wrong actor"}`,
		b,
		view.CSRFToken,
		"https://kowa.test",
		"late-a")
	if status != 401 {
		t.Fatal("late A csrf accepted for B")
	}
	status, _, _, _ = call("POST", "/api/v2/auth/logout", "", b, view.CSRFToken, "https://kowa.test", "")
	if status != 401 {
		t.Fatal("cross-session logout accepted")
	}
	status, _, _, _ = call("POST", "/api/v2/auth/logout", "", b, bView.CSRFToken, "https://kowa.test", "")
	if status != 204 {
		t.Fatal("legal logout failed after refusal")
	}
	status, _ = recover(b, "revoked")
	if status != 401 {
		t.Fatal("revoked recovery accepted")
	}
	clock.nanos.Add(int64(25 * time.Hour))
	status, _ = recover(a, "expired")
	if status != 401 {
		t.Fatal("expired recovery accepted")
	}
	db, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := db.QueryRowContext(ctx,
		`select count(*) from workspace where name = 'wrong actor'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("refused write changed durable facts")
	}
}

type integrationClock struct{ nanos atomic.Int64 }

func (c *integrationClock) Now() time.Time { return time.Unix(0, c.nanos.Load()).UTC() }

type integrationVisibility struct{}

func (integrationVisibility) RepositoryVisible(_ context.Context, install, repo string) error {
	if install != "4001" || (repo != "3001" && repo != "3002") {
		return workspace.ErrResourceUnavailable
	}
	return nil
}
