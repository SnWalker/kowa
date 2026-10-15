package materialize_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/materialize"
)

// TestFrozenCommitReadIsRebuildable pins the frozen-input read: a commit
// resolves to exactly its own content, a branch that moves on changes nothing,
// and deleting the local checkout rebuilds the very same commit.
func TestFrozenCommitReadIsRebuildable(t *testing.T) {
	remote := buildRepository(t)
	branchHead := headOf(t, remote, "refs/heads/main")
	cache := t.TempDir()
	fetcher, err := materialize.New(map[string]string{"9001": remote}, cache)
	if err != nil {
		t.Fatalf("build the materializer: %v", err)
	}

	resolved, err := fetcher.BranchHead(t.Context(), "9001", "refs/heads/main")
	if err != nil || resolved != branchHead {
		t.Fatalf("branch head = %s, %v, want %s", resolved, err, branchHead)
	}

	first := read(t, fetcher, "9001", branchHead)
	if first != "first revision\n" {
		t.Fatalf("frozen content = %q", first)
	}

	// A branch that advances never changes the frozen revision.
	commit(t, remote, "second revision\n")
	if advanced := headOf(t, remote, "refs/heads/main"); advanced == branchHead {
		t.Fatal("the branch did not advance")
	}
	if again := read(t, fetcher, "9001", branchHead); again != first {
		t.Fatalf("the frozen revision changed to %q", again)
	}

	// Removing the local checkout rebuilds exactly the same frozen commit.
	if err := os.RemoveAll(cache); err != nil {
		t.Fatalf("remove the cache: %v", err)
	}
	if err := os.MkdirAll(cache, 0o750); err != nil {
		t.Fatalf("recreate the cache: %v", err)
	}
	rebuilt, err := materialize.New(map[string]string{"9001": remote}, cache)
	if err != nil {
		t.Fatalf("rebuild the materializer: %v", err)
	}
	if after := read(t, rebuilt, "9001", branchHead); after != first {
		t.Fatalf("the rebuilt checkout holds %q", after)
	}

	// An unregistered repository or an unreadable commit stays refused.
	if _, err := fetcher.Open(t.Context(), "9002", branchHead); err == nil {
		t.Fatal("an unregistered repository resolved")
	}
	if _, err := fetcher.Open(t.Context(), "9001", "main"); err == nil {
		t.Fatal("a branch name was accepted as a frozen commit")
	}
	if _, err := fetcher.Open(t.Context(), "9001", strings.Repeat("0", 40)); err == nil {
		t.Fatal("an absent commit resolved")
	}
}

func read(t *testing.T, fetcher *materialize.Git, repositoryID, commit string) string {
	t.Helper()
	repository, err := fetcher.Open(t.Context(), repositoryID, commit)
	if err != nil {
		t.Fatalf("open %s: %v", commit, err)
	}
	defer func() { _ = repository.Close() }()
	source := repository.Source()
	if err := source.Probe(t.Context(), repositoryID, commit); err != nil {
		t.Fatalf("probe the frozen checkout: %v", err)
	}
	data, err := source.Read(t.Context(), repositoryID, commit, "NOTES.md")
	if err != nil {
		t.Fatalf("read the frozen content: %v", err)
	}
	return string(data)
}

// buildRepository creates one local bare repository with an initial commit.
func buildRepository(t *testing.T) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "project.git")
	runGit(t, "", "init", "--quiet", "--bare", "--initial-branch=main", remote)
	commit(t, remote, "first revision\n")
	return remote
}

// commit pushes one revision to the repository's main branch.
func commit(t *testing.T, remote, content string) {
	t.Helper()
	work := t.TempDir()
	runGit(t, "", "clone", "--quiet", remote, work)
	if err := os.WriteFile(filepath.Join(work, "NOTES.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write the revision: %v", err)
	}
	runGit(t, work, "add", "NOTES.md")
	runGit(t, work, "-c", "user.email=materialize@example.com", "-c", "user.name=materialize",
		"commit", "--quiet", "-m", "revision")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

func headOf(t *testing.T, remote, ref string) string {
	t.Helper()
	output := runGit(t, "", "ls-remote", remote, ref)
	fields := strings.Fields(output)
	if len(fields) != 2 {
		t.Fatalf("unexpected ls-remote output %q", output)
	}
	return fields[0]
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
