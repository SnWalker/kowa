// Package knowledge freezes verified source bytes independently of human confirmation.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
)

var (
	ErrUnavailable    = errors.New("knowledge: source unavailable")
	ErrUnconfirmed    = errors.New("knowledge: confirmation required")
	ErrValidation     = errors.New("knowledge: invalid input")
	ErrConflict       = errors.New("knowledge: version conflict")
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// Source must resolve the exact repository identity/commit and confine paths to that tree.
// Probe distinguishes a successful empty selection from an inaccessible repository.
type Source interface {
	Probe(context.Context, string, string) error
	Read(context.Context, string, string, string) ([]byte, error)
}
type Entry struct {
	Path     string `json:"path"`
	Digest   string `json:"digest"`
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
	Content  []byte `json:"content"`
}
type Selection struct {
	WorkspaceID, RepositoryID, Commit string
	Entries                           []Entry
}
type Content struct {
	SchemaVersion string  `json:"schemaVersion"`
	WorkspaceID   string  `json:"workspaceId"`
	RepositoryID  string  `json:"repositoryId"`
	Commit        string  `json:"commit"`
	EmptyResult   bool    `json:"emptyResult"`
	Entries       []Entry `json:"entries"`
}
type Snapshot struct {
	ID        string  `json:"snapshotId"`
	Digest    string  `json:"digest"`
	Content   Content `json:"content"`
	Canonical []byte  `json:"-"`
}

func Capture(ctx context.Context, source Source, selection Selection) (Snapshot, error) {
	if !artifact.ValidID(selection.WorkspaceID) || !repositoryPattern.MatchString(selection.RepositoryID) || !commitPattern.MatchString(selection.Commit) || selection.Entries == nil {
		return Snapshot{}, ErrValidation
	}
	if err := source.Probe(ctx, selection.RepositoryID, selection.Commit); err != nil {
		return Snapshot{}, fmt.Errorf("probe knowledge: %w", errors.Join(ErrUnavailable, err))
	}
	entries := make([]Entry, 0, len(selection.Entries))
	seen := map[string]bool{}
	total := 0
	for _, entry := range selection.Entries {
		if !validPath(entry.Path) || seen[entry.Path] || entry.Scope == "" || !artifact.ValidDigest(entry.Digest) {
			return Snapshot{}, ErrValidation
		}
		seen[entry.Path] = true
		data, err := source.Read(ctx, selection.RepositoryID, selection.Commit, entry.Path)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read knowledge entry: %w", errors.Join(ErrUnavailable, err))
		}
		total += len(data)
		if total > artifact.MaxSize {
			return Snapshot{}, ErrValidation
		}
		if artifact.Digest(data) != entry.Digest {
			return Snapshot{}, artifact.ErrIntegrity
		}
		entry.Content = append([]byte{}, data...)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	content := Content{SchemaVersion: artifact.SchemaVersion, WorkspaceID: selection.WorkspaceID, RepositoryID: selection.RepositoryID, Commit: selection.Commit, EmptyResult: len(entries) == 0, Entries: entries}
	canonical, err := json.Marshal(content)
	if err != nil {
		return Snapshot{}, err
	}
	if len(canonical) > artifact.MaxSize {
		return Snapshot{}, ErrValidation
	}
	digest := artifact.Digest(canonical)
	return Snapshot{ID: "ks_" + strings.TrimPrefix(digest, "sha256:"), Digest: digest, Content: content, Canonical: canonical}, nil
}
func validPath(p string) bool {
	return fs.ValidPath(p) && p != "." && !strings.ContainsAny(p, "\\\x00:")
}

// ValidateSnapshot verifies exact stored canonical bytes, never an arbitrary JSONB serialization.
func ValidateSnapshot(s Snapshot) error {
	canonical, err := json.Marshal(s.Content)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, s.Canonical) || len(canonical) > artifact.MaxSize || artifact.Digest(canonical) != s.Digest || s.ID != "ks_"+strings.TrimPrefix(s.Digest, "sha256:") {
		return artifact.ErrIntegrity
	}
	c := s.Content
	if c.SchemaVersion != artifact.SchemaVersion || !artifact.ValidID(c.WorkspaceID) || !repositoryPattern.MatchString(c.RepositoryID) || !commitPattern.MatchString(c.Commit) || c.Entries == nil || c.EmptyResult != (len(c.Entries) == 0) {
		return ErrValidation
	}
	last := ""
	for _, e := range c.Entries {
		if !validPath(e.Path) || e.Path <= last || e.Scope == "" || artifact.Digest(e.Content) != e.Digest {
			return artifact.ErrIntegrity
		}
		last = e.Path
	}
	return nil
}

type BindingEvent struct {
	ID              string    `json:"eventId"`
	WorkspaceID     string    `json:"workspaceId"`
	SubjectID       string    `json:"subjectId"`
	SnapshotID      string    `json:"snapshotId"`
	HumanResponseID string    `json:"humanResponseId"`
	PreviousEventID string    `json:"previousEventId"`
	ActorID         string    `json:"actorId"`
	Version         int64     `json:"version"`
	ConfirmedAt     time.Time `json:"confirmedAt"`
}
type ConfirmCommand struct {
	SubjectID, HumanResponseID string
	ExpectedVersion            int64
	Snapshot                   Snapshot
}
type Upstream struct {
	Name      string       `json:"name"`
	NodeRunID string       `json:"nodeRunId"`
	Ref       artifact.Ref `json:"ref"`
}
type Field struct {
	Name  string          `json:"name"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}
type InputRequest struct {
	// RequiredUpstream comes from the frozen definition, never from provider fields.
	RequiredUpstream   []string   `json:"-"`
	WorkspaceID        string     `json:"workspaceId"`
	SubjectID          string     `json:"subjectId"`
	RequirementVersion string     `json:"requirementVersion"`
	ProjectCommit      string     `json:"projectCommit"`
	DefinitionDigest   string     `json:"definitionDigest"`
	PolicyVersion      string     `json:"policyVersion"`
	ConfigVersion      string     `json:"configVersion"`
	Upstream           []Upstream `json:"upstream"`
	Fields             []Field    `json:"fields"`
}
type InputContent struct {
	SchemaVersion string `json:"schemaVersion"`
	InputRequest
	SnapshotID      string `json:"snapshotId"`
	BindingEventID  string `json:"bindingEventId"`
	HumanResponseID string `json:"humanResponseId"`
}
type EffectiveInput struct {
	ID        string       `json:"inputId"`
	Digest    string       `json:"digest"`
	Content   InputContent `json:"content"`
	Canonical []byte       `json:"-"`
}

// Assemble accepts a persisted confirmation, not a prefilled field or a readable candidate.
// The Store must check event provenance/current binding before committing this input.
func Assemble(req InputRequest, snapshot Snapshot, event BindingEvent) (EffectiveInput, error) {
	if event.ID == "" || event.HumanResponseID == "" || event.Version < 1 || event.ConfirmedAt.IsZero() || event.SnapshotID != snapshot.ID {
		return EffectiveInput{}, ErrUnconfirmed
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		return EffectiveInput{}, err
	}
	if event.WorkspaceID != req.WorkspaceID || event.SubjectID != req.SubjectID || snapshot.Content.WorkspaceID != req.WorkspaceID {
		return EffectiveInput{}, artifact.ErrForbidden
	}
	if !artifact.ValidID(req.SubjectID) || req.RequirementVersion == "" || !commitPattern.MatchString(req.ProjectCommit) || !artifact.ValidDigest(req.DefinitionDigest) || req.PolicyVersion == "" || req.ConfigVersion == "" {
		return EffectiveInput{}, ErrValidation
	}
	req.Upstream = append([]Upstream{}, req.Upstream...)
	req.Fields = append([]Field{}, req.Fields...)
	seen := map[string]bool{}
	for _, u := range req.Upstream {
		if !artifact.ValidID(u.Name) || !artifact.ValidID(u.NodeRunID) || seen[u.Name] {
			return EffectiveInput{}, ErrValidation
		}
		seen[u.Name] = true
		if err := u.Ref.Validate(); err != nil {
			return EffectiveInput{}, err
		}
		if u.Ref.WorkspaceID != req.WorkspaceID {
			return EffectiveInput{}, artifact.ErrForbidden
		}
	}
	required := map[string]bool{}
	for _, name := range req.RequiredUpstream {
		if !artifact.ValidID(name) || required[name] || !seen[name] {
			return EffectiveInput{}, ErrValidation
		}
		required[name] = true
	}
	reserved := map[string]bool{"workspaceId": true, "subjectId": true, "requirementVersion": true, "projectCommit": true, "definitionDigest": true, "policyVersion": true, "configVersion": true, "snapshotId": true, "bindingEventId": true, "humanResponseId": true, "schemaVersion": true}
	for i, f := range req.Fields {
		if !artifact.ValidID(f.Name) || seen[f.Name] || reserved[f.Name] {
			return EffectiveInput{}, ErrValidation
		}
		seen[f.Name] = true
		var v any
		if err := json.Unmarshal(f.Value, &v); err != nil {
			return EffectiveInput{}, ErrValidation
		}
		valid := false
		switch f.Type {
		case "string":
			_, valid = v.(string)
		case "boolean":
			_, valid = v.(bool)
		case "number":
			_, valid = v.(float64)
		}
		if !valid {
			return EffectiveInput{}, ErrValidation
		}
		value, err := json.Marshal(v)
		if err != nil {
			return EffectiveInput{}, err
		}
		req.Fields[i].Value = value
	}
	sort.Slice(req.Upstream, func(i, j int) bool { return req.Upstream[i].Name < req.Upstream[j].Name })
	sort.Slice(req.Fields, func(i, j int) bool { return req.Fields[i].Name < req.Fields[j].Name })
	c := InputContent{SchemaVersion: artifact.SchemaVersion, InputRequest: req, SnapshotID: snapshot.ID, BindingEventID: event.ID, HumanResponseID: event.HumanResponseID}
	canonical, err := json.Marshal(c)
	if err != nil {
		return EffectiveInput{}, err
	}
	if len(canonical) > artifact.MaxSize {
		return EffectiveInput{}, ErrValidation
	}
	digest := artifact.Digest(canonical)
	return EffectiveInput{ID: "ei_" + strings.TrimPrefix(digest, "sha256:"), Digest: digest, Content: c, Canonical: canonical}, nil
}
