package httpapi

import (
	"errors"
	"net/http"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

// workspaceAccess authenticates the S01 session, re-reads the current Workspace
// membership and applies the frozen error categories. Membership, never the
// request body, decides the actor; a revoked member is refused on the next
// request.
type workspaceAccess struct {
	identity   identityService
	workspaces workspaceReader
	origin     string
}

// member authenticates and authorizes one request. An empty roles list accepts
// any current member; otherwise the caller must hold one of the roles.
func (a workspaceAccess) member(
	writer http.ResponseWriter,
	request *http.Request,
	roles []workspace.Role,
	requireCSRF bool,
) (workspace.Actor, workspace.Workspace, bool) {
	if requireCSRF && (a.origin == "" || request.Header.Get("Origin") != a.origin) {
		writeWebError(writer, identity.ErrUnauthorized)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		writeWebError(writer, identity.ErrUnauthorized)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	webIdentity, err := a.identity.Authenticate(
		request.Context(), cookie.Value, request.Header.Get("X-CSRF-Token"), requireCSRF,
	)
	if err != nil {
		writeWebError(writer, err)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	actor := workspace.Actor{GitHubUserID: webIdentity.GitHubUserID}
	current, err := a.workspaces.Get(request.Context(), actor, request.PathValue("workspaceID"))
	if err != nil {
		writeWebError(writer, workspace.ErrForbidden)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	if len(roles) > 0 && !memberHoldsRole(current, actor.GitHubUserID, roles) {
		writeWebError(writer, workspace.ErrForbidden)
		return workspace.Actor{}, workspace.Workspace{}, false
	}
	return actor, current, true
}

func memberHoldsRole(current workspace.Workspace, githubUserID string, roles []workspace.Role) bool {
	for _, member := range current.Members {
		if member.GitHubUserID != githubUserID || member.RevokedAt != nil {
			continue
		}
		for _, role := range roles {
			if member.Role == role {
				return true
			}
		}
	}
	return false
}

// currentRoles returns the active roles of one member, used for the frozen
// human-task assignment rule.
func currentRoles(current workspace.Workspace, githubUserID string) []roleName {
	roles := []roleName{}
	for _, member := range current.Members {
		if member.GitHubUserID == githubUserID && member.RevokedAt == nil {
			roles = append(roles, roleName(member.Role))
		}
	}
	return roles
}

// roleName mirrors workspace.Role for the human-task assignment check without
// exposing the Workspace package through the delivery handler.
type roleName = string

// writeWebError maps the frozen business error categories onto the HTTP
// surface for the S05 contract families. Internal details never reach the
// response body.
func writeWebError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	category := "INTERNAL_ERROR"
	switch {
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, workspace.ErrUnauthorized):
		status, category = http.StatusUnauthorized, "UNAUTHORIZED"
	case errors.Is(err, workspace.ErrForbidden), errors.Is(err, workitem.ErrForbidden),
		errors.Is(err, humantask.ErrForbidden), errors.Is(err, githubdelivery.ErrForbidden),
		errors.Is(err, evaluation.ErrForbidden), errors.Is(err, gitremote.ErrForbidden),
		errors.Is(err, orchestration.ErrForbidden):
		status, category = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, workspace.ErrValidation), errors.Is(err, workitem.ErrValidation),
		errors.Is(err, humantask.ErrValidation), errors.Is(err, githubdelivery.ErrValidation),
		errors.Is(err, evaluation.ErrValidation), errors.Is(err, gitremote.ErrValidation),
		errors.Is(err, orchestration.ErrValidation):
		status, category = http.StatusBadRequest, "VALIDATION_ERROR"
	case errors.Is(err, workspace.ErrVersionConflict), errors.Is(err, identity.ErrConflict),
		errors.Is(err, workitem.ErrVersionConflict), errors.Is(err, humantask.ErrVersionConflict),
		errors.Is(err, githubdelivery.ErrVersionConflict), errors.Is(err, evaluation.ErrVersionConflict),
		errors.Is(err, orchestration.ErrVersionConflict), errors.Is(err, execution.ErrVersionConflict):
		status, category = http.StatusConflict, "VERSION_CONFLICT"
	case errors.Is(err, workitem.ErrResourceUnavailable), errors.Is(err, workspace.ErrResourceUnavailable),
		errors.Is(err, identity.ErrResourceUnavailable), errors.Is(err, orchestration.ErrResourceUnavailable),
		errors.Is(err, execution.ErrCapabilityUnavailable), errors.Is(err, execution.ErrRuntimeConflict):
		status, category = http.StatusServiceUnavailable, "RESOURCE_UNAVAILABLE"
	case errors.Is(err, githubdelivery.ErrExternalUnknown), errors.Is(err, workitem.ErrExternalUnknown):
		status, category = http.StatusConflict, "EXTERNAL_UNKNOWN"
	case errors.Is(err, workspace.ErrPolicyBlocked), errors.Is(err, workitem.ErrPolicyBlocked),
		errors.Is(err, humantask.ErrPolicyBlocked), errors.Is(err, githubdelivery.ErrPolicyBlocked),
		errors.Is(err, evaluation.ErrPolicyBlocked), errors.Is(err, orchestration.ErrPolicyBlocked),
		errors.Is(err, execution.ErrPolicyBlocked), errors.Is(err, execution.ErrRetryNotAllowed):
		status, category = http.StatusUnprocessableEntity, "POLICY_BLOCKED"
	case errors.Is(err, workitem.ErrNotFound), errors.Is(err, humantask.ErrNotFound),
		errors.Is(err, githubdelivery.ErrNotFound), errors.Is(err, evaluation.ErrNotFound),
		errors.Is(err, orchestration.ErrNotFound), errors.Is(err, execution.ErrNotFound):
		status, category = http.StatusNotFound, "RESOURCE_UNAVAILABLE"
	}
	writeJSON(writer, status, struct {
		Error string `json:"error"`
	}{Error: category})
}
