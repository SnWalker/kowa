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
	c.mu.Lock()
	c.commandCounter++
	commandIndex := c.commandCounter
	c.mu.Unlock()
	stdout := filepath.Join(logDirectory, fmt.Sprintf("git-%d-stdout.log", commandIndex))
	stderr := filepath.Join(logDirectory, fmt.Sprintf("git-%d-stderr.log", commandIndex))
	if err := os.MkdirAll(logDirectory, 0o750); err != nil {
		return gitResult{err: fmt.Errorf("create git log directory: %w", err)}
	}
	processContext, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	outcome, runErr := c.run(processContext, proc.Spec{
		Command: append([]string{"git"}, arguments...), Dir: directory,
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
		if err := record(c.execute(ctx, directory, logDirectory, marker, "fetch", "--quiet", url),
			"fetch", "--quiet", "<url>"); err != nil {
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
	if refKind == scopeRefCommit && evidence.Head != refValue {
		return &gitFailure{Code: "git_materialize_failed",
			Message: fmt.Sprintf("checked out %s but the frozen ref is %s", evidence.Head, refValue)}
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

// deliveryRecord is the result of one controlled commit/push.
type deliveryRecord struct {
	RepositoryID string `json:"repositoryId"`
	Ref          string `json:"ref"`
	CommitOid    string `json:"commitOid"`
	BaseOid      string `json:"baseOid"`
	Reconciled   bool   `json:"reconciled"`
}

type deliveryEvidence struct {
	RepositoryID string       `json:"repositoryId"`
	Role         string       `json:"role"`
	Operation    string       `json:"operation"`
	Ref          string       `json:"ref"`
	BaseOid      string       `json:"baseOid"`
	LocalHead    string       `json:"localHead"`
	RemoteOid    string       `json:"remoteOid"`
	Reconciled   bool         `json:"reconciled"`
	Identity     string       `json:"githubUserId"`
	GitUserName  string       `json:"gitUserName"`
	GitUserMail  string       `json:"gitUserEmail"`
	Commands     []gitCommand `json:"commands"`
	ObservedAt   time.Time    `json:"observedAt"`
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
	case "commit", "push":
	case "publish_pr":
		return refuse("publish_pr belongs to the fixed workflow stage and is not implemented here")
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
