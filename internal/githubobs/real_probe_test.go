//go:build githubprobe

package githubobs_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/SnWalker/kowa/internal/githubobs"
)

// Real GitHub reads are opt-in and strictly read-only:
//
//	KOWA_REAL_GITHUB_PROBE=true KOWA_REAL_GITHUB_USER_ID=135722937 \
//	KOWA_REAL_REPOSITORY_ID=1401511579 KOWA_REAL_REF=main \
//	go test -tags githubprobe -run TestRealRepositoryObservation -v ./internal/githubobs/
func TestRealRepositoryObservation(t *testing.T) {
	if os.Getenv("KOWA_REAL_GITHUB_PROBE") != "true" {
		t.Skip("set KOWA_REAL_GITHUB_PROBE=true to run the real read-only probe")
	}
	observer := githubobs.NewGHObserver(os.Getenv("KOWA_REAL_GITHUB_USER_ID"), os.Environ())
	repositoryID := os.Getenv("KOWA_REAL_REPOSITORY_ID")
	repository, err := observer.Repository(t.Context(), repositoryID)
	if err != nil || repository.Unknown {
		t.Fatalf("repository observation = %+v, %v", repository, err)
	}
	ref, err := observer.BranchHead(t.Context(), repositoryID, os.Getenv("KOWA_REAL_REF"))
	if err != nil || ref.Unknown {
		t.Fatalf("ref observation = %+v, %v", ref, err)
	}
	evidence, _ := json.MarshalIndent(map[string]any{
		"repository": repository, "ref": ref,
	}, "", "  ")
	t.Logf("read-only observation evidence:\n%s", evidence)
}
