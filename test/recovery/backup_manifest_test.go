// Package recovery holds backend:S07's recovery evidence tests. They run
// without any external service: the isolated end-to-end restart/backup/restore
// drill lives in scripts/test-recovery-drill.sh, and this package proves the
// deterministic rules that drill depends on.
package recovery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/backup"
)

func buildFixture(t *testing.T) (string, backup.Manifest) {
	t.Helper()
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, "postgres", "database.pg_dump"),
		"-- kowa postgres dump\nCREATE TABLE work_item (id text primary key);\n")
	writeFile(t, filepath.Join(directory, "artifacts", "art_0001"),
		"plan v1: the frozen plan artifact\n")
	writeFile(t, filepath.Join(directory, "artifacts", "art_0002"),
		"review evidence v1\n")
	writeFile(t, filepath.Join(directory, "runner-state", "state", "runtime.json"),
		"{\"epoch\":\"epoch-0123456789abcdef\"}\n")
	writeFile(t, filepath.Join(directory, "config", "config.snapshot"),
		"KOWA_SERVER_ADDR=127.0.0.1:8080\n")
	manifest, err := backup.Build(directory, backup.BuildOptions{
		BackupID:         "backup-20261016-120000",
		CreatedAt:        time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		ConsistencyPoint: "writers quiesced at 2026-10-16T12:00:00Z before pg_dump",
		Tools:            map[string]string{"postgres": "17.11", "kowa": "6d88be1"},
		RestoreNotes:     []string{"apply postgres/database.pg_dump with psql before starting the server"},
	})
	if err != nil {
		t.Fatalf("building the backup failed: %v", err)
	}
	return directory, manifest
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s failed: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

func TestAMatchingBackupVerifies(t *testing.T) {
	directory, manifest := buildFixture(t)
	verified, err := backup.Verify(directory)
	if err != nil {
		t.Fatalf("a freshly built backup must verify: %v", err)
	}
	if verified.BackupID != manifest.BackupID || verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("verify returned another manifest: %+v", verified)
	}
	if len(verified.Components) != 4 {
		t.Fatalf("the frozen layout has four components, got %d", len(verified.Components))
	}
}

// TestATamperedBackupIsRefused is the recovery red light: a backup whose content
// no longer matches its manifest must not be usable as a restore source.
func TestATamperedBackupIsRefused(t *testing.T) {
	cases := map[string]func(t *testing.T, directory string){
		"one byte changed in the dump": func(t *testing.T, directory string) {
			writeFile(t, filepath.Join(directory, "postgres", "database.pg_dump"),
				"-- kowa postgres dump\nCREATE TABLE work_item (id text primary key, extra int);\n")
		},
		"an artifact was removed": func(t *testing.T, directory string) {
			if err := os.Remove(filepath.Join(directory, "artifacts", "art_0002")); err != nil {
				t.Fatalf("removing the artifact failed: %v", err)
			}
		},
		"an artifact was added": func(t *testing.T, directory string) {
			writeFile(t, filepath.Join(directory, "artifacts", "art_0003"), "smuggled\n")
		},
		"a component directory disappeared": func(t *testing.T, directory string) {
			if err := os.RemoveAll(filepath.Join(directory, "runner-state")); err != nil {
				t.Fatalf("removing the component failed: %v", err)
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			directory, _ := buildFixture(t)
			tamper(t, directory)
			if _, err := backup.Verify(directory); !errors.Is(err, backup.ErrDigestMismatch) {
				t.Fatalf("want a digest mismatch, got %v", err)
			}
		})
	}
}

func TestAnUnsupportedLayoutIsRefused(t *testing.T) {
	directory, _ := buildFixture(t)
	writeFile(t, filepath.Join(directory, "secrets", "leftover.txt"), "not a component\n")
	if _, err := backup.Verify(directory); !errors.Is(err, backup.ErrUnsupportedLayout) {
		t.Fatalf("an unknown component directory must be refused, got %v", err)
	}
	if _, err := backup.Build(directory, backup.BuildOptions{
		BackupID: "backup-2", CreatedAt: time.Now(), ConsistencyPoint: "x",
	}); !errors.Is(err, backup.ErrUnsupportedLayout) {
		t.Fatalf("build must refuse an unknown directory too, got %v", err)
	}
}

// TestTheManifestDigestCoversTheConsistencyPoint pins that the manifest cannot be
// edited to claim a different consistency point than the one it was taken at.
func TestTheManifestDigestCoversTheConsistencyPoint(t *testing.T) {
	directory, manifest := buildFixture(t)
	manifest.ConsistencyPoint = "writers were live and nothing was flushed"
	if err := backup.Save(directory, manifest); err != nil {
		t.Fatalf("saving the edited manifest failed: %v", err)
	}
	if _, err := backup.Load(directory); !errors.Is(err, backup.ErrDigestMismatch) {
		t.Fatalf("an edited manifest must not load, got %v", err)
	}
}

func TestRestoreRefusesBeforeItWritesAnything(t *testing.T) {
	directory, _ := buildFixture(t)
	writeFile(t, filepath.Join(directory, "artifacts", "art_0001"), "tampered\n")
	destination := filepath.Join(t.TempDir(), "artifacts")
	_, err := backup.Restore(directory, map[backup.ComponentKind]string{
		backup.ComponentPostgresDump:  filepath.Join(t.TempDir(), "postgres"),
		backup.ComponentArtifactStore: destination,
		backup.ComponentRunnerState:   filepath.Join(t.TempDir(), "runner-state"),
		backup.ComponentConfig:        filepath.Join(t.TempDir(), "config"),
	})
	if !errors.Is(err, backup.ErrDigestMismatch) {
		t.Fatalf("a tampered backup must be refused, got %v", err)
	}
	if entries, err := os.ReadDir(destination); err == nil && len(entries) > 0 {
		t.Fatalf("nothing may be written before the check passes, found %d entries", len(entries))
	}
}

func TestRestoreAppliesAndProvesTheCopy(t *testing.T) {
	directory, manifest := buildFixture(t)
	root := t.TempDir()
	report, err := backup.Restore(directory, map[backup.ComponentKind]string{
		backup.ComponentPostgresDump:  filepath.Join(root, "postgres"),
		backup.ComponentArtifactStore: filepath.Join(root, "artifacts"),
		backup.ComponentRunnerState:   filepath.Join(root, "runner-state"),
		backup.ComponentConfig:        filepath.Join(root, "config"),
	})
	if err != nil {
		t.Fatalf("restoring failed: %v", err)
	}
	if report.BackupID != manifest.BackupID || len(report.Components) != 4 {
		t.Fatalf("the report must describe every component: %+v", report)
	}
	content, err := os.ReadFile(filepath.Join(root, "artifacts", "art_0001"))
	if err != nil || string(content) != "plan v1: the frozen plan artifact\n" {
		t.Fatalf("the restored artifact is wrong: %q (%v)", content, err)
	}
	// Every component the manifest names must have been applied, not merely
	// listed: an entry count of zero would make the restore report vacuous.
	for _, component := range report.Components {
		if component.Entries == 0 || component.Bytes == 0 || component.Digest == "" {
			t.Fatalf("a restored component reported no work: %+v", component)
		}
	}
	// The dump is the component the operating procedure applies with psql, so it
	// must still be identifiable by name after the copy.
	if _, err := os.Stat(filepath.Join(root, "postgres", "database.pg_dump")); err != nil {
		t.Fatalf("the restored dump is missing: %v", err)
	}
}

func TestRestoreRefusesAMissingDestination(t *testing.T) {
	directory, _ := buildFixture(t)
	if _, err := backup.Restore(directory, map[backup.ComponentKind]string{
		backup.ComponentPostgresDump: filepath.Join(t.TempDir(), "postgres"),
	}); !errors.Is(err, backup.ErrValidation) {
		t.Fatalf("a component with no destination must be refused, got %v", err)
	}
}

func TestBuildRefusesIncompleteOptions(t *testing.T) {
	directory, _ := buildFixture(t)
	cases := map[string]backup.BuildOptions{
		"no id":                {CreatedAt: time.Now(), ConsistencyPoint: "x"},
		"no created at":        {BackupID: "backup-1", ConsistencyPoint: "x"},
		"no consistency point": {BackupID: "backup-1", CreatedAt: time.Now()},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := backup.Build(directory, options); !errors.Is(err, backup.ErrValidation) {
				t.Fatalf("want a validation refusal, got %v", err)
			}
		})
	}
}
