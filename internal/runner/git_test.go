package runner_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
	"github.com/SnWalker/kowa/internal/runner"
)

// runGitInTest executes git for test fixtures with a deterministic identity.
func runGitInTest(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

// seedBareRepository creates a local origin with one commit on main.
func seedBareRepository(t *testing.T) (barePath, baseOid string) {
	t.Helper()
	barePath = filepath.Join(t.TempDir(), "origin.git")
	runGitInTest(t, "", "init", "--quiet", "--bare", "--initial-branch=main", barePath)
	work := filepath.Join(t.TempDir(), "seed")
	runGitInTest(t, "", "clone", "--quiet", barePath, work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInTest(t, work, "add", ".")
	runGitInTest(t, work, "commit", "--quiet", "-m", "seed")
	runGitInTest(t, work, "push", "--quiet", "origin", "main")
	return barePath, strings.TrimSpace(runGitInTest(t, work, "rev-parse", "HEAD"))
}

func channelForTest(t *testing.T, repositories map[string]string, identity string) *runner.GitChannel {
	t.Helper()
	return runner.NewTestGitChannel(repositories, []string{"main", "master"},
		func(context.Context) (string, error) { return identity, nil },
		gitIdentityEnvironment(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// gitIdentityEnvironment mirrors a machine whose git configuration provides
// the commit identity the controlled channel must not override.
func gitIdentityEnvironment() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=Kowa Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Kowa Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
}

func TestGitChannelMaterializesFrozenOID(t *testing.T) {
	ctx := t.Context()
	bare, baseOid := seedBareRepository(t)
	channel := channelForTest(t, map[string]string{"1001": bare}, "9001")
	workspace := t.TempDir()
	scope := execution.GitScope{RepositoryID: "1001", Role: "knowledge", Operation: "clone", Ref: baseOid}
	if err := channel.MaterializeScopeForTest(ctx, scope, workspace, "m3-marker"); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(workspace, "repos", "1001")
	head := strings.TrimSpace(runGitInTest(t, checkout, "rev-parse", "HEAD"))
	if head != baseOid {
		t.Fatalf("materialized head = %s, want frozen %s", head, baseOid)
	}
	evidence, err := os.ReadFile(filepath.Join(workspace, "materialization.json"))
	if err != nil || !strings.Contains(string(evidence), baseOid) {
		t.Fatalf("materialization evidence = %s, %v", evidence, err)
	}

	// Removing the checkout and re-materializing the same frozen ref must
	// rebuild the identical commit.
	if err := os.RemoveAll(checkout); err != nil {
		t.Fatal(err)
	}
	if err := channel.MaterializeScopeForTest(ctx, scope, workspace, "m3-marker"); err != nil {
		t.Fatal(err)
	}
	rebuilt := strings.TrimSpace(runGitInTest(t, checkout, "rev-parse", "HEAD"))
	if rebuilt != baseOid {
		t.Fatalf("rebuilt head = %s, want %s", rebuilt, baseOid)
	}
}

func TestGitChannelRefusesUnknownRepositoryAndKnowledgeWrites(t *testing.T) {
	ctx := t.Context()
	bare, baseOid := seedBareRepository(t)
	channel := channelForTest(t, map[string]string{"1001": bare}, "9001")
	workspace := t.TempDir()

	unknown := execution.GitScope{RepositoryID: "9999", Role: "project", Operation: "clone", Ref: baseOid}
	if err := channel.MaterializeScopeForTest(ctx, unknown, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("unknown repository = %v, want refusal", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "repos", "9999")); !os.IsNotExist(err) {
		t.Fatal("unknown repository was materialized")
	}

	read := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: baseOid}
	if err := channel.MaterializeScopeForTest(ctx, read, workspace, "m3-marker"); err != nil {
		t.Fatal(err)
	}
	task := execution.TaskSpec{ID: "task-1", GitExecutionUserID: "9001"}

	knowledgeWrite := execution.GitScope{RepositoryID: "1001", Role: "knowledge", Operation: "push", Ref: "task/xyz"}
	if _, err := channel.DeliverScopeForTest(ctx, knowledgeWrite, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("knowledge write = %v, want refusal", err)
	}
	publishPR := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "publish_pr", Ref: "task/xyz"}
	if _, err := channel.DeliverScopeForTest(ctx, publishPR, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("publish_pr = %v, want refusal", err)
	}
	protected := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "main"}
	if _, err := channel.DeliverScopeForTest(ctx, protected, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("protected ref = %v, want refusal", err)
	}
}

func TestGitChannelIdentityMismatchRefusesBeforeAnyWrite(t *testing.T) {
	ctx := t.Context()
	bare, baseOid := seedBareRepository(t)
	channel := channelForTest(t, map[string]string{"1001": bare}, "7777")
	workspace := t.TempDir()
	read := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: baseOid}
	if err := channel.MaterializeScopeForTest(ctx, read, workspace, "m3-marker"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "repos", "1001", "change.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := execution.TaskSpec{ID: "task-1", GitExecutionUserID: "9001"}
	pushScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "task/identity"}
	if _, err := channel.DeliverScopeForTest(ctx, pushScope, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_identity_mismatch") {
		t.Fatalf("identity mismatch = %v, want refusal", err)
	}
	// Nothing may have been pushed.
	remote := strings.TrimSpace(runGitInTest(t, bare, "branch", "--list", "task/identity"))
	if remote != "" {
		t.Fatalf("remote branch created despite identity mismatch: %q", remote)
	}
}

func TestGitChannelDeliversAndReconcilesUnknownPushOutcome(t *testing.T) {
	ctx := t.Context()
	bare, baseOid := seedBareRepository(t)
	channel := channelForTest(t, map[string]string{"1001": bare}, "9001")
	workspace := t.TempDir()
	read := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: baseOid}
	if err := channel.MaterializeScopeForTest(ctx, read, workspace, "m3-marker"); err != nil {
		t.Fatal(err)
	}
	task := execution.TaskSpec{ID: "task-deliver", GitExecutionUserID: "9001"}
	checkout := filepath.Join(workspace, "repos", "1001")

	// An unchanged workspace must not fabricate a delivery.
	pushScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "task/deliver"}
	if _, err := channel.DeliverScopeForTest(ctx, pushScope, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "no_changes") {
		t.Fatalf("unchanged delivery = %v, want no_changes", err)
	}

	if err := os.WriteFile(filepath.Join(checkout, "change.txt"), []byte("delivered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, err := channel.DeliverScopeForTest(ctx, pushScope, task, workspace, "m3-marker")
	if err != nil {
		t.Fatal(err)
	}
	if record.Branch != "task/deliver" || record.BaseOid != baseOid || !strings.HasPrefix(record.CommitOid, "") {
		t.Fatalf("delivery record = %+v", record)
	}
	remoteOid := strings.TrimSpace(runGitInTest(t, bare, "rev-parse", "task/deliver"))
	if remoteOid != record.CommitOid {
		t.Fatalf("remote %s != delivered %s", remoteOid, record.CommitOid)
	}
	deliveryEvidence, err := os.ReadFile(filepath.Join(workspace, "delivery-"+repositoryNameHash("1001")+".json"))
	if err != nil || !strings.Contains(string(deliveryEvidence), remoteOid) {
		t.Fatalf("delivery evidence = %s, %v", deliveryEvidence, err)
	}

	// Simulate a push whose response was lost: the push lands remotely, then
	// the channel must reconcile through ls-remote instead of blind retrying.
	// The branch starts empty so the delivery is a genuine first push.
	lostScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "task/lost"}
	lostResponse := channelForTest(t, map[string]string{"1001": bare}, "9001")
	runner.SetGitRunnerForTest(lostResponse, func(innerCtx context.Context, spec proc.Spec) (proc.Outcome, error) {
		outcome, runErr := proc.Run(innerCtx, spec)
		if len(spec.Command) >= 2 && spec.Command[1] == "push" {
			return outcome, errors.New("simulated lost push response")
		}
		return outcome, runErr
	})
	second := t.TempDir()
	if err := lostResponse.MaterializeScopeForTest(ctx, read, second, "m3b"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "repos", "1001", "lost.txt"), []byte("lost response\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reconciled, err := lostResponse.DeliverScopeForTest(ctx, lostScope, task, second, "m3b")
	if err != nil || !reconciled.Reconciled {
		t.Fatalf("lost-response delivery = %+v, %v, want reconciled success", reconciled, err)
	}
	if remoteOid := strings.TrimSpace(runGitInTest(t, bare, "rev-parse", "task/lost")); remoteOid != reconciled.CommitOid {
		t.Fatalf("remote after reconciliation = %s, want %s", remoteOid, reconciled.CommitOid)
	}
}

func TestGitChannelPushConflictAndUnknown(t *testing.T) {
	ctx := t.Context()
	bare, baseOid := seedBareRepository(t)
	workspace := t.TempDir()
	read := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: baseOid}
	task := execution.TaskSpec{ID: "task-conflict", GitExecutionUserID: "9001"}
	pushScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "task/conflict"}

	// Prepare a divergent remote branch written by someone else.
	other := filepath.Join(t.TempDir(), "other")
	runGitInTest(t, "", "clone", "--quiet", bare, other)
	runGitInTest(t, other, "checkout", "--quiet", "-b", "task/conflict")
	if err := os.WriteFile(filepath.Join(other, "other.txt"), []byte("elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInTest(t, other, "add", ".")
	runGitInTest(t, other, "commit", "--quiet", "-m", "other")
	runGitInTest(t, other, "push", "--quiet", "origin", "task/conflict")
	otherOid := strings.TrimSpace(runGitInTest(t, other, "rev-parse", "HEAD"))

	channel := channelForTest(t, map[string]string{"1001": bare}, "9001")
	if err := channel.MaterializeScopeForTest(ctx, read, workspace, "m3c"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "repos", "1001", "mine.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	failingPush := channelForTest(t, map[string]string{"1001": bare}, "9001")
	runner.SetGitRunnerForTest(failingPush, func(innerCtx context.Context, spec proc.Spec) (proc.Outcome, error) {
		if len(spec.Command) >= 2 && spec.Command[1] == "push" {
			return proc.Outcome{ExitCode: 1}, &exec.ExitError{}
		}
		return proc.Run(innerCtx, spec)
	})
	if _, err := failingPush.DeliverScopeForTest(ctx, pushScope, task, workspace, "m3c"); err == nil ||
		!strings.Contains(err.Error(), "git_push_conflict") {
		t.Fatalf("divergent remote = %v, want conflict", err)
	}
	if remoteOid := strings.TrimSpace(runGitInTest(t, bare, "rev-parse", "task/conflict")); remoteOid != otherOid {
		t.Fatalf("remote was overwritten: %s != %s", remoteOid, otherOid)
	}

	// A remote that cannot be read back is unknown, never a silent success.
	unreadable := channelForTest(t, map[string]string{"1001": bare}, "9001")
	runner.SetGitRunnerForTest(unreadable, func(innerCtx context.Context, spec proc.Spec) (proc.Outcome, error) {
		if len(spec.Command) >= 2 && (spec.Command[1] == "push" || spec.Command[1] == "ls-remote") {
			return proc.Outcome{ExitCode: 1}, &exec.ExitError{}
		}
		return proc.Run(innerCtx, spec)
	})
	unreadableWorkspace := t.TempDir()
	if err := unreadable.MaterializeScopeForTest(ctx, read, unreadableWorkspace, "m3c2"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unreadableWorkspace, "repos", "1001", "unknown.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := unreadable.DeliverScopeForTest(ctx, pushScope, task, unreadableWorkspace, "m3c2"); err == nil ||
		!strings.Contains(err.Error(), "git_push_unknown") {
		t.Fatalf("unreadable remote = %v, want unknown", err)
	}
}

// repositoryNameHash mirrors the delivery evidence file naming.
func repositoryNameHash(repositoryID string) string {
	sum := sha256.Sum256([]byte(repositoryID))
	return hex.EncodeToString(sum[:4])
}
