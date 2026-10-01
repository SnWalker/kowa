package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/workspace"
)

func TestIdentityWorkspaceHandler_CompleteLoginSetsHardenedSessionCookie(t *testing.T) {
	t.Parallel()

	identityService := &fakeIdentityService{
		completeResult: identity.CompleteLoginResult{
			Identity:     identity.WebIdentity{GitHubUserID: "101", Login: "member-a"},
			SessionToken: "session-token",
			CSRFToken:    "csrf-token",
			ReturnTo:     "/workspaces",
			ExpiresAt:    time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC),
		},
	}
	handler := NewIdentityWorkspaceHandler(identityService, &fakeWorkspaceService{}, "https://kowa.test")
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?state=state&code=code", nil)
	request.AddCookie(secureCookie(oauthStateCookie, "state", time.Now().Add(time.Minute)))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookie {
			session = cookie
		}
	}
	if session == nil ||
		!session.Secure ||
		!session.HttpOnly ||
		session.Path != "/" ||
		session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", session)
	}
	if strings.Contains(response.Body.String(), "session-token") {
		t.Fatal("session token leaked into response body")
	}
}

func TestIdentityWorkspaceHandler_WriteUsesSessionActorAndRequiresCSRF(t *testing.T) {
	t.Parallel()

	identityService := &fakeIdentityService{identity: identity.WebIdentity{GitHubUserID: "101", Login: "member-a"}}
	workspaceService := &fakeWorkspaceService{bootstrapResult: workspace.Workspace{ID: "workspace-1", Version: 1}}
	handler := NewIdentityWorkspaceHandler(identityService, workspaceService, "https://kowa.test")

	request := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(`{"name":"Kowa"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("Origin", "https://kowa.test")
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status without CSRF = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(`{"name":"Kowa"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("X-CSRF-Token", "csrf-token")
	request.Header.Set("Origin", "https://attacker.test")
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status with untrusted origin = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(`{"name":"Kowa"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("X-CSRF-Token", "csrf-token")
	request.Header.Set("Origin", "https://kowa.test")
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if workspaceService.lastActor.GitHubUserID != "101" {
		t.Fatalf("actor = %#v, want session actor", workspaceService.lastActor)
	}
}

func TestIdentityWorkspaceHandler_MapsCrossWorkspaceAccessToForbidden(t *testing.T) {
	t.Parallel()

	handler := NewIdentityWorkspaceHandler(
		&fakeIdentityService{identity: identity.WebIdentity{GitHubUserID: "101"}},
		&fakeWorkspaceService{getErr: workspace.ErrForbidden},
		"https://kowa.test",
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/workspace-2", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "FORBIDDEN") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

type fakeIdentityService struct {
	identity       identity.WebIdentity
	completeResult identity.CompleteLoginResult
}

func (s *fakeIdentityService) BeginLogin(context.Context, string) (identity.BeginLoginResult, error) {
	return identity.BeginLoginResult{}, nil
}

func (s *fakeIdentityService) CompleteLogin(
	context.Context,
	identity.CompleteLoginCommand,
) (identity.CompleteLoginResult, error) {
	return s.completeResult, nil
}

func (s *fakeIdentityService) Authenticate(
	_ context.Context,
	sessionToken string,
	csrfToken string,
	requireCSRF bool,
) (identity.WebIdentity, error) {
	if sessionToken != "session-token" || (requireCSRF && csrfToken != "csrf-token") {
		return identity.WebIdentity{}, identity.ErrUnauthorized
	}
	return s.identity, nil
}

func (s *fakeIdentityService) Logout(context.Context, string) error { return nil }

type fakeWorkspaceService struct {
	lastActor       workspace.Actor
	bootstrapResult workspace.Workspace
	getErr          error
}

func (s *fakeWorkspaceService) Bootstrap(
	_ context.Context,
	actor workspace.Actor,
	_ workspace.BootstrapCommand,
) (workspace.Workspace, error) {
	s.lastActor = actor
	return s.bootstrapResult, nil
}

func (s *fakeWorkspaceService) Get(
	context.Context,
	workspace.Actor,
	string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, s.getErr
}

func (s *fakeWorkspaceService) ConfigureRepositories(
	context.Context,
	workspace.Actor,
	workspace.ConfigureRepositoriesCommand,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, errors.New("not implemented")
}

func (s *fakeWorkspaceService) UpsertMember(
	context.Context,
	workspace.Actor,
	workspace.UpsertMemberCommand,
) error {
	return errors.New("not implemented")
}

func (s *fakeWorkspaceService) RevokeMember(
	context.Context,
	workspace.Actor,
	workspace.RevokeMemberCommand,
) error {
	return errors.New("not implemented")
}

// Characterizes the actual pre-rework callback producer, not a hand-written example.
func TestCallbackWireFrozenSchema(t *testing.T) {
	handler := NewIdentityWorkspaceHandler(&fakeIdentityService{completeResult: identity.CompleteLoginResult{
		Identity: identity.WebIdentity{GitHubUserID: "101",
			Login: "member-a"},
		SessionToken: "opaque",
		CSRFToken:    "csrf",
		ReturnTo:     "/workspaces",
	}}, &fakeWorkspaceService{}, "https://kowa.test")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?state=s&code=c", nil)
	req.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "s"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"githubUserId":"101","login":"member-a"`) {
		t.Fatalf("frozen schema violated: %d %s", response.Code, response.Body.String())
	}
	t.Logf("actual callback wire: %s", response.Body.String())
}

func (s *fakeIdentityService) RecoverSession(context.Context, string) (identity.SessionView, error) {
	return identity.SessionView{Identity: s.identity,
			CSRFToken: "csrf-token",
			SessionID: "session-id",
			ExpiresAt: time.Now().Add(time.Hour)},
		nil
}

func TestIdentityWorkspaceHandler_UIHandoffAndRecoveryOrigin(t *testing.T) {
	t.Parallel()
	handler := NewIdentityWorkspaceHandler(&fakeIdentityService{identity: identity.WebIdentity{GitHubUserID: "101",
		Login: "member-a"},
		completeResult: identity.CompleteLoginResult{SessionToken: "opaque-session",
			CSRFToken: "csrf-token",
			ReturnTo:  "/workspaces",
			ExpiresAt: time.Now().Add(time.Hour)}},
		&fakeWorkspaceService{},
		"https://kowa.test")
	request := httptest.NewRequest("GET",
		"/api/v2/auth/github/callback?state=s&code=secret&returnTo=https://evil.test",
		nil)
	request.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: "s"})
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, request)
	if r.Code != 303 || r.Header().Get("Location") != "https://kowa.test/workspaces" || r.Body.Len() != 0 {
		t.Fatalf("unsafe UI handoff: %d %s %s", r.Code, r.Header(), r.Body.String())
	}
	if r.Header().Get("Cache-Control") != "no-store" || r.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("sensitive callback cache/referrer headers missing")
	}
	for _, tc := range []struct {
		name, origin, body string
		status             int
	}{{"foreign origin",
		"https://evil.test",
		`{"requestId":"r"}`,
		401},
		{"missing origin",
			"",
			`{"requestId":"r"}`,
			401},
		{"empty correlation",
			"https://kowa.test",
			`{"requestId":""}`,
			400},
		{"unknown actor field",
			"https://kowa.test",
			`{"requestId":"r","actor":"attacker"}`,
			400},
		{"valid after refusals",
			"https://kowa.test",
			`{"requestId":"r"}`,
			200}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v2/auth/session", strings.NewReader(tc.body))
			req.Header.Set("Origin", tc.origin)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-token"})
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if out.Code != tc.status {
				t.Fatalf("status %d body %s", out.Code, out.Body.String())
			}
			if out.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable recovery")
			}
			if out.Code == 200 && !strings.Contains(out.Body.String(), `"requestId":"r"`) {
				t.Fatal("missing correlation")
			}
		})
	}
}
