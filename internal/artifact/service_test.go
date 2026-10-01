package artifact_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
)

func TestArtifactContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scope := artifact.Scope{WorkspaceID: "w1", ActorID: "1"}
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	repo := artifact.NewMemoryCatalog()
	repo.Grant("w1", "1", true)
	repo.Grant("w2", "1", true)
	root := t.TempDir()
	blobs, err := artifact.NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Error(err)
		}
	})
	svc := artifact.NewService(repo, blobs, func() time.Time { return now })
	data := []byte("immutable evidence")
	upload := func(t *testing.T) artifact.Ref {
		t.Helper()
		ref, e := svc.Upload(ctx, scope, artifact.Upload{SubjectID: "work-1", Kind: "evidence", MediaType: "text/plain", Producer: artifact.Producer{Kind: "operation", ID: "test-upload"}, Digest: artifact.Digest(data)}, data)
		if e != nil {
			t.Fatal(e)
		}
		return ref
	}
	t.Run("A17_digest_mismatch", func(t *testing.T) {
		_, e := svc.Upload(ctx, scope, artifact.Upload{SubjectID: "work-1", Kind: "evidence", MediaType: "text/plain", Producer: artifact.Producer{Kind: "operation", ID: "test"}, Digest: artifact.Digest([]byte("wrong"))}, data)
		if !errors.Is(e, artifact.ErrIntegrity) {
			t.Fatalf("got %v", e)
		}
	})
	t.Run("unpublished_and_cross_workspace", func(t *testing.T) {
		ref := upload(t)
		if _, e := svc.Read(ctx, scope, ref); !errors.Is(e, artifact.ErrUnpublished) {
			t.Fatalf("got %v", e)
		}
		if e := svc.Publish(ctx, scope, ref); e != nil {
			t.Fatal(e)
		}
		if _, e := svc.Read(ctx, artifact.Scope{WorkspaceID: "w2", ActorID: "1"}, ref); !errors.Is(e, artifact.ErrForbidden) {
			t.Fatalf("got %v", e)
		}
		bad := ref
		bad.Digest = artifact.Digest([]byte("wrong"))
		if _, e := svc.Read(ctx, scope, bad); !errors.Is(e, artifact.ErrIntegrity) {
			t.Fatalf("got %v", e)
		}
	})
	t.Run("A16_removed_upstream_directory", func(t *testing.T) {
		taskDir := t.TempDir()
		p := filepath.Join(taskDir, "result.txt")
		if e := os.WriteFile(p, data, 0600); e != nil {
			t.Fatal(e)
		}
		ref := upload(t)
		if e := svc.Publish(ctx, scope, ref); e != nil {
			t.Fatal(e)
		}
		if e := os.RemoveAll(taskDir); e != nil {
			t.Fatal(e)
		}
		other, e := artifact.NewLocalStore(root)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if err := other.Close(); err != nil {
				t.Error(err)
			}
		})
		downstream := artifact.NewService(repo, other, func() time.Time { return now })
		got, e := downstream.Read(ctx, scope, ref)
		if e != nil || string(got) != string(data) {
			t.Fatalf("%q %v", got, e)
		}
	})
	t.Run("A27_reference_cleanup_race", func(t *testing.T) {
		for range 30 {
			ref := upload(t)
			if e := svc.Publish(ctx, scope, ref); e != nil {
				t.Fatal(e)
			}
			var wg sync.WaitGroup
			wg.Add(2)
			var addErr, cleanErr error
			go func() {
				defer wg.Done()
				addErr = svc.Reference(ctx, scope, ref, artifact.Reference{Kind: "run", ID: "run-1"})
			}()
			go func() {
				defer wg.Done()
				cleanErr = svc.Clean(ctx, scope, ref.ArtifactID, now.Add(time.Hour), time.Minute)
			}()
			wg.Wait()
			if addErr == nil {
				if _, e := svc.Read(ctx, scope, ref); e != nil {
					t.Fatalf("deleted referenced content: %v; clean %v", e, cleanErr)
				}
			} else if !errors.Is(addErr, artifact.ErrUnavailable) {
				t.Fatal(addErr)
			}
		}
	})
}

func TestCandidateRequiresExplicitApproval(t *testing.T) {
	ctx := t.Context()
	scope := artifact.Scope{WorkspaceID: "w", ActorID: "1"}
	catalog := artifact.NewMemoryCatalog()
	catalog.Grant("w", "1", true)
	blobs, err := artifact.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Error(err)
		}
	})
	svc := artifact.NewService(catalog, blobs, time.Now)
	ref, err := svc.Upload(ctx, scope, artifact.Upload{SubjectID: "work", Kind: "plan", MediaType: "text/plain", Digest: artifact.Digest([]byte("plan")), Producer: artifact.Producer{Kind: "operation", ID: "candidate"}, RequiresApproval: true}, []byte("plan"))
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Publish(ctx, scope, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Read(ctx, scope, ref); err != nil {
		t.Fatal(err)
	}
	if err = svc.Reference(ctx, scope, ref, artifact.Reference{Kind: "run", ID: "run"}); !errors.Is(err, artifact.ErrUnapproved) {
		t.Fatalf("candidate executed: %v", err)
	}
	if err = svc.Approve(ctx, scope, ref, "response-1"); err != nil {
		t.Fatal(err)
	}
	if err = svc.Reference(ctx, scope, ref, artifact.Reference{Kind: "run", ID: "run"}); err != nil {
		t.Fatal(err)
	}
}

// faultStore exercises errors after database reservation and after logical deletion.
type faultStore struct {
	artifact.BlobStore
	putError, removeError error
}

func (f *faultStore) Put(ctx context.Context, id string, b []byte) error {
	if f.putError != nil {
		return f.putError
	}
	return f.BlobStore.Put(ctx, id, b)
}
func (f *faultStore) Remove(ctx context.Context, id string) error {
	if f.removeError != nil {
		return f.removeError
	}
	return f.BlobStore.Remove(ctx, id)
}

func TestUploadAndCleanupRecovery(t *testing.T) {
	scope := artifact.Scope{WorkspaceID: "w", ActorID: "1"}
	catalog := artifact.NewMemoryCatalog()
	catalog.Grant("w", "1", true)
	blobs, err := artifact.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	fault := &faultStore{BlobStore: blobs, putError: errors.New("disk unavailable")}
	svc := artifact.NewService(catalog, fault, func() time.Time { return now })
	request := artifact.Upload{SubjectID: "work", Kind: "evidence", MediaType: "text/plain", Digest: artifact.Digest([]byte("data")), Producer: artifact.Producer{Kind: "operation", ID: "test"}}
	if _, err = svc.Upload(t.Context(), scope, request, []byte("data")); err == nil {
		t.Fatal("false upload success")
	}
	orphans, err := catalog.Orphans(t.Context(), scope, now.Add(time.Hour), 100)
	if err != nil || len(orphans) != 1 {
		t.Fatalf("untracked upload %v %v", orphans, err)
	}
	if err = svc.Clean(t.Context(), scope, orphans[0], now, time.Minute); !errors.Is(err, artifact.ErrGracePeriod) {
		t.Fatalf("grace %v", err)
	}
	fault.removeError = errors.New("remove failed")
	if err = svc.Clean(t.Context(), scope, orphans[0], now.Add(time.Hour), time.Minute); err == nil {
		t.Fatal("false delete success")
	}
	fault.removeError = nil
	if err = svc.Clean(t.Context(), scope, orphans[0], now.Add(time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = svc.Clean(t.Context(), scope, orphans[0], now.Add(time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}
	orphans, err = catalog.Orphans(t.Context(), scope, now.Add(time.Hour), 100)
	if err != nil || len(orphans) != 0 {
		t.Fatalf("cleanup incomplete %v %v", orphans, err)
	}
}

func TestLocalStoreRejectsTraversalOverwriteAndCorruption(t *testing.T) {
	root := t.TempDir()
	blobs, err := artifact.NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{"../escape", "/tmp/escape", "file://etc/passwd"} {
		if _, err = blobs.Get(t.Context(), id); !errors.Is(err, artifact.ErrValidation) {
			t.Fatalf("path %q: %v", id, err)
		}
	}
	scope := artifact.Scope{WorkspaceID: "w", ActorID: "1"}
	catalog := artifact.NewMemoryCatalog()
	catalog.Grant("w", "1", true)
	svc := artifact.NewService(catalog, blobs, time.Now)
	ref, err := svc.Upload(t.Context(), scope, artifact.Upload{SubjectID: "work", Kind: "evidence", MediaType: "text/plain", Digest: artifact.Digest([]byte("data")), Producer: artifact.Producer{Kind: "operation", ID: "test"}}, []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	if err = blobs.Put(t.Context(), ref.ArtifactID, []byte("new")); err == nil {
		t.Fatal("overwrote immutable object")
	}
	if err = svc.Publish(t.Context(), scope, ref); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ref.ArtifactID), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Read(t.Context(), scope, ref); !errors.Is(err, artifact.ErrIntegrity) {
		t.Fatalf("corruption accepted %v", err)
	}
	if err = os.Remove(filepath.Join(root, ref.ArtifactID)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err = os.WriteFile(outside, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, ref.ArtifactID)); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Read(t.Context(), scope, ref); err == nil {
		t.Fatal("followed escaped symlink")
	}
}
