package httpapi

import (
	"context"
	"net/http"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

// humanTaskService is the HumanTask authority this handler projects.
type humanTaskService interface {
	List(ctx context.Context, workspaceID string, openOnly bool) ([]humantask.HumanTask, error)
	Get(ctx context.Context, workspaceID, taskID string) (humantask.HumanTask, error)
	Answer(ctx context.Context, actor humantask.Actor, command humantask.AnswerCommand) (humantask.HumanTask, error)
}

// deliveryProjectionService reads the delivery facts and the git remote
// projection; both are read-only views of already-produced facts.
type deliveryProjectionService interface {
	DeliveryView(ctx context.Context, workspaceID, workItemID string) (githubdelivery.DeliveryView, error)
	Status(ctx context.Context, command gitremote.StatusCommand) (gitremote.Status, error)
}

// DeliveryHandler provides the kowa.delivery-governance.v1 routes: the human
// task surface, the delivery evidence projection and the git remote status.
type DeliveryHandler struct {
	identity identityService
	access   workspaceAccess
	humans   humanTaskService
	delivery deliveryProjectionService
	mux      *http.ServeMux
}

func NewDeliveryHandler(
	identityService identityService,
	workspaceService workspaceReader,
	humans humanTaskService,
	delivery deliveryProjectionService,
	trustedOrigin string,
) *DeliveryHandler {
	handler := &DeliveryHandler{
		identity: identityService,
		access:   workspaceAccess{identity: identityService, workspaces: workspaceService, origin: trustedOrigin},
		humans:   humans, delivery: delivery,
		mux: http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/human-tasks", handler.listHumanTasks)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/human-tasks/{humanTaskID}", handler.humanTaskDetail)
	handler.mux.HandleFunc("POST /api/v1/workspaces/{workspaceID}/human-tasks/{humanTaskID}/responses", handler.answerHumanTask)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/delivery", handler.deliveryView)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}/git-remote-status", handler.gitRemoteStatus)
	return handler
}

func (h *DeliveryHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(writer, request)
}

type humanTaskWire struct {
	humantask.HumanTask
	AvailableActions []string `json:"availableActions"`
}

func humanTaskResponse(task humantask.HumanTask, actorID string, roles []roleName) humanTaskWire {
	return humanTaskWire{HumanTask: task, AvailableActions: availableActions(task, actorID, roles)}
}

// availableActions is the server-side decision of what the current member may
// do with this request: the browser never decides action permission.
func availableActions(task humantask.HumanTask, actorID string, roles []roleName) []string {
	if task.State != humantask.StateOpen {
		return []string{}
	}
	if task.Request.Mode == humantask.ModeExternalFact {
		return []string{"await-external-fact"}
	}
	typedRoles := make([]humantask.Role, 0, len(roles))
	for _, role := range roles {
		typedRoles = append(typedRoles, humantask.Role(role))
	}
	if humantask.Assigned(task.Request, actorID, typedRoles) {
		return []string{"respond"}
	}
	return []string{}
}

func (h *DeliveryHandler) listHumanTasks(writer http.ResponseWriter, request *http.Request) {
	actor, current, ok := h.access.member(writer, request, nil, false)
	if !ok {
		return
	}
	tasks, err := h.humans.List(request.Context(), request.PathValue("workspaceID"),
		request.URL.Query().Get("state") == "open")
	if err != nil {
		writeWebError(writer, err)
		return
	}
	roles := currentRoles(current, actor.GitHubUserID)
	wire := make([]humanTaskWire, 0, len(tasks))
	for _, task := range tasks {
		wire = append(wire, humanTaskResponse(task, actor.GitHubUserID, roles))
	}
	writeJSON(writer, http.StatusOK, humanTaskListResponse{
		SchemaVersion: humantask.SchemaVersion, Kind: humantask.HumanTaskListKind, HumanTasks: wire,
	})
}

func (h *DeliveryHandler) humanTaskDetail(writer http.ResponseWriter, request *http.Request) {
	actor, current, ok := h.access.member(writer, request, nil, false)
	if !ok {
		return
	}
	task, err := h.humans.Get(request.Context(),
		request.PathValue("workspaceID"), request.PathValue("humanTaskID"))
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, humanTaskDetailResponse{
		SchemaVersion: humantask.SchemaVersion, Kind: humantask.HumanTaskKind,
		HumanTask: humanTaskResponse(task, actor.GitHubUserID, currentRoles(current, actor.GitHubUserID)),
	})
}

type answerHumanTaskRequest struct {
	RequestKey     string                  `json:"requestKey"`
	RequestVersion int                     `json:"requestVersion"`
	Object         humantask.ObjectBinding `json:"object"`
	Decision       string                  `json:"decision"`
	Values         map[string]string       `json:"values"`
}

func (h *DeliveryHandler) answerHumanTask(writer http.ResponseWriter, request *http.Request) {
	actor, current, ok := h.access.member(writer, request, nil, true)
	if !ok {
		return
	}
	var body answerHumanTaskRequest
	if err := decodeJSON(request, &body); err != nil {
		writeWebError(writer, workspace.ErrValidation)
		return
	}
	typedRoles := make([]humantask.Role, 0)
	for _, role := range currentRoles(current, actor.GitHubUserID) {
		typedRoles = append(typedRoles, humantask.Role(role))
	}
	resolved, err := h.humans.Answer(request.Context(),
		humantask.Actor{GitHubUserID: actor.GitHubUserID, Roles: typedRoles},
		humantask.AnswerCommand{
			WorkspaceID: request.PathValue("workspaceID"), TaskID: request.PathValue("humanTaskID"),
			RequestKey: body.RequestKey, RequestVersion: body.RequestVersion,
			Object: body.Object, Decision: body.Decision, Values: body.Values,
		})
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, humanTaskDetailResponse{
		SchemaVersion: humantask.SchemaVersion, Kind: humantask.HumanTaskKind,
		HumanTask: humanTaskResponse(resolved, actor.GitHubUserID, currentRoles(current, actor.GitHubUserID)),
	})
}

func (h *DeliveryHandler) deliveryView(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := h.access.member(writer, request, nil, false); !ok {
		return
	}
	view, err := h.delivery.DeliveryView(request.Context(),
		request.PathValue("workspaceID"), request.PathValue("workItemID"))
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, deliveryViewResponse{
		SchemaVersion: githubdelivery.SchemaVersion, Kind: githubdelivery.DeliveryViewKind,
		WorkItem: view.WorkItem, RunBindings: view.Bindings, Operations: view.Operations,
		Facts: view.Facts, Verification: view.Verification, Closure: view.Closure,
	})
}

func (h *DeliveryHandler) gitRemoteStatus(writer http.ResponseWriter, request *http.Request) {
	actor, current, ok := h.access.member(writer, request, nil, false)
	if !ok {
		return
	}
	repositories := make([]gitremote.RepositoryRef, 0, 2)
	if current.Project != nil {
		repositories = append(repositories, gitremote.RepositoryRef{
			RepositoryID: current.Project.GitHubRepositoryID, Role: gitremote.RoleProject,
		})
	}
	if current.Knowledge != nil {
		repositories = append(repositories, gitremote.RepositoryRef{
			RepositoryID: current.Knowledge.GitHubRepositoryID, Role: gitremote.RoleKnowledge,
		})
	}
	status, err := h.delivery.Status(request.Context(), gitremote.StatusCommand{
		WorkspaceID: request.PathValue("workspaceID"), RequestedByGitHubUserID: actor.GitHubUserID,
		Repositories: repositories,
	})
	if err != nil {
		writeWebError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, gitRemoteStatusResponse{
		SchemaVersion: gitremote.SchemaVersion, Kind: gitremote.GitRemoteStatusKind, Status: status,
	})
}

type humanTaskDetailResponse struct {
	SchemaVersion string        `json:"schemaVersion"`
	Kind          string        `json:"kind"`
	HumanTask     humanTaskWire `json:"humanTask"`
}

type humanTaskListResponse struct {
	SchemaVersion string          `json:"schemaVersion"`
	Kind          string          `json:"kind"`
	HumanTasks    []humanTaskWire `json:"humanTasks"`
}

type deliveryViewResponse struct {
	SchemaVersion string                             `json:"schemaVersion"`
	Kind          string                             `json:"kind"`
	WorkItem      workitem.WorkItem                  `json:"workItem"`
	RunBindings   []workitem.RunBinding              `json:"runBindings"`
	Operations    []githubdelivery.ExternalOperation `json:"operations"`
	Facts         []githubdelivery.Observation       `json:"facts"`
	Verification  []evaluation.Verification          `json:"verification"`
	Closure       *evaluation.ClosureRecord          `json:"closure,omitempty"`
}

type gitRemoteStatusResponse struct {
	SchemaVersion string           `json:"schemaVersion"`
	Kind          string           `json:"kind"`
	Status        gitremote.Status `json:"status"`
}
