package httpapi

import (
	"context"
	"net/http"

	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

// workItemService is the WorkItem authority this handler projects.
type workItemService interface {
	Create(ctx context.Context, actor workitem.Actor, command workitem.CreateCommand) (workitem.WorkItem, error)
	Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error)
	List(ctx context.Context, workspaceID string) ([]workitem.WorkItem, error)
	Cancel(ctx context.Context, actor workitem.Actor, command workitem.CancelCommand) (workitem.WorkItem, error)
}

// WorkItemHandler provides the kowa.workitem.v1 routes: intake, list, detail
// and cancellation. Creating a work item records a goal; it never starts a
// workflow run.
type WorkItemHandler struct {
	identity  identityService
	access    workspaceAccess
	workItems workItemService
	mux       *http.ServeMux
}

func NewWorkItemHandler(
	identityService identityService,
	workspaceService workspaceReader,
	workItems workItemService,
	trustedOrigin string,
) *WorkItemHandler {
	handler := &WorkItemHandler{
		identity:  identityService,
		access:    workspaceAccess{identity: identityService, workspaces: workspaceService, origin: trustedOrigin},
		workItems: workItems,
		mux:       http.NewServeMux(),
	}
	handler.mux.HandleFunc("POST /api/v1/workspaces/{workspaceID}/work-items", handler.create)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/work-items", handler.list)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/work-items/{workItemID}", handler.detail)
	handler.mux.HandleFunc("POST /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/cancel", handler.cancel)
	return handler
}

func (h *WorkItemHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(writer, request)
}

type createWorkItemRequest struct {
	RequestKey  string                  `json:"requestKey"`
	Title       string                  `json:"title"`
	Requirement versionedContentRequest `json:"requirement"`
	Acceptance  versionedContentRequest `json:"acceptance"`
}

type versionedContentRequest struct {
	Version int    `json:"version"`
	Content string `json:"content"`
}

func (h *WorkItemHandler) create(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.access.member(writer, request,
		[]workspace.Role{workspace.RoleAdmin, workspace.RoleDeveloper}, true)
	if !ok {
		return
	}
	var body createWorkItemRequest
	if err := decodeJSON(request, &body); err != nil {
		writeWebError(writer, workspace.ErrValidation)
		return
	}
	created, err := h.workItems.Create(request.Context(), workitem.Actor{GitHubUserID: actor.GitHubUserID},
		workitem.CreateCommand{
			WorkspaceID: request.PathValue("workspaceID"), RequestKey: body.RequestKey,
			Title: body.Title,
			Requirement: workitem.VersionedContent{
				Version: body.Requirement.Version, Content: body.Requirement.Content,
			},
			Acceptance: workitem.VersionedContent{
				Version: body.Acceptance.Version, Content: body.Acceptance.Content,
			},
		})
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, workItemResponse{
		SchemaVersion: workitem.SchemaVersion, Kind: workitem.WorkItemCreatedKind, WorkItem: created,
	})
}

func (h *WorkItemHandler) list(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	items, err := h.workItems.List(request.Context(), request.PathValue("workspaceID"))
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, workItemListResponse{
		SchemaVersion: workitem.SchemaVersion, Kind: workitem.WorkItemListKind, WorkItems: items,
	})
}

func (h *WorkItemHandler) detail(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	item, err := h.workItems.Get(request.Context(),
		request.PathValue("workspaceID"), request.PathValue("workItemID"))
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, workItemResponse{
		SchemaVersion: workitem.SchemaVersion, Kind: workitem.WorkItemCreatedKind, WorkItem: item,
	})
}

type cancelWorkItemRequest struct {
	RequestKey      string `json:"requestKey"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Reason          string `json:"reason"`
}

func (h *WorkItemHandler) cancel(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.access.member(writer, request,
		[]workspace.Role{workspace.RoleAdmin, workspace.RoleDeveloper}, true)
	if !ok {
		return
	}
	var body cancelWorkItemRequest
	if err := decodeJSON(request, &body); err != nil {
		writeWebError(writer, workspace.ErrValidation)
		return
	}
	canceled, err := h.workItems.Cancel(request.Context(), workitem.Actor{GitHubUserID: actor.GitHubUserID},
		workitem.CancelCommand{
			WorkspaceID: request.PathValue("workspaceID"), WorkItemID: request.PathValue("workItemID"),
			RequestKey: body.RequestKey, ExpectedVersion: body.ExpectedVersion, Reason: body.Reason,
		})
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, workItemResponse{
		SchemaVersion: workitem.SchemaVersion, Kind: workitem.WorkItemCreatedKind, WorkItem: canceled,
	})
}

type workItemResponse struct {
	SchemaVersion string            `json:"schemaVersion"`
	Kind          string            `json:"kind"`
	WorkItem      workitem.WorkItem `json:"workItem"`
}

type workItemListResponse struct {
	SchemaVersion string              `json:"schemaVersion"`
	Kind          string              `json:"kind"`
	WorkItems     []workitem.WorkItem `json:"workItems"`
}
