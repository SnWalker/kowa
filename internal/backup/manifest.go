// Package backup owns the shape and the integrity rule of a local Kowa backup.
//
// A backup is a directory tree plus one manifest that names what it holds and
// what each part hashed to when it was taken. The manifest is the only thing a
// restore is allowed to trust: the restore refuses to run when a component no
// longer matches, so a partially copied or tampered backup can never be applied
// as if it were whole.
//
// The manifest is a product file, not a state authority: it describes a copy of
// PostgreSQL, the artifact store and the Runner state, and never replaces any of
// them.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/observability"
)

// SchemaVersion is the frozen identity of a backup manifest.
const SchemaVersion = "kowa.backup.v1"

// ManifestFile is the manifest's fixed name inside a backup directory.
const ManifestFile = "manifest.json"

var (
	// ErrValidation marks a manifest that cannot be admitted.
	ErrValidation = errors.New("backup: invalid manifest")
	// ErrDigestMismatch marks a backup whose content no longer matches its
	// manifest. A restore must refuse rather than proceed.
	ErrDigestMismatch = errors.New("backup: digest mismatch")
	// ErrUnsupportedLayout marks a directory layout the frozen rule cannot name.
	ErrUnsupportedLayout = errors.New("backup: unsupported layout")
)

// ComponentKind names one part of a backup. The frozen directory layout maps one
// top-level directory to one kind.
type ComponentKind string

const (
	ComponentPostgresDump  ComponentKind = "postgres_dump"
	ComponentArtifactStore ComponentKind = "artifact_store"
	ComponentRunnerState   ComponentKind = "runner_state"
	ComponentConfig        ComponentKind = "config_snapshot"
)

// layout maps the frozen directory names to their kinds. A directory that is not
// in this table fails the build instead of being silently left out of the
// manifest.
var layout = map[string]ComponentKind{
	"postgres":     ComponentPostgresDump,
	"artifacts":    ComponentArtifactStore,
	"runner-state": ComponentRunnerState,
	"config":       ComponentConfig,
}

// Component is one part of a backup: where it lives inside the backup, how many
// files it holds, and the digest those files hashed to.
type Component struct {
	Kind    ComponentKind `json:"kind"`
	Locator string        `json:"locator"`
	Digest  string        `json:"sha256"`
	Bytes   int64         `json:"bytes"`
	Entries int           `json:"entries"`
}

// Manifest describes one backup. Every free-text field is scrubbed on the way
// in: a backup manifest is copied around by operators and shown in evidence.
type Manifest struct {
	SchemaVersion    string            `json:"schemaVersion"`
	BackupID         string            `json:"backupId"`
	CreatedAt        time.Time         `json:"createdAt"`
	ConsistencyPoint string            `json:"consistencyPoint"`
	Tools            map[string]string `json:"toolVersions"`
	Components       []Component       `json:"components"`
	RestoreNotes     []string          `json:"restoreNotes"`
	ManifestDigest   string            `json:"manifestDigest"`
}

// BuildOptions carries the facts the manifest records about how the backup was
// taken. None of them are inferred: a caller that does not know the consistency
// point says so, and the build refuses.
type BuildOptions struct {
	BackupID         string
	CreatedAt        time.Time
	ConsistencyPoint string
	Tools            map[string]string
	RestoreNotes     []string
}

var (
	digestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	locatorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Build walks a backup directory, hashes every component and writes
// manifest.json next to them.
func Build(directory string, options BuildOptions) (Manifest, error) {
	if !idPattern.MatchString(options.BackupID) {
		return Manifest{}, fmt.Errorf("%w: the backup id is malformed", ErrValidation)
	}
	if options.CreatedAt.IsZero() {
		return Manifest{}, fmt.Errorf("%w: the creation time is required", ErrValidation)
	}
	if strings.TrimSpace(options.ConsistencyPoint) == "" {
		return Manifest{}, fmt.Errorf("%w: the consistency point is required", ErrValidation)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: read the backup directory: %v", ErrValidation, err)
	}
	components := make([]Component, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		kind, known := layout[entry.Name()]
		if !known {
			return Manifest{}, fmt.Errorf(
				"%w: directory %q is not part of the frozen backup layout", ErrUnsupportedLayout, entry.Name())
		}
		component, err := hashComponent(filepath.Join(directory, entry.Name()), kind, entry.Name())
		if err != nil {
			return Manifest{}, err
		}
		components = append(components, component)
	}
	if len(components) == 0 {
		return Manifest{}, fmt.Errorf("%w: the backup holds no component", ErrValidation)
	}
	sort.Slice(components, func(left, right int) bool { return components[left].Locator < components[right].Locator })

	manifest := Manifest{
		SchemaVersion:    SchemaVersion,
		BackupID:         options.BackupID,
		CreatedAt:        options.CreatedAt.UTC(),
		ConsistencyPoint: observability.Redact(options.ConsistencyPoint),
		Tools:            map[string]string{},
		Components:       components,
		RestoreNotes:     []string{},
	}
	for name, value := range options.Tools {
		manifest.Tools[observability.Redact(name)] = observability.Redact(value)
	}
	for _, note := range options.RestoreNotes {
		manifest.RestoreNotes = append(manifest.RestoreNotes, observability.Redact(note))
	}
	if manifest.RestoreNotes == nil {
		manifest.RestoreNotes = []string{}
	}
	manifest.ManifestDigest = digestOf(manifest)
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := Save(directory, manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Save writes the manifest into the backup directory, atomically.
func Save(directory string, manifest Manifest) error {
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode the manifest: %v", ErrValidation, err)
	}
	encoded = append(encoded, '\n')
	return writeAtomic(filepath.Join(directory, ManifestFile), encoded)
}

// Load reads the manifest of a backup directory.
func Load(directory string) (Manifest, error) {
	encoded, err := os.ReadFile(filepath.Join(directory, ManifestFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: read the manifest: %v", ErrValidation, err)
	}
	var manifest Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode the manifest: %v", ErrValidation, err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Validate checks the manifest against the frozen shape.
func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unknown schema version %q", ErrValidation, m.SchemaVersion)
	}
	if !idPattern.MatchString(m.BackupID) {
		return fmt.Errorf("%w: the backup id is malformed", ErrValidation)
	}
	if m.CreatedAt.IsZero() || strings.TrimSpace(m.ConsistencyPoint) == "" {
		return fmt.Errorf("%w: the creation time and consistency point are required", ErrValidation)
	}
	if len(m.Components) == 0 {
		return fmt.Errorf("%w: the backup holds no component", ErrValidation)
	}
	seen := map[string]bool{}
	for _, component := range m.Components {
		if !locatorPattern.MatchString(component.Locator) {
			return fmt.Errorf("%w: malformed locator %q", ErrValidation, component.Locator)
		}
		if seen[component.Locator] {
			return fmt.Errorf("%w: duplicate locator %q", ErrValidation, component.Locator)
		}
		seen[component.Locator] = true
		if _, known := layout[component.Locator]; !known {
			return fmt.Errorf("%w: locator %q is not in the frozen layout", ErrUnsupportedLayout, component.Locator)
		}
		if layout[component.Locator] != component.Kind {
			return fmt.Errorf("%w: locator %q does not match kind %q", ErrValidation, component.Locator, component.Kind)
		}
		if !digestPattern.MatchString(component.Digest) {
			return fmt.Errorf("%w: malformed digest for %q", ErrValidation, component.Locator)
		}
		if component.Entries < 0 || component.Bytes < 0 {
			return fmt.Errorf("%w: malformed counts for %q", ErrValidation, component.Locator)
		}
	}
	if !digestPattern.MatchString(m.ManifestDigest) {
		return fmt.Errorf("%w: malformed manifest digest", ErrValidation)
	}
	if digestOf(m) != m.ManifestDigest {
		return fmt.Errorf("%w: the manifest digest does not cover its content", ErrDigestMismatch)
	}
	return nil
}

// Verify re-hashes the backup directory and compares it with the manifest. It
// reports the first component that no longer matches, so an operator knows what
// to re-copy.
func Verify(directory string) (Manifest, error) {
	manifest, err := Load(directory)
	if err != nil {
		return Manifest{}, err
	}
	index := map[string]Component{}
	for _, component := range manifest.Components {
		index[component.Locator] = component
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: read the backup directory: %v", ErrValidation, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		kind, known := layout[entry.Name()]
		if !known {
			return Manifest{}, fmt.Errorf("%w: directory %q is not in the frozen layout", ErrUnsupportedLayout, entry.Name())
		}
		recorded, listed := index[entry.Name()]
		if !listed {
			return Manifest{}, fmt.Errorf("%w: component %q is present but not in the manifest", ErrDigestMismatch, entry.Name())
		}
		actual, err := hashComponent(filepath.Join(directory, entry.Name()), kind, entry.Name())
		if err != nil {
			return Manifest{}, err
		}
		if actual.Digest != recorded.Digest {
			return Manifest{}, fmt.Errorf("%w: component %q hashes to %s, the manifest records %s",
				ErrDigestMismatch, entry.Name(), actual.Digest, recorded.Digest)
		}
		if actual.Entries != recorded.Entries {
			return Manifest{}, fmt.Errorf("%w: component %q holds %d files, the manifest records %d",
				ErrDigestMismatch, entry.Name(), actual.Entries, recorded.Entries)
		}
	}
	for locator := range index {
		if _, err := os.Stat(filepath.Join(directory, locator)); err != nil {
			return Manifest{}, fmt.Errorf("%w: component %q is in the manifest but missing", ErrDigestMismatch, locator)
		}
	}
	return manifest, nil
}

// digestOf hashes a manifest's content with the digest field treated as empty,
// so the digest covers every other field.
func digestOf(manifest Manifest) string {
	copied := manifest
	copied.ManifestDigest = ""
	encoded, err := json.Marshal(copied)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// hashComponent hashes every regular file under one component directory. The
// rule is canonical: files are walked in sorted order, and each file
// contributes its relative path, its size and its own digest.
func hashComponent(directory string, kind ComponentKind, locator string) (Component, error) {
	component := Component{Kind: kind, Locator: locator, Entries: 0, Bytes: 0}
	type fileEntry struct {
		path   string
		size   int64
		digest string
	}
	files := make([]fileEntry, 0, 8)
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", ErrUnsupportedLayout, path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%w: read %s: %v", ErrValidation, path, err)
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrValidation, err)
		}
		sum := sha256.Sum256(content)
		files = append(files, fileEntry{
			path:   filepath.ToSlash(relative),
			size:   int64(len(content)),
			digest: hex.EncodeToString(sum[:]),
		})
		component.Entries++
		component.Bytes += int64(len(content))
		return nil
	})
	if err != nil {
		return Component{}, err
	}
	// An empty component is legitimate (a deployment that has published nothing
	// yet); it is recorded with zero entries and the digest of the empty
	// canonical form, so its presence is still proven.
	sort.Slice(files, func(left, right int) bool { return files[left].path < files[right].path })
	canonical := strings.Builder{}
	for _, file := range files {
		fmt.Fprintf(&canonical, "%s\x00%d\x00%s\x00", file.path, file.size, file.digest)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	component.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return component, nil
}

func writeAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: create a temporary file: %v", ErrValidation, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("%w: write the manifest: %v", ErrValidation, err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%w: sync the manifest: %v", ErrValidation, err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return fmt.Errorf("%w: close the manifest: %v", ErrValidation, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		cleanup()
		return fmt.Errorf("%w: commit the manifest: %v", ErrValidation, err)
	}
	return nil
}
