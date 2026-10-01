//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/knowledge"
)

type knowledgeSource struct{}

func (knowledgeSource) Probe(context.Context, string, string) error { return nil }
func (knowledgeSource) Read(context.Context, string, string, string) ([]byte, error) {
	return []byte("frozen guide"), nil
}

func TestKnowledgeArtifactPostgres(t *testing.T) {
	ctx := t.Context()
	db, err := postgres.Open(ctx, os.Getenv("KOWA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = db.ExecContext(ctx, `
 insert into workspace(workspace_id,name,version,created_at,updated_at) values ('ka-w','KA',1,now(),now()),('ka-other','Other',1,now(),now());
 insert into workspace_member(workspace_id,github_user_id,role,created_at,updated_at) values ('ka-w','1','developer',now(),now()),('ka-w','2','viewer',now(),now()),('ka-other','1','developer',now(),now());
 insert into repository_binding(workspace_id,role,github_repository_id,installation_id,display_name,access_mode,created_at,updated_at) values ('ka-w','knowledge','10','20','test/knowledge','read',now(),now());
 `)
	if err != nil {
		t.Fatal(err)
	}
	scope := artifact.Scope{WorkspaceID: "ka-w", ActorID: "1"}
	store := postgres.NewStore(db)
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
	now := time.Now().UTC()
	artifacts := artifact.NewService(store, blobs, func() time.Time { return now })
	ks := knowledge.NewService(store, artifacts)
	snap, err := knowledge.Capture(ctx, knowledgeSource{}, knowledge.Selection{WorkspaceID: "ka-w", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{{Path: "guide.md", Digest: artifact.Digest([]byte("frozen guide")), Scope: "all", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PublishKnowledgeSnapshot(ctx, scope, snap); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.ReadKnowledgeSnapshot(ctx, scope, snap.ID)
	if err != nil || candidate.ID != snap.ID {
		t.Fatalf("candidate read %v", err)
	}
	if _, err = store.AssembleKnowledgeInput(ctx, scope, knowledge.InputRequest{WorkspaceID: "ka-w", SubjectID: "work-1"}, ""); !errors.Is(err, knowledge.ErrUnconfirmed) {
		t.Fatalf("readable candidate implicitly confirmed %v", err)
	}
	t.Run("C02_candidate_publication_scope_and_integrity", func(t *testing.T) {
		if e := store.PublishKnowledgeSnapshot(ctx, scope, snap); e != nil {
			t.Fatal(e)
		}
		if e := store.PublishKnowledgeSnapshot(ctx, artifact.Scope{WorkspaceID: "ka-w", ActorID: "2"}, snap); !errors.Is(e, artifact.ErrForbidden) {
			t.Fatalf("viewer publish %v", e)
		}
		if e := store.PublishKnowledgeSnapshot(ctx, artifact.Scope{WorkspaceID: "ka-other", ActorID: "1"}, snap); !errors.Is(e, artifact.ErrForbidden) {
			t.Fatalf("cross workspace %v", e)
		}
		bad := snap
		bad.Digest = artifact.Digest([]byte("bad"))
		if e := store.PublishKnowledgeSnapshot(ctx, scope, bad); !errors.Is(e, artifact.ErrIntegrity) {
			t.Fatalf("invalid candidate %v", e)
		}
		var count int
		if e := db.QueryRowContext(ctx, "select count(*) from knowledge_snapshot where workspace_id='ka-w'").Scan(&count); e != nil || count != 1 {
			t.Fatalf("candidate dedup %d %v", count, e)
		}
	})
	cmd := knowledge.ConfirmCommand{SubjectID: "work-1", HumanResponseID: "response-1", Snapshot: snap}
	var first, second knowledge.BindingEvent
	t.Run("C01_confirmation_idempotency_and_audit", func(t *testing.T) {
		first, err = ks.Confirm(ctx, scope, cmd)
		if err != nil {
			t.Fatal(err)
		}
		repeat, e := ks.Confirm(ctx, scope, cmd)
		if e != nil || repeat.ID != first.ID {
			t.Fatalf("repeat %+v %v", repeat, e)
		}
		conflict := cmd
		conflict.ExpectedVersion = 8
		if _, e = ks.Confirm(ctx, scope, conflict); !errors.Is(e, knowledge.ErrConflict) {
			t.Fatalf("same key changed request: %v", e)
		}
		cmd.ExpectedVersion = 1
		cmd.HumanResponseID = "response-2"
		second, e = ks.Confirm(ctx, scope, cmd)
		if e != nil || second.ID == first.ID || second.SnapshotID != first.SnapshotID || second.PreviousEventID != first.ID {
			t.Fatalf("second %+v %v", second, e)
		}
		var count int
		if e = db.QueryRowContext(ctx, "select count(*) from knowledge_binding_event where workspace_id='ka-w'").Scan(&count); e != nil || count != 2 {
			t.Fatalf("count %d %v", count, e)
		}
	})
	taskDirectory := t.TempDir()
	taskFile := filepath.Join(taskDirectory, "result.txt")
	if err = os.WriteFile(taskFile, []byte("candidate evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(taskFile)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := artifacts.Upload(ctx, scope, artifact.Upload{SubjectID: "work-1", Kind: "plan", MediaType: "text/plain", Producer: artifact.Producer{Kind: "operation", ID: "candidate"}, RequiresApproval: true, Digest: artifact.Digest(data)}, data)
	if err != nil {
		t.Fatal(err)
	}
	req := knowledge.InputRequest{WorkspaceID: "ka-w", SubjectID: "work-1", RequirementVersion: "1", ProjectCommit: strings.Repeat("a", 40), DefinitionDigest: artifact.Digest([]byte("definition")), PolicyVersion: "1", ConfigVersion: "1", Upstream: []knowledge.Upstream{{Name: "plan", NodeRunID: "node-1", Ref: ref}}}
	t.Run("C02_unpublished_and_unconfirmed", func(t *testing.T) {
		if _, e := ks.Assemble(ctx, scope, req, second.ID); !errors.Is(e, artifact.ErrUnpublished) {
			t.Fatalf("unpublished: %v", e)
		}
		if e := artifacts.Publish(ctx, scope, ref); e != nil {
			t.Fatal(e)
		}
		if _, e := artifacts.Read(ctx, scope, ref); e != nil {
			t.Fatal(e)
		}
		if _, e := ks.Assemble(ctx, scope, req, ""); !errors.Is(e, knowledge.ErrUnconfirmed) {
			t.Fatalf("unconfirmed %v", e)
		}
		if _, e := ks.Assemble(ctx, scope, req, first.ID); !errors.Is(e, knowledge.ErrConflict) {
			t.Fatalf("stale event %v", e)
		}
	})
	t.Run("C02_readable_candidate_requires_approval", func(t *testing.T) {
		if _, e := ks.Assemble(ctx, scope, req, second.ID); !errors.Is(e, artifact.ErrUnapproved) {
			t.Fatalf("unapproved candidate consumed: %v", e)
		}
		if e := artifacts.Approve(ctx, scope, ref, "approve-plan-1"); e != nil {
			t.Fatal(e)
		}
	})
	var input knowledge.EffectiveInput
	t.Run("A05_A16_frozen_input_recovery_and_protection", func(t *testing.T) {
		input, err = ks.Assemble(ctx, scope, req, second.ID)
		if err != nil {
			t.Fatal(err)
		}
		if e := artifacts.Clean(ctx, scope, ref.ArtifactID, now.Add(time.Hour), time.Minute); !errors.Is(e, artifact.ErrReferenced) {
			t.Fatalf("clean %v", e)
		}
		if e := os.RemoveAll(taskDirectory); e != nil {
			t.Fatal(e)
		}
		encoded, e := json.Marshal(ref)
		if e != nil {
			t.Fatal(e)
		}
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArtifactReadProcess$", "-test.v")
		child.Env = append(os.Environ(), "KOWA_ARTIFACT_CHILD_ROOT="+root, "KOWA_ARTIFACT_CHILD_REF="+string(encoded))
		output, e := child.CombinedOutput()
		if e != nil {
			t.Fatalf("isolated child read: %v: %s", e, output)
		}
		if !strings.Contains(string(output), "ARTIFACT_CHILD_READ_PASS") {
			t.Fatalf("missing child evidence: %s", output)
		}
		t.Log("A16_ARTIFACT_CHILD_READ_PASS: upstream task directory removed")
		// A new connection and store recover exact canonical bytes without a source read.
		other, e := postgres.Open(ctx, os.Getenv("KOWA_TEST_DATABASE_URL"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if err := other.Close(); err != nil {
				t.Error(err)
			}
		})
		recovered, e := postgres.NewStore(other).ReadEffectiveInput(ctx, scope, input.ID)
		if e != nil || recovered.Digest != input.Digest || string(recovered.Canonical) != string(input.Canonical) {
			t.Fatalf("recovery %+v %v", recovered, e)
		}
		frozen, e := store.ReadKnowledgeSnapshot(ctx, scope, snap.ID)
		if e != nil || string(frozen.Content.Entries[0].Content) != "frozen guide" {
			t.Fatalf("snapshot %+v %v", frozen, e)
		}
	})
	t.Run("access_and_transaction_rollback", func(t *testing.T) {
		if _, e := store.ReadKnowledgeSnapshot(ctx, artifact.Scope{WorkspaceID: "ka-other", ActorID: "1"}, snap.ID); !errors.Is(e, artifact.ErrUnavailable) {
			t.Fatalf("cross workspace %v", e)
		}
		if _, e := ks.Confirm(ctx, artifact.Scope{WorkspaceID: "ka-w", ActorID: "2"}, cmd); !errors.Is(e, artifact.ErrForbidden) {
			t.Fatalf("viewer %v", e)
		}
		invalid := req
		invalid.Upstream = append(append([]knowledge.Upstream{}, req.Upstream...), knowledge.Upstream{Name: "missing", NodeRunID: "node-2", Ref: ref})
		invalid.Upstream[1].Ref.ArtifactID = strings.Repeat("f", 48)
		invalid.Upstream[1].Ref.StorageRef = "artifact:" + strings.Repeat("f", 48)
		failedInput, e := knowledge.Assemble(invalid, snap, second)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := store.AssembleKnowledgeInput(ctx, scope, invalid, second.ID); e == nil {
			t.Fatal("accepted missing upstream")
		}
		var dangling int
		if e := db.QueryRowContext(ctx, "select count(*) from artifact_reference where workspace_id=$1 and owner_kind='input' and owner_id=$2", scope.WorkspaceID, failedInput.ID).Scan(&dangling); e != nil || dangling != 0 {
			t.Fatalf("partial reference persisted %d %v", dangling, e)
		}
		var n int
		if e := db.QueryRowContext(ctx, "select count(*) from effective_input where workspace_id='ka-w'").Scan(&n); e != nil || n != 1 {
			t.Fatalf("partial input persisted %d %v", n, e)
		}
	})
	t.Run("A04_empty_snapshot_still_requires_confirmation", func(t *testing.T) {
		empty, e := knowledge.Capture(ctx, knowledgeSource{}, knowledge.Selection{WorkspaceID: "ka-w", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{}})
		if e != nil {
			t.Fatal(e)
		}
		if e = store.PublishKnowledgeSnapshot(ctx, scope, empty); e != nil {
			t.Fatal(e)
		}
		r := req
		r.SubjectID = "empty-work"
		r.Upstream = nil
		if _, e = ks.Assemble(ctx, scope, r, ""); !errors.Is(e, knowledge.ErrUnconfirmed) {
			t.Fatalf("empty bypass %v", e)
		}
		event, e := ks.Confirm(ctx, scope, knowledge.ConfirmCommand{SubjectID: "empty-work", HumanResponseID: "empty-answer", Snapshot: empty})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ks.Assemble(ctx, scope, r, event.ID); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("A27_database_publication_reference_cleanup_race", func(t *testing.T) {
		for range 20 {
			r, e := artifacts.Upload(ctx, scope, artifact.Upload{SubjectID: "work-1", Kind: "evidence", MediaType: "text/plain", Producer: artifact.Producer{Kind: "operation", ID: "race"}, Digest: artifact.Digest(data)}, data)
			if e != nil {
				t.Fatal(e)
			}
			var wg sync.WaitGroup
			wg.Add(2)
			var publishErr, cleanErr error
			go func() {
				defer wg.Done()
				publishErr = artifacts.Publish(ctx, scope, r)
				if publishErr == nil {
					publishErr = artifacts.Reference(ctx, scope, r, artifact.Reference{Kind: "closure", ID: "close-1"})
				}
			}()
			go func() {
				defer wg.Done()
				cleanErr = artifacts.Clean(ctx, scope, r.ArtifactID, now.Add(time.Hour), time.Minute)
			}()
			wg.Wait()
			if publishErr == nil {
				if _, e := artifacts.Read(ctx, scope, r); e != nil {
					t.Fatalf("lost referenced artifact %v (clean %v)", e, cleanErr)
				}
			} else if !errors.Is(publishErr, artifact.ErrUnavailable) {
				t.Fatal(publishErr)
			}
		}
	})
}

func TestArtifactReadProcess(t *testing.T) {
	root := os.Getenv("KOWA_ARTIFACT_CHILD_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	var ref artifact.Ref
	if err := json.Unmarshal([]byte(os.Getenv("KOWA_ARTIFACT_CHILD_REF")), &ref); err != nil {
		t.Fatal(err)
	}
	db, err := postgres.Open(t.Context(), os.Getenv("KOWA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	blobs, err := artifact.NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Error(err)
		}
	})
	svc := artifact.NewService(postgres.NewStore(db), blobs, time.Now)
	data, err := svc.Read(t.Context(), artifact.Scope{WorkspaceID: "ka-w", ActorID: "1"}, ref)
	if err != nil || artifact.Digest(data) != ref.Digest {
		t.Fatalf("isolated read: %v", err)
	}
	t.Log("ARTIFACT_CHILD_READ_PASS")
}

func TestKnowledgeArtifactTaskPublication(t *testing.T) {
	ctx := t.Context()
	db, err := postgres.Open(ctx, os.Getenv("KOWA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = db.ExecContext(ctx, `insert into workspace(workspace_id,name,version,created_at,updated_at) values ('workspace-1','Task artifact test',1,now(),now()); insert into workspace_member(workspace_id,github_user_id,role,created_at,updated_at) values('workspace-1','101','developer',now(),now());`)
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.NewStore(db)
	definition := publishIntegrationPlan(t, store)
	now := time.Date(2026, 9, 29, 5, 0, 0, 0, time.UTC)
	executionService := execution.NewService(store, func() time.Time { return now })
	err = executionService.RegisterRuntime(ctx, execution.RuntimeRegistration{ID: "ka-runtime", Epoch: "1", GitHubUserID: "9001", Provider: execution.ProviderSelection{ID: "codex", Version: "1"}, ProviderAuthenticated: true, Capabilities: []execution.Capability{{ID: "plan.create", Version: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	createPostgresRun(t, executionService, "ka-run", "ka-node", "ka-task", definition)
	lease, err := executionService.LeaseTask(ctx, execution.LeaseRequest{RuntimeID: "ka-runtime", RuntimeEpoch: "1", LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	scope := artifact.Scope{WorkspaceID: "workspace-1", ActorID: "101"}
	blobs, err := artifact.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := blobs.Close(); err != nil {
			t.Error(err)
		}
	})
	svc := artifact.NewService(store, blobs, func() time.Time { return now })
	upload := artifact.Upload{SubjectID: "work-ka-run", Kind: "plan", MediaType: "text/plain", Producer: artifact.Producer{Kind: "task", ID: "ka-task"}, Digest: artifact.Digest([]byte("plan")), RequiresApproval: true}
	ref, err := svc.Upload(ctx, scope, upload, []byte("plan"))
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Publish(ctx, scope, ref); !errors.Is(err, artifact.ErrForbidden) {
		t.Fatalf("unaccepted result published %v", err)
	}
	report := execution.ResultReport{SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind, TaskID: "ka-task", LeaseID: lease.LeaseID, FencingToken: lease.FencingToken, Status: execution.ResultAwaitingInput, ResultDigest: artifact.Digest([]byte("result")), Output: map[string]any{"artifactRefs": []artifact.Ref{ref}}, InputRequest: map[string]any{"kind": "approve_plan"}}
	stale := report
	stale.LeaseID = "stale"
	if _, err = executionService.ReportResult(ctx, stale); !errors.Is(err, execution.ErrStaleLease) {
		t.Fatalf("stale result %v", err)
	}
	if err = svc.Publish(ctx, scope, ref); !errors.Is(err, artifact.ErrForbidden) {
		t.Fatalf("stale result published %v", err)
	}
	if _, err = executionService.ReportResult(ctx, report); err != nil {
		t.Fatal(err)
	}
	if err = svc.Publish(ctx, scope, ref); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Read(ctx, scope, ref); err != nil {
		t.Fatal(err)
	}
	if err = svc.Reference(ctx, scope, ref, artifact.Reference{Kind: "run", ID: "downstream"}); !errors.Is(err, artifact.ErrUnapproved) {
		t.Fatalf("awaiting_input auto-consumed %v", err)
	}
	if err = svc.Clean(ctx, scope, ref.ArtifactID, now.Add(time.Hour), time.Minute); !errors.Is(err, artifact.ErrReferenced) {
		t.Fatalf("accepted task reference lost to cleanup: %v", err)
	}
	// An accepted task does not authorize additional uploads absent from its exact result.
	extra, err := svc.Upload(ctx, scope, upload, []byte("plan"))
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Publish(ctx, scope, extra); !errors.Is(err, artifact.ErrForbidden) {
		t.Fatalf("unreported artifact published %v", err)
	}
}
