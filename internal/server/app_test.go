package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
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

func TestServer_DisabledRuntimeFailsClosed(t *testing.T) {
	var srv *http.Server
	app := New(fx.Replace(config.Server{Address: "127.0.0.1:0"}, config.Runtime{}), fx.Populate(&srv))
	if app.Err() != nil {
		t.Fatal(app.Err())
	}
	req := httptest.NewRequest("POST", "/api/v1/runtime/register", nil)
	r := httptest.NewRecorder()
	srv.Handler.ServeHTTP(r, req)
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled runtime transport status=%d want503", r.Code)
	}
	if err := app.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestUnboundTaskInputFailsClosed(t *testing.T) {
	provider := unboundTaskInput{}
	if _, err := provider.TaskInput(context.Background(), "task-1"); !errors.Is(err, httpapi.ErrTaskInputUnbound) {
		t.Fatalf("task input = %v, want unbound", err)
	}
	if _, err := provider.TaskInputArtifact(context.Background(), "task-1", "artifact-1"); !errors.Is(err, httpapi.ErrTaskInputUnbound) {
		t.Fatalf("task input artifact = %v, want unbound", err)
	}
}
