package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/runnertransport"
	"github.com/SnWalker/kowa/internal/runtimeops"
)

// RuntimeAuthorizer maps a Runtime credential to its bound runtimeId. A token
// that is not bound must not authenticate any Runtime.
type RuntimeAuthorizer interface {
	AuthenticateRuntime(token string) (runtimeID string, ok bool)
}

// RuntimeObservationSink records capability-ladder observations reported by
// authenticated Runtimes.
type RuntimeObservationSink interface {
	ReportObservation(context.Context, runtimeops.Observation) error
}

// TaskInputProvider resolves the frozen input bundle and referenced artifact
// content for one task. The binding producer is run admission (S05/S06); a
// provider without a binding returns ErrTaskInputUnbound so the transport can
// fail closed instead of serving unverified content.
type TaskInputProvider interface {
	TaskInput(ctx context.Context, taskID string) (runnertransport.InputManifestResponse, error)
	TaskInputArtifact(ctx context.Context, taskID, artifactID string) ([]byte, error)
}

// ErrTaskInputUnbound reports that no frozen input binding exists for the task
// yet; it maps to a retryable RESOURCE_UNAVAILABLE for the Runner.
var ErrTaskInputUnbound = errors.New("httpapi: task input binding is not available")

const maxInputArtifacts = 32

type runtimeExecution interface {
	RegisterRuntime(context.Context, execution.RuntimeRegistration) error
	LeaseTask(context.Context, execution.LeaseRequest) (execution.TaskLease, error)
	ReportProgress(context.Context, execution.ProgressReport) error
	ReportResult(context.Context, execution.ResultReport) (execution.ResultAcceptance, error)
	TaskStatus(context.Context, string) (execution.TaskStatus, error)
	RuntimeStatus(context.Context, string) (execution.RuntimeStatus, error)
	RunView(context.Context, string) (execution.RunView, error)
}

// RuntimeTransportHandler serves the S04 outbound Runner transport. The frozen
// S02 wire messages travel inside transport envelopes unchanged; HTTP routing,
// timing and credential checks belong to this layer alone.
type RuntimeTransportHandler struct {
	authorizer   RuntimeAuthorizer
	execution    runtimeExecution
	inputs       TaskInputProvider
	observations RuntimeObservationSink
	logger       *slog.Logger
	mux          *http.ServeMux

	// PollInterval is the delay between lease attempts inside one long-poll.
	PollInterval time.Duration
	// MaxWait caps the server-side long-poll window regardless of request.
	MaxWait time.Duration
}

func NewRuntimeTransportHandler(
	authorizer RuntimeAuthorizer,
	executionService runtimeExecution,
	inputs TaskInputProvider,
	observations RuntimeObservationSink,
	logger *slog.Logger,
) *RuntimeTransportHandler {
	if logger == nil {
		logger = slog.Default()
	}
	handler := &RuntimeTransportHandler{
		authorizer: authorizer, execution: executionService, inputs: inputs,
		observations: observations, logger: logger,
		mux: http.NewServeMux(), PollInterval: 250 * time.Millisecond,
		MaxWait: runnertransport.MaxWaitSeconds * time.Second,
	}
	handler.mux.HandleFunc("POST /api/v1/runtime/register", handler.register)
	handler.mux.HandleFunc("POST /api/v1/runtime/tasks/lease", handler.lease)
	handler.mux.HandleFunc("POST /api/v1/runtime/heartbeat", handler.heartbeat)
	handler.mux.HandleFunc("POST /api/v1/runtime/observations", handler.observation)
	handler.mux.HandleFunc("POST /api/v1/runtime/tasks/{taskID}/progress", handler.progress)
	handler.mux.HandleFunc("POST /api/v1/runtime/tasks/{taskID}/result", handler.result)
	handler.mux.HandleFunc("GET /api/v1/runtime/tasks/{taskID}", handler.taskStatus)
	handler.mux.HandleFunc("GET /api/v1/runtime/tasks/{taskID}/input", handler.taskInput)
	handler.mux.HandleFunc("GET /api/v1/runtime/tasks/{taskID}/input/artifacts/{artifactID}", handler.taskInputArtifact)
	return handler
}

func (h *RuntimeTransportHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	h.mux.ServeHTTP(writer, request)
}

func (h *RuntimeTransportHandler) register(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	var registration execution.RuntimeRegistration
	if err := decodeJSON(request, &registration); err != nil {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"runtime registration body is invalid")
		return
	}
	if registration.ID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"runtime credential is not bound to this runtime id")
		return
	}
	if registration.SchemaVersion != "" && registration.SchemaVersion != execution.SchemaVersion {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"unsupported schema version")
		return
	}
	if registration.Kind != "" && registration.Kind != execution.RuntimeRegistrationKind {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"unsupported registration kind")
		return
	}
	if err := h.execution.RegisterRuntime(request.Context(), registration); err != nil {
		// A same-epoch declaration conflict is an identity conflict, not a
		// superseded generation: the persisted registration stays untouched.
		if errors.Is(err, execution.ErrRuntimeConflict) {
			writeTransportError(writer, http.StatusConflict, runnertransport.ErrorVersionConflict, err.Error())
			return
		}
		h.writeExecutionError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, runnertransport.RegisterAck{
		RuntimeID: registration.ID, RuntimeEpoch: registration.Epoch,
	})
}

func (h *RuntimeTransportHandler) lease(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	var command runnertransport.LeaseCommand
	if err := decodeJSON(request, &command); err != nil {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"lease command body is invalid")
		return
	}
	if command.RuntimeID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"runtime credential is not bound to this runtime id")
		return
	}
	if command.RuntimeEpoch == "" ||
		command.LeaseSeconds < runnertransport.MinLeaseSeconds ||
		command.LeaseSeconds > runnertransport.MaxLeaseSeconds ||
		command.WaitSeconds < 0 {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"lease command is outside the frozen bounds")
		return
	}
	wait := time.Duration(command.WaitSeconds) * time.Second
	if wait > h.MaxWait {
		wait = h.MaxWait
	}
	deadline := time.Now().Add(wait)
	leaseRequest := execution.LeaseRequest{
		RuntimeID: runtimeID, RuntimeEpoch: command.RuntimeEpoch,
		LeaseDuration: time.Duration(command.LeaseSeconds) * time.Second,
	}
	for {
		lease, err := h.execution.LeaseTask(request.Context(), leaseRequest)
		if err == nil {
			writeJSON(writer, http.StatusOK, lease)
			return
		}
		if !errors.Is(err, execution.ErrCapabilityUnavailable) {
			h.writeExecutionError(writer, request, err)
			return
		}
		if !time.Now().Before(deadline) {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(h.PollInterval):
		}
	}
}

func (h *RuntimeTransportHandler) heartbeat(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	var command runnertransport.HeartbeatCommand
	if err := decodeJSON(request, &command); err != nil {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"heartbeat body is invalid")
		return
	}
	if command.RuntimeID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"runtime credential is not bound to this runtime id")
		return
	}
	registration, err := h.execution.RuntimeStatus(request.Context(), runtimeID)
	if err != nil {
		h.writeExecutionError(writer, request, err)
		return
	}
	// The persisted generation is the only one that may observe or report.
	if registration.Epoch != command.RuntimeEpoch {
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorResourceUnavailable,
			"runtime epoch is not the current dispatch generation")
		return
	}
	cancelTaskIDs := make([]string, 0, len(command.TaskIDs))
	for _, taskID := range command.TaskIDs {
		status, err := h.execution.TaskStatus(request.Context(), taskID)
		if err != nil || status.RuntimeID != runtimeID {
			continue
		}
		if status.State != execution.TaskLeased && status.State != execution.TaskRunning &&
			status.State != execution.TaskExpired {
			continue
		}
		view, err := h.execution.RunView(request.Context(), status.RunID)
		if err != nil {
			continue
		}
		if view.State == execution.RunCanceling || view.State == execution.RunCanceled {
			cancelTaskIDs = append(cancelTaskIDs, taskID)
		}
	}
	writeJSON(writer, http.StatusOK, runnertransport.HeartbeatAck{CancelTaskIDs: cancelTaskIDs})
}

// observation records one capability-ladder observation for the current epoch.
// Failure only affects projection freshness; it never fails a task.
func (h *RuntimeTransportHandler) observation(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	var command runnertransport.ObservationCommand
	if err := decodeJSON(request, &command); err != nil {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"observation body is invalid")
		return
	}
	if command.RuntimeID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"runtime credential is not bound to this runtime id")
		return
	}
	registration, err := h.execution.RuntimeStatus(request.Context(), runtimeID)
	if err != nil {
		h.writeExecutionError(writer, request, err)
		return
	}
	if registration.Epoch != command.RuntimeEpoch {
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorResourceUnavailable,
			"runtime epoch is not the current dispatch generation")
		return
	}
	if h.observations == nil {
		writeTransportError(writer, http.StatusServiceUnavailable, runnertransport.ErrorResourceUnavailable,
			"runtime observations are not configured on this control plane")
		return
	}
	err = h.observations.ReportObservation(request.Context(), runtimeops.Observation{
		RuntimeID: command.RuntimeID, RuntimeEpoch: command.RuntimeEpoch,
		ObservedAt: command.ObservedAt, Executable: command.Executable,
		CLIVersion: command.CLIVersion, VersionCompatible: command.VersionCompatible,
		Authenticated: command.Authenticated, CanExecute: command.CanExecute,
		Detail: command.Detail,
	})
	switch {
	case err == nil:
		writeJSON(writer, http.StatusOK, runnertransport.ObservationAck{Accepted: true})
	case errors.Is(err, runtimeops.ErrRequestConflict):
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorVersionConflict, err.Error())
	case errors.Is(err, runtimeops.ErrValidation):
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation, err.Error())
	default:
		h.logger.ErrorContext(request.Context(), "runtime observation rejected", "runtime", runtimeID, "error", err)
		writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
			"internal error")
	}
}

func (h *RuntimeTransportHandler) progress(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	var report execution.ProgressReport
	if err := decodeJSON(request, &report); err != nil {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"progress body is invalid")
		return
	}
	if report.TaskID != request.PathValue("taskID") {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"task id in path and body disagree")
		return
	}
	if !h.ownsTask(writer, request, runtimeID, report.TaskID) {
		return
	}
	if err := h.execution.ReportProgress(request.Context(), report); err != nil {
		h.writeExecutionError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, runnertransport.ProgressAck{Accepted: true})
}

func (h *RuntimeTransportHandler) result(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	var report execution.ResultReport
	if err := decodeJSON(request, &report); err != nil {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"result body is invalid")
		return
	}
	if report.TaskID != request.PathValue("taskID") {
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation,
			"task id in path and body disagree")
		return
	}
	if !h.ownsTask(writer, request, runtimeID, report.TaskID) {
		return
	}
	acceptance, err := h.execution.ReportResult(request.Context(), report)
	if err != nil {
		h.writeExecutionError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, runnertransport.ResultAck{
		Accepted: acceptance.Applied || acceptance.Duplicate, Duplicate: acceptance.Duplicate,
	})
}

func (h *RuntimeTransportHandler) taskStatus(writer http.ResponseWriter, request *http.Request) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return
	}
	taskID := request.PathValue("taskID")
	status, err := h.execution.TaskStatus(request.Context(), taskID)
	if err != nil {
		h.writeExecutionError(writer, request, err)
		return
	}
	if status.RuntimeID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"task is not assigned to this runtime")
		return
	}
	response := runnertransport.TaskStatusResponse{
		TaskID: status.ID, State: string(status.State), RuntimeID: status.RuntimeID,
		RuntimeEpoch: status.RuntimeEpoch, LeaseID: status.LeaseID,
		FencingToken: status.FencingToken, ResultAccepted: status.ResultAccepted,
	}
	if !status.LeaseExpiresAt.IsZero() {
		response.LeaseExpiresAt = status.LeaseExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(writer, http.StatusOK, response)
}

// taskInput serves the frozen input bundle for one task. The bundle is verified
// against the task's frozen inputDigest before it leaves the control plane, so
// the Runner never receives unverified content through this route.
func (h *RuntimeTransportHandler) taskInput(writer http.ResponseWriter, request *http.Request) {
	status, ok := h.ownedTask(writer, request)
	if !ok {
		return
	}
	manifest, err := h.resolveTaskInput(writer, request, status)
	if err != nil {
		return
	}
	writeJSON(writer, http.StatusOK, manifest)
}

// taskInputArtifact serves the raw frozen content of one artifact referenced by
// the task's manifest; unreferenced or foreign artifacts are not exposed.
func (h *RuntimeTransportHandler) taskInputArtifact(writer http.ResponseWriter, request *http.Request) {
	status, ok := h.ownedTask(writer, request)
	if !ok {
		return
	}
	manifest, err := h.resolveTaskInput(writer, request, status)
	if err != nil {
		return
	}
	artifactID := request.PathValue("artifactID")
	var reference *artifact.Ref
	for index := range manifest.Artifacts {
		if manifest.Artifacts[index].ArtifactID == artifactID {
			reference = &manifest.Artifacts[index]
			break
		}
	}
	if reference == nil {
		writeTransportError(writer, http.StatusNotFound, runnertransport.ErrorResourceUnavailable,
			"artifact is not referenced by this task")
		return
	}
	data, err := h.inputs.TaskInputArtifact(request.Context(), status.ID, artifactID)
	if err != nil {
		h.writeInputError(writer, request, err)
		return
	}
	if int64(len(data)) != reference.Size || artifact.Digest(data) != reference.Digest {
		h.logger.ErrorContext(request.Context(), "referenced artifact content does not match its frozen reference",
			"task", status.ID, "artifact", artifactID)
		writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
			"referenced artifact content does not match its frozen reference")
		return
	}
	writer.Header().Set("Content-Type", reference.MediaType)
	writer.Header().Set("Content-Length", strconv.Itoa(len(data)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(data)
}

// ownedTask authenticates the request and returns the task's minimal projection
// only when the authenticated Runtime owns it.
func (h *RuntimeTransportHandler) ownedTask(
	writer http.ResponseWriter,
	request *http.Request,
) (execution.TaskStatus, bool) {
	runtimeID, ok := h.authenticate(request)
	if !ok {
		writeTransportError(writer, http.StatusUnauthorized, runnertransport.ErrorUnauthorized,
			"runtime credential is missing or unknown")
		return execution.TaskStatus{}, false
	}
	status, err := h.execution.TaskStatus(request.Context(), request.PathValue("taskID"))
	if err != nil {
		h.writeExecutionError(writer, request, err)
		return execution.TaskStatus{}, false
	}
	if status.RuntimeID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"task is not assigned to this runtime")
		return execution.TaskStatus{}, false
	}
	return status, true
}

// resolveTaskInput loads and verifies the manifest for a task.
func (h *RuntimeTransportHandler) resolveTaskInput(
	writer http.ResponseWriter,
	request *http.Request,
	status execution.TaskStatus,
) (runnertransport.InputManifestResponse, error) {
	if h.inputs == nil {
		writeTransportError(writer, http.StatusServiceUnavailable, runnertransport.ErrorResourceUnavailable,
			"input delivery is not configured on this control plane")
		return runnertransport.InputManifestResponse{}, ErrTaskInputUnbound
	}
	manifest, err := h.inputs.TaskInput(request.Context(), status.ID)
	if err != nil {
		h.writeInputError(writer, request, err)
		return runnertransport.InputManifestResponse{}, err
	}
	if len(manifest.Artifacts) > maxInputArtifacts {
		writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
			"task input manifest exceeds the artifact bound")
		return runnertransport.InputManifestResponse{}, runnertransport.ErrProtocol
	}
	bundleDigest := artifact.Digest(manifest.Bundle)
	if manifest.BundleDigest != bundleDigest || bundleDigest != status.InputDigest {
		h.logger.ErrorContext(request.Context(), "bound input does not match the frozen task input digest",
			"task", status.ID)
		writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
			"bound input does not match the frozen task input digest")
		return runnertransport.InputManifestResponse{}, runnertransport.ErrProtocol
	}
	for _, reference := range manifest.Artifacts {
		if err := reference.Validate(); err != nil {
			writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
				"task input manifest references an invalid artifact")
			return runnertransport.InputManifestResponse{}, runnertransport.ErrProtocol
		}
	}
	return manifest, nil
}

func (h *RuntimeTransportHandler) writeInputError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, ErrTaskInputUnbound):
		writeTransportError(writer, http.StatusServiceUnavailable, runnertransport.ErrorResourceUnavailable,
			"task input binding is not available yet")
	case errors.Is(err, execution.ErrNotFound):
		writeTransportError(writer, http.StatusNotFound, runnertransport.ErrorResourceUnavailable,
			"task input is not available")
	default:
		h.logger.ErrorContext(request.Context(), "task input resolution failed",
			"path", request.URL.Path, "error", err)
		writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
			"internal error")
	}
}

// ownsTask gates progress and result reports on the authenticated Runtime's
// assignment. The domain lease identity remains the authority for acceptance.
func (h *RuntimeTransportHandler) ownsTask(
	writer http.ResponseWriter,
	request *http.Request,
	runtimeID string,
	taskID string,
) bool {
	status, err := h.execution.TaskStatus(request.Context(), taskID)
	if err != nil {
		h.writeExecutionError(writer, request, err)
		return false
	}
	if status.RuntimeID != runtimeID {
		writeTransportError(writer, http.StatusForbidden, runnertransport.ErrorForbidden,
			"task is not assigned to this runtime")
		return false
	}
	return true
}

func (h *RuntimeTransportHandler) authenticate(request *http.Request) (string, bool) {
	header := request.Header.Get("Authorization")
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found || token == "" {
		return "", false
	}
	return h.authorizer.AuthenticateRuntime(strings.TrimSpace(token))
}

func (h *RuntimeTransportHandler) writeExecutionError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, execution.ErrInvalidInput):
		writeTransportError(writer, http.StatusBadRequest, runnertransport.ErrorValidation, err.Error())
	case errors.Is(err, execution.ErrNotFound):
		writeTransportError(writer, http.StatusNotFound, runnertransport.ErrorResourceUnavailable, err.Error())
	case errors.Is(err, execution.ErrStaleLease):
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorStaleResult, err.Error())
	case errors.Is(err, execution.ErrResultConflict):
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorVersionConflict, err.Error())
	case errors.Is(err, execution.ErrVersionConflict):
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorVersionConflict, err.Error())
	case errors.Is(err, execution.ErrRuntimeConflict):
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorResourceUnavailable, err.Error())
	case errors.Is(err, execution.ErrCapabilityUnavailable):
		writeTransportError(writer, http.StatusConflict, runnertransport.ErrorResourceUnavailable, err.Error())
	default:
		h.logger.ErrorContext(request.Context(), "runtime transport request failed",
			"method", request.Method, "path", request.URL.Path, "error", err)
		writeTransportError(writer, http.StatusInternalServerError, runnertransport.ErrorInternal,
			"internal error")
	}
}

func writeTransportError(writer http.ResponseWriter, status int, category, message string) {
	writeJSON(writer, status, runnertransport.APIError{
		SchemaVersion: execution.SchemaVersion, Kind: "apiError",
		Error: category, Message: message,
	})
}
