// Package orchestrationtest holds the adapter-independent contract of the
// orchestration store, so an in-memory run and a PostgreSQL run can never
// disagree about what the store promises.
package orchestrationtest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/orchestration"
)

// StoreEnv supplies one adapter under test.
type StoreEnv struct {
	Store       orchestration.Store
	WorkspaceID string
	WorkItemID  string
	// PrepareWorkspace makes the workspace the admission's foreign keys need.
	PrepareWorkspace func(*testing.T, string)
	// AdmissionOf creates one admitted run and returns its admission.
	AdmissionOf func(*testing.T, StoreEnv) orchestration.Admission
}

// StoreContract applies the same invariants to each adapter.
func StoreContract(t *testing.T, newEnv func(*testing.T) StoreEnv) {
	t.Run("admission_is_idempotent_and_conflicts_on_other_content", func(t *testing.T) {
		env := newEnv(t)
		env.PrepareWorkspace(t, env.WorkspaceID)
		admission := env.AdmissionOf(t, env)
		digest := commandDigest()
		stored, replayed, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-1", digest)
		if err != nil || replayed || stored.RunID != admission.RunID {
			t.Fatalf("first admission = %+v, replayed=%v, err=%v", stored, replayed, err)
		}
		replay, replayed, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-1", digest)
		if err != nil || !replayed || replay.RunID != admission.RunID {
			t.Fatalf("replayed admission = %+v, replayed=%v, err=%v", replay, replayed, err)
		}
		if _, _, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-1", commandDigest()+"0"); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("same request key with other content = %v, want version conflict", err)
		}
		read, err := env.Store.Admission(t.Context(), admission.RunID)
		if err != nil || read.ProjectCommitOID != admission.ProjectCommitOID {
			t.Fatalf("read admission = %+v, err=%v", read, err)
		}
		if _, err := env.Store.Admission(t.Context(), "run-absent"); !errors.Is(err, orchestration.ErrNotFound) {
			t.Fatalf("unknown admission = %v, want not found", err)
		}
	})

	t.Run("recommendation_is_frozen_and_confirmable_once", func(t *testing.T) {
		env := newEnv(t)
		env.PrepareWorkspace(t, env.WorkspaceID)
		admission := env.AdmissionOf(t, env)
		if _, _, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-2", commandDigest()); err != nil {
			t.Fatal(err)
		}
		recommendation := orchestration.Recommendation{
			RunID: admission.RunID, WorkspaceID: env.WorkspaceID, WorkItemID: env.WorkItemID,
			KnowledgeRepositoryID: admission.KnowledgeRepositoryID,
			KnowledgeCommitOID:    admission.KnowledgeCommitOID,
			IndexPath:             "kowa-index.json", IndexDigest: "sha256:" + repeat("a", 64),
			State: orchestration.StateRecommended, RecommendedAt: time.Now().UTC(),
			Candidates: []orchestration.Candidate{
				{Path: "docs/design.md", Digest: "sha256:" + repeat("b", 64), Scope: "design", Required: true},
			},
		}
		stored, err := env.Store.Recommend(t.Context(), recommendation)
		if err != nil || stored.State != orchestration.StateRecommended || len(stored.Candidates) != 1 {
			t.Fatalf("recommend = %+v, err=%v", stored, err)
		}
		if _, err := env.Store.Recommend(t.Context(), recommendation); err != nil {
			t.Fatalf("identical recommendation = %v", err)
		}
		other := recommendation
		other.IndexDigest = "sha256:" + repeat("c", 64)
		if _, err := env.Store.Recommend(t.Context(), other); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("other recommendation content = %v, want version conflict", err)
		}
		version, err := env.Store.ConfirmedKnowledgeVersion(t.Context(), env.WorkspaceID, env.WorkItemID)
		if err != nil || version != 0 {
			t.Fatalf("confirmed version before confirmation = %d, err=%v", version, err)
		}
		if _, err := env.Store.AttachConfirmationTask(t.Context(), admission.RunID, "ht-1"); err != nil {
			t.Fatalf("attach confirmation task: %v", err)
		}
		if _, err := env.Store.AttachConfirmationTask(t.Context(), admission.RunID, "ht-other"); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("second confirmation task = %v, want version conflict", err)
		}
		mutation := orchestration.ConfirmMutation{
			RunID: admission.RunID, HumanTaskID: "ht-1",
			SnapshotID: "ks-1", BindingEventID: "kb-1", EffectiveInputID: "ei-1", At: time.Now().UTC(),
		}
		confirmed, err := env.Store.ConfirmRecommendation(t.Context(), mutation)
		if err != nil || !confirmed.Confirmed() || confirmed.EffectiveInputID != "ei-1" {
			t.Fatalf("confirm = %+v, err=%v", confirmed, err)
		}
		replay, err := env.Store.ConfirmRecommendation(t.Context(), mutation)
		if err != nil || replay.EffectiveInputID != "ei-1" {
			t.Fatalf("confirmation replay = %+v, err=%v", replay, err)
		}
		otherConfirmed := orchestration.ConfirmMutation{
			RunID: admission.RunID, HumanTaskID: "ht-1",
			SnapshotID: "ks-2", BindingEventID: "kb-2", EffectiveInputID: "ei-2", At: time.Now().UTC(),
		}
		if _, err := env.Store.ConfirmRecommendation(t.Context(), otherConfirmed); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("second confirmation content = %v, want version conflict", err)
		}
		version, err = env.Store.ConfirmedKnowledgeVersion(t.Context(), env.WorkspaceID, env.WorkItemID)
		if err != nil || version != 1 {
			t.Fatalf("confirmed version after confirmation = %d, err=%v", version, err)
		}
	})

	t.Run("bindings_are_immutable_and_replays_are_idempotent", func(t *testing.T) {
		env := newEnv(t)
		env.PrepareWorkspace(t, env.WorkspaceID)
		admission := env.AdmissionOf(t, env)
		if _, _, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-3", commandDigest()); err != nil {
			t.Fatal(err)
		}
		binding := orchestration.Binding{
			TaskID: env.WorkItemID + ":task:plan:1", WorkspaceID: env.WorkspaceID, RunID: admission.RunID,
			NodeID: "plan", NodeRunID: env.WorkItemID + ":node:plan:1",
			InputID: "ei-1", InputDigest: "sha256:" + repeat("a", 64),
			ProjectRepositoryID: admission.ProjectRepositoryID, ProjectCommitOID: admission.ProjectCommitOID,
			KnowledgeRepositoryID: admission.KnowledgeRepositoryID, KnowledgeCommitOID: admission.KnowledgeCommitOID,
			CapabilityID: "plan.create", CapabilityVersion: 1, ProviderID: "scripted", ProviderVersion: "scripted-1",
			Scopes: []orchestration.Scope{{
				RepositoryID: admission.ProjectRepositoryID, Role: "project", Operation: "clone",
				Ref: admission.ProjectCommitOID,
			}},
			ArtifactIDs: []string{}, DeadlineAt: time.Now().UTC().Add(time.Hour), BoundAt: time.Now().UTC(),
		}
		stored, err := env.Store.BindInput(t.Context(), binding)
		if err != nil || stored.TaskID != binding.TaskID {
			t.Fatalf("bind = %+v, err=%v", stored, err)
		}
		replay, err := env.Store.BindInput(t.Context(), binding)
		if err != nil || replay.InputDigest != binding.InputDigest {
			t.Fatalf("binding replay = %+v, err=%v", replay, err)
		}
		drifted := binding
		drifted.InputDigest = "sha256:" + repeat("d", 64)
		if _, err := env.Store.BindInput(t.Context(), drifted); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("drifted binding = %v, want version conflict", err)
		}
		read, err := env.Store.Binding(t.Context(), binding.TaskID)
		if err != nil || read.InputDigest != binding.InputDigest || len(read.Scopes) != 1 {
			t.Fatalf("read binding = %+v, err=%v", read, err)
		}
		if _, err := env.Store.Binding(t.Context(), "task-absent"); !errors.Is(err, orchestration.ErrNotFound) {
			t.Fatalf("unknown binding = %v, want not found", err)
		}
	})

	t.Run("commands_and_active_runs_are_replayable", func(t *testing.T) {
		env := newEnv(t)
		env.PrepareWorkspace(t, env.WorkspaceID)
		admission := env.AdmissionOf(t, env)
		if _, _, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-4", commandDigest()); err != nil {
			t.Fatal(err)
		}
		digest := commandDigest()
		response := []byte(`{"applied":true}`)
		if err := env.Store.RecordCommand(t.Context(), "7001", orchestration.CommandRecovery,
			admission.RunID, "recover-1", digest, response); err != nil {
			t.Fatal(err)
		}
		stored, found, err := env.Store.ReplayedCommand(t.Context(), "7001", orchestration.CommandRecovery,
			admission.RunID, "recover-1", digest)
		if err != nil || !found || string(stored) != string(response) {
			t.Fatalf("replayed command = %s, found=%v, err=%v", stored, found, err)
		}
		if _, _, err := env.Store.ReplayedCommand(t.Context(), "7001", orchestration.CommandRecovery,
			admission.RunID, "recover-1", digest+"0"); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("same key with other content = %v, want version conflict", err)
		}
		active, err := env.Store.ActiveRuns(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		found = false
		for _, runID := range active {
			if runID == admission.RunID {
				found = true
			}
		}
		if !found {
			t.Fatalf("admitted run %s is not reconcilable: %v", admission.RunID, active)
		}
	})
}

// commandDigest mirrors the bare-hex digest the command-idempotency table
// stores.
func commandDigest() string {
	sum := sha256.Sum256([]byte("orchestration-test"))
	return hex.EncodeToString(sum[:])
}

func repeat(character string, count int) string {
	repeated := make([]byte, count)
	for index := range repeated {
		repeated[index] = character[0]
	}
	return string(repeated)
}
