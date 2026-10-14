//go:build sandboxprobe

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
)

// TestSandboxControlledDeliveryProbe is the M3 real-environment acceptance: the
// Runner's controlled Git channel materializes the frozen sandbox baselines,
// delivers one synthetic probe commit on a self-created task branch, reconciles
// the push through ls-remote, exercises the channel's refusal paths locally,
// and finally deletes only that self-created branch.
//
// It is opt-in and authorized per session:
//
//	KOWA_SANDBOX_PROBE=true \
//	KOWA_SANDBOX_REPOSITORY_ID=1401511579 KOWA_SANDBOX_REPOSITORY_URL=git@github.com:SnWalker/kowa-sandbox.git \
//	KOWA_SANDBOX_BASE_OID=b99754c... \
//	KOWA_SANDBOX_KNOWLEDGE_ID=1413481670 KOWA_SANDBOX_KNOWLEDGE_URL=git@github.com:SnWalker/kowa-sandbox-knowledge.git \
//	KOWA_SANDBOX_KNOWLEDGE_OID=bd2d5073... KOWA_SANDBOX_EXECUTION_USER_ID=135722937 \
//	go test -count=1 -tags sandboxprobe -run TestSandboxControlledDeliveryProbe -v ./internal/runner/
func TestSandboxControlledDeliveryProbe(t *testing.T) {
	if os.Getenv("KOWA_SANDBOX_PROBE") != "true" {
		t.Skip("set KOWA_SANDBOX_PROBE=true to run the authorized sandbox probe")
	}
	projectID := os.Getenv("KOWA_SANDBOX_REPOSITORY_ID")
	projectURL := os.Getenv("KOWA_SANDBOX_REPOSITORY_URL")
	baseOID := os.Getenv("KOWA_SANDBOX_BASE_OID")
	knowledgeID := os.Getenv("KOWA_SANDBOX_KNOWLEDGE_ID")
	knowledgeURL := os.Getenv("KOWA_SANDBOX_KNOWLEDGE_URL")
	knowledgeOID := os.Getenv("KOWA_SANDBOX_KNOWLEDGE_OID")
	executionUserID := os.Getenv("KOWA_SANDBOX_EXECUTION_USER_ID")
	if projectID == "" || projectURL == "" || baseOID == "" || knowledgeID == "" ||
		knowledgeURL == "" || knowledgeOID == "" || executionUserID == "" {
		t.Fatal("sandbox probe requires the full frozen sandbox environment")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	channel := newGitChannel(
		map[string]string{projectID: projectURL, knowledgeID: knowledgeURL},
		[]string{"main", "master"},
		ghIdentityResolver(os.Environ()), os.Environ(), logger)
	workspace := t.TempDir()
	ctx := t.Context()

	randomSuffix, err := randomHex(4)
	if err != nil {
		t.Fatal(err)
	}
	workBranch := "refs/heads/task/s04-m3-" + randomSuffix
	cleanedUp := false
	t.Cleanup(func() {
		if cleanedUp {
			return
		}
		// t.Context() is already canceled by the time cleanups run, and the
		// self-created branch must still be removed after a failed assertion, so
		// the cleanup gets its own bounded context instead of the test's.
		cleanupContext, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancelCleanup()
		if cleanupErr := deleteRemoteBranch(cleanupContext, channel,
			filepath.Join(workspace, "repos", projectID), projectURL, workBranch); cleanupErr != nil {
			t.Errorf("cleanup failed: %v", cleanupErr)
		}
	})

	// Read-only pre-assertion: the frozen baselines must be exactly what the
	// authorization listed before anything touches the repository.
	if head := remoteHead(t, channel, projectURL, "refs/heads/main"); head != baseOID {
		t.Fatalf("main = %s, want frozen %s", head, baseOID)
	}

	// Materialize both frozen references through the channel.
	projectScope := execution.GitScope{RepositoryID: projectID, Role: "project", Operation: "clone", Ref: baseOID}
	knowledgeScope := execution.GitScope{RepositoryID: knowledgeID, Role: "knowledge", Operation: "clone", Ref: knowledgeOID}
	if err := channel.materializeScope(ctx, projectScope, workspace, "sandbox-probe"); err != nil {
		t.Fatalf("project materialization = %v", err)
	}
	if err := channel.materializeScope(ctx, knowledgeScope, workspace, "sandbox-probe"); err != nil {
		t.Fatalf("knowledge materialization = %v", err)
	}
	projectHead := localHead(t, filepath.Join(workspace, "repos", projectID))
	if projectHead != baseOID {
		t.Fatalf("materialized project head = %s, want %s", projectHead, baseOID)
	}
	knowledgeHead := localHead(t, filepath.Join(workspace, "repos", knowledgeID))
	if knowledgeHead != knowledgeOID {
		t.Fatalf("materialized knowledge head = %s, want %s", knowledgeHead, knowledgeOID)
	}

	// The task carries the same frozen read scopes the delivery baseline check
	// re-verifies before anything is pushed: HEAD must still descend from each
	// materialized reference, so the real acceptance exercises that gate too.
	task := execution.TaskSpec{
		ID: "sandbox-probe-" + randomSuffix, GitExecutionUserID: executionUserID,
		GitScopes: []execution.GitScope{projectScope, knowledgeScope},
	}

	// Refusal paths run entirely locally: knowledge writes, protected refs and
	// publish_pr never reach the remote.
	knowledgeWrite := execution.GitScope{RepositoryID: knowledgeID, Role: "knowledge", Operation: "push", Ref: workBranch}
	knowledgeWriteErr := func() error {
		_, err := channel.deliverScope(ctx, knowledgeWrite, task, workspace, "sandbox-probe")
		return err
	}()
	if knowledgeWriteErr == nil || !strings.Contains(knowledgeWriteErr.Error(), "git_scope_refused") {
		t.Fatalf("knowledge write = %v, want refusal", knowledgeWriteErr)
	}
	// The protected ref must be given in the frozen full form, otherwise the
	// bare-name syntax refusal would fire first and this would prove nothing
	// about the protection predicate itself.
	protected := execution.GitScope{
		RepositoryID: projectID, Role: "project", Operation: "push", Ref: "refs/heads/main"}
	protectedErr := func() error {
		_, err := channel.deliverScope(ctx, protected, task, workspace, "sandbox-probe")
		return err
	}()
	if protectedErr == nil || !strings.Contains(protectedErr.Error(), "git_scope_refused") ||
		!strings.Contains(protectedErr.Error(), "is protected") {
		t.Fatalf("protected ref = %v, want the protection predicate to refuse it", protectedErr)
	}
	publishPR := execution.GitScope{RepositoryID: projectID, Role: "project", Operation: "publish_pr", Ref: workBranch}
	publishPRErr := func() error {
		_, err := channel.deliverScope(ctx, publishPR, task, workspace, "sandbox-probe")
		return err
	}()
	if publishPRErr == nil || !strings.Contains(publishPRErr.Error(), "git_scope_refused") {
		t.Fatalf("publish_pr = %v, want refusal", publishPRErr)
	}

	// One synthetic probe commit, delivered through the controlled channel.
	probeFile := filepath.Join(workspace, "repos", projectID, "README.md")
	existing, err := os.ReadFile(probeFile)
	if err != nil {
		t.Fatal(err)
	}
	probeLine := "\n<!-- kowa S04 M3 controlled delivery probe " + randomSuffix + " -->\n"
	if err := os.WriteFile(probeFile, append(existing, []byte(probeLine)...), 0o644); err != nil {
		t.Fatal(err)
	}
	pushScope := execution.GitScope{RepositoryID: projectID, Role: "project", Operation: "push", Ref: workBranch}
	delivery, err := channel.deliverScope(ctx, pushScope, task, workspace, "sandbox-probe")
	if err != nil {
		t.Fatalf("controlled delivery = %v", err)
	}
	if delivery.Ref != workBranch || delivery.BaseOid != baseOID || delivery.CommitOid == "" ||
		delivery.CommitOid == baseOID || delivery.Reconciled {
		t.Fatalf("delivery record = %+v", delivery)
	}

	// The remote branch must be exactly the delivered commit (channel already
	// reconciled during delivery; assert again from outside).
	if remote := remoteHead(t, channel, projectURL, workBranch); remote != delivery.CommitOid {
		t.Fatalf("remote branch %s = %s, want delivered %s", workBranch, remote, delivery.CommitOid)
	}

	// The delivery evidence must record who actually authored and committed.
	// The repository is public, so the committed evidence carries only the
	// presence of the ident and a digest of the address, never the address.
	projectNameHash := sha256.Sum256([]byte(projectID))
	identBytes, err := os.ReadFile(
		filepath.Join(workspace, "delivery-"+hex.EncodeToString(projectNameHash[:4])+".json"))
	if err != nil {
		t.Fatalf("delivery evidence = %v", err)
	}
	var ident struct {
		AuthorName     string `json:"authorName"`
		AuthorEmail    string `json:"authorEmail"`
		CommitterName  string `json:"committerName"`
		CommitterEmail string `json:"committerEmail"`
	}
	if err := json.Unmarshal(identBytes, &ident); err != nil {
		t.Fatal(err)
	}
	if ident.AuthorName == "" || ident.AuthorEmail == "" ||
		ident.CommitterName == "" || ident.CommitterEmail == "" {
		t.Fatalf("delivery evidence does not record the actual commit ident: %s", identBytes)
	}
	authorEmailDigest := sha256.Sum256([]byte(ident.AuthorEmail))
	committerEmailDigest := sha256.Sum256([]byte(ident.CommitterEmail))

	evidence := map[string]any{
		"branch": workBranch, "baseOid": baseOID, "commitOid": delivery.CommitOid,
		"remoteOid": delivery.CommitOid, "reconciled": delivery.Reconciled,
		"materializedProjectOid": projectHead, "materializedKnowledgeOid": knowledgeHead,
		"executionUserId":    executionUserID,
		"authorIdentPresent": true, "committerIdentPresent": true,
		"authorEmailSha256Prefix":    hex.EncodeToString(authorEmailDigest[:8]),
		"committerEmailSha256Prefix": hex.EncodeToString(committerEmailDigest[:8]),
		"refusals": map[string]string{
			"knowledge-write": knowledgeWriteErr.Error(),
			"protected-ref":   protectedErr.Error(),
			"publish_pr":      publishPRErr.Error(),
		},
		"observedAt": time.Now().UTC().Format(time.RFC3339),
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("KOWA_SANDBOX_EVIDENCE_PATH"); path != "" {
		// The evidence path is given relative to the repository root, while the
		// test binary runs in the package directory.
		if !filepath.IsAbs(path) {
			path = filepath.Join(repositoryRoot(t), path)
		}
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("sandbox controlled delivery evidence:\n%s", encoded)

	// Cleanup: delete only the self-created branch, then assert the frozen
	// refs are untouched.
	if err := deleteRemoteBranch(ctx, channel,
		filepath.Join(workspace, "repos", projectID), projectURL, workBranch); err != nil {
		t.Fatalf("branch cleanup = %v", err)
	}
	cleanedUp = true
	if remote := remoteHead(t, channel, projectURL, workBranch); remote != "" {
		t.Fatalf("branch still present after cleanup: %s", remote)
	}
	if head := remoteHead(t, channel, projectURL, "refs/heads/main"); head != baseOID {
		t.Fatalf("main changed after probe: %s", head)
	}
}

// remoteHead reads one remote branch head through the channel's git runner.
// remoteHead reads one remote ref head through the channel's git runner; ref
// is the full frozen ref (refs/heads/<branch>). One transient SSH/network
// failure is retried, and a persistent failure reports the captured stderr
// instead of silently returning empty.
func remoteHead(t *testing.T, channel *gitChannel, url, ref string) string {
	t.Helper()
	var last gitResult
	for attempt := 0; attempt < 2; attempt++ {
		directory := t.TempDir()
		last = channel.execute(t.Context(), directory, directory, "sandbox-probe",
			"ls-remote", url, ref)
		if last.err == nil && last.exitCode == 0 {
			break
		}
		if attempt == 0 {
			time.Sleep(time.Second)
		}
	}
	if last.err != nil || last.exitCode != 0 {
		t.Fatalf("ls-remote %s failed: err=%v exit=%d stderr=%s",
			ref, last.err, last.exitCode, strings.TrimSpace(last.stderr))
	}
	fields := strings.Fields(strings.TrimSpace(last.stdout))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func localHead(t *testing.T, directory string) string {
	t.Helper()
	command := proc.Spec{Command: []string{"git", "-C", directory, "rev-parse", "HEAD"},
		Dir: directory, Env: os.Environ(),
		StdoutPath: filepath.Join(t.TempDir(), "stdout"),
		StderrPath: filepath.Join(t.TempDir(), "stderr"), Marker: "sandbox-probe-local"}
	if err := os.MkdirAll(filepath.Dir(command.StdoutPath), 0o750); err != nil {
		t.Fatal(err)
	}
	outcome, runErr := proc.Run(t.Context(), command)
	if runErr != nil || outcome.ExitCode != 0 {
		t.Fatalf("local rev-parse: %v", runErr)
	}
	encoded, err := os.ReadFile(command.StdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(encoded))
}

// deleteRemoteBranch removes one self-created task branch with an explicit
// refspec from an existing repository context (git push needs one even with an
// explicit url); it never touches any other ref.
func deleteRemoteBranch(ctx context.Context, channel *gitChannel, repoDirectory, url, branch string) error {
	if !strings.HasPrefix(branch, "refs/heads/task/s04-m3-") {
		return errors.New("refusing to delete a branch this probe did not create")
	}
	result := channel.execute(ctx, repoDirectory, repoDirectory, "sandbox-probe",
		"push", url, "--delete", branch)
	if result.err != nil && !processFailedButRan(result.err) {
		return result.err
	}
	if result.exitCode != 0 {
		return errors.New("git push --delete exited: " + result.stderr)
	}
	return nil
}

// TestSandboxCleanupSelfCreatedBranch completes the authorized cleanup path
// against a leftover self-created probe branch without creating a new commit:
//
//	KOWA_SANDBOX_CLEANUP_BRANCH=task/s04-m3-xxxxxxxx plus the frozen env values
//	go test -count=1 -tags sandboxprobe -run TestSandboxCleanupSelfCreatedBranch -v ./internal/runner/
func TestSandboxCleanupSelfCreatedBranch(t *testing.T) {
	if os.Getenv("KOWA_SANDBOX_PROBE") != "true" {
		t.Skip("set KOWA_SANDBOX_PROBE=true to run the authorized sandbox cleanup")
	}
	branch := os.Getenv("KOWA_SANDBOX_CLEANUP_BRANCH")
	projectID := os.Getenv("KOWA_SANDBOX_REPOSITORY_ID")
	projectURL := os.Getenv("KOWA_SANDBOX_REPOSITORY_URL")
	baseOID := os.Getenv("KOWA_SANDBOX_BASE_OID")
	if branch == "" || projectID == "" || projectURL == "" || baseOID == "" {
		t.Fatal("cleanup requires the frozen sandbox environment plus the branch name")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	channel := newGitChannel(map[string]string{projectID: projectURL},
		[]string{"main", "master"}, ghIdentityResolver(os.Environ()), os.Environ(), logger)
	ctx := t.Context()

	// A scratch repository provides the push context; no clone data is needed.
	scratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(scratch, 0o750); err != nil {
		t.Fatal(err)
	}
	init := channel.execute(ctx, scratch, scratch, "sandbox-probe", "init", "--quiet")
	if init.exitCode != 0 {
		t.Fatalf("scratch init failed: %s", init.stderr)
	}
	if before := remoteHead(t, channel, projectURL, branch); before == "" {
		t.Fatalf("branch %s is already absent", branch)
	}
	if err := deleteRemoteBranch(ctx, channel, scratch, projectURL, branch); err != nil {
		t.Fatalf("cleanup = %v", err)
	}
	if after := remoteHead(t, channel, projectURL, branch); after != "" {
		t.Fatalf("branch survived cleanup: %s", after)
	}
	if head := remoteHead(t, channel, projectURL, "refs/heads/main"); head != baseOID {
		t.Fatalf("main changed: %s", head)
	}
	evidence, _ := json.MarshalIndent(map[string]any{
		"deletedRef": branch, "mainAfter": baseOID,
		"observedAt": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	t.Logf("sandbox cleanup evidence: %s", evidence)
}

// repositoryRoot walks up from the package directory to the module root, so an
// evidence path given relative to the repository resolves the same way however
// the test binary was started.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root with go.mod was not found")
		}
		directory = parent
	}
}
