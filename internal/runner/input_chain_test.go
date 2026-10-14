package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/knowledge"
	"github.com/SnWalker/kowa/internal/runnertransport"
)

// TestLoopDeliversS03ProducedFrozenInput is the F4 evidence: the frozen input
// that reaches the Worker is produced by the real S03 producers —
// knowledge.Capture/Assemble canonical bytes plus artifact.Service published
// content — travels the real HTTP transport, is re-verified by the Runner, and
// lands byte-identical in the attempt workspace. No synthetic bundle stands in
// for the chain.
func TestLoopDeliversS03ProducedFrozenInput(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC()
	workspaceID := "workspace-s04"
	subjectID := "work-s04"

	// --- Real artifact producer: upload, verify and publish content ---
	catalog := artifact.NewMemoryCatalog()
	// The S03 authority checks current membership on every operation.
	catalog.Grant(workspaceID, "9001", true)
	blobs, err := artifact.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifactService := artifact.NewService(catalog, blobs, func() time.Time { return now })
	artifactScope := artifact.Scope{WorkspaceID: workspaceID, ActorID: "9001"}
	artifactContent := []byte("# plan candidate\n\nsynthetic plan body for the frozen input\n")
	published, err := artifactService.Upload(ctx, artifactScope, artifact.Upload{
		SubjectID: subjectID, Kind: "plan", MediaType: "text/markdown",
		Digest: artifact.Digest(artifactContent),
		// An operation-produced artifact is publishable directly; task-produced
		// artifacts require an accepted TaskResult (S03 contract).
		Producer: artifact.Producer{Kind: "operation", ID: "plan-publication"},
	}, artifactContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := artifactService.Publish(ctx, artifactScope, published); err != nil {
		t.Fatal(err)
	}
	publishedBytes, err := artifactService.Read(ctx, artifactScope, published)
	if err != nil || !bytes.Equal(publishedBytes, artifactContent) {
		t.Fatalf("published artifact read-back = %d bytes, %v", len(publishedBytes), err)
	}

	// --- Real knowledge producer: capture a committed tree, then assemble ---
	knowledgeDirectory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(knowledgeDirectory, "docs"), 0o750); err != nil {
		t.Fatal(err)
	}
	knowledgeBytes := []byte("# calc convention\n\nclamp upper bound is inclusive (frozen fixture)\n")
	if err := os.WriteFile(filepath.Join(knowledgeDirectory, "docs", "calc-convention.md"), knowledgeBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	repositoryID := "1413481670"
	commit := strings.Repeat("ab", 20)
	source, err := knowledge.NewDirectorySource(knowledgeDirectory, repositoryID, commit)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := knowledge.Capture(ctx, source, knowledge.Selection{
		WorkspaceID: workspaceID, RepositoryID: repositoryID, Commit: commit,
		Entries: []knowledge.Entry{{
			Path: "docs/calc-convention.md", Digest: artifact.Digest(knowledgeBytes),
			Scope: "project", Required: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	effective, err := knowledge.Assemble(
		knowledge.InputRequest{
			WorkspaceID: workspaceID, SubjectID: subjectID, RequirementVersion: "req-1",
			ProjectCommit:    strings.Repeat("cd", 20),
			DefinitionDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PolicyVersion:    "policy-1", ConfigVersion: "config-1",
			Upstream: []knowledge.Upstream{{Name: "plan_doc", NodeRunID: "node-1", Ref: published}},
		},
		snapshot,
		knowledge.BindingEvent{
			ID: "event-1", HumanResponseID: "response-1", Version: 1, ConfirmedAt: now,
			SnapshotID: snapshot.ID, WorkspaceID: workspaceID, SubjectID: subjectID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(effective.Digest, "sha256:") || len(effective.Canonical) == 0 {
		t.Fatalf("assembled effective input = %+v", effective)
	}

	// --- Serve those exact bytes over the real transport ---
	counter := filepath.Join(t.TempDir(), "counter")
	f := newLoopFixture(t, counterScript(counter), nil)
	inputs := scriptedInput{
		bundle: effective.Canonical, artifacts: []artifact.Ref{published},
		contents: map[string][]byte{published.ArtifactID: publishedBytes},
	}
	handler := httpapi.NewRuntimeTransportHandler(
		staticAuthorizer{"token-1": "runtime-1"}, f.service, inputs,
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler.PollInterval = 20 * time.Millisecond
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.client = runnertransport.NewClient(server.URL, "runtime-1", "token-1")
	f.client.Epoch = f.epoch
	f.client.HTTP = &http.Client{Timeout: 30 * time.Second}
	f.loop = f.newLoop(counterScript(counter), f.config)

	// The task's frozen inputDigest is the assembled EffectiveInput digest.
	f.seedRunWithDigest("s03", effective.Digest)

	loopCtx, cancel := context.WithCancel(t.Context())
	outcome := f.start(loopCtx)
	f.waitFor("s03 input delivered", func() bool { return f.taskState("task-s03") == execution.TaskCompleted })
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}

	// --- The Worker's task directory holds the S03-produced bytes verbatim ---
	workspaces, err := filepath.Glob(filepath.Join(f.workRoot, "tasks", "*"))
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("attempt workspaces = %v, %v", workspaces, err)
	}
	deliveredBundle, err := os.ReadFile(filepath.Join(workspaces[0], "input", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(deliveredBundle, effective.Canonical) {
		t.Fatalf("delivered bundle is not the assembled canonical bytes: %d vs %d bytes",
			len(deliveredBundle), len(effective.Canonical))
	}
	deliveredArtifact, err := os.ReadFile(filepath.Join(workspaces[0], "input", "artifacts", published.ArtifactID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(deliveredArtifact, artifactContent) {
		t.Fatalf("delivered artifact is not the published content")
	}
	manifest, err := os.ReadFile(filepath.Join(workspaces[0], "input", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		BundleDigest string `json:"bundleDigest"`
		Artifacts    []struct {
			ArtifactID string `json:"artifactId"`
			Digest     string `json:"digest"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(manifest, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.BundleDigest != effective.Digest || len(recorded.Artifacts) != 1 ||
		recorded.Artifacts[0].ArtifactID != published.ArtifactID ||
		recorded.Artifacts[0].Digest != published.Digest {
		t.Fatalf("delivery manifest = %+v", recorded)
	}
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("executions = %d, want 1", got)
	}
}
