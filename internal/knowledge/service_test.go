package knowledge_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/knowledge"
)

type source struct {
	files map[string][]byte
	err   error
}

func (s source) Read(_ context.Context, _, _, path string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	b, ok := s.files[path]
	if !ok {
		return nil, knowledge.ErrUnavailable
	}
	return b, nil
}
func (s source) Probe(context.Context, string, string) error { return s.err }

func TestKnowledgeContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := knowledge.Selection{WorkspaceID: "w1", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{{Path: "guide.md", Digest: artifact.Digest([]byte("guide")), Scope: "all", Required: true}}}
	t.Run("A03_unreachable_missing_and_digest", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			src  source
			want error
		}{
			{"unreachable", source{err: errors.New("offline")}, knowledge.ErrUnavailable},
			{"missing", source{files: map[string][]byte{}}, knowledge.ErrUnavailable},
			{"digest", source{files: map[string][]byte{"guide.md": []byte("other")}}, artifact.ErrIntegrity},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, e := knowledge.Capture(ctx, tc.src, req); !errors.Is(e, tc.want) {
					t.Fatalf("got %v", e)
				}
			})
		}
	})
	t.Run("A04_explicit_empty_requires_successful_read", func(t *testing.T) {
		empty := req
		empty.Entries = []knowledge.Entry{}
		if _, e := knowledge.Capture(ctx, source{err: errors.New("offline")}, empty); !errors.Is(e, knowledge.ErrUnavailable) {
			t.Fatal(e)
		}
		snap, e := knowledge.Capture(ctx, source{}, empty)
		if e != nil || !snap.Content.EmptyResult {
			t.Fatalf("%+v %v", snap, e)
		}
	})
	t.Run("A05_frozen_commit_C01_repeat_content", func(t *testing.T) {
		src := source{files: map[string][]byte{"guide.md": []byte("guide")}}
		first, e := knowledge.Capture(ctx, src, req)
		if e != nil {
			t.Fatal(e)
		}
		repeat, e := knowledge.Capture(ctx, src, req)
		if e != nil || repeat.ID != first.ID {
			t.Fatalf("%+v %v", repeat, e)
		}
		next := req
		next.Commit = strings.Repeat("b", 40)
		second, e := knowledge.Capture(ctx, src, next)
		if e != nil || second.ID == first.ID || first.Content.Commit != req.Commit {
			t.Fatalf("%+v %v", second, e)
		}
	})
	t.Run("A06_C02_prefilled_is_not_confirmation", func(t *testing.T) {
		_, e := knowledge.Assemble(knowledge.InputRequest{WorkspaceID: "w1", SubjectID: "work-1", RequirementVersion: "1", ProjectCommit: strings.Repeat("a", 40), DefinitionDigest: strings.Repeat("b", 64), PolicyVersion: "1", ConfigVersion: "1"}, knowledge.Snapshot{}, knowledge.BindingEvent{})
		if !errors.Is(e, knowledge.ErrUnconfirmed) {
			t.Fatalf("got %v", e)
		}
	})
}

func TestEffectiveInputBindings(t *testing.T) {
	snap, err := knowledge.Capture(t.Context(), source{}, knowledge.Selection{WorkspaceID: "w", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{}})
	if err != nil {
		t.Fatal(err)
	}
	event := knowledge.BindingEvent{ID: "event-1", WorkspaceID: "w", SubjectID: "work", SnapshotID: snap.ID, HumanResponseID: "answer-1", Version: 1, ConfirmedAt: time.Now()}
	req := knowledge.InputRequest{WorkspaceID: "w", SubjectID: "work", RequirementVersion: "1", ProjectCommit: strings.Repeat("a", 40), DefinitionDigest: artifact.Digest([]byte("definition")), PolicyVersion: "1", ConfigVersion: "1"}
	for _, tc := range []struct {
		name   string
		fields []knowledge.Field
	}{
		{"reserved", []knowledge.Field{{Name: "policyVersion", Type: "string", Value: []byte(`"override"`)}}},
		{"duplicate", []knowledge.Field{{Name: "x", Type: "string", Value: []byte(`"a"`)}, {Name: "x", Type: "string", Value: []byte(`"b"`)}}},
		{"type_mismatch", []knowledge.Field{{Name: "x", Type: "boolean", Value: []byte(`"true"`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			r.Fields = tc.fields
			if _, e := knowledge.Assemble(r, snap, event); !errors.Is(e, knowledge.ErrValidation) {
				t.Fatalf("got %v", e)
			}
		})
	}
	req.Fields = []knowledge.Field{{Name: "z", Type: "boolean", Value: []byte(`true`)}, {Name: "a", Type: "string", Value: []byte(`"value"`)}}
	first, err := knowledge.Assemble(req, snap, event)
	if err != nil {
		t.Fatal(err)
	}
	req.Fields[0], req.Fields[1] = req.Fields[1], req.Fields[0]
	second, err := knowledge.Assemble(req, snap, event)
	if err != nil || first.Digest != second.Digest {
		t.Fatalf("ordering changes identity %v", err)
	}
	req.Fields[0].Value[1] = 'X'
	if string(first.Content.Fields[0].Value) != `"value"` {
		t.Fatal("caller mutated frozen input")
	}
}

func TestMissingRequiredUpstream(t *testing.T) {
	snap, err := knowledge.Capture(t.Context(), source{}, knowledge.Selection{WorkspaceID: "w", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{}})
	if err != nil {
		t.Fatal(err)
	}
	event := knowledge.BindingEvent{ID: "event", WorkspaceID: "w", SubjectID: "work", SnapshotID: snap.ID, HumanResponseID: "answer", Version: 1, ConfirmedAt: time.Now()}
	req := knowledge.InputRequest{WorkspaceID: "w", SubjectID: "work", RequirementVersion: "1", ProjectCommit: strings.Repeat("a", 40), DefinitionDigest: artifact.Digest([]byte("definition")), PolicyVersion: "1", ConfigVersion: "1", RequiredUpstream: []string{"plan"}}
	if _, err = knowledge.Assemble(req, snap, event); !errors.Is(err, knowledge.ErrValidation) {
		t.Fatalf("missing required upstream accepted %v", err)
	}
}

func TestRequiredUpstreamDeclarations(t *testing.T) {
	snapshot, err := knowledge.Capture(t.Context(), source{}, knowledge.Selection{WorkspaceID: "w", RepositoryID: "10", Commit: strings.Repeat("a", 40), Entries: []knowledge.Entry{}})
	if err != nil {
		t.Fatal(err)
	}
	event := knowledge.BindingEvent{ID: "event", WorkspaceID: "w", SubjectID: "work", SnapshotID: snapshot.ID, HumanResponseID: "answer", Version: 1, ConfirmedAt: time.Now()}
	ref := artifact.Ref{SchemaVersion: artifact.SchemaVersion, ArtifactID: strings.Repeat("a", 48), WorkspaceID: "w", SubjectID: "work", Kind: "plan", Producer: artifact.Producer{Kind: "operation", ID: "op"}, Digest: artifact.Digest([]byte("plan")), Size: 4, MediaType: "text/plain", StorageRef: "artifact:" + strings.Repeat("a", 48)}
	req := knowledge.InputRequest{WorkspaceID: "w", SubjectID: "work", RequirementVersion: "1", ProjectCommit: strings.Repeat("a", 40), DefinitionDigest: artifact.Digest([]byte("definition")), PolicyVersion: "1", ConfigVersion: "1", RequiredUpstream: []string{"plan"}, Upstream: []knowledge.Upstream{{Name: "plan", NodeRunID: "node", Ref: ref}}}
	if _, err = knowledge.Assemble(req, snapshot, event); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"plan", "plan"}, {"../plan"}, {"missing"}} {
		req.RequiredUpstream = names
		if _, err = knowledge.Assemble(req, snapshot, event); !errors.Is(err, knowledge.ErrValidation) {
			t.Fatalf("invalid declarations %v: %v", names, err)
		}
	}
}
