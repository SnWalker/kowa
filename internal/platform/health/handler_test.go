package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandler(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	NewHandler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := recorder.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Errorf("body = %q, want health response", got)
	}
}
