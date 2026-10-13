// Package runnertransport owns the S04 runner transport envelopes and the strict
// codec shared by the control-plane handlers and the Runner client. The frozen
// kowa.workflow-execution.v1 messages travel inside these envelopes unchanged;
// this package never reinterprets the S02 wire.
package runnertransport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/SnWalker/kowa/internal/artifact"
)

const (
	// MaxBody bounds every transport request and response body.
	MaxBody = 1 << 20
	// MaxWaitSeconds is the server-side clamp for one long-poll call.
	MaxWaitSeconds = 50
	// MinWaitSeconds and MaxLeaseSeconds bound the lease request parameters.
	MinLeaseSeconds = 30
	MaxLeaseSeconds = 86400
)

// LeaseCommand is the long-poll lease request body.
type LeaseCommand struct {
	RuntimeID    string `json:"runtimeId"`
	RuntimeEpoch string `json:"runtimeEpoch"`
	WaitSeconds  int    `json:"waitSeconds"`
	LeaseSeconds int    `json:"leaseSeconds"`
}

// HeartbeatCommand is a liveness observation. It never renews a lease.
type HeartbeatCommand struct {
	RuntimeID    string   `json:"runtimeId"`
	RuntimeEpoch string   `json:"runtimeEpoch"`
	TaskIDs      []string `json:"taskIds"`
}

// HeartbeatAck carries cancellation directives for tasks the Runtime still
// claims: the server only directs tasks whose run is CANCELING or CANCELED and
// whose attempt has not settled.
type HeartbeatAck struct {
	CancelTaskIDs []string `json:"cancelTaskIds"`
}

// RegisterAck confirms the authenticated registration generation.
type RegisterAck struct {
	RuntimeID    string `json:"runtimeId"`
	RuntimeEpoch string `json:"runtimeEpoch"`
}

// ResultAck reports transport-level acceptance of a task result.
type ResultAck struct {
	Accepted  bool `json:"accepted"`
	Duplicate bool `json:"duplicate"`
}

// ProgressAck reports transport-level acceptance of a progress observation.
type ProgressAck struct {
	Accepted bool `json:"accepted"`
}

// TaskStatusResponse is the minimal read-only projection used for restart
// investigation. It intentionally omits scopes, outputs and business content.
type TaskStatusResponse struct {
	TaskID         string `json:"taskId"`
	State          string `json:"state"`
	RuntimeID      string `json:"runtimeId,omitempty"`
	RuntimeEpoch   string `json:"runtimeEpoch,omitempty"`
	LeaseID        string `json:"leaseId,omitempty"`
	FencingToken   int64  `json:"fencingToken,omitempty"`
	LeaseExpiresAt string `json:"leaseExpiresAt,omitempty"`
	ResultAccepted bool   `json:"resultAccepted"`
}

// MaxInputBody bounds the frozen-input manifest response; it is larger than
// ordinary transport bodies because the bundle itself travels inside it.
const MaxInputBody = 32 << 20

// InputManifestResponse is the frozen input bundle for one task plus the
// ArtifactRef list the task may consume. The bundle bytes are the exact
// canonical content whose digest is the task's frozen inputDigest.
type InputManifestResponse struct {
	Bundle       []byte         `json:"bundle"`
	BundleDigest string         `json:"bundleDigest"`
	Artifacts    []artifact.Ref `json:"artifacts"`
}

// APIError mirrors the frozen v1 apiError kind for transport responses.
type APIError struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Error         string `json:"error"`
	Message       string `json:"message"`
}

const (
	ErrorValidation          = "VALIDATION_ERROR"
	ErrorUnauthorized        = "UNAUTHORIZED"
	ErrorForbidden           = "FORBIDDEN"
	ErrorVersionConflict     = "VERSION_CONFLICT"
	ErrorResourceUnavailable = "RESOURCE_UNAVAILABLE"
	ErrorStaleResult         = "STALE_RESULT"
	ErrorExternalUnknown     = "EXTERNAL_UNKNOWN"
	ErrorPolicyBlocked       = "POLICY_BLOCKED"
	ErrorInternal            = "INTERNAL_ERROR"
)

// EncodeJSON serializes a transport envelope.
func EncodeJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode transport body: %w", err)
	}
	return encoded, nil
}

// DecodeStrict rejects unknown fields, missing values that the caller must
// supply and trailing data after the JSON value.
func DecodeStrict(data []byte, target any) error {
	if len(data) > MaxBody {
		return errors.New("transport: body too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode transport body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("transport: trailing data after JSON value")
	}
	return nil
}
