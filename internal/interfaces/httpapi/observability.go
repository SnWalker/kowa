package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/observability"
)

// observabilityRoutes matches the kowa.observability.v1 path family.
var observabilityRoutes = struct {
	health     string
	watermarks string
}{
	health:     "/api/v1/observability/health",
	watermarks: "/api/v1/workspaces/{workspaceID}/work-items/{workItemID}/observability/watermarks",
}

// ReadyPath is the readiness endpoint: it answers from the same health
// statement the API publishes, so an operator and a probe read one source.
const ReadyPath = "/readyz"

// NewReadinessHandler answers readiness with one bit and the reason for a
// negative answer. It never exposes the health statement's detail: a process
// supervisor is not an authenticated reader.
func NewReadinessHandler(projector observabilityProjector) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeJSON(writer, http.StatusMethodNotAllowed, map[string]string{"error": "METHOD_NOT_ALLOWED"})
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		view, err := projector.Health(request.Context())
		switch {
		case err != nil:
			writeJSON(writer, http.StatusServiceUnavailable, readinessResponse{
				Status: "unavailable", Reason: "the health statement could not be assembled"})
		case !view.Database.Available:
			writeJSON(writer, http.StatusServiceUnavailable, readinessResponse{
				Status: "unavailable", Reason: view.Database.Reason})
		default:
			writeJSON(writer, http.StatusOK, readinessResponse{Status: "ready"})
		}
	})
}

type readinessResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// observabilityProjector is the projection authority this handler serves.
type observabilityProjector interface {
	Health(ctx context.Context) (observability.HealthView, error)
	Watermarks(ctx context.Context, workspaceID, workItemID string) (observability.WatermarkView, error)
}

// ObservabilityHandler serves kowa.observability.v1: the process health and the
// per-work-item watermark statement. Both are read-only projections assembled
// from facts the owning services publish; neither carries a credential, a token
// or a complete user input.
type ObservabilityHandler struct {
	access    workspaceAccess
	projector observabilityProjector
	logger    *slog.Logger
	mux       *http.ServeMux
}

// NewObservabilityHandler assembles the handler over the S01 session and
// membership authorities.
func NewObservabilityHandler(
	auth identityService,
	workspaces workspaceReader,
	projector observabilityProjector,
	trustedOrigin string,
	logger *slog.Logger,
) *ObservabilityHandler {
	handler := &ObservabilityHandler{
		access:    workspaceAccess{identity: auth, workspaces: workspaces, origin: trustedOrigin},
		projector: projector, logger: logger, mux: http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET "+observabilityRoutes.health, handler.health)
	handler.mux.HandleFunc("GET "+observabilityRoutes.watermarks, handler.watermarks)
	return handler
}

func (h *ObservabilityHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(writer, request)
}

func (h *ObservabilityHandler) fail(writer http.ResponseWriter, err error) {
	if h.logger != nil {
		h.logger.Error("observability operation failed", "error", err)
	}
	writeObservabilityError(writer, err)
}

// session authenticates the caller without requiring a workspace: the process
// health statement is readable by any signed-in user, because it exposes
// versions and availability and no business fact.
func (h *ObservabilityHandler) session(writer http.ResponseWriter, request *http.Request) (identity.WebIdentity, bool) {
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		writeWebError(writer, identity.ErrUnauthorized)
		return identity.WebIdentity{}, false
	}
	webIdentity, err := h.access.identity.Authenticate(request.Context(), cookie.Value, "", false)
	if err != nil {
		writeWebError(writer, err)
		return identity.WebIdentity{}, false
	}
	return webIdentity, true
}

func (h *ObservabilityHandler) health(writer http.ResponseWriter, request *http.Request) {
	if _, ok := h.session(writer, request); !ok {
		return
	}
	view, err := h.projector.Health(request.Context())
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (h *ObservabilityHandler) watermarks(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	view, err := h.projector.Watermarks(request.Context(),
		request.PathValue("workspaceID"), request.PathValue("workItemID"))
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

// writeObservabilityError maps the projection's categories onto the frozen HTTP
// categories. Internal details never reach the response body.
func writeObservabilityError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, observability.ErrValidation):
		writeErrorWithStatus(writer, http.StatusBadRequest, "VALIDATION_ERROR")
	case errors.Is(err, observability.ErrForbidden):
		writeErrorWithStatus(writer, http.StatusForbidden, "FORBIDDEN")
	case errors.Is(err, observability.ErrNotFound):
		writeErrorWithStatus(writer, http.StatusNotFound, "RESOURCE_UNAVAILABLE")
	default:
		writeError(writer, err)
	}
}
