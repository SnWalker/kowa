package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// RestoreReport is what one restore actually did, per component. It is written
// into the recovery evidence: a restore that reports nothing cannot be audited.
type RestoreReport struct {
	BackupID       string         `json:"backupId"`
	ManifestDigest string         `json:"manifestDigest"`
	Components     []RestoredPart `json:"components"`
	Bytes          int64          `json:"bytes"`
}

// RestoredPart is one component's restore outcome, including the digest the
// restored tree actually has — recomputed after the copy, not copied from the
// manifest. It carries counts and digests only: an evidence file must not
// accumulate host paths.
type RestoredPart struct {
	Kind    ComponentKind `json:"kind"`
	Entries int           `json:"entries"`
	Bytes   int64         `json:"bytes"`
	Digest  string        `json:"sha256"`
}

// Restore verifies a backup and then applies it.
//
// The order is the rule: the manifest is checked, every component is re-hashed
// and compared, and only then is anything written. A mismatch stops the restore
// with the inconsistent component named.
//
// Each destination must hold that component alone: the restored tree is hashed
// after the copy and compared with the manifest, so restoring over unrelated
// content is refused instead of silently mixing two backups. Nothing is deleted
// on the destination side — a restore never removes evidence a run or a closure
// still refers to; clearing a destination is the operator's explicit decision.
func Restore(directory string, destinations map[ComponentKind]string) (RestoreReport, error) {
	manifest, err := Verify(directory)
	if err != nil {
		return RestoreReport{}, err
	}
	report := RestoreReport{BackupID: manifest.BackupID, ManifestDigest: manifest.ManifestDigest, Components: []RestoredPart{}}
	for _, component := range manifest.Components {
		destination, configured := destinations[component.Kind]
		if !configured || destination == "" {
			return RestoreReport{}, fmt.Errorf(
				"%w: no restore destination is configured for %s", ErrValidation, component.Kind)
		}
		part, err := copyComponent(filepath.Join(directory, component.Locator), destination, component)
		if err != nil {
			return RestoreReport{}, err
		}
		report.Components = append(report.Components, part)
		report.Bytes += part.Bytes
	}
	return report, nil
}

// copyComponent copies one component tree and proves the copy by re-hashing it.
func copyComponent(source, destination string, expected Component) (RestoredPart, error) {
	if err := os.MkdirAll(destination, 0o750); err != nil {
		return RestoredPart{}, fmt.Errorf("%w: create %s: %v", ErrValidation, destination, err)
	}
	var copied int64
	entries := 0
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		written, err := copyFile(path, target)
		if err != nil {
			return err
		}
		copied += written
		entries++
		return nil
	})
	if err != nil {
		return RestoredPart{}, fmt.Errorf("%w: copy %s: %v", ErrValidation, source, err)
	}
	actual, err := hashComponent(destination, expected.Kind, expected.Locator)
	if err != nil {
		return RestoredPart{}, err
	}
	if actual.Digest != expected.Digest {
		// The copy is what the operator will run on, so the copy is what has to
		// match — not the source it was read from.
		return RestoredPart{}, fmt.Errorf("%w: the restored %s hashes to %s, the manifest records %s",
			ErrDigestMismatch, expected.Locator, actual.Digest, expected.Digest)
	}
	return RestoredPart{
		Kind: expected.Kind, Entries: entries, Bytes: copied, Digest: actual.Digest,
	}, nil
}

func copyFile(source, destination string) (int64, error) {
	from, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer func() { _ = from.Close() }()
	info, err := from.Stat()
	if err != nil {
		return 0, err
	}
	to, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(to, from)
	if err != nil {
		_ = to.Close()
		return written, err
	}
	if err := to.Sync(); err != nil {
		_ = to.Close()
		return written, err
	}
	return written, to.Close()
}

// Kinds lists the component kinds of the frozen layout, in directory order.
func Kinds() []ComponentKind {
	locators := make([]string, 0, len(layout))
	for locator := range layout {
		locators = append(locators, locator)
	}
	sort.Strings(locators)
	kinds := make([]ComponentKind, 0, len(locators))
	for _, locator := range locators {
		kinds = append(kinds, layout[locator])
	}
	return kinds
}
