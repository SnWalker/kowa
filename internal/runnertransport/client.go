package runnertransport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
)

// Client errors classify every failure for the Runner loop. Unauthorized,
// Forbidden and Superseded are fatal for the process; Stale and Conflict settle
// one attempt; everything else is transient and retried with backoff.
var (
	ErrUnauthorized = errors.New("runtime transport: unauthorized")
	ErrForbidden    = errors.New("runtime transport: forbidden")
	ErrSuperseded   = errors.New("runtime transport: runtime epoch is not current")
	ErrStale        = errors.New("runtime transport: stale lease")
	ErrConflict     = errors.New("runtime transport: identity conflict")
	ErrUnavailable  = errors.New("runtime transport: resource unavailable")
	ErrProtocol     = errors.New("runtime transport: protocol violation")
	ErrServer       = errors.New("runtime transport: server error")
)

// Client is the Runner's outbound HTTP client. The Runtime credential is only
// ever sent in the Authorization header and is never logged or serialized.
type Client struct {
	BaseURL     string
	RuntimeID   string
	Credentials string
	// Epoch is the Runner process epoch; it is set once at registration and
	// reused for the life of the process.
	Epoch string
	HTTP  *http.Client
}

func NewClient(baseURL, runtimeID, credentials string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"), RuntimeID: runtimeID, Credentials: credentials,
		HTTP: &http.Client{Timeout: 90 * time.Second},
	}
}

// Register submits the frozen v1 RuntimeRegistration for the authenticated
// Runtime; the body runtimeId must equal the credential's binding.
func (c *Client) Register(ctx context.Context, registration execution.RuntimeRegistration) (RegisterAck, error) {
	registration.SchemaVersion = execution.SchemaVersion
	registration.Kind = execution.RuntimeRegistrationKind
	if registration.ID != c.RuntimeID {
		return RegisterAck{}, fmt.Errorf("%w: registration runtime id mismatch", ErrProtocol)
	}
	var ack RegisterAck
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/runtime/register", registration, &ack); err != nil {
		return RegisterAck{}, err
	}
	if ack.RuntimeID != registration.ID || ack.RuntimeEpoch != registration.Epoch {
		return RegisterAck{}, fmt.Errorf("%w: registration ack does not confirm the submitted epoch", ErrProtocol)
	}
	return ack, nil
}

// Lease performs one long-poll lease call. found is false on an empty poll.
func (c *Client) Lease(ctx context.Context, waitSeconds, leaseSeconds int) (execution.TaskLease, bool, error) {
	command := LeaseCommand{
		RuntimeID: c.RuntimeID, RuntimeEpoch: c.Epoch, WaitSeconds: waitSeconds, LeaseSeconds: leaseSeconds,
	}
	var lease execution.TaskLease
	found, err := c.doJSONAllowEmpty(ctx, http.MethodPost, "/api/v1/runtime/tasks/lease", command, &lease)
	if err != nil || !found {
		return execution.TaskLease{}, found, err
	}
	if lease.SchemaVersion != execution.SchemaVersion || lease.Kind != execution.TaskLeaseKind ||
		lease.RuntimeEpoch != command.RuntimeEpoch || lease.Task.ID == "" ||
		lease.LeaseID == "" || lease.FencingToken < 1 {
		return execution.TaskLease{}, false, fmt.Errorf("%w: malformed task lease", ErrProtocol)
	}
	return lease, true, nil
}

// Heartbeat reports liveness for the given in-flight tasks and returns the
// server's cancellation directives. It never renews a lease.
func (c *Client) Heartbeat(ctx context.Context, taskIDs []string) (HeartbeatAck, error) {
	command := HeartbeatCommand{RuntimeID: c.RuntimeID, RuntimeEpoch: c.Epoch, TaskIDs: taskIDs}
	if command.TaskIDs == nil {
		command.TaskIDs = []string{}
	}
	var ack HeartbeatAck
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/runtime/heartbeat", command, &ack); err != nil {
		return HeartbeatAck{}, err
	}
	return ack, nil
}

// ReportProgress submits a frozen v1 ProgressEvent observation.
func (c *Client) ReportProgress(ctx context.Context, report execution.ProgressReport) error {
	report.SchemaVersion = execution.SchemaVersion
	report.Kind = execution.ProgressEventKind
	var ack ProgressAck
	if err := c.doJSON(ctx, http.MethodPost, taskRoute(report.TaskID, "progress"), report, &ack); err != nil {
		return err
	}
	if !ack.Accepted {
		return fmt.Errorf("%w: progress was not accepted", ErrProtocol)
	}
	return nil
}

// ReportResult submits a frozen v1 TaskResult and returns transport acceptance.
func (c *Client) ReportResult(ctx context.Context, report execution.ResultReport) (ResultAck, error) {
	report.SchemaVersion = execution.SchemaVersion
	report.Kind = execution.TaskResultKind
	payload, err := EncodeJSON(report)
	if err != nil {
		return ResultAck{}, err
	}
	return c.ReportResultPayload(ctx, report.TaskID, payload)
}

// ReportResultPayload submits a pre-serialized frozen v1 TaskResult. The Runner
// journals the exact payload bytes and reuses them verbatim on retry so the
// result digest identity stays stable across restarts.
func (c *Client) ReportResultPayload(ctx context.Context, taskID string, payload []byte) (ResultAck, error) {
	var ack ResultAck
	if _, err := c.doDecoded(ctx, http.MethodPost, taskRoute(taskID, "result"), payload, &ack); err != nil {
		return ResultAck{}, err
	}
	if !ack.Accepted && !ack.Duplicate {
		return ResultAck{}, fmt.Errorf("%w: result was not accepted", ErrProtocol)
	}
	return ack, nil
}

// TaskStatus reads the minimal attempt projection for restart investigation.
func (c *Client) TaskStatus(ctx context.Context, taskID string) (TaskStatusResponse, error) {
	var status TaskStatusResponse
	if err := c.doJSON(ctx, http.MethodGet, taskRoute(taskID, ""), nil, &status); err != nil {
		return TaskStatusResponse{}, err
	}
	if status.TaskID != taskID || status.State == "" {
		return TaskStatusResponse{}, fmt.Errorf("%w: malformed task status", ErrProtocol)
	}
	return status, nil
}

// FetchInput reads the frozen input manifest for a task. The caller must
// re-verify the bundle digest against the task's frozen inputDigest.
func (c *Client) FetchInput(ctx context.Context, taskID string) (InputManifestResponse, error) {
	data, found, err := c.do(ctx, http.MethodGet, taskRoute(taskID, "input"), nil, MaxInputBody)
	if err != nil {
		return InputManifestResponse{}, err
	}
	if !found {
		return InputManifestResponse{}, fmt.Errorf("%w: empty input manifest", ErrProtocol)
	}
	var manifest InputManifestResponse
	if err := DecodeStrict(data, &manifest); err != nil {
		return InputManifestResponse{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	if manifest.BundleDigest == "" {
		return InputManifestResponse{}, fmt.Errorf("%w: input manifest has no bundle digest", ErrProtocol)
	}
	return manifest, nil
}

// FetchInputArtifact reads the raw frozen content of one referenced artifact.
func (c *Client) FetchInputArtifact(ctx context.Context, taskID, artifactID string) ([]byte, error) {
	data, found, err := c.do(ctx, http.MethodGet,
		taskRoute(taskID, "input/artifacts/"+url.PathEscape(artifactID)), nil, artifact.MaxSize)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: empty artifact body", ErrProtocol)
	}
	return data, nil
}

func taskRoute(taskID, suffix string) string {
	route := "/api/v1/runtime/tasks/" + url.PathEscape(taskID)
	if suffix != "" {
		route += "/" + suffix
	}
	return route
}

func (c *Client) doJSON(ctx context.Context, method, route string, body, target any) error {
	found, err := c.doJSONAllowEmpty(ctx, method, route, body, target)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: empty response from %s", ErrProtocol, route)
	}
	return nil
}

func (c *Client) doJSONAllowEmpty(ctx context.Context, method, route string, body, target any) (bool, error) {
	var payload []byte
	if body != nil {
		encoded, err := EncodeJSON(body)
		if err != nil {
			return false, err
		}
		payload = encoded
	}
	return c.doDecoded(ctx, method, route, payload, target)
}

// do performs one authenticated transport call with a size limit. A nil
// payload sends no body; a 204 response reports found=false.
func (c *Client) do(ctx context.Context, method, route string, payload []byte, limit int64) ([]byte, bool, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+route, reader)
	if err != nil {
		return nil, false, fmt.Errorf("build runtime request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Credentials)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, false, fmt.Errorf("runtime request %s: %w", route, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNoContent {
		return nil, false, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, false, fmt.Errorf("read runtime response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, false, fmt.Errorf("%w: response exceeds %d bytes", ErrProtocol, limit)
	}
	if response.StatusCode != http.StatusOK {
		return nil, false, classifyError(response.StatusCode, data)
	}
	return data, true, nil
}

// doDecoded performs a JSON call and strictly decodes the response body.
func (c *Client) doDecoded(ctx context.Context, method, route string, payload []byte, target any) (bool, error) {
	data, found, err := c.do(ctx, method, route, payload, MaxBody)
	if err != nil || !found {
		return found, err
	}
	if target == nil {
		return true, nil
	}
	if err := DecodeStrict(data, target); err != nil {
		return false, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return true, nil
}

// classifyError maps an error response onto the client taxonomy. The body must
// be a frozen v1 APIError; anything else is a protocol violation.
func classifyError(status int, data []byte) error {
	var apiError APIError
	if err := DecodeStrict(data, &apiError); err != nil ||
		apiError.SchemaVersion != execution.SchemaVersion || apiError.Kind != "apiError" ||
		apiError.Error == "" || apiError.Message == "" {
		return fmt.Errorf("%w: unexpected status %d body", ErrProtocol, status)
	}
	message := apiError.Message
	switch apiError.Error {
	case ErrorUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	case ErrorForbidden:
		return fmt.Errorf("%w: %s", ErrForbidden, message)
	case ErrorStaleResult:
		return fmt.Errorf("%w: %s", ErrStale, message)
	case ErrorVersionConflict:
		return fmt.Errorf("%w: %s", ErrConflict, message)
	case ErrorResourceUnavailable:
		if status == http.StatusConflict {
			return fmt.Errorf("%w: %s", ErrSuperseded, message)
		}
		return fmt.Errorf("%w: %s", ErrUnavailable, message)
	case ErrorValidation:
		return fmt.Errorf("%w: %s", ErrProtocol, message)
	default:
		if status >= 500 {
			return fmt.Errorf("%w: %s", ErrServer, message)
		}
		return fmt.Errorf("%w: %s %s", ErrProtocol, apiError.Error, message)
	}
}
