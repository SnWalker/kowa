package server

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/platform/clock"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/platform/random"
	"github.com/SnWalker/kowa/internal/workspace"
)

func newOAuthClient(c config.Identity) (identity.OAuthClient, error) {
	if !c.Enabled {
		return nil, nil
	}
	return identity.NewGitHubOAuthClient(identity.GitHubOAuthConfig{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.TrustedOrigin + "/api/v2/auth/github/callback",
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	})
}

// Installation observations require a separately verified adapter; unavailable
// external facts fail closed rather than granting repository visibility.
type unavailableRepositoryVisibility struct{}

func (unavailableRepositoryVisibility) RepositoryVisible(context.Context, string, string) error {
	return workspace.ErrResourceUnavailable
}
func newRepositoryVisibility() workspace.RepositoryVisibility {
	return unavailableRepositoryVisibility{}
}

func newIdentityWorkspace(c config.Identity,
	auth *identity.Service,
	ws *workspace.Service) *httpapi.IdentityWorkspaceHandler {
	if !c.Enabled || auth == nil || ws == nil {
		return nil
	}
	return httpapi.NewIdentityWorkspaceHandler(auth, ws, c.TrustedOrigin)
}

// newIdentityService is the shared S01 session authority; the runtime-web
// surface authenticates through the same instance.
func newIdentityService(c config.Identity,
	db *sql.DB,
	oauth identity.OAuthClient,
	identityClock identity.Clock) *identity.Service {
	if !c.Enabled {
		return nil
	}
	return identity.NewService(postgres.NewStore(db), oauth, random.Generator{}, identityClock, identity.DefaultConfig())
}

// newWorkspaceService is the shared S01 membership authority.
func newWorkspaceService(c config.Identity,
	db *sql.DB,
	visibility workspace.RepositoryVisibility) *workspace.Service {
	if !c.Enabled {
		return nil
	}
	admins := workspace.Allowlist{}
	for _, id := range c.AdminIDs {
		admins[id] = struct{}{}
	}
	// Usage identifiers travel into the artifact and knowledge contracts, which
	// freeze the artifact identifier grammar; the generator respects it.
	return workspace.NewService(postgres.NewStore(db), visibility, admins, random.HexGenerator{})
}

// unavailableAPI fails closed with the frozen error envelope for every API
// route whose owning surface is disabled.
func unavailableAPI(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_, _ = writer.Write([]byte(`{"error":"RESOURCE_UNAVAILABLE"}`))
}

// runtimeWebRoutes matches the kowa.runtime-web.v1 path family, which the
// runtime-web surface owns inside the workspace route family.
var runtimeWebRoutes = regexp.MustCompile(`^/api/v1/workspaces/[^/]+/(runtimes|execution-config)(/[^/]+)?$`)

// deliveryRoutes matches the kowa.delivery-governance.v1 path family, which the
// S05 delivery surface owns inside the workspace route family.
var deliveryRoutes = regexp.MustCompile(`^/api/v1/workspaces/[^/]+/(human-tasks(/[^/]+)?(/responses)?|git-remote-status|work-items/[^/]+/delivery)$`)

// workItemRoutes matches the kowa.workitem.v1 path family.
var workItemRoutes = regexp.MustCompile(`^/api/v1/workspaces/[^/]+/work-items(/[^/]+)?(/cancel)?$`)

// workspaceAPIDispatch routes each frozen contract family to its own handler
// and everything else in the workspace family to the S01 surface. A disabled
// family fails closed instead of falling through to another owner.
type workspaceAPIDispatch struct {
	identity   http.Handler
	runtimeWeb http.Handler
	delivery   http.Handler
	workItems  http.Handler
}

func (d workspaceAPIDispatch) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case runtimeWebRoutes.MatchString(request.URL.Path):
		dispatchTo(writer, request, d.runtimeWeb)
	case deliveryRoutes.MatchString(request.URL.Path):
		dispatchTo(writer, request, d.delivery)
	case workItemRoutes.MatchString(request.URL.Path):
		dispatchTo(writer, request, d.workItems)
	default:
		if d.identity == nil {
			unavailableAPI(writer, request)
			return
		}
		d.identity.ServeHTTP(writer, request)
	}
}

func dispatchTo(writer http.ResponseWriter, request *http.Request, handler http.Handler) {
	if handler == nil {
		unavailableAPI(writer, request)
		return
	}
	handler.ServeHTTP(writer, request)
}

func mountIdentityWorkspace(
	srv *http.Server,
	handler *httpapi.IdentityWorkspaceHandler,
	runtimeWeb *httpapi.RuntimeWebHandler,
	workItems *httpapi.WorkItemHandler,
	delivery *httpapi.DeliveryHandler,
) {
	var identityAPI http.Handler
	if handler != nil {
		identityAPI = handler
	}
	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler)
	workspaceAPI := workspaceAPIDispatch{
		identity: identityAPI, runtimeWeb: runtimeWebHandler(runtimeWeb),
		delivery: deliveryHandler(delivery), workItems: workItemHandler(workItems),
	}
	mux.Handle("/api/v1/workspaces", workspaceAPI)
	mux.Handle("/api/v1/workspaces/", workspaceAPI)
	authAPI := identityAPI
	if authAPI == nil {
		authAPI = http.HandlerFunc(unavailableAPI)
	}
	mux.Handle("/api/v1/auth/", authAPI)
	mux.Handle("/api/v2/auth/", authAPI)
	srv.Handler = mux
}

func newIdentityClock() identity.Clock { return clock.Clock{} }

// runtimeWebHandler converts a typed nil handler into a nil interface so the
// dispatcher can fail closed without invoking methods on a nil pointer.
func runtimeWebHandler(handler *httpapi.RuntimeWebHandler) http.Handler {
	if handler == nil {
		return nil
	}
	return handler
}

// workItemHandler converts a typed nil handler into a nil interface so the
// dispatch fails closed while the S05 surface is disabled.
func workItemHandler(handler *httpapi.WorkItemHandler) http.Handler {
	if handler == nil {
		return nil
	}
	return handler
}

// deliveryHandler is the delivery analogue of runtimeWebHandler.
func deliveryHandler(handler *httpapi.DeliveryHandler) http.Handler {
	if handler == nil {
		return nil
	}
	return handler
}
