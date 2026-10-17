package runner_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/platform/proc"
	"github.com/SnWalker/kowa/internal/runner"
)

// publishFake simulates the controlled channel's outside world: one remote
// branch, and the tiny slice of the GitHub CLI the publish_pr write uses. Every
// command the channel runs lands in commands, so a test can prove what was and
// was not invoked.
type publishFake struct {
	url       string
	head      string
	authorID  string
	branch    string
	base      string
	commands  []string
	createOut string // stderr when the create must fail
	createErr bool
	prNumber  int
	prExists  bool
	prState   string
	prBase    string
	prHead    string
	prAuthor  string
}

func newPublishFake(url, head, authorID, branch string) *publishFake {
	return &publishFake{
		url: url, head: head, authorID: authorID, branch: branch, base: "main",
		prNumber: 1, prState: "OPEN", prBase: "main", prHead: head, prAuthor: authorID,
	}
}

func (f *publishFake) run(_ context.Context, spec proc.Spec) (proc.Outcome, error) {
	tool := spec.Command[0]
	arguments := spec.Command[1:]
	f.commands = append(f.commands, tool+" "+strings.Join(arguments, " "))
	write := func(stdout, stderr string, code int) (proc.Outcome, error) {
		if err := os.WriteFile(spec.StdoutPath, []byte(stdout), 0o600); err != nil {
			return proc.Outcome{}, err
		}
		if err := os.WriteFile(spec.StderrPath, []byte(stderr), 0o600); err != nil {
			return proc.Outcome{}, err
		}
		return proc.Outcome{ExitCode: code}, nil
	}
	switch tool {
	case "git":
		switch arguments[0] {
		case "config":
			return write(f.url+"\n", "", 0)
		case "fetch":
			// The controlled channel fetches the delivered branch so the pull
			// request tooling resolves exactly the head that was read.
			return write("", "", 0)
		case "ls-remote":
			for _, argument := range arguments {
				if argument == "refs/heads/main" || argument == "refs/heads/master" {
					return write(f.head+"\trefs/heads/"+f.base+"\n", "", 0)
				}
			}
			return write(f.head+"\t"+arguments[len(arguments)-1]+"\n", "", 0)
		}
		return write("", "unexpected git "+strings.Join(arguments, " "), 1)
	case "gh":
		switch strings.Join(arguments[:2], " ") {
		case "pr list":
			if f.prExists {
				return write(f.prJSON(), "", 0)
			}
			return write("[]", "", 0)
		case "pr create":
			if f.createErr {
				// The response may be lost even though GitHub created the
				// object; the test decides whether prExists is also set.
				f.prExists = true
				return write("", f.createOut, 1)
			}
			f.prExists = true
			return write(fmt.Sprintf("https://github.example/%d\n", f.prNumber), "", 0)
		case "pr view":
			return write(f.prJSON()[1:len(f.prJSON())-1], "", 0)
		}
		return write("", "unexpected gh "+strings.Join(arguments, " "), 1)
	}
	return write("", "unexpected tool "+tool, 1)
}

func (f *publishFake) prJSON() string {
	return fmt.Sprintf(`[{"number":%d,"url":"https://github.example/%d","state":"%s","baseRefName":"%s","headRefOid":"%s","author":{"id":"%s"}}]`,
		f.prNumber, f.prNumber, f.prState, f.prBase, f.prHead, f.prAuthor)
}

func (f *publishFake) invoked(prefix string) int {
	count := 0
	for _, command := range f.commands {
		if strings.HasPrefix(command, prefix) {
			count++
		}
	}
	return count
}

// publishChannel builds one channel whose whole outside world is the fake. The
// attempt workspace carries the materialized project checkout the channel
// insists on before any delivery.
func publishChannel(t *testing.T, fake *publishFake) (*runner.GitChannel, string, string) {
	t.Helper()
	workspace := t.TempDir()
	repositoryDirectory := filepath.Join(workspace, "repos", "1001")
	if err := os.MkdirAll(filepath.Join(repositoryDirectory, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	channel := runner.NewTestGitChannel(
		map[string]string{"1001": fake.url},
		[]string{"main", "master"},
		func(context.Context) (string, error) { return fake.authorID, nil },
		[]string{"PATH=" + os.Getenv("PATH")},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	runner.SetGitRunnerForTest(channel, fake.run)
	return channel, workspace, repositoryDirectory
}

func publishTask() execution.TaskSpec {
	return execution.TaskSpec{
		ID: "run-publish:task:publish_pr:1", GitExecutionUserID: "9001",
		GitScopes: []execution.GitScope{
			{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: strings.Repeat("a", 40)},
			{RepositoryID: "1001", Role: "project", Operation: "publish_pr", Ref: "refs/heads/task/kowa-run1"},
		},
	}
}

func publishScope() execution.GitScope {
	return execution.GitScope{RepositoryID: "1001", Role: "project",
		Operation: "publish_pr", Ref: "refs/heads/task/kowa-run1"}
}

// TestPublishPullRequestCreatesExactlyOneOpenPullRequest is the target-red
// behaviour of this stage: the controlled channel performs the publish_pr write
// itself instead of refusing it, and reports the pull request it opened.
func TestPublishPullRequestCreatesExactlyOneOpenPullRequest(t *testing.T) {
	head := strings.Repeat("b", 40)
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", head, "9001", "task/kowa-run1")
	channel, workspace, _ := publishChannel(t, fake)

	record, err := channel.DeliverScopeForTest(t.Context(), publishScope(), publishTask(), workspace, "marker")
	if err != nil {
		t.Fatalf("publish_pr delivery: %v", err)
	}
	if record.PullRequestNumber != "1" {
		t.Fatalf("pull request number = %q, want 1", record.PullRequestNumber)
	}
	if record.CommitOid != head {
		t.Fatalf("delivered head = %q, want the frozen branch tip %q", record.CommitOid, head)
	}
	if fake.invoked("gh pr create") != 1 {
		t.Fatalf("gh pr create calls = %d, want exactly 1", fake.invoked("gh pr create"))
	}
	if fake.invoked("gh pr view") != 1 {
		t.Fatalf("the opened pull request must be read back once, got %d", fake.invoked("gh pr view"))
	}
	// The write targets the protected delivery branch, not anything else.
	for _, command := range fake.commands {
		if strings.HasPrefix(command, "gh pr create") && !strings.Contains(command, "--base main --head task/kowa-run1") {
			t.Fatalf("create did not target base main and the frozen head: %s", command)
		}
	}
}

// TestPublishPullRequestReusesAnExistingPullRequest proves a retried attempt
// never opens a second object for the same head.
func TestPublishPullRequestReusesAnExistingPullRequest(t *testing.T) {
	head := strings.Repeat("c", 40)
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", head, "9001", "task/kowa-run1")
	fake.prExists = true
	channel, workspace, _ := publishChannel(t, fake)

	record, err := channel.DeliverScopeForTest(t.Context(), publishScope(), publishTask(), workspace, "marker")
	if err != nil {
		t.Fatalf("publish_pr delivery: %v", err)
	}
	if record.PullRequestNumber != "1" {
		t.Fatalf("pull request number = %q, want the reused 1", record.PullRequestNumber)
	}
	if fake.invoked("gh pr create") != 0 {
		t.Fatalf("an existing pull request must be reused, got %d create calls", fake.invoked("gh pr create"))
	}
}

// TestPublishPullRequestReconcilesALostCreateResponse proves a create whose
// response was lost is reconciled against GitHub instead of failing or
// retrying blindly.
func TestPublishPullRequestReconcilesALostCreateResponse(t *testing.T) {
	head := strings.Repeat("d", 40)
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", head, "9001", "task/kowa-run1")
	fake.createErr = true
	fake.createOut = "a pull request for branch \"task/kowa-run1\" already exists"
	channel, workspace, _ := publishChannel(t, fake)

	record, err := channel.DeliverScopeForTest(t.Context(), publishScope(), publishTask(), workspace, "marker")
	if err != nil {
		t.Fatalf("publish_pr delivery must reconcile the lost response: %v", err)
	}
	if record.PullRequestNumber != "1" {
		t.Fatalf("pull request number = %q, want the reconciled 1", record.PullRequestNumber)
	}
}

// TestPublishPullRequestRefusesAForeignAuthor freezes the identity boundary:
// a pull request authored by anybody but the frozen machine identity is never
// reported as this attempt's delivery.
func TestPublishPullRequestRefusesAForeignAuthor(t *testing.T) {
	head := strings.Repeat("e", 40)
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", head, "9001", "task/kowa-run1")
	fake.prExists = true
	fake.prAuthor = "424242"
	channel, workspace, _ := publishChannel(t, fake)

	_, err := channel.DeliverScopeForTest(t.Context(), publishScope(), publishTask(), workspace, "marker")
	if err == nil {
		t.Fatal("a foreign-authored pull request must be refused")
	}
	if !strings.Contains(err.Error(), "git_identity_mismatch") {
		t.Fatalf("error = %v, want git_identity_mismatch", err)
	}
}

// TestPublishPullRequestRefusesAHeadDrift freezes the head boundary: the pull
// request must sit on the delivered head, never on a moved branch.
func TestPublishPullRequestRefusesAHeadDrift(t *testing.T) {
	head := strings.Repeat("f", 40)
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", head, "9001", "task/kowa-run1")
	fake.prExists = true
	fake.prHead = strings.Repeat("9", 40)
	channel, workspace, _ := publishChannel(t, fake)

	_, err := channel.DeliverScopeForTest(t.Context(), publishScope(), publishTask(), workspace, "marker")
	if err == nil {
		t.Fatal("a pull request on a different head must be refused")
	}
	if !strings.Contains(err.Error(), "git_push_conflict") {
		t.Fatalf("error = %v, want git_push_conflict", err)
	}
}

// TestPublishPullRequestRefusesAnUnreadableBranch closes the fail-open door: a
// working branch the remote does not have is never published.
func TestPublishPullRequestRefusesAnUnreadableBranch(t *testing.T) {
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", "", "9001", "task/kowa-run1")
	channel, workspace, _ := publishChannel(t, fake)

	started := time.Now()
	_, err := channel.DeliverScopeForTest(t.Context(), publishScope(), publishTask(), workspace, "marker")
	if err == nil {
		t.Fatal("an unreadable working branch must be refused")
	}
	if !strings.Contains(err.Error(), "git_push_unknown") {
		t.Fatalf("error = %v, want git_push_unknown", err)
	}
	if time.Since(started) > time.Minute {
		t.Fatal("the refusal must be immediate, not a retry loop")
	}
}

// TestPublishPullRequestRefusesAProtectedHead freezes the existing boundary:
// publish_pr can never target a protected branch.
func TestPublishPullRequestRefusesAProtectedHead(t *testing.T) {
	fake := newPublishFake("git@github.com:Fixture/kowa-sandbox.git", strings.Repeat("a", 40), "9001", "main")
	channel, workspace, _ := publishChannel(t, fake)
	scope := publishScope()
	scope.Ref = "refs/heads/main"

	_, err := channel.DeliverScopeForTest(t.Context(), scope, publishTask(), workspace, "marker")
	if err == nil {
		t.Fatal("publishing a protected branch must be refused")
	}
	if !strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("error = %v, want git_scope_refused", err)
	}
}
