package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workspace"
)

type fakeIdentity struct {
	user      identity.WebIdentity
	err       error
	csrfToken string
}

func (f *fakeIdentity) BeginLogin(context.Context, string) (identity.BeginLoginResult, error) {
	return identity.BeginLoginResult{}, errors.New("unexpected")
}
func (f *fakeIdentity) CompleteLogin(context.Context, identity.CompleteLoginCommand) (identity.CompleteLoginResult, error) {
	return identity.CompleteLoginResult{}, errors.New("unexpected")
}
func (f *fakeIdentity) Authenticate(_ context.Context, _ string, csrf string, requireCSRF bool) (identity.WebIdentity, error) {
	if f.err != nil {
		return identity.WebIdentity{}, f.err
	}
	if requireCSRF && csrf != f.csrfToken {
		return identity.WebIdentity{}, identity.ErrUnauthorized
	}
	return f.user, nil
}
func (f *fakeIdentity) Logout(context.Context, string) error { return nil }
func (f *fakeIdentity) RecoverSession(context.Context, string) (identity.SessionView, error) {
	return identity.SessionView{}, errors.New("unexpected")
}

type fakeWorkspaces struct {
	members map[string]workspace.Member
	err     error
}

func (f *fakeWorkspaces) Get(_ context.Context, actor workspace.Actor, workspaceID string) (workspace.Workspace, error) {
	if f.err != nil {
		return workspace.Workspace{}, f.err
	}
	member, ok := f.members[actor.GitHubUserID]
	if !ok || workspaceID != "workspace-a" {
		return workspace.Workspace{}, workspace.ErrForbidden
	}
	return workspace.Workspace{
		ID: workspaceID, Name: "alpha", Version: 1,
		Members: []workspace.Member{member},
	}, nil
}

type fakeRuntimeWeb struct {
	views     []runtimeops.RuntimeView
	config    runtimeops.ExecutionConfig
	forbidden map[string]bool
	last      *runtimeops.UpdateExecutionConfigCommand
}

func (f *fakeRuntimeWeb) WorkspaceRuntimes(context.Context, string) ([]runtimeops.RuntimeView, error) {
	return f.views, nil
}
func (f *fakeRuntimeWeb) WorkspaceRuntime(_ context.Context, _, runtimeID string) (runtimeops.RuntimeView, error) {
	if f.forbidden[runtimeID] {
		return runtimeops.RuntimeView{}, runtimeops.ErrForbidden
	}
	return runtimeops.RuntimeView{RuntimeID: runtimeID, Freshness: runtimeops.FreshnessUnknown}, nil
}
func (f *fakeRuntimeWeb) ExecutionConfig(context.Context, string) (runtimeops.ExecutionConfig, error) {
	return f.config, nil
}
func (f *fakeRuntimeWeb) UpdateExecutionConfig(
	_ context.Context,
	command runtimeops.UpdateExecutionConfigCommand,
) (runtimeops.ExecutionConfig, error) {
	f.last = &command
	if command.ExpectedVersion != f.config.Version {
		return runtimeops.ExecutionConfig{}, runtimeops.ErrVersionConflict
	}
	f.config.Version++
	f.config.Capabilities = command.Capabilities
	f.config.Runtimes = command.Runtimes
	f.config.UpdatedByGitHubUserID = command.ActorID
	return f.config, nil
}

const (
	webOrigin     = "https://kowa.test"
	webCSRF       = "csrf-token-value"
	webSessionTok = "session-token-value"
)

func newRuntimeWebFixture(t *testing.T, members map[string]workspace.Member) (*httptest.Server, *fakeRuntimeWeb) {
	t.Helper()
	runtimes := &fakeRuntimeWeb{
		views: []runtimeops.RuntimeView{{
			RuntimeID: "runtime-1", GitHubUserID: "135722937",
			ProviderID: "codex", ProviderVersion: "codex-cli 0.162.1",
			ProviderAuthenticated: true, Capabilities: []runtimeops.Capability{{ID: "plan.create", Version: 1}},
			RegisteredAt: time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC),
			Freshness:    runtimeops.FreshnessUnknown,
		}},
		config:    runtimeops.ExecutionConfig{WorkspaceID: "workspace-a", Version: 0, Capabilities: []runtimeops.Capability{}, Runtimes: []string{}},
		forbidden: map[string]bool{"runtime-foreign": true},
	}
	handler := httpapi.NewRuntimeWebHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: "101", Login: "alice"}, csrfToken: webCSRF},
		&fakeWorkspaces{members: members},
		runtimes, webOrigin,
	)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, runtimes
}

func webCall(
	t *testing.T,
	server *httptest.Server,
	method, path, body string,
	withSession bool,
	origin string,
	csrf string,
) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if withSession {
		request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: webSessionTok})
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(encoded)
}

func TestRuntimeWebAuthorizationMatrix(t *testing.T) {
	members := map[string]workspace.Member{
		"101": {GitHubUserID: "101", Role: workspace.RoleAdmin},
		"202": {GitHubUserID: "202", Role: workspace.RoleViewer},
		"303": {GitHubUserID: "303", Role: workspace.RoleDeveloper},
	}
	server, _ := newRuntimeWebFixture(t, members)

	// No session: refused before any lookup.
	if status, _ := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-a/runtimes", "", false, "", ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d", status)
	}
	// Member list succeeds and leaks no secret-shaped keys.
	status, body := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-a/runtimes", "", true, "", "")
	if status != http.StatusOK || !strings.Contains(body, `"kind":"runtimeList"`) ||
		!strings.Contains(body, `"freshness":"unknown"`) {
		t.Fatalf("member list = %d %s", status, body)
	}
	for _, forbidden := range []string{"token", "secret", "environment", "password"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("list response leaked %q: %s", forbidden, body)
		}
	}
	// The runtime-web family does not require CSRF for reads, but the workspace
	// boundary still applies: a runtime outside the configuration is refused.
	if status, _ := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-a/runtimes/runtime-foreign", "", true, "", ""); status != http.StatusForbidden {
		t.Fatalf("foreign runtime = %d", status)
	}
	if status, _ := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-a/runtimes/runtime-1", "", true, "", ""); status != http.StatusOK {
		t.Fatalf("authorized runtime = %d", status)
	}
	// Another workspace is refused by membership, not by data.
	if status, _ := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-b/runtimes", "", true, "", ""); status != http.StatusForbidden {
		t.Fatalf("cross workspace = %d", status)
	}
}

func TestRuntimeWebExecutionConfigMutation(t *testing.T) {
	server, runtimes := newRuntimeWebFixture(t, map[string]workspace.Member{
		"101": {GitHubUserID: "101", Role: workspace.RoleAdmin},
		"202": {GitHubUserID: "202", Role: workspace.RoleViewer},
	})
	body := `{"requestKey":"cfg-1","expectedVersion":0,"capabilities":[{"id":"plan.create","version":1}],"runtimes":["runtime-1"]}`

	// A member may read the frozen configuration; before the first write it is
	// a zero version with empty lists.
	status, response := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-a/execution-config", "", true, "", "")
	if status != http.StatusOK || !strings.Contains(response, `"version":0`) {
		t.Fatalf("member read = %d %s", status, response)
	}

	// Writes require the exact Origin and CSRF token.
	if status, _ := webCall(t, server, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", body, true, "", webCSRF); status != http.StatusUnauthorized {
		t.Fatalf("missing origin = %d, want 401", status)
	}
	if status, _ := webCall(t, server, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", body, true, "https://evil.test", webCSRF); status != http.StatusUnauthorized {
		t.Fatalf("foreign origin = %d, want 401", status)
	}
	if status, _ := webCall(t, server, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", body, true, webOrigin, "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong csrf = %d, want 401", status)
	}

	// A viewer may read but never write.
	viewerHandler := httpapi.NewRuntimeWebHandler(
		&fakeIdentity{user: identity.WebIdentity{GitHubUserID: "202", Login: "bob"}, csrfToken: webCSRF},
		&fakeWorkspaces{members: map[string]workspace.Member{
			"202": {GitHubUserID: "202", Role: workspace.RoleViewer},
		}}, runtimes, webOrigin,
	)
	viewerServer := httptest.NewServer(viewerHandler)
	t.Cleanup(viewerServer.Close)
	if status, _ := webCall(t, viewerServer, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", body, true, webOrigin, webCSRF); status != http.StatusForbidden {
		t.Fatalf("viewer write = %d, want 403", status)
	}

	// The admin write succeeds, carries the session actor, and advances the
	// version exactly once.
	status, response = webCall(t, server, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", body, true, webOrigin, webCSRF)
	if status != http.StatusOK || !strings.Contains(response, `"kind":"executionConfig"`) ||
		!strings.Contains(response, `"version":1`) || !strings.Contains(response, `"updatedByGitHubUserId":"101"`) {
		t.Fatalf("admin write = %d %s", status, response)
	}
	if runtimes.last == nil || runtimes.last.WorkspaceID != "workspace-a" || runtimes.last.ActorID != "101" {
		t.Fatalf("command = %+v", runtimes.last)
	}

	// Version drift from the frozen version conflicts.
	drift := `{"requestKey":"cfg-2","expectedVersion":7,"capabilities":[],"runtimes":[]}`
	if status, _ := webCall(t, server, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", drift, true, webOrigin, webCSRF); status != http.StatusConflict {
		t.Fatalf("version drift = %d, want 409", status)
	}
	// Unknown fields in the request body are rejected (for example a smuggled
	// runtime token), so the strict decoder never accepts extra keys.
	smuggled := `{"requestKey":"cfg-3","expectedVersion":1,"capabilities":[],"runtimes":[],"runtimeToken":"x"}`
	if status, _ := webCall(t, server, http.MethodPut, "/api/v1/workspaces/workspace-a/execution-config", smuggled, true, webOrigin, webCSRF); status != http.StatusBadRequest {
		t.Fatalf("smuggled field = %d, want 400", status)
	}
}

func TestRuntimeWebResponseShapeMatchesContract(t *testing.T) {
	server, _ := newRuntimeWebFixture(t, map[string]workspace.Member{
		"101": {GitHubUserID: "101", Role: workspace.RoleAdmin},
	})
	status, body := webCall(t, server, http.MethodGet, "/api/v1/workspaces/workspace-a/runtimes", "", true, "", "")
	if status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}
	// The live response must decode under the frozen strict shapes: unknown
	// fields and missing required values are rejected exactly like the
	// contract examples are.
	var envelope struct {
		SchemaVersion string `json:"schemaVersion"`
		Kind          string `json:"kind"`
		Runtimes      []struct {
			RuntimeID             string                  `json:"runtimeId"`
			GitHubUserID          string                  `json:"gitHubUserId"`
			ProviderID            string                  `json:"providerId"`
			ProviderVersion       string                  `json:"providerVersion"`
			ProviderAuthenticated bool                    `json:"providerAuthenticated"`
			Capabilities          []runtimeops.Capability `json:"capabilities"`
			RegisteredAt          time.Time               `json:"registeredAt"`
			Freshness             string                  `json:"freshness"`
			StaleReason           string                  `json:"staleReason,omitempty"`
			Observation           *runtimeops.Observation `json:"observation,omitempty"`
		} `json:"runtimes"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("live response violates the frozen shape: %v (%s)", err, body)
	}
	if envelope.SchemaVersion != runtimeops.SchemaVersion || envelope.Kind != "runtimeList" ||
		len(envelope.Runtimes) != 1 || envelope.Runtimes[0].Freshness != runtimeops.FreshnessUnknown {
		t.Fatalf("envelope = %+v", envelope)
	}
}
