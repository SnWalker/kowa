// Package artifact owns immutable publication, verified reads and reference protection.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"
)

const SchemaVersion = "kowa.knowledge-artifact.v1"
const MaxSize = 16 << 20 // The local v1 adapter deliberately bounds each object to 16 MiB.

var (
	ErrValidation  = errors.New("artifact: invalid input")
	ErrForbidden   = errors.New("artifact: forbidden")
	ErrUnavailable = errors.New("artifact: unavailable")
	ErrUnpublished = errors.New("artifact: unpublished")
	ErrIntegrity   = errors.New("artifact: integrity mismatch")
	ErrReferenced  = errors.New("artifact: still referenced")
	ErrGracePeriod = errors.New("artifact: grace period has not elapsed")
	ErrConflict    = errors.New("artifact: identity conflict")
	ErrUnapproved  = errors.New("artifact: approval required")
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	idPattern      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,199}$`)
)

func Digest(data []byte) string {
	d := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(d[:])
}
func ValidDigest(s string) bool { return digestPattern.MatchString(s) }
func ValidID(s string) bool     { return idPattern.MatchString(s) }

// Scope is supplied by an authenticated control-plane caller, never decoded from wire JSON.
type Scope struct{ WorkspaceID, ActorID string }
type Producer struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Ref struct {
	SchemaVersion    string   `json:"schemaVersion"`
	ArtifactID       string   `json:"artifactId"`
	WorkspaceID      string   `json:"workspaceId"`
	SubjectID        string   `json:"subjectId"`
	Kind             string   `json:"kind"`
	Producer         Producer `json:"producer"`
	Digest           string   `json:"digest"`
	Size             int64    `json:"size"`
	MediaType        string   `json:"mediaType"`
	StorageRef       string   `json:"storageRef"`
	RequiresApproval bool     `json:"requiresApproval"`
}

func (r Ref) Validate() error {
	if r.SchemaVersion != SchemaVersion || !objectID.MatchString(r.ArtifactID) || !ValidID(r.WorkspaceID) || !ValidID(r.SubjectID) || !ValidID(r.Kind) || !ValidID(r.Producer.ID) || (r.Producer.Kind != "operation" && r.Producer.Kind != "task") || !ValidDigest(r.Digest) || r.Size < 0 || r.Size > MaxSize || r.MediaType == "" || r.StorageRef != "artifact:"+r.ArtifactID {
		return ErrValidation
	}
	return nil
}

type Reference struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (r Reference) Validate() error {
	if !ValidID(r.ID) || (r.Kind != "run" && r.Kind != "closure" && r.Kind != "input" && r.Kind != "snapshot" && r.Kind != "approval") {
		return ErrValidation
	}
	return nil
}

type State string

const (
	Staging   State = "STAGING"
	Uploaded  State = "UPLOADED"
	Published State = "PUBLISHED"
	Deleting  State = "DELETING"
	Deleted   State = "DELETED"
)

type Record struct {
	Ref        Ref
	State      State
	CreatedAt  time.Time
	References []Reference
}

// Catalog serializes the callback with all publication/reference/cleanup operations
// in this Workspace. It persists changes only on success and rechecks membership.
// Task publications must additionally be tied to an accepted, immutable TaskResult.
type Catalog interface {
	WithArtifact(context.Context, Scope, string, *Record, bool, func(*Record) error) error
}

// BlobStore addresses objects only by server-generated opaque IDs, never producer paths.
// Put is create-only and durable before returning; Remove is idempotent.
type BlobStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Remove(context.Context, string) error
}

// Approved separates readable publication from permission to consume a candidate.
func (r Record) Approved() bool {
	if !r.Ref.RequiresApproval {
		return true
	}
	for _, ref := range r.References {
		if ref.Kind == "approval" {
			return true
		}
	}
	return false
}
