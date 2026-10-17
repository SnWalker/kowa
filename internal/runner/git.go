package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
)

// gitFailure is one controlled-Git refusal or failure, classified for the
// frozen runner error taxonomy.
type gitFailure struct {
	Code      string
	Message   string
	Retryable bool
}

func (f *gitFailure) Error() string { return f.Code + ": " + f.Message }

func gitFailureOf(err error) (*gitFailure, bool) {
	var failure *gitFailure
	if errors.As(err, &failure) {
		return failure, true
	}
	return nil, false
}

var oidPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var branchRefPattern = regexp.MustCompile(`^refs/heads/([A-Za-z0-9][A-Za-z0-9._/-]{0,254})$`)

// scopeRefKind distinguishes the two frozen ref forms of kowa.workflow-execution.v1:
// a full branch ref (`refs/heads/<branch>`, as the S02 examples and the actual
// producer emit) or a full commit OID. Any other shape is refused; the channel
// keeps exactly one grammar and never reinterprets the upstream form.
type scopeRefKind int

const (
	scopeRefCommit scopeRefKind = iota
	scopeRefBranch
)

func parseScopeRef(ref string) (scopeRefKind, string, error) {
	if oidPattern.MatchString(ref) {
		return scopeRefCommit, ref, nil
	}
	match := branchRefPattern.FindStringSubmatch(ref)
	if match == nil || strings.Contains(match[1], "..") || strings.HasSuffix(match[1], "/") {
		return 0, "", &gitFailure{
			Code:    "git_scope_refused",
			Message: fmt.Sprintf("ref %q is not a frozen refs/heads/<branch> reference or commit oid", ref),
		}
	}
	return scopeRefBranch, match[1], nil
}

// normalizeProtectedRef folds a configured protected ref into its branch form.
func normalizeProtectedRef(ref string) string {
	return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
}

// gitCommand is one executed command captured as delivery evidence.
type gitCommand struct {
	Argv     []string `json:"argv"`
	ExitCode int      `json:"exitCode"`
}

// gitChannel performs every Git operation the Runner executes on behalf of a
// task, strictly inside the task's frozen gitScopes. It never reads provider
// workspaces for credentials and never guesses a remote for an unregistered
// repository id.
type gitChannel struct {
	repositories  map[string]string
	protectedRefs map[string]bool
	identity      func(context.Context) (string, error)
	environment   []string
	logger        *slog.Logger
	now           func() time.Time
	grace         time.Duration
	// run is injectable so tests can simulate unknown push outcomes.
	run func(context.Context, proc.Spec) (proc.Outcome, error)

	mu             sync.Mutex
	commandCounter int
}

func newGitChannel(
	repositories map[string]string,
	protectedRefs []string,
	identity func(context.Context) (string, error),
	environment []string,
	logger *slog.Logger,
) *gitChannel {
	channel := &gitChannel{
		repositories:  repositories,
		protectedRefs: map[string]bool{},
		identity:      identity,
		environment:   environment,
		logger:        logger,
		now:           time.Now,
		grace:         10 * time.Second,
		run:           proc.Run,
	}
	for _, ref := range protectedRefs {
		channel.protectedRefs[normalizeProtectedRef(ref)] = true
	}
	return channel
}

type gitResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

// execute runs one git command inside the attempt, capturing bounded output in
// the attempt log directory (never inside the repository worktree, which would
// pollute the delivered change set).
func (c *gitChannel) execute(
	ctx context.Context,
	directory string,
	logDirectory string,
	marker string,
	arguments ...string,
) gitResult {
	return c.executeTool(ctx, directory, logDirectory, marker, "git", arguments...)
}

// executeTool runs one controlled tool (git, or gh for the publish_pr write)
// inside the attempt, capturing bounded output in the attempt log directory.
func (c *gitChannel) executeTool(
	ctx context.Context,
	directory string,
	logDirectory string,
	marker string,
	tool string,
	arguments ...string,
) gitResult {
	c.mu.Lock()
	c.commandCounter++
	commandIndex := c.commandCounter
	c.mu.Unlock()
	stdout := filepath.Join(logDirectory, fmt.Sprintf("%s-%d-stdout.log", tool, commandIndex))
	stderr := filepath.Join(logDirectory, fmt.Sprintf("%s-%d-stderr.log", tool, commandIndex))
	if err := os.MkdirAll(logDirectory, 0o750); err != nil {
		return gitResult{err: fmt.Errorf("create %s log directory: %w", tool, err)}
	}
	processContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	outcome, runErr := c.run(processContext, proc.Spec{
		Command: append([]string{tool}, arguments...), Dir: directory,
		Env: c.environment, StdoutPath: stdout, StderrPath: stderr,
		Marker: marker, Grace: c.grace,
	})
	outcomeBytes, _ := os.ReadFile(stdout)
	errorBytes, _ := os.ReadFile(stderr)
	result := gitResult{
		stdout: string(outcomeBytes), stderr: string(errorBytes),
		exitCode: outcome.ExitCode, err: runErr,
	}
	if runErr != nil && !processFailedButRan(runErr) {
		result.err = runErr
	}
	if outcome.TimedOut || outcome.Canceled {
		result.err = fmt.Errorf("git command did not finish: %v", processContext.Err())
	}
	return result
}

func processFailedButRan(err error) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError)
}

func (c *gitChannel) cloneURL(repositoryID string) (string, error) {
	url, ok := c.repositories[repositoryID]
	if !ok {
		return "", &gitFailure{
			Code:    "git_scope_refused",
			Message: fmt.Sprintf("repository %s is not registered on this machine", repositoryID),
		}
	}
	return url, nil
}

// materializedDirectory returns the per-repository checkout path.
func materializedDirectory(workspace, repositoryID string) string {
	return filepath.Join(workspace, "repos", repositoryID)
}

type materializationEvidence struct {
	RepositoryID string       `json:"repositoryId"`
	Role         string       `json:"role"`
	Operation    string       `json:"operation"`
	Ref          string       `json:"ref"`
	Head         string       `json:"head"`
	Commands     []gitCommand `json:"commands"`
	ObservedAt   time.Time    `json:"observedAt"`
}

// materializeScope checks out one frozen scope into the attempt workspace. A
// 40-hex ref must resolve to exactly that commit; branch refs resolve through
// origin. Knowledge repositories are never modified.
func (c *gitChannel) materializeScope(
	ctx context.Context,
	scope execution.GitScope,
	workspace string,
	marker string,
) error {
	url, err := c.cloneURL(scope.RepositoryID)
	if err != nil {
		return err
	}
	refKind, refValue, err := parseScopeRef(scope.Ref)
	if err != nil {
		return err
	}
	directory := materializedDirectory(workspace, scope.RepositoryID)
	evidence := materializationEvidence{
		RepositoryID: scope.RepositoryID, Role: scope.Role, Operation: scope.Operation,
		Ref: scope.Ref, ObservedAt: c.now().UTC(),
	}
	record := func(result gitResult, arguments ...string) error {
		evidence.Commands = append(evidence.Commands, gitCommand{
			Argv: append([]string{"git"}, arguments...), ExitCode: result.exitCode,
		})
		if result.err != nil {
			return &gitFailure{Code: "git_materialize_failed",
				Message: fmt.Sprintf("git %s failed: %v", strings.Join(arguments, " "), result.err), Retryable: true}
		}
		if result.exitCode != 0 {
			return &gitFailure{Code: "git_materialize_failed",
				Message: fmt.Sprintf("git %s exited %d: %s", strings.Join(arguments, " "), result.exitCode,
					proc.Truncate(strings.TrimSpace(result.stderr), 400)), Retryable: true}
		}
		return nil
	}
	defer func() {
		if writeErr := c.writeMaterializationEvidence(workspace, evidence); writeErr != nil {
			c.logger.Warn("write materialization evidence", "error", writeErr)
		}
	}()

	logDirectory := filepath.Join(workspace, "logs")
	if _, statErr := os.Stat(filepath.Join(directory, ".git")); statErr != nil {
		if mkdirErr := os.MkdirAll(filepath.Join(workspace, "repos"), 0o750); mkdirErr != nil {
			return &gitFailure{Code: "git_materialize_failed", Message: mkdirErr.Error(), Retryable: true}
		}
		if err := record(c.execute(ctx, filepath.Join(workspace, "repos"), logDirectory, marker,
			"clone", "--quiet", url, directory),
			"clone", "--quiet", "<url>", "<dir>"); err != nil {
			return err
		}
	} else {
		// A plain `git fetch <url>` only updates FETCH_HEAD; branch refs would
		// then be checked out from a stale origin/<branch>. Fetch through the
		// clone's origin remote (its default refspec refreshes origin/*) and
		// verify the workspace remote identity first.
		remoteURL := c.execute(ctx, directory, logDirectory, marker, "config", "--get", "remote.origin.url")
		if remoteURL.exitCode != 0 || strings.TrimSpace(remoteURL.stdout) != url {
			return &gitFailure{Code: "git_scope_refused",
				Message: "existing workspace remote does not match the registered repository identity"}
		}
		if err := record(c.execute(ctx, directory, logDirectory, marker, "fetch", "--quiet", "origin"),
			"fetch", "--quiet", "origin"); err != nil {
			return err
		}
	}
	if refKind == scopeRefCommit {
		if result := c.execute(ctx, directory, logDirectory, marker, "checkout", "--quiet", "--detach", refValue); result.exitCode != 0 {
			// The object may not be reachable from any fetched branch: fetch it by oid.
			if err := record(c.execute(ctx, directory, logDirectory, marker, "fetch", "--quiet", url, refValue),
				"fetch", "--quiet", "<url>", refValue); err != nil {
				return err
			}
			if err := record(c.execute(ctx, directory, logDirectory, marker, "checkout", "--quiet", "--detach", refValue),
				"checkout", "--quiet", "--detach", refValue); err != nil {
				return err
			}
		} else {
			evidence.Commands = append(evidence.Commands, gitCommand{
				Argv:     []string{"git", "checkout", "--quiet", "--detach", refValue},
				ExitCode: 0,
			})
		}
	} else {
		if err := record(c.execute(ctx, directory, logDirectory, marker, "checkout", "--quiet", "--detach", "origin/"+refValue),
			"checkout", "--quiet", "--detach", "origin/<branch>"); err != nil {
			return err
		}
	}
	head := c.execute(ctx, directory, logDirectory, marker, "rev-parse", "HEAD")
	if head.exitCode != 0 {
		return &gitFailure{Code: "git_materialize_failed", Message: "could not read checked-out commit"}
	}
	evidence.Head = strings.TrimSpace(head.stdout)
	if refKind == scopeRefCommit {
		// A commit ref is a frozen input: the checkout must be exactly that
		// commit, and rebuilding the directory later must yield the same OID.
		if evidence.Head != refValue {
			return &gitFailure{Code: "git_materialize_failed",
				Message: fmt.Sprintf("checked out %s but the frozen ref is %s", evidence.Head, refValue)}
		}
		return nil
	}
	// A branch ref is an authorized moving ref: the checkout must equal the
	// remote tip fetched in this run (never a stale origin/<branch>).
	tip := c.execute(ctx, directory, logDirectory, marker, "rev-parse", "origin/"+refValue)
	if tip.exitCode != 0 {
		return &gitFailure{Code: "git_materialize_failed",
			Message: fmt.Sprintf("origin/%s could not be resolved after fetch", refValue)}
	}
	if resolved := strings.TrimSpace(tip.stdout); resolved != evidence.Head {
		return &gitFailure{Code: "git_materialize_failed",
			Message: fmt.Sprintf("checked out %s but origin/%s is %s", evidence.Head, refValue, resolved)}
	}
	return nil
}

func (c *gitChannel) writeMaterializationEvidence(workspace string, evidence materializationEvidence) error {
	path := filepath.Join(workspace, "materialization.json")
	var existing []materializationEvidence
	if encoded, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(encoded, &existing)
	}
	existing = append(existing, evidence)
	encoded, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(encoded, '\n'))
}

// deliveryRecord is the result of one controlled commit/push, or of one
// publish_pr write: the pull request fields are set only for the latter.
type deliveryRecord struct {
	RepositoryID string `json:"repositoryId"`
	Ref          string `json:"ref"`
	CommitOid    string `json:"commitOid"`
	BaseOid      string `json:"baseOid"`
	Reconciled   bool   `json:"reconciled"`
	// PullRequestNumber and PullRequestURL name the one pull request the
	// controlled channel created (or reused) for the delivered head. The
	// control plane settles the admission operation by reading GitHub itself;
	// these fields only carry what the write answered.
	PullRequestNumber string `json:"pullRequestNumber,omitempty"`
	PullRequestURL    string `json:"pullRequestUrl,omitempty"`
}

type deliveryEvidence struct {
	RepositoryID string `json:"repositoryId"`
	Role         string `json:"role"`
	Operation    string `json:"operation"`
	Ref          string `json:"ref"`
	BaseOid      string `json:"baseOid"`
	LocalHead    string `json:"localHead"`
	RemoteOid    string `json:"remoteOid"`
	Reconciled   bool   `json:"reconciled"`
	// PullRequestBase and PullRequestNumber are set for a publish_pr write only.
	PullRequestBase   string       `json:"pullRequestBase,omitempty"`
	PullRequestNumber string       `json:"pullRequestNumber,omitempty"`
	Identity          string       `json:"githubUserId"`
	GitUserName       string       `json:"gitUserName"`
	GitUserMail       string       `json:"gitUserEmail"`
	AuthorName        string       `json:"authorName"`
	AuthorEmail       string       `json:"authorEmail"`
	CommitterName     string       `json:"committerName"`
	CommitterEmail    string       `json:"committerEmail"`
	Commands          []gitCommand `json:"commands"`
	ObservedAt        time.Time    `json:"observedAt"`
}

// deliverScope commits workspace changes to the approved work branch and
// pushes them, reconciling unknown push outcomes against remote reality.
func (c *gitChannel) deliverScope(
	ctx context.Context,
	scope execution.GitScope,
	task execution.TaskSpec,
	workspace string,
	marker string,
) (deliveryRecord, error) {
	refuse := func(message string) (deliveryRecord, error) {
		return deliveryRecord{}, &gitFailure{Code: "git_scope_refused", Message: message}
	}
	if scope.Role != "project" {
		return refuse(fmt.Sprintf("role %q may not be written", scope.Role))
	}
	switch scope.Operation {
	case "commit", "push", "publish_pr":
	default:
		return refuse(fmt.Sprintf("operation %q may not be delivered", scope.Operation))
	}
	refKind, refValue, refErr := parseScopeRef(scope.Ref)
	if refErr != nil {
		return deliveryRecord{}, refErr
	}
	if refKind != scopeRefBranch {
		return refuse("delivery requires a frozen refs/heads/<branch> reference")
	}
	if c.protectedRefs[refValue] {
		return refuse(fmt.Sprintf("ref %q is protected", scope.Ref))
	}
	url, err := c.cloneURL(scope.RepositoryID)
	if err != nil {
		return deliveryRecord{}, err
	}
	directory := materializedDirectory(workspace, scope.RepositoryID)
	if _, statErr := os.Stat(filepath.Join(directory, ".git")); statErr != nil {
		return refuse("project repository was not materialized for this attempt")
	}
	remoteURL := c.execute(ctx, directory, filepath.Join(workspace, "logs"), marker,
		"config", "--get", "remote.origin.url")
	if remoteURL.exitCode != 0 || strings.TrimSpace(remoteURL.stdout) != url {
		return refuse("workspace remote does not match the registered repository identity")
	}
	if task.GitExecutionUserID == "" {
		return deliveryRecord{}, &gitFailure{Code: "git_identity_mismatch",
			Message: "the task did not freeze a git execution identity"}
	}
	actualIdentity, identityErr := c.identity(ctx)
	if identityErr != nil {
		return deliveryRecord{}, &gitFailure{Code: "git_identity_mismatch",
			Message: "could not verify the machine GitHub identity: " + identityErr.Error()}
	}
	if actualIdentity != task.GitExecutionUserID {
		return deliveryRecord{}, &gitFailure{Code: "git_identity_mismatch",
			Message: fmt.Sprintf("machine GitHub identity %s does not match the frozen execution identity %s",
				actualIdentity, task.GitExecutionUserID)}
	}
	evidence := deliveryEvidence{
		RepositoryID: scope.RepositoryID, Role: scope.Role, Operation: scope.Operation,
		Ref: scope.Ref, Identity: actualIdentity, ObservedAt: c.now().UTC(),
	}
	commands := &evidence.Commands
	run := func(arguments ...string) gitResult {
		result := c.execute(ctx, directory, filepath.Join(workspace, "logs"), marker, arguments...)
		*commands = append(*commands, gitCommand{
			Argv: append([]string{"git"}, arguments...), ExitCode: result.exitCode,
		})
		return result
	}
	writeEvidence := func() {
		if writeErr := writeDeliveryEvidence(workspace, scope.RepositoryID, evidence); writeErr != nil {
			c.logger.Warn("write delivery evidence", "error", writeErr)
		}
	}

	if scope.Operation == "publish_pr" {
		return c.publishPullRequest(ctx, scope, workspace, marker, url, directory, run, writeEvidence, &evidence)
	}

	// F5: the checkout must still be based on this task's frozen materialized
	// refs. Worker commits ahead of the baseline are legal; a reset/checkout to
	// a different commit is refused before anything is delivered.
	for _, readScope := range task.GitScopes {
		if readScope.RepositoryID != scope.RepositoryID {
			continue
		}
		switch readScope.Operation {
		case "clone", "fetch", "pull":
		default:
			continue
		}
		refKind, refValue, refErr := parseScopeRef(readScope.Ref)
		if refErr != nil {
			writeEvidence()
			return deliveryRecord{}, refErr
		}
		expected := refValue
		if refKind == scopeRefBranch {
			tip := run("rev-parse", "origin/"+refValue)
			if tip.exitCode != 0 {
				writeEvidence()
				return deliveryRecord{}, &gitFailure{Code: "git_baseline_mismatch",
					Message: fmt.Sprintf("origin/%s cannot be resolved for the baseline check", refValue)}
			}
			expected = strings.TrimSpace(tip.stdout)
		}
		ancestor := run("merge-base", "--is-ancestor", expected, "HEAD")
		if ancestor.exitCode != 0 {
			writeEvidence()
			return deliveryRecord{}, &gitFailure{Code: "git_baseline_mismatch",
				Message: fmt.Sprintf("workspace HEAD is not based on the materialized ref %s", readScope.Ref)}
		}
	}

	if name := run("config", "user.name"); name.exitCode == 0 {
		evidence.GitUserName = strings.TrimSpace(name.stdout)
	}
	if mail := run("config", "user.email"); mail.exitCode == 0 {
		evidence.GitUserMail = strings.TrimSpace(mail.stdout)
	}
	head := run("rev-parse", "HEAD")
	if head.exitCode != 0 {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "git_push_failed", Message: "workspace has no HEAD commit"}
	}
	evidence.BaseOid = strings.TrimSpace(head.stdout)
	status := run("status", "--porcelain")
	if status.exitCode != 0 {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "git_push_failed", Message: "could not read workspace status"}
	}
	if strings.TrimSpace(status.stdout) == "" {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "no_changes",
			Message: "the attempt produced no workspace changes to deliver"}
	}
	if add := run("add", "-A"); add.exitCode != 0 {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "git_push_failed", Message: "git add failed"}
	}
	message := fmt.Sprintf("kowa: deliver %s", task.ID)
	if commit := run("commit", "--quiet", "-m", message); commit.exitCode != 0 {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "git_push_failed",
			Message: "git commit failed: " + proc.Truncate(strings.TrimSpace(commit.stderr), 300)}
	}
	committed := run("rev-parse", "HEAD")
	evidence.LocalHead = strings.TrimSpace(committed.stdout)
	if ident := run("log", "-1", "--format=%an|%ae|%cn|%ce"); ident.exitCode == 0 {
		parts := strings.Split(strings.TrimSpace(ident.stdout), "|")
		if len(parts) == 4 {
			evidence.AuthorName, evidence.AuthorEmail = parts[0], parts[1]
			evidence.CommitterName, evidence.CommitterEmail = parts[2], parts[3]
		}
	}
	record := deliveryRecord{
		RepositoryID: scope.RepositoryID, Ref: scope.Ref,
		CommitOid: evidence.LocalHead, BaseOid: evidence.BaseOid,
	}
	if scope.Operation == "commit" {
		writeEvidence()
		return record, nil
	}
	pushRefspec := "HEAD:" + scope.Ref
	push := run("push", "--quiet", url, pushRefspec)
	pushFailed := push.exitCode != 0 || push.err != nil
	// Reconcile against remote reality regardless of the push result: a lost
	// or interrupted response never justifies a blind retry.
	listRemote := run("ls-remote", url, scope.Ref)
	remoteOid := ""
	remoteReadable := listRemote.exitCode == 0 && listRemote.err == nil
	if remoteReadable {
		fields := strings.Fields(strings.TrimSpace(listRemote.stdout))
		if len(fields) >= 1 && oidPattern.MatchString(fields[0]) {
			remoteOid = fields[0]
		}
	}
	evidence.RemoteOid = remoteOid
	evidence.Reconciled = pushFailed && remoteOid == evidence.LocalHead
	writeEvidence()
	if remoteOid == evidence.LocalHead {
		record.Reconciled = pushFailed
		return record, nil
	}
	if remoteOid != "" {
		return deliveryRecord{}, &gitFailure{Code: "git_push_conflict",
			Message: fmt.Sprintf("remote ref %s is at %s, not the delivered %s",
				scope.Ref, remoteOid, evidence.LocalHead)}
	}
	if !remoteReadable {
		return deliveryRecord{}, &gitFailure{Code: "git_push_unknown",
			Message: "push outcome could not be reconciled against the remote"}
	}
	return deliveryRecord{}, &gitFailure{Code: "git_push_failed",
		Message: "push failed: " + proc.Truncate(strings.TrimSpace(push.stderr), 300)}
}

// publishPullRequest performs the write of one publish_pr attempt: exactly one
// pull request is opened for the frozen working branch with the machine's own
// gh identity, or the pull request an earlier attempt already opened for that
// head is reused. The control plane keeps owning admission and fact
// acceptance — it settles the operation by reading GitHub itself — so this
// method only performs the write and reports what GitHub answers.
func (c *gitChannel) publishPullRequest(
	ctx context.Context,
	scope execution.GitScope,
	workspace string,
	marker string,
	url string,
	directory string,
	run func(arguments ...string) gitResult,
	writeEvidence func(),
	evidence *deliveryEvidence,
) (deliveryRecord, error) {
	// The head is the frozen working branch's remote tip: the code delivery
	// pushed it and verify_change reconciled it. The delivered commit itself
	// stays frozen in the control plane's admission record.
	remoteHead := ""
	if listed := run("ls-remote", url, scope.Ref); listed.exitCode == 0 {
		fields := strings.Fields(strings.TrimSpace(listed.stdout))
		if len(fields) >= 1 && oidPattern.MatchString(fields[0]) {
			remoteHead = fields[0]
		}
	}
	if remoteHead == "" {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "git_push_unknown",
			Message: fmt.Sprintf("the working branch %s is not readable on the remote", scope.Ref)}
	}
	evidence.RemoteOid = remoteHead
	branch := strings.TrimPrefix(scope.Ref, "refs/heads/")
	// The attempt workspace was cloned at the frozen baseline, so the delivered
	// branch exists only on the remote: fetch it into the local remote-tracking
	// namespace so the pull request tooling resolves exactly the head that was
	// just read, and so the write re-verifies the remote tip at this moment.
	fetched := run("fetch", "--quiet", url, scope.Ref+":refs/remotes/origin/"+branch)
	if fetched.exitCode != 0 {
		writeEvidence()
		return deliveryRecord{}, &gitFailure{Code: "git_push_unknown",
			Message: fmt.Sprintf("the working branch %s could not be fetched for publication", scope.Ref)}
	}
	slug, err := githubSlug(url)
	if err != nil {
		writeEvidence()
		return deliveryRecord{}, err
	}
	logDirectory := filepath.Join(workspace, "logs")
	base, err := c.pullRequestBase(ctx, directory, logDirectory, marker, url)
	if err != nil {
		writeEvidence()
		return deliveryRecord{}, err
	}
	evidence.PullRequestBase = base
	number, pullURL, err := c.ensurePullRequest(ctx, directory, logDirectory, marker, slug, base, branch, remoteHead, evidence)
	if err != nil {
		writeEvidence()
		return deliveryRecord{}, err
	}
	evidence.PullRequestNumber = number
	writeEvidence()
	return deliveryRecord{
		RepositoryID: scope.RepositoryID, Ref: scope.Ref, CommitOid: remoteHead,
		PullRequestNumber: number, PullRequestURL: pullURL,
	}, nil
}

// githubSlug converts one registered clone URL into the owner/name slug the
// GitHub CLI addresses repositories by.
func githubSlug(url string) (string, error) {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(url), "/"), ".git")
	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/", "https://github.com/", "http://github.com/"} {
		if strings.HasPrefix(trimmed, prefix) {
			slug := strings.TrimPrefix(trimmed, prefix)
			if strings.Count(slug, "/") == 1 && !strings.HasPrefix(slug, "/") {
				return slug, nil
			}
			break
		}
	}
	return "", &gitFailure{Code: "git_scope_refused",
		Message: "the registered repository is not a github.com owner/name remote"}
}

// pullRequestBase resolves the protected delivery branch of one repository.
// The frozen delivery governance names main or master; a publish_pr write
// never targets anything else.
func (c *gitChannel) pullRequestBase(
	ctx context.Context,
	directory string,
	logDirectory string,
	marker string,
	url string,
) (string, error) {
	listed := c.execute(ctx, directory, logDirectory, marker,
		"ls-remote", url, "refs/heads/main", "refs/heads/master")
	if listed.exitCode != 0 || listed.err != nil {
		return "", &gitFailure{Code: "git_push_unknown",
			Message: "the protected delivery branch could not be read from the remote"}
	}
	hasMain, hasMaster := false, false
	for _, line := range strings.Split(strings.TrimSpace(listed.stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[1] {
		case "refs/heads/main":
			hasMain = true
		case "refs/heads/master":
			hasMaster = true
		}
	}
	switch {
	case hasMain:
		return "main", nil
	case hasMaster:
		return "master", nil
	}
	return "", &gitFailure{Code: "git_push_failed",
		Message: "the repository has no protected delivery branch (main or master)"}
}

// pullRequestFacts is the subset of one GitHub pull request this channel reads
// back. The author id arrives as a JSON string or number depending on the CLI
// field, so it is normalized on read.
type pullRequestFacts struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	BaseRefName string `json:"baseRefName"`
	HeadRefOid  string `json:"headRefOid"`
	Author      struct {
		ID any `json:"id"`
	} `json:"author"`
}

// ensurePullRequest opens exactly one pull request for the delivered head, or
// reuses the one an earlier attempt already opened: a retried publish never
// creates a second object. Whatever path is taken, the object is read back and
// verified against the frozen facts (state, base, head, author) before the
// attempt reports success.
func (c *gitChannel) ensurePullRequest(
	ctx context.Context,
	directory string,
	logDirectory string,
	marker string,
	slug string,
	base string,
	branch string,
	head string,
	evidence *deliveryEvidence,
) (string, string, error) {
	if existing, found, err := c.listedPullRequest(ctx, directory, logDirectory, marker, slug, branch, evidence); err != nil {
		return "", "", err
	} else if found {
		if err := c.verifyPullRequest(ctx, directory, logDirectory, marker, slug, existing, base, head, evidence); err != nil {
			return "", "", err
		}
		return strconv.Itoa(existing.Number), existing.URL, nil
	}
	title := "Kowa delivery: " + branch
	body := fmt.Sprintf("Opened by the Kowa controlled publish_pr step for branch %s at %s.", branch, head)
	created := c.executeTool(ctx, directory, logDirectory, marker, "gh",
		"pr", "create", "--repo", slug, "--base", base, "--head", branch, "--title", title, "--body", body)
	evidence.Commands = append(evidence.Commands, gitCommand{
		Argv: []string{"gh", "pr", "create", "--repo", slug, "--base", base, "--head", branch}, ExitCode: created.exitCode,
	})
	if created.exitCode != 0 || created.err != nil {
		// The create may have raced with an earlier attempt or lost its
		// response: reconcile against GitHub before reporting a failure.
		if existing, found, err := c.listedPullRequest(ctx, directory, logDirectory, marker, slug, branch, evidence); err == nil && found {
			if verifyErr := c.verifyPullRequest(ctx, directory, logDirectory, marker, slug, existing, base, head, evidence); verifyErr != nil {
				return "", "", verifyErr
			}
			return strconv.Itoa(existing.Number), existing.URL, nil
		}
		return "", "", &gitFailure{Code: "git_push_failed",
			Message: "gh pr create failed: " + proc.Truncate(strings.TrimSpace(created.stderr), 300)}
	}
	opened, found, err := c.listedPullRequest(ctx, directory, logDirectory, marker, slug, branch, evidence)
	if err != nil || !found {
		return "", "", &gitFailure{Code: "git_push_unknown",
			Message: "the pull request could not be read back after creation"}
	}
	if err := c.verifyPullRequest(ctx, directory, logDirectory, marker, slug, opened, base, head, evidence); err != nil {
		return "", "", err
	}
	return strconv.Itoa(opened.Number), opened.URL, nil
}

// listedPullRequest reads the open pull request of one head branch, if any.
func (c *gitChannel) listedPullRequest(
	ctx context.Context,
	directory string,
	logDirectory string,
	marker string,
	slug string,
	branch string,
	evidence *deliveryEvidence,
) (pullRequestFacts, bool, error) {
	listed := c.executeTool(ctx, directory, logDirectory, marker, "gh",
		"pr", "list", "--repo", slug, "--head", branch, "--state", "open",
		"--json", "number,url,state,baseRefName,headRefOid,author", "--limit", "10")
	evidence.Commands = append(evidence.Commands, gitCommand{
		Argv: []string{"gh", "pr", "list", "--repo", slug, "--head", "<branch>", "--state", "open"}, ExitCode: listed.exitCode,
	})
	if listed.exitCode != 0 || listed.err != nil {
		return pullRequestFacts{}, false, &gitFailure{Code: "git_push_unknown",
			Message: "the repository pull requests could not be listed: " + proc.Truncate(strings.TrimSpace(listed.stderr), 200)}
	}
	var facts []pullRequestFacts
	if err := json.Unmarshal([]byte(strings.TrimSpace(listed.stdout)), &facts); err != nil {
		return pullRequestFacts{}, false, &gitFailure{Code: "git_push_unknown",
			Message: "the pull request listing could not be decoded"}
	}
	for _, fact := range facts {
		if fact.HeadRefOid == "" || fact.Number == 0 {
			continue
		}
		return fact, true, nil
	}
	return pullRequestFacts{}, false, nil
}

// verifyPullRequest refuses to report a pull request that does not match the
// frozen intent: it must be open, on the protected base, at the delivered head,
// and authored by the machine identity the task froze.
func (c *gitChannel) verifyPullRequest(
	ctx context.Context,
	directory string,
	logDirectory string,
	marker string,
	slug string,
	facts pullRequestFacts,
	base string,
	head string,
	evidence *deliveryEvidence,
) error {
	viewed := c.executeTool(ctx, directory, logDirectory, marker, "gh",
		"pr", "view", strconv.Itoa(facts.Number), "--repo", slug,
		"--json", "number,url,state,baseRefName,headRefOid,author")
	evidence.Commands = append(evidence.Commands, gitCommand{
		Argv: []string{"gh", "pr", "view", strconv.Itoa(facts.Number), "--repo", slug}, ExitCode: viewed.exitCode,
	})
	if viewed.exitCode != 0 || viewed.err != nil {
		return &gitFailure{Code: "git_push_unknown",
			Message: "the pull request could not be verified against GitHub"}
	}
	var verified pullRequestFacts
	if err := json.Unmarshal([]byte(strings.TrimSpace(viewed.stdout)), &verified); err != nil {
		return &gitFailure{Code: "git_push_unknown",
			Message: "the pull request verification could not be decoded"}
	}
	if verified.State != "OPEN" {
		return &gitFailure{Code: "git_push_conflict",
			Message: fmt.Sprintf("pull request %d is %s, not open", verified.Number, verified.State)}
	}
	if verified.BaseRefName != base {
		return &gitFailure{Code: "git_push_conflict",
			Message: fmt.Sprintf("pull request %d targets %s, not the protected %s", verified.Number, verified.BaseRefName, base)}
	}
	if verified.HeadRefOid != head {
		return &gitFailure{Code: "git_push_conflict",
			Message: fmt.Sprintf("pull request %d heads %s, not the delivered %s", verified.Number, verified.HeadRefOid, head)}
	}
	if author := fmt.Sprint(verified.Author.ID); author != evidence.Identity {
		return &gitFailure{Code: "git_identity_mismatch",
			Message: fmt.Sprintf("pull request %d was authored by %s, the frozen machine identity is %s",
				verified.Number, author, evidence.Identity)}
	}
	return nil
}

func writeDeliveryEvidence(workspace, repositoryID string, evidence deliveryEvidence) error {
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(repositoryID))
	name := "delivery-" + hex.EncodeToString(sum[:4]) + ".json"
	return writeAtomic(filepath.Join(workspace, name), append(encoded, '\n'))
}

// ghIdentityResolver reads the machine's authenticated GitHub numeric id; the
// public identity is never logged beyond the numeric id that already appears
// in frozen task facts.
func ghIdentityResolver(environment []string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		directory, err := os.MkdirTemp("", "kowa-identity-")
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(directory) }()
		processContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		outcome, runErr := proc.Run(processContext, proc.Spec{
			Command: []string{"gh", "api", "user", "--jq", ".id"}, Dir: directory,
			Env:        environment,
			StdoutPath: filepath.Join(directory, "stdout.log"),
			StderrPath: filepath.Join(directory, "stderr.log"),
			Marker:     fmt.Sprintf("kowa-identity-%d", time.Now().UnixNano()),
			Grace:      5 * time.Second,
		})
		if runErr != nil && !processFailedButRan(runErr) {
			return "", runErr
		}
		stdout, _ := os.ReadFile(filepath.Join(directory, "stdout.log"))
		identity := strings.TrimSpace(string(stdout))
		if outcome.ExitCode != 0 {
			stderr, _ := os.ReadFile(filepath.Join(directory, "stderr.log"))
			return "", fmt.Errorf("gh api user exited %d: %s", outcome.ExitCode,
				proc.Truncate(strings.TrimSpace(string(stderr)), 200))
		}
		if _, err := strconv.ParseUint(identity, 10, 64); err != nil {
			return "", fmt.Errorf("gh api user returned a non-numeric identity")
		}
		return identity, nil
	}
}
