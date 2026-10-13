//go:build integration

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/platform/config"
	"go.uber.org/fx"
)

// TestRuntimeTransportServerPostgres proves the production composition root:
// the Runner transport is mounted over a PostgreSQL-backed execution service
// with credential-bound authentication.
func TestRuntimeTransportServerPostgres(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("KOWA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated database required")
	}
	const token = "server-integration-runtime-token"
	var srv *http.Server
	app := New(
		fx.Replace(config.Server{Address: "127.0.0.1:0"},
			config.Runtime{
				Enabled: true, DatabaseURL: dsn,
				Credentials: map[string]string{"runtime-1": token},
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

	call := func(method, path, credential, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if credential != "" {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	const registration = `{"schemaVersion":"kowa.workflow-execution.v1","kind":"runtimeRegistration","runtimeId":"runtime-1","runtimeEpoch":"epoch-server-1","gitHubUserId":"9001","provider":{"id":"scripted","version":"scripted-1"},"providerAuthenticated":true,"capabilities":[{"id":"plan.create","version":1}],"registeredAt":"2026-10-14T00:00:00Z"}`

	if status, _ := call(http.MethodPost, "/api/v1/runtime/register", "", registration); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated register = %d, want 401", status)
	}
	if status, _ := call(http.MethodPost, "/api/v1/runtime/register", "wrong-token-0123456789", registration); status != http.StatusUnauthorized {
		t.Fatalf("unknown credential register = %d, want 401", status)
	}
	status, body := call(http.MethodPost, "/api/v1/runtime/register", token, registration)
	if status != http.StatusOK || !strings.Contains(body, "epoch-server-1") {
		t.Fatalf("register = %d %s", status, body)
	}
	// Idempotent re-registration of the same generation.
	if status, _ := call(http.MethodPost, "/api/v1/runtime/register", token, registration); status != http.StatusOK {
		t.Fatalf("idempotent register = %d", status)
	}

	// No queued work: an immediate long-poll returns 204, not an error.
	status, body = call(http.MethodPost, "/api/v1/runtime/tasks/lease", token,
		`{"runtimeId":"runtime-1","runtimeEpoch":"epoch-server-1","waitSeconds":0,"leaseSeconds":60}`)
	if status != http.StatusNoContent {
		t.Fatalf("empty lease = %d %s", status, body)
	}

	status, body = call(http.MethodPost, "/api/v1/runtime/heartbeat", token,
		`{"runtimeId":"runtime-1","runtimeEpoch":"epoch-server-1","taskIds":[]}`)
	if status != http.StatusOK || !strings.Contains(body, `"cancelTaskIds":[]`) {
		t.Fatalf("heartbeat = %d %s", status, body)
	}

	// A superseded generation is refused with the frozen RESOURCE_UNAVAILABLE.
	superseding := strings.Replace(registration, "epoch-server-1", "epoch-server-2", 1)
	if status, _ := call(http.MethodPost, "/api/v1/runtime/register", token, superseding); status != http.StatusOK {
		t.Fatalf("new generation register = %d", status)
	}
	status, body = call(http.MethodPost, "/api/v1/runtime/tasks/lease", token,
		`{"runtimeId":"runtime-1","runtimeEpoch":"epoch-server-1","waitSeconds":0,"leaseSeconds":60}`)
	if status != http.StatusConflict || !strings.Contains(body, "RESOURCE_UNAVAILABLE") {
		t.Fatalf("superseded lease = %d %s", status, body)
	}
}
