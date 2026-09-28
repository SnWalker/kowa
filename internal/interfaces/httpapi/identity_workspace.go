// Package httpapi exposes authenticated Identity and Workspace HTTP seams.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/workspace"
)

const (
	oauthStateCookie = "__Host-kowa-oauth-state"
	sessionCookie    = "__Host-kowa-session"
	maxRequestBody   = 1 << 20
)

type identityService interface {
	BeginLogin(context.Context, string) (identity.BeginLoginResult, error)
	CompleteLogin(context.Context, identity.CompleteLoginCommand) (identity.CompleteLoginResult, error)
	Authenticate(context.Context, string, string, bool) (identity.WebIdentity, error)
	Logout(context.Context, string) error
}

type workspaceService interface {
	Bootstrap(context.Context, workspace.Actor, workspace.BootstrapCommand) (workspace.Workspace, error)
	Get(context.Context, workspace.Actor, string) (workspace.Workspace, error)
	ConfigureRepositories(
		context.Context,
		workspace.Actor,
		workspace.ConfigureRepositoriesCommand,
	) (workspace.Workspace, error)
	UpsertMember(context.Context, workspace.Actor, workspace.UpsertMemberCommand) error
	RevokeMember(context.Context, workspace.Actor, workspace.RevokeMemberCommand) error
}

// IdentityWorkspaceHandler provides kowa.identity-workspace.v1 routes.
type IdentityWorkspaceHandler struct {
	identity  identityService
	workspace workspaceService
	origin    string
	mux       *http.ServeMux
}

func NewIdentityWorkspaceHandler(
	identityService identityService,
	workspaceService workspaceService,
	trustedOrigin string,
) *IdentityWorkspaceHandler {
	handler := &IdentityWorkspaceHandler{
		identity:  identityService,
		workspace: workspaceService,
		origin:    trustedOrigin,
		mux:       http.NewServeMux(),
	}
	handler.mux.HandleFunc("POST /api/v1/auth/github/login", handler.beginLogin)
	handler.mux.HandleFunc("GET /api/v1/auth/github/callback", handler.completeLogin)
	handler.mux.HandleFunc("POST /api/v1/auth/logout", handler.logout)
	handler.mux.HandleFunc("POST /api/v1/workspaces", handler.bootstrapWorkspace)
	handler.mux.HandleFunc("GET /api/v1/workspaces/{workspaceID}", handler.getWorkspace)
	handler.mux.HandleFunc("PUT /api/v1/workspaces/{workspaceID}/repositories", handler.configureRepositories)
	handler.mux.HandleFunc("PUT /api/v1/workspaces/{workspaceID}/members/{githubUserID}", handler.upsertMember)
	handler.mux.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/members/{githubUserID}", handler.revokeMember)
	return handler
}

func (h *IdentityWorkspaceHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
}

func (h *IdentityWorkspaceHandler) beginLogin(writer http.ResponseWriter, request *http.Request) {
	if !h.trustedOrigin(request) {
		writeError(writer, identity.ErrUnauthorized)
		return
	}
	var body struct {
		ReturnTo string `json:"returnTo"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, workspace.ErrValidation)
		return
	}
	result, err := h.identity.BeginLogin(request.Context(), body.ReturnTo)
	if err != nil {
		writeError(writer, err)
		return
	}
	http.SetCookie(writer, secureCookie(oauthStateCookie, result.State, time.Now().Add(10*time.Minute)))
	writeJSON(writer, http.StatusOK, map[string]string{"authorizationUrl": result.AuthorizationURL})
}

func (h *IdentityWorkspaceHandler) completeLogin(writer http.ResponseWriter, request *http.Request) {
	stateCookie, err := request.Cookie(oauthStateCookie)
	if err != nil {
		writeError(writer, identity.ErrUnauthorized)
		return
	}
	result, err := h.identity.CompleteLogin(request.Context(), identity.CompleteLoginCommand{
		State:       request.URL.Query().Get("state"),
		StateCookie: stateCookie.Value,
		Code:        request.URL.Query().Get("code"),
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	http.SetCookie(writer, secureCookie(sessionCookie, result.SessionToken, result.ExpiresAt))
	http.SetCookie(writer, expiredCookie(oauthStateCookie))
	writeJSON(writer, http.StatusOK, map[string]any{
		"identity":  result.Identity,
		"csrfToken": result.CSRFToken,
		"returnTo":  result.ReturnTo,
	})
}

func (h *IdentityWorkspaceHandler) logout(writer http.ResponseWriter, request *http.Request) {
	_, sessionToken, ok := h.authenticate(writer, request, true)
	if !ok {
		return
	}
	if err := h.identity.Logout(request.Context(), sessionToken); err != nil {
		writeError(writer, err)
		return
	}
	http.SetCookie(writer, expiredCookie(sessionCookie))
	writer.WriteHeader(http.StatusNoContent)
}

func (h *IdentityWorkspaceHandler) bootstrapWorkspace(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.authenticate(writer, request, true)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, workspace.ErrValidation)
		return
	}
	result, err := h.workspace.Bootstrap(request.Context(), actor, workspace.BootstrapCommand{
		Name:       body.Name,
		RequestKey: request.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (h *IdentityWorkspaceHandler) getWorkspace(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.authenticate(writer, request, false)
	if !ok {
		return
	}
	result, err := h.workspace.Get(request.Context(), actor, request.PathValue("workspaceID"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (h *IdentityWorkspaceHandler) configureRepositories(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.authenticate(writer, request, true)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64                    `json:"expectedVersion"`
		Project         repositoryBindingRequest `json:"project"`
		Knowledge       repositoryBindingRequest `json:"knowledge"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, workspace.ErrValidation)
		return
	}
	result, err := h.workspace.ConfigureRepositories(request.Context(), actor, workspace.ConfigureRepositoriesCommand{
		WorkspaceID:     request.PathValue("workspaceID"),
		ExpectedVersion: body.ExpectedVersion,
		RequestKey:      request.Header.Get("Idempotency-Key"),
		Project:         body.Project.input(),
		Knowledge:       body.Knowledge.input(),
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (h *IdentityWorkspaceHandler) revokeMember(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.authenticate(writer, request, true)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, workspace.ErrValidation)
		return
	}
	err := h.workspace.RevokeMember(request.Context(), actor, workspace.RevokeMemberCommand{
		WorkspaceID:     request.PathValue("workspaceID"),
		ExpectedVersion: body.ExpectedVersion,
		RequestKey:      request.Header.Get("Idempotency-Key"),
		GitHubUserID:    request.PathValue("githubUserID"),
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (h *IdentityWorkspaceHandler) upsertMember(writer http.ResponseWriter, request *http.Request) {
	actor, _, ok := h.authenticate(writer, request, true)
	if !ok {
		return
	}
	var body struct {
		ExpectedVersion int64          `json:"expectedVersion"`
		Role            workspace.Role `json:"role"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, workspace.ErrValidation)
		return
	}
	err := h.workspace.UpsertMember(request.Context(), actor, workspace.UpsertMemberCommand{
		WorkspaceID:     request.PathValue("workspaceID"),
		ExpectedVersion: body.ExpectedVersion,
		RequestKey:      request.Header.Get("Idempotency-Key"),
		GitHubUserID:    request.PathValue("githubUserID"),
		Role:            body.Role,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

type repositoryBindingRequest struct {
	GitHubRepositoryID string `json:"githubRepositoryId"`
	InstallationID     string `json:"installationId"`
	DisplayName        string `json:"displayName"`
}

func (r repositoryBindingRequest) input() workspace.RepositoryInput {
	return workspace.RepositoryInput{
		GitHubRepositoryID: r.GitHubRepositoryID,
		InstallationID:     r.InstallationID,
		DisplayName:        r.DisplayName,
	}
}

func (h *IdentityWorkspaceHandler) authenticate(
	writer http.ResponseWriter,
	request *http.Request,
	requireCSRF bool,
) (workspace.Actor, string, bool) {
	if requireCSRF && !h.trustedOrigin(request) {
		writeError(writer, identity.ErrUnauthorized)
		return workspace.Actor{}, "", false
	}
	cookie, err := request.Cookie(sessionCookie)
	if err != nil {
		writeError(writer, identity.ErrUnauthorized)
		return workspace.Actor{}, "", false
	}
	webIdentity, err := h.identity.Authenticate(
		request.Context(),
		cookie.Value,
		request.Header.Get("X-CSRF-Token"),
		requireCSRF,
	)
	if err != nil {
		writeError(writer, err)
		return workspace.Actor{}, "", false
	}
	return workspace.Actor{GitHubUserID: webIdentity.GitHubUserID}, cookie.Value, true
}

func (h *IdentityWorkspaceHandler) trustedOrigin(request *http.Request) bool {
	return h.origin != "" && request.Header.Get("Origin") == h.origin
}

func decodeJSON(request *http.Request, target any) (err error) {
	defer func() {
		err = errors.Join(err, request.Body.Close())
	}()
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(target); decodeErr != nil {
		return decodeErr
	}
	if decodeErr := decoder.Decode(&struct{}{}); !errors.Is(decodeErr, io.EOF) {
		return errors.New("request body contains trailing data")
	}
	return nil
}

func secureCookie(name, value string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func expiredCookie(name string) *http.Cookie {
	cookie := secureCookie(name, "", time.Unix(1, 0))
	cookie.MaxAge = -1
	return cookie
}

func writeError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	category := "INTERNAL_ERROR"
	switch {
	case errors.Is(err, identity.ErrUnauthorized), errors.Is(err, workspace.ErrUnauthorized):
		status = http.StatusUnauthorized
		category = "UNAUTHORIZED"
	case errors.Is(err, workspace.ErrForbidden):
		status = http.StatusForbidden
		category = "FORBIDDEN"
	case errors.Is(err, workspace.ErrValidation):
		status = http.StatusBadRequest
		category = "VALIDATION_ERROR"
	case errors.Is(err, workspace.ErrVersionConflict), errors.Is(err, identity.ErrConflict):
		status = http.StatusConflict
		category = "VERSION_CONFLICT"
	case errors.Is(err, identity.ErrResourceUnavailable), errors.Is(err, workspace.ErrResourceUnavailable):
		status = http.StatusServiceUnavailable
		category = "RESOURCE_UNAVAILABLE"
	case errors.Is(err, workspace.ErrPolicyBlocked):
		status = http.StatusUnprocessableEntity
		category = "POLICY_BLOCKED"
	}
	writeJSON(writer, status, map[string]string{"error": category})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
