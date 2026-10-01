package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/platform/clock"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/platform/random"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

func newIdentityDatabase(lc fx.Lifecycle, c config.Identity) (*sql.DB, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled {
		return nil, nil
	}
	db, err := postgres.NewPool(c.DatabaseURL)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return errors.Join(err, db.Close())
		}
		return nil
	}, OnStop: func(context.Context) error { return db.Close() }})
	return db, nil
}

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
	db *sql.DB,
	oauth identity.OAuthClient,
	visibility workspace.RepositoryVisibility,
	identityClock identity.Clock) *httpapi.IdentityWorkspaceHandler {
	if !c.Enabled {
		return nil
	}
	store := postgres.NewStore(db)
	ids := random.Generator{}
	auth := identity.NewService(store, oauth, ids, identityClock, identity.DefaultConfig())
	admins := workspace.Allowlist{}
	for _, id := range c.AdminIDs {
		admins[id] = struct{}{}
	}
	ws := workspace.NewService(store, visibility, admins, ids)
	return httpapi.NewIdentityWorkspaceHandler(auth, ws, c.TrustedOrigin)
}

func mountIdentityWorkspace(srv *http.Server, handler *httpapi.IdentityWorkspaceHandler) {
	var api http.Handler = handler
	if handler == nil {
		api = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"RESOURCE_UNAVAILABLE"}`))
		})
	}
	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler)
	mux.Handle("/api/v1/auth/", api)
	mux.Handle("/api/v1/workspaces", api)
	mux.Handle("/api/v1/workspaces/", api)
	mux.Handle("/api/v2/auth/", api)
	srv.Handler = mux
}

func newIdentityClock() identity.Clock { return clock.Clock{} }
