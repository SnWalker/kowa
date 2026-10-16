package server

// kowa.observability.v1 is reachable only when the composition root's mount
// predicate recognises the frozen path first; the handler tests in test/security
// call the surface directly and therefore cannot see that seam. The frozen
// watermark route was lost exactly there, so both halves are pinned here: the
// predicate itself and the mounted surface over the real workspace dispatcher.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/observability"
	"github.com/SnWalker/kowa/internal/workspace"
)

// frozenWatermarkPath is the route frozen by
// api/observability/v1/delivery.json (observability.watermarks) and consumed by
// the web client: /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/observability/watermarks
const frozenWatermarkPath = "/api/v1/workspaces/ws_1/work-items/wi_1/observability/watermarks"

// TestTheWatermarkPathPredicateMatchesExactlyTheFrozenRoute pins the mount
// predicate against the frozen route and its near misses. A predicate that
// accepts a shape the contract does not freeze hands the observability surface a
// path its own mux answers with 404; one that rejects the frozen shape loses the
// operation entirely.
func TestTheWatermarkPathPredicateMatchesExactlyTheFrozenRoute(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "the frozen route", path: frozenWatermarkPath, want: true},
		{
			name: "the frozen route with opaque ids",
			path: "/api/v1/workspaces/ws_0f3a1c/work-items/wi_9c21be/observability/watermarks",
			want: true,
		},
		{
			name: "a missing work-items segment",
			path: "/api/v1/workspaces/ws_1/wi_1/observability/watermarks",
			want: false,
		},
		{
			name: "an extra segment before the work item",
			path: "/api/v1/workspaces/ws_1/projects/p_1/work-items/wi_1/observability/watermarks",
			want: false,
		},
		{
			name: "an extra segment before the observability family",
			path: "/api/v1/workspaces/ws_1/work-items/wi_1/notes/n_1/observability/watermarks",
			want: false,
		},
		{
			name: "a missing work item",
			path: "/api/v1/workspaces/ws_1/work-items/observability/watermarks",
			want: false,
		},
		{
			name: "an empty work item",
			path: "/api/v1/workspaces/ws_1/work-items//observability/watermarks",
			want: false,
		},
		{
			name: "an empty workspace",
			path: "/api/v1/workspaces//work-items/wi_1/observability/watermarks",
			want: false,
		},
		{
			name: "a missing observability segment",
			path: "/api/v1/workspaces/ws_1/work-items/wi_1/watermarks",
			want: false,
		},
		{
			name: "a trailing segment",
			path: frozenWatermarkPath + "/extra",
			want: false,
		},
		{
			name: "a different observability suffix",
			path: "/api/v1/workspaces/ws_1/work-items/wi_1/observability/health",
			want: false,
		},
		{name: "the frozen health route", path: "/api/v1/observability/health", want: false},
		{name: "the workspace family root", path: "/api/v1/workspaces/ws_1/work-items/wi_1", want: false},
		{name: "the workspace collection", path: "/api/v1/workspaces/", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			if got := isObservabilityWatermarksPath(request); got != testCase.want {
				t.Fatalf("isObservabilityWatermarksPath(%q) = %v, want %v",
					testCase.path, got, testCase.want)
			}
		})
	}
}

// TestTheMountedSurfaceReachesTheFrozenWatermarkRoute proves the frozen path
// arrives at the observability surface's own session check (401 UNAUTHORIZED)
// instead of falling through to the workspace dispatcher's 404. The inner
// handler is the real dispatcher; its identity surface is a stand-in for the S01
// handler, whose 404 is what a production instance answered before the fix.
func TestTheMountedSurfaceReachesTheFrozenWatermarkRoute(t *testing.T) {
	cases := []struct {
		name          string
		path          string
		wantStatus    int
		wantCategory  string
		innerAnswered bool
	}{
		{
			name:         "the frozen watermark route reaches the surface",
			path:         frozenWatermarkPath,
			wantStatus:   http.StatusUnauthorized,
			wantCategory: "UNAUTHORIZED",
		},
		{
			name:         "the frozen health route reaches the surface",
			path:         "/api/v1/observability/health",
			wantStatus:   http.StatusUnauthorized,
			wantCategory: "UNAUTHORIZED",
		},
		{
			name:          "a missing work-items segment stays with the dispatcher",
			path:          "/api/v1/workspaces/ws_1/observability/watermarks",
			wantStatus:    http.StatusNotFound,
			innerAnswered: true,
		},
		{
			name:          "an extra segment stays with the dispatcher",
			path:          "/api/v1/workspaces/ws_1/projects/p_1/work-items/wi_1/observability/watermarks",
			wantStatus:    http.StatusNotFound,
			innerAnswered: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			projector := observability.NewProjector(nil, nil, nil, nil, nil)
			// The stubs are the S01 session and membership authorities; the
			// requests below carry no session, so neither is consulted. What is
			// under test is the real mount over the real dispatcher.
			handler := httpapi.NewObservabilityHandler(
				refusingIdentity{}, refusingWorkspaces{}, projector, "", nil)
			innerAnswered := false
			inner := workspaceAPIDispatch{identity: http.HandlerFunc(
				func(writer http.ResponseWriter, request *http.Request) {
					innerAnswered = true
					http.NotFound(writer, request)
				})}
			srv := &http.Server{Handler: inner}
			mountObservabilitySurface(srv, handler, projector, nil)

			recorder := httptest.NewRecorder()
			srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, testCase.path, nil))
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("GET %s = %d, want %d (%s)", testCase.path, recorder.Code, testCase.wantStatus, recorder.Body)
			}
			if innerAnswered != testCase.innerAnswered {
				t.Fatalf("GET %s: the workspace dispatcher answered = %v, want %v",
					testCase.path, innerAnswered, testCase.innerAnswered)
			}
			if testCase.wantCategory != "" {
				var body struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("GET %s: the surface answered with a non-JSON body: %s", testCase.path, recorder.Body)
				}
				if body.Error != testCase.wantCategory {
					t.Fatalf("GET %s: error category = %q, want %q", testCase.path, body.Error, testCase.wantCategory)
				}
			}
		})
	}
}

// refusingIdentity satisfies the S01 session authority while authenticating
// nobody, so a mount test cannot accidentally depend on an authenticated caller.
type refusingIdentity struct{}

func (refusingIdentity) BeginLogin(context.Context, string) (identity.BeginLoginResult, error) {
	return identity.BeginLoginResult{}, identity.ErrUnauthorized
}

func (refusingIdentity) CompleteLogin(
	context.Context, identity.CompleteLoginCommand,
) (identity.CompleteLoginResult, error) {
	return identity.CompleteLoginResult{}, identity.ErrUnauthorized
}

func (refusingIdentity) Authenticate(context.Context, string, string, bool) (identity.WebIdentity, error) {
	return identity.WebIdentity{}, identity.ErrUnauthorized
}

func (refusingIdentity) Logout(context.Context, string) error { return nil }

func (refusingIdentity) RecoverSession(context.Context, string) (identity.SessionView, error) {
	return identity.SessionView{}, identity.ErrUnauthorized
}

// refusingWorkspaces satisfies the S01 membership authority while granting no
// workspace to anybody.
type refusingWorkspaces struct{}

func (refusingWorkspaces) Get(context.Context, workspace.Actor, string) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrForbidden
}
