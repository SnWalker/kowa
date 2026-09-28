package health

import (
	"encoding/json"
	"net/http"
)

// NewHandler returns the process liveness endpoint.
func NewHandler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok"})
	})
}
