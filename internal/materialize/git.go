// Package materialize owns the control plane's read-only view of the frozen
// repositories: it resolves a delivery branch head once at admission and opens
// a verified checkout of one exact commit. It never writes to a remote and
// never resolves a branch as a substitute for a frozen commit.
package materialize

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/SnWalker/kowa/internal/knowledge"
)

var (
	oidPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repositoryRule = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
)

// Git is the git-backed materializer. Repositories are addressed by their Kowa
// repository id through an operator-provided registry; an unknown id is refused
// instead of guessed.
type Git struct {
	registry map[string]string
	root     string
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
}

// New builds the materializer over a registry of repository id to clone URL
// pairs and a cache root. The cache is rebuildable state, never a source of
// truth: every checkout is verified against the frozen commit.
func New(registry map[string]string, root string) (*Git, error) {
	if root == "" {
		return nil, fmt.Errorf("materialize: a cache root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("materialize: create cache root: %w", err)
	}
	return &Git{registry: registry, root: root, locks: map[string]*sync.Mutex{}}, nil
}

// BranchHead resolves one branch reference to the current commit of the remote.
// It is only used to freeze a baseline once; a frozen commit is never resolved
// this way.
func (g *Git) BranchHead(ctx context.Context, repositoryID, branchRef string) (string, error) {
	url, err := g.url(repositoryID)
	if err != nil {
		return "", err
	}
	output, err := g.git(ctx, "", "ls-remote", "--exit-code", url, branchRef)
	if err != nil {
		return "", fmt.Errorf("materialize: resolve %s of repository %s: %w", branchRef, repositoryID, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == branchRef && oidPattern.MatchString(fields[0]) {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("materialize: %s of repository %s does not resolve to a commit", branchRef, repositoryID)
}

// Repository is one verified read view of a frozen commit.
type Repository struct {
	directory    string
	repositoryID string
	commit       string
	source       *knowledge.DirectorySource
}

// Source reads repository content at the frozen commit.
func (r *Repository) Source() knowledge.Source { return r.source }

// Close releases the view; the checkout stays in the cache for reuse.
func (r *Repository) Close() error { return r.source.Close() }

// Open returns a verified checkout of one frozen commit. A commit that cannot
// be materialised exactly is an error, never a fallback to another revision.
func (g *Git) Open(ctx context.Context, repositoryID, commit string) (knowledge.Repository, error) {
	url, err := g.url(repositoryID)
	if err != nil {
		return nil, err
	}
	if !oidPattern.MatchString(commit) {
		return nil, fmt.Errorf("materialize: %q is not a frozen commit oid", commit)
	}
	lock := g.lockFor(repositoryID)
	lock.Lock()
	defer lock.Unlock()

	directory := filepath.Join(g.root, repositoryID)
	if _, err := os.Stat(filepath.Join(directory, ".git")); err != nil {
		if err := os.RemoveAll(directory); err != nil {
			return nil, fmt.Errorf("materialize: clear a partial checkout: %w", err)
		}
		if _, err := g.git(ctx, "", "clone", "--quiet", "--no-checkout", url, directory); err != nil {
			return nil, fmt.Errorf("materialize: clone repository %s: %w", repositoryID, err)
		}
	}
	registered, err := g.git(ctx, directory, "config", "--get", "remote.origin.url")
	if err != nil || strings.TrimSpace(registered) != url {
		return nil, fmt.Errorf("materialize: the cached checkout of repository %s has another remote", repositoryID)
	}
	if _, err := g.git(ctx, directory, "fetch", "--quiet", "origin", commit); err != nil {
		return nil, fmt.Errorf("materialize: fetch the frozen commit %s: %w", commit, err)
	}
	if _, err := g.git(ctx, directory, "checkout", "--quiet", "--detach", commit); err != nil {
		return nil, fmt.Errorf("materialize: check out the frozen commit %s: %w", commit, err)
	}
	head, err := g.git(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("materialize: read the checked out commit: %w", err)
	}
	if strings.TrimSpace(head) != commit {
		return nil, fmt.Errorf("materialize: checked out %s but the frozen commit is %s",
			strings.TrimSpace(head), commit)
	}
	source, err := knowledge.NewDirectorySource(directory, repositoryID, commit)
	if err != nil {
		return nil, fmt.Errorf("materialize: open the frozen checkout: %w", err)
	}
	return &Repository{directory: directory, repositoryID: repositoryID, commit: commit, source: source}, nil
}

func (g *Git) url(repositoryID string) (string, error) {
	if !repositoryRule.MatchString(repositoryID) {
		return "", fmt.Errorf("materialize: %q is not a repository id", repositoryID)
	}
	url, known := g.registry[repositoryID]
	if !known || url == "" {
		return "", fmt.Errorf("materialize: repository %s is not registered", repositoryID)
	}
	return url, nil
}

func (g *Git) lockFor(repositoryID string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	lock, found := g.locks[repositoryID]
	if !found {
		lock = &sync.Mutex{}
		g.locks[repositoryID] = lock
	}
	return lock
}

// git runs one read-only git command. The environment is inherited so the
// operator's existing credentials (ssh agent, credential helper) keep working;
// Kowa itself never stores a token.
func (g *Git) git(ctx context.Context, directory string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(arguments, " "), err, truncate(string(output), 400))
	}
	return string(output), nil
}

func truncate(value string, limit int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "…"
}
