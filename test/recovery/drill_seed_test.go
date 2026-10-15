//go:build integration

package recovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/infra/postgres"
)

// DrillSeed is the evidence the drill freezes before it takes a backup and
// checks again after the restore. It is written as one JSON file so the shell
// driver never has to parse a test's stdout.
type DrillSeed struct {
	WorkspaceID  string    `json:"workspaceId"`
	SubjectID    string    `json:"subjectId"`
	ArtifactID   string    `json:"artifactId"`
	Digest       string    `json:"sha256"`
	Size         int64     `json:"size"`
	StorageRef   string    `json:"storageRef"`
	PayloadHash  string    `json:"payloadHash"`
	SeededAt     time.Time `json:"seededAt"`
	DatabaseName string    `json:"databaseName"`
}

// seedDrillWorkspace creates the workspace and member rows the artifact store
// authorizes against. It is isolated fixture data in a disposable database, not
// a business result; the same shape is used by the other integration suites.
func seedDrillWorkspace(t *testing.T, ctx context.Context, db *sql.DB, workspaceID, actorID string) {
	t.Helper()
	statements := []string{
		`insert into workspace (workspace_id, name, version, created_at, updated_at)
		 values ('` + workspaceID + `', 'S07 recovery drill', 1, now(), now())
		 on conflict (workspace_id) do nothing`,
		`insert into workspace_member (workspace_id, github_user_id, role, created_at, updated_at)
		 values ('` + workspaceID + `', '` + actorID + `', 'developer', now(), now())
		 on conflict (workspace_id, github_user_id) do update set role = excluded.role, revoked_at = null`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seeding the drill workspace failed: %v", err)
		}
	}
}

const (
	drillWorkspaceID = "wsdrill20261016000000"
	drillActorID     = "135722937"
)

const drillPayload = "kowa recovery drill evidence: the artifact that must survive a restore\n"

// TestRecoveryDrillSeed writes one artifact through the real artifact service —
// a real digest, a real blob in the artifact store and a real catalog row in
// PostgreSQL — and records the frozen reference in the drill directory.
func TestRecoveryDrillSeed(t *testing.T) {
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	artifactRoot := os.Getenv("KOWA_DRILL_ARTIFACT_ROOT")
	drillDirectory := os.Getenv("KOWA_DRILL_DIRECTORY")
	if databaseURL == "" || artifactRoot == "" || drillDirectory == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL, KOWA_DRILL_ARTIFACT_ROOT and KOWA_DRILL_DIRECTORY are all required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening the database failed: %v", err)
	}
	defer func() { _ = db.Close() }()
	blobs, err := artifact.NewLocalStore(artifactRoot)
	if err != nil {
		t.Fatalf("opening the artifact store failed: %v", err)
	}
	defer func() { _ = blobs.Close() }()

	seedDrillWorkspace(t, context.Background(), db, drillWorkspaceID, drillActorID)
	service := artifact.NewService(postgres.NewStore(db), blobs, time.Now)
	scope := artifact.Scope{WorkspaceID: drillWorkspaceID, ActorID: drillActorID}
	reference, err := service.Upload(context.Background(), scope, artifact.Upload{
		SubjectID: "widrill2026101600000", Kind: "recovery-drill-evidence",
		MediaType: "text/plain", Digest: artifact.Digest([]byte(drillPayload)),
		Producer: artifact.Producer{Kind: "operation", ID: "opdrill20261016000000"},
	}, []byte(drillPayload))
	if err != nil {
		t.Fatalf("uploading the drill artifact failed: %v", err)
	}
	if err := service.Publish(context.Background(), scope, reference); err != nil {
		t.Fatalf("publishing the drill artifact failed: %v", err)
	}
	seed := DrillSeed{
		WorkspaceID: reference.WorkspaceID, SubjectID: reference.SubjectID,
		ArtifactID: reference.ArtifactID, Digest: reference.Digest, Size: reference.Size,
		StorageRef: reference.StorageRef, PayloadHash: artifact.Digest([]byte(drillPayload)),
		SeededAt: time.Now().UTC(), DatabaseName: databaseName(t, db),
	}
	encoded, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		t.Fatalf("encoding the seed failed: %v", err)
	}
	if err := os.MkdirAll(drillDirectory, 0o750); err != nil {
		t.Fatalf("creating the drill directory failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(drillDirectory, "seed.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("writing the seed failed: %v", err)
	}
	fmt.Printf("DRILL_SEED_OK artifactId=%s digest=%s size=%d\n", seed.ArtifactID, seed.Digest, seed.Size)
}

// TestRecoveryDrillVerify reads the frozen artifact back through the same real
// service and compares it with the seed. It is the post-restore half of the
// drill: a restore that produced the same file names but different bytes fails
// here.
func TestRecoveryDrillVerify(t *testing.T) {
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	artifactRoot := os.Getenv("KOWA_DRILL_ARTIFACT_ROOT")
	drillDirectory := os.Getenv("KOWA_DRILL_DIRECTORY")
	if databaseURL == "" || artifactRoot == "" || drillDirectory == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL, KOWA_DRILL_ARTIFACT_ROOT and KOWA_DRILL_DIRECTORY are all required")
	}
	encoded, err := os.ReadFile(filepath.Join(drillDirectory, "seed.json"))
	if err != nil {
		t.Fatalf("reading the seed failed: %v", err)
	}
	var seed DrillSeed
	if err := json.Unmarshal(encoded, &seed); err != nil {
		t.Fatalf("decoding the seed failed: %v", err)
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening the database failed: %v", err)
	}
	defer func() { _ = db.Close() }()
	blobs, err := artifact.NewLocalStore(artifactRoot)
	if err != nil {
		t.Fatalf("opening the artifact store failed: %v", err)
	}
	defer func() { _ = blobs.Close() }()

	service := artifact.NewService(postgres.NewStore(db), blobs, time.Now)
	scope := artifact.Scope{WorkspaceID: seed.WorkspaceID, ActorID: drillActorID}
	content, err := service.Read(context.Background(), scope, artifact.Ref{
		SchemaVersion: artifact.SchemaVersion, ArtifactID: seed.ArtifactID,
		WorkspaceID: seed.WorkspaceID, SubjectID: seed.SubjectID, Kind: "recovery-drill-evidence",
		Producer: artifact.Producer{Kind: "operation", ID: "opdrill20261016000000"},
		Digest:   seed.Digest, Size: seed.Size, MediaType: "text/plain",
		StorageRef: seed.StorageRef,
	})
	if err != nil {
		t.Fatalf("the frozen artifact is not readable after the restore: %v", err)
	}
	if got := artifact.Digest(content); got != seed.PayloadHash {
		t.Fatalf("the restored artifact hashes to %s, the seed recorded %s", got, seed.PayloadHash)
	}
	if string(content) != drillPayload {
		t.Fatalf("the restored artifact content changed: %q", content)
	}
	if name := databaseName(t, db); name != seed.DatabaseName {
		t.Fatalf("the restored database is %s, the seed recorded %s", name, seed.DatabaseName)
	}
	fmt.Printf("DRILL_VERIFY_OK artifactId=%s digest=%s\n", seed.ArtifactID, seed.Digest)
}

func databaseName(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name string
	if err := db.QueryRow("select current_database()").Scan(&name); err != nil {
		t.Fatalf("reading the database name failed: %v", err)
	}
	return name
}
