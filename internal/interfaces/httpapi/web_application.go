package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"log/slog"

	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

// webApplicationRoutes matches the kowa.web-application.v1 path family.
var webApplicationRoutes = struct {
	workspaces string
	fixedFlow  string
	runs       string
	recovery   string
	artifacts  string
	journey    string
}{
	workspaces: "/api/v1/workspaces",
	fixedFlow:  "/api/v1/workspaces/{workspaceID}/fixed-workflow",
	runs:       "/api/v1/workspaces/{workspaceID}/work-items/{workItemID}/runs",
	recovery:   "/api/v1/workspaces/{workspaceID}/work-items/{workItemID}/runs/{runID}/recovery",
	artifacts:  "/api/v1/workspaces/{workspaceID}/work-items/{workItemID}/artifacts",
	journey:    "/api/v1/workspaces/{workspaceID}/work-items/{workItemID}/journey",
}

// webApplicationTargets is the composite application surface this handler
// serves. Every method re-checks authorization in the owning service.
type webApplicationTargets interface {
	Plan() (fixedflow.View, error)
	Admit(ctx context.Context, command orchestration.AdmitCommand) (orchestration.AdmissionResult, error)
	RunDetail(ctx context.Context, workspaceID, workItemID, runID string) (orchestration.RunDetailView, error)
	Recover(ctx context.Context, command orchestration.RecoveryCommand) (orchestration.RunRecovery, error)
	Artifacts(ctx context.Context, workspaceID, workItemID string) (orchestration.ArtifactListView, error)
	Journey(ctx context.Context, workspaceID, workItemID string) (orchestration.JourneyView, error)
}

// workspaceDirectory lists the workspaces one authenticated member belongs to.
// The S01 membership tables stay the authority; this handler never assembles a
// list from local state.
type workspaceDirectory interface {
	WorkspacesForMember(ctx context.Context, githubUserID string) ([]workspace.Workspace, error)
}

// WebApplicationHandler serves the fixed workflow entry and the composite
// delivery reads of kowa.web-application.v1.
type WebApplicationHandler struct {
	access    workspaceAccess
	directory workspaceDirectory
	targets   webApplicationTargets
	logger    *slog.Logger
	mux       *http.ServeMux
}

// fail maps one error to the frozen category and logs what the response cannot
// expose: the internal detail of an unexpected failure.
func (h *WebApplicationHandler) fail(writer http.ResponseWriter, err error) {
	if h.logger != nil {
		h.logger.Error("web application operation failed", "error", err)
	}
	writeWebError(writer, err)
}

// NewWebApplicationHandler assembles the handler over the S01 session and
// membership authorities.
func NewWebApplicationHandler(
	auth identityService,
	workspaces workspaceReader,
	directory workspaceDirectory,
	targets webApplicationTargets,
	trustedOrigin string,
	logger *slog.Logger,
) *WebApplicationHandler {
	if logger == nil {
		logger = slog.Default()
	}
	handler := &WebApplicationHandler{
		access:    workspaceAccess{identity: auth, workspaces: workspaces, origin: trustedOrigin},
		directory: directory, targets: targets, logger: logger, mux: http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET /api/v1/workspaces", handler.listWorkspaces)
	handler.mux.HandleFunc("GET "+webApplicationRoutes.fixedFlow, handler.fixedWorkflow)
	handler.mux.HandleFunc("POST "+webApplicationRoutes.runs, handler.startRun)
	handler.mux.HandleFunc("GET "+webApplicationRoutes.runs+"/{runID}", handler.runDetail)
	handler.mux.HandleFunc("POST "+webApplicationRoutes.recovery, handler.runRecovery)
	handler.mux.HandleFunc("GET "+webApplicationRoutes.artifacts, handler.artifactList)
	handler.mux.HandleFunc("GET "+webApplicationRoutes.journey, handler.journey)
	return handler
}

// ServeHTTP routes only the frozen web-application operations.
func (h *WebApplicationHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
}

// listWorkspaces answers the workspace list of the current session identity.
func (h *WebApplicationHandler) listWorkspaces(writer http.ResponseWriter, request *http.Request) {
	if h.directory == nil {
		writeWebError(writer, workspace.ErrResourceUnavailable)
		return
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		writeWebError(writer, workspace.ErrUnauthorized)
		return
	}
	webIdentity, err := h.access.identity.Authenticate(request.Context(), cookie.Value,
		request.Header.Get("X-CSRF-Token"), false)
	if err != nil {
		writeWebError(writer, err)
		return
	}
	workspaces, err := h.directory.WorkspacesForMember(request.Context(), webIdentity.GitHubUserID)
	if err != nil {
		writeWebError(writer, err)
		return
	}
	type entry struct {
		WorkspaceID string   `json:"workspaceId"`
		Name        string   `json:"name"`
		Roles       []string `json:"roles"`
		Version     int64    `json:"version"`
	}
	entries := make([]entry, 0, len(workspaces))
	for _, current := range workspaces {
		roles := []string{}
		for _, member := range current.Members {
			if member.GitHubUserID == webIdentity.GitHubUserID && member.RevokedAt == nil {
				roles = append(roles, string(member.Role))
			}
		}
		if len(roles) == 0 {
			continue
		}
		entries = append(entries, entry{
			WorkspaceID: current.ID, Name: current.Name, Roles: roles, Version: current.Version,
		})
	}
	writeJSON(writer, http.StatusOK, struct {
		SchemaVersion string  `json:"schemaVersion"`
		Kind          string  `json:"kind"`
		Workspaces    []entry `json:"workspaces"`
	}{SchemaVersion: orchestration.SchemaVersion, Kind: "workspaceList", Workspaces: entries})
}

// fixedWorkflow serves the frozen definition the browser renders.
func (h *WebApplicationHandler) fixedWorkflow(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	view, err := h.targets.Plan()
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

type startRunRequest struct {
	RequestKey      string `json:"requestKey"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

// startRun admits the fixed workflow of one WorkItem.
func (h *WebApplicationHandler) startRun(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.access.member(writer, request, writerRoles(), true)
	if !ok {
		return
	}
	var body startRunRequest
	if !decodeWebBody(writer, request, &body) {
		return
	}
	result, err := h.targets.Admit(request.Context(), orchestration.AdmitCommand{
		WorkspaceID: request.PathValue("workspaceID"), WorkItemID: request.PathValue("workItemID"),
		ActorID: actor.GitHubUserID, RequestKey: body.RequestKey, ExpectedVersion: body.ExpectedVersion,
		Kind: workitem.BindingInitial,
	})
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

// runDetail serves the composite run history of one run.
func (h *WebApplicationHandler) runDetail(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	detail, err := h.targets.RunDetail(request.Context(), request.PathValue("workspaceID"),
		request.PathValue("workItemID"), request.PathValue("runID"))
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

type recoveryRequest struct {
	Kind            string `json:"kind"`
	NodeID          string `json:"nodeId"`
	Reason          string `json:"reason"`
	ExpectedVersion int64  `json:"expectedVersion"`
	RequestKey      string `json:"requestKey"`
}

// runRecovery applies one authorized recovery command.
func (h *WebApplicationHandler) runRecovery(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.access.member(writer, request, writerRoles(), true)
	if !ok {
		return
	}
	var body recoveryRequest
	if !decodeWebBody(writer, request, &body) {
		return
	}
	recovery, err := h.targets.Recover(request.Context(), orchestration.RecoveryCommand{
		WorkspaceID: request.PathValue("workspaceID"), WorkItemID: request.PathValue("workItemID"),
		RunID: request.PathValue("runID"), Kind: orchestration.RecoveryKind(body.Kind),
		NodeID: body.NodeID, Reason: body.Reason, ExpectedVersion: body.ExpectedVersion,
		RequestKey: body.RequestKey, ActorID: actor.GitHubUserID,
	})
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, recovery)
}

// artifactList serves the published deliverables and the repair lineage.
func (h *WebApplicationHandler) artifactList(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	view, err := h.targets.Artifacts(request.Context(), request.PathValue("workspaceID"),
		request.PathValue("workItemID"))
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

// journey serves the composite journey of one work item.
func (h *WebApplicationHandler) journey(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	view, err := h.targets.Journey(request.Context(), request.PathValue("workspaceID"),
		request.PathValue("workItemID"))
	if err != nil {
		h.fail(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

// writerRoles are the workspace roles allowed to change a delivery.
func writerRoles() []workspace.Role {
	return []workspace.Role{workspace.RoleAdmin, workspace.RoleDeveloper}
}

// decodeWebBody strictly decodes one command body; unknown fields are refused
// so a drifted consumer cannot silently send a field the server ignores.
func decodeWebBody(writer http.ResponseWriter, request *http.Request, target any) bool {
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		writeWebError(writer, workspace.ErrValidation)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeWebError(writer, workspace.ErrValidation)
		return false
	}
	if decoder.More() {
		writeWebError(writer, workspace.ErrValidation)
		return false
	}
	return true
}
