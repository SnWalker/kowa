package knowledge_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/knowledge"
)

func TestDirectorySourceConfinement(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	src, err := knowledge.NewDirectorySource(root, "10", strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := src.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{"../secret", "/etc/passwd", "a/../secret", "escape", "a\\b", "."} {
		t.Run(path, func(t *testing.T) {
			_, err := knowledge.Capture(t.Context(), src, knowledge.Selection{WorkspaceID: "w", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{{Path: path, Digest: artifact.Digest([]byte("secret")), Scope: "all"}}})
			if err == nil {
				t.Fatal("accepted unconfined entry")
			}
		})
	}
	if err = src.Probe(t.Context(), "10", strings.Repeat("b", 40)); err == nil {
		t.Fatal("read wrong frozen commit")
	}
}
