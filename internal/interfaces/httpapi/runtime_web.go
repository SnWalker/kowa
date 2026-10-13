package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workspace"
)

// runtimeWebService is the runtime-operations authority this handler projects.
type runtimeWebService interface {
	WorkspaceRuntimes(context.Context, string) ([]runtimeops.RuntimeView, error)
	WorkspaceRuntime(context.Context, string, string) (runtimeops.RuntimeView, error)
	ExecutionConfig(context.Context, string) (runtimeops.ExecutionConfig, error)
	UpdateExecutionConfig(context.Context, runtimeops.UpdateExecutionConfigCommand) (runtimeops.ExecutionConfig, error)
}

// workspaceReader is the S01 membership authority; every request re-reads it so
// a revoked membership is refused immediately.
type workspaceReader interface {
	Get(context.Context, workspace.Actor, string) (workspace.Workspace, error)
}

// RuntimeWebHandler provides kowa.runtime-web.v1 routes: the authorized runtime
// projection and the admin execution configuration. Runtime self-reports never
// grant workspace visibility; only the frozen execution configuration does.
type RuntimeWebHandler struct {
	identity   identityService
	workspaces workspaceReader
	runtimes   runtimeWebService
	origin     string
	mux        *http.ServeMux
}

func NewRuntimeWebHandler(
	identityService identityService,
	workspaceService workspaceReader,
	runtimeService runtimeWebService,
	trustedOrigin string,
) *RuntimeWebHandler {
	handler := &RuntimeWebHandler{
		identity: identityService, workspaces: workspaceService,
		runtimes: runtimeService, origin: trustedOrigin, mux: http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/runtimes", handler.listRuntimes)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/runtimes/{runtimeID}", handler.getRuntime)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/execution-config", handler.getExecutionConfig)
	handler.mux.HandleFunc("PUT /api/v1/workspaces/{workspaceID}/execution-config", handler.putExecutionConfig)
	return handler
}

func (h *RuntimeWebHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(writer, request)
}

// member authenticates the session and re-reads current membership per
// request; a revoked member is refused on the next call.
func (h *RuntimeWebHandler) member(
	writer http.ResponseWriter,
	request *http.Request,
	requireAdmin bool,
	requireCSRF bool,
) (workspace.Actor, workspace.Workspace, bool) {
	if requireCSRF && !h.trustedOrigin(request) {
		writeError(writer, identity.ErrUnauthorized)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		writeError(writer, identity.ErrUnauthorized)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	webIdentity, err := h.identity.Authenticate(
		request.Context(), cookie.Value, request.Header.Get("X-CSRF-Token"), requireCSRF,
	)
	if err != nil {
		writeError(writer, err)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	actor := workspace.Actor{GitHubUserID: webIdentity.GitHubUserID}
	current, err := h.workspaces.Get(request.Context(), actor, request.PathValue("workspaceID"))
	if err != nil {
		writeError(writer, workspace.ErrForbidden)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	if requireAdmin {
		admin := false
		for _, member := range current.Members {
			if member.GitHubUserID == actor.GitHubUserID && member.Role == workspace.RoleAdmin && member.RevokedAt == nil {
				admin = true
				break
			}
		}
		if !admin {
			writeError(writer, workspace.ErrForbidden)
			return workspace.Actor{}, workspace.Workspace{}, false
		}
	}
	return actor, current, true
}

func (h *RuntimeWebHandler) listRuntimes(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.member(writer, request, false, false); !ok {
		return
	}
	views, err := h.runtimes.WorkspaceRuntimes(request.Context(), request.PathValue("workspaceID"))
	if err != nil {
		writeRuntimeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, runtimeListResponse{
		SchemaVersion: runtimeops.SchemaVersion, Kind: "runtimeList", Runtimes: views,
	})
}

func (h *RuntimeWebHandler) getRuntime(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.member(writer, request, false, false); !ok {
		return
	}
	view, err := h.runtimes.WorkspaceRuntime(
		request.Context(), request.PathValue("workspaceID"), request.PathValue("runtimeID"))
	if err != nil {
		writeRuntimeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, runtimeViewResponse{
		SchemaVersion: runtimeops.SchemaVersion, Kind: "runtimeView", Runtime: view,
	})
}

func (h *RuntimeWebHandler) getExecutionConfig(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.member(writer, request, false, false); !ok {
		return
	}
	config, err := h.runtimes.ExecutionConfig(request.Context(), request.PathValue("workspaceID"))
	if err != nil {
		writeRuntimeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, runtimeConfigResponse{
		SchemaVersion: runtimeops.SchemaVersion, Kind: "executionConfig", Config: config,
	})
}

type updateExecutionConfigRequest struct {
	RequestKey      string                  `json:"requestKey"`
	ExpectedVersion int64                   `json:"expectedVersion"`
	Capabilities    []runtimeops.Capability `json:"capabilities"`
	Runtimes        []string                `json:"runtimes"`
}

func (h *RuntimeWebHandler) putExecutionConfig(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.member(writer, request, true, true)
	if !ok {
		return
	}
	var body updateExecutionConfigRequest
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, workspace.ErrValidation)
		return
	}
	config, err := h.runtimes.UpdateExecutionConfig(request.Context(), runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: request.PathValue("workspaceID"), ActorID: actor.GitHubUserID,
		RequestKey: body.RequestKey, ExpectedVersion: body.ExpectedVersion,
		Capabilities: body.Capabilities, Runtimes: body.Runtimes,
	})
	if err != nil {
		writeRuntimeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, runtimeConfigResponse{
		SchemaVersion: runtimeops.SchemaVersion, Kind: "executionConfig", Config: config,
	})
}

func (h *RuntimeWebHandler) trustedOrigin(request *http.Request) bool {
	return request.Header.Get("Origin") == h.origin
}

type runtimeListResponse struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Kind          string                   `json:"kind"`
	Runtimes      []runtimeops.RuntimeView `json:"runtimes"`
}

type runtimeViewResponse struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Kind          string                 `json:"kind"`
	Runtime       runtimeops.RuntimeView `json:"runtime"`
}

type runtimeConfigResponse struct {
	SchemaVersion string                     `json:"schemaVersion"`
	Kind          string                     `json:"kind"`
	Config        runtimeops.ExecutionConfig `json:"config"`
}

// writeRuntimeWebError maps runtime-operations failures onto the frozen error
// categories. Internal details never reach the response body.
func writeRuntimeWebError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtimeops.ErrValidation):
		writeErrorWithStatus(writer, http.StatusBadRequest, "VALIDATION_ERROR")
	case errors.Is(err, runtimeops.ErrForbidden):
		writeErrorWithStatus(writer, http.StatusForbidden, "FORBIDDEN")
	case errors.Is(err, runtimeops.ErrNotFound):
		writeErrorWithStatus(writer, http.StatusNotFound, "RESOURCE_UNAVAILABLE")
	case errors.Is(err, runtimeops.ErrVersionConflict):
		writeErrorWithStatus(writer, http.StatusConflict, "VERSION_CONFLICT")
	case errors.Is(err, runtimeops.ErrRequestConflict):
		writeErrorWithStatus(writer, http.StatusConflict, "VERSION_CONFLICT")
	default:
		writeErrorWithStatus(writer, http.StatusInternalServerError, "INTERNAL_ERROR")
	}
}

func writeErrorWithStatus(writer http.ResponseWriter, status int, category string) {
	writeJSON(writer, status, struct {
		Error string `json:"error"`
	}{Error: category})
}
