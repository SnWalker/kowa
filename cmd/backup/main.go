// Command backup writes and checks the manifest of a local Kowa backup.
//
// The heavy lifting — dumping PostgreSQL, copying the artifact store and the
// Runner state — is done by the operating procedure in scripts/backup-local.sh,
// because it talks to real services. This command owns the part that must be
// deterministic: what the backup holds, what it hashed to, and whether it still
// matches.
//
// Usage:
//
//	backup manifest --dir <backup-dir> --id <backup-id> --consistency-point <text> --tool name=value ...
//	backup verify --dir <backup-dir>
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/backup"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "manifest":
		err = runManifest(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "restore":
		err = runRestore(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: backup manifest --dir <backup-dir> --id <backup-id> --consistency-point <text> [--tool name=value]...")
	fmt.Fprintln(os.Stderr, "       backup verify --dir <backup-dir>")
	fmt.Fprintln(os.Stderr, "       backup restore --dir <backup-dir> --to-postgres <dir> --to-artifacts <dir> --to-runner-state <dir> --to-config <dir>")
}

// toolList collects repeated --tool flags.
type toolList map[string]string

func (t toolList) String() string {
	names := make([]string, 0, len(t))
	for name := range t {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+t[name])
	}
	return strings.Join(parts, ",")
}

func (t toolList) Set(value string) error {
	name, version, found := strings.Cut(value, "=")
	if !found || strings.TrimSpace(name) == "" || strings.TrimSpace(version) == "" {
		return errors.New("tool entries must be name=version")
	}
	t[strings.TrimSpace(name)] = strings.TrimSpace(version)
	return nil
}

func runManifest(arguments []string) error {
	flags := flag.NewFlagSet("manifest", flag.ContinueOnError)
	directory := flags.String("dir", "", "the backup directory to describe")
	backupID := flags.String("id", "", "the backup identity, for example backup-20261016-120000")
	consistencyPoint := flags.String("consistency-point", "", "the fact the backup is consistent as of")
	notes := noteList{}
	flags.Var(&notes, "note", "one restore note; repeatable")
	tools := toolList{}
	flags.Var(tools, "tool", "one name=version pair; repeatable")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" || *backupID == "" || *consistencyPoint == "" {
		flags.Usage()
		return errors.New("--dir, --id and --consistency-point are all required")
	}
	manifest, err := backup.Build(*directory, backup.BuildOptions{
		BackupID:         *backupID,
		CreatedAt:        time.Now().UTC(),
		ConsistencyPoint: *consistencyPoint,
		Tools:            tools,
		RestoreNotes:     notes,
	})
	if err != nil {
		return err
	}
	fmt.Printf("BACKUP_MANIFEST_OK id=%s components=%d manifestDigest=%s\n",
		manifest.BackupID, len(manifest.Components), manifest.ManifestDigest)
	for _, component := range manifest.Components {
		fmt.Printf("  %s %s entries=%d bytes=%d\n",
			component.Kind, component.Digest, component.Entries, component.Bytes)
	}
	return nil
}

func runVerify(arguments []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	directory := flags.String("dir", "", "the backup directory to check")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *directory == "" {
		flags.Usage()
		return errors.New("--dir is required")
	}
	manifest, err := backup.Verify(*directory)
	if err != nil {
		return err
	}
	fmt.Printf("BACKUP_VERIFY_OK id=%s manifestDigest=%s components=%d\n",
		manifest.BackupID, manifest.ManifestDigest, len(manifest.Components))
	return nil
}

func runRestore(arguments []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	directory := flags.String("dir", "", "the backup directory to restore")
	toPostgres := flags.String("to-postgres", "", "destination of the database dump file")
	toArtifacts := flags.String("to-artifacts", "", "destination of the artifact store")
	toRunnerState := flags.String("to-runner-state", "", "destination of the Runner state")
	toConfig := flags.String("to-config", "", "destination of the configuration snapshot")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	destinations := map[backup.ComponentKind]string{
		backup.ComponentPostgresDump:  *toPostgres,
		backup.ComponentArtifactStore: *toArtifacts,
		backup.ComponentRunnerState:   *toRunnerState,
		backup.ComponentConfig:        *toConfig,
	}
	if *directory == "" {
		flags.Usage()
		return errors.New("--dir is required")
	}
	report, err := backup.Restore(*directory, destinations)
	if err != nil {
		return err
	}
	for _, part := range report.Components {
		fmt.Printf("BACKUP_RESTORE_PART kind=%s entries=%d bytes=%d digest=%s\n",
			part.Kind, part.Entries, part.Bytes, part.Digest)
	}
	fmt.Printf("BACKUP_RESTORE_OK id=%s bytes=%d\n", report.BackupID, report.Bytes)
	return nil
}

// noteList collects repeated --note flags.
type noteList []string

func (n *noteList) String() string { return strings.Join(*n, " | ") }

func (n *noteList) Set(value string) error {
	*n = append(*n, value)
	return nil
}
