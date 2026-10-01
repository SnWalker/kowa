package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SnWalker/kowa/internal/platform/config"
	"go.uber.org/fx"
)

func TestServer_DisabledIdentityFailsClosed(t *testing.T) {
	var srv *http.Server
	app := New(fx.Replace(config.Server{Address: "127.0.0.1:0"}, config.Identity{}), fx.Populate(&srv))
	if app.Err() != nil {
		t.Fatal(app.Err())
	}
	req := httptest.NewRequest("POST", "/api/v2/auth/session", nil)
	r := httptest.NewRecorder()
	srv.Handler.ServeHTTP(r, req)
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled identity status=%d want503", r.Code)
	}
	if r.Header().Get("Referrer-Policy") != "no-referrer" || r.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("disabled identity failure lacks frozen safety headers")
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
