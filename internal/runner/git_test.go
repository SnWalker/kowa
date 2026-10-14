package runner_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	knowledgeWrite := execution.GitScope{RepositoryID: "1001", Role: "knowledge", Operation: "push", Ref: "refs/heads/task/xyz"}
	if _, err := channel.DeliverScopeForTest(ctx, knowledgeWrite, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("knowledge write = %v, want refusal", err)
	}
	publishPR := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "publish_pr", Ref: "refs/heads/task/xyz"}
	if _, err := channel.DeliverScopeForTest(ctx, publishPR, task, workspace, "m3-marker"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("publish_pr = %v, want refusal", err)
	}
	protected := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/main"}
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
	pushScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/task/identity"}
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
	pushScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/task/deliver"}
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
	if record.Ref != "refs/heads/task/deliver" || record.BaseOid != baseOid || !strings.HasPrefix(record.CommitOid, "") {
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
	lostScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/task/lost"}
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
	pushScope := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/task/conflict"}

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

// producedScopes derives git scopes from the real S02 producer: a run created
// through the execution service, leased, JSON-marshalled and strictly decoded
// exactly as the Runner consumes the frozen wire. Runner-side tests use these
// bytes so the channel is exercised against the producer's ref grammar
// (`refs/heads/<branch>`) instead of hand-typed literals.
func producedScopes(t *testing.T, scopes []execution.GitScope) []execution.GitScope {
	t.Helper()
	now := time.Now().UTC()
	store := execution.NewMemoryStore()
	service := execution.NewService(store, func() time.Time { return now })
	runtime := execution.RuntimeRegistration{
		ID: "producer-runtime", Epoch: "epoch-1", GitHubUserID: "9001",
		Provider:              execution.ProviderSelection{ID: "codex", Version: "grammar"},
		ProviderAuthenticated: true,
		Capabilities:          []execution.Capability{{ID: "plan.create", Version: 1}},
	}
	if err := service.RegisterRuntime(t.Context(), runtime); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := service.CreateRun(t.Context(), execution.CreateRunCommand{
		Run: execution.Run{
			ID: "producer-run", WorkspaceID: "grammar-workspace", WorkItemID: "grammar-work",
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: digest, State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: "producer-node", RunID: "producer-run", NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: digest, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: "producer-task", NodeRunID: "producer-node", Attempt: 1,
			AttemptKind: execution.AttemptInitial,
			Capability:  execution.Capability{ID: "plan.create", Version: 1},
			Provider:    execution.ProviderSelection{ID: "codex", Version: "grammar"},
			GitScopes:   scopes, State: execution.TaskQueued,
			InputDigest: digest, DeadlineAt: now.Add(time.Hour),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	lease, err := service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: runtime.ID, RuntimeEpoch: runtime.Epoch, LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Task struct {
			GitScopes []json.RawMessage `json:"gitScopes"`
		} `json:"task"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	decoded := make([]execution.GitScope, 0, len(envelope.Task.GitScopes))
	for _, raw := range envelope.Task.GitScopes {
		var scope execution.GitScope
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&scope); err != nil {
			t.Fatalf("strict consumer rejected producer scope: %v", err)
		}
		decoded = append(decoded, scope)
	}
	return decoded
}

func TestGitChannelUsesTheProducerRefGrammar(t *testing.T) {
	ctx := t.Context()
	bare, baseOid := seedBareRepository(t)
	channel := channelForTest(t, map[string]string{"1001": bare}, "9001")
	workspace := t.TempDir()

	// The positive path consumes scopes decoded from the real producer bytes:
	// a full branch ref for materialization and a full branch ref for delivery.
	produced := producedScopes(t, []execution.GitScope{
		{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: "refs/heads/main"},
		{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/task/produced"},
	})
	if len(produced) != 2 || produced[0].Ref != "refs/heads/main" || produced[1].Ref != "refs/heads/task/produced" {
		t.Fatalf("produced scopes = %+v", produced)
	}
	if err := channel.MaterializeScopeForTest(ctx, produced[0], workspace, "grammar"); err != nil {
		t.Fatalf("materialize with producer ref = %v", err)
	}
	checkout := filepath.Join(workspace, "repos", "1001")
	if head := strings.TrimSpace(runGitInTest(t, checkout, "rev-parse", "HEAD")); head != baseOid {
		t.Fatalf("materialized head = %s, want %s", head, baseOid)
	}
	if err := os.WriteFile(filepath.Join(checkout, "produced.txt"), []byte("produced\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := execution.TaskSpec{ID: "grammar-task", GitExecutionUserID: "9001"}
	delivery, err := channel.DeliverScopeForTest(ctx, produced[1], task, workspace, "grammar")
	if err != nil {
		t.Fatalf("deliver with producer ref = %v", err)
	}
	if delivery.Ref != "refs/heads/task/produced" {
		t.Fatalf("delivery ref = %q", delivery.Ref)
	}
	remote := strings.TrimSpace(runGitInTest(t, bare, "rev-parse", "task/produced"))
	if remote != delivery.CommitOid {
		t.Fatalf("remote %s != delivered %s", remote, delivery.CommitOid)
	}

	// The protected-ref boundary must survive the full ref form.
	protected := execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "refs/heads/main"}
	if _, err := channel.DeliverScopeForTest(ctx, protected, task, workspace, "grammar"); err == nil ||
		!strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("full-form protected ref = %v, want refusal", err)
	}
	// A bare branch name is a second grammar and is refused, not reinterpreted.
	if err := channel.MaterializeScopeForTest(ctx,
		execution.GitScope{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: "main"},
		workspace, "grammar"); err == nil || !strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("bare branch name = %v, want refusal", err)
	}
}
