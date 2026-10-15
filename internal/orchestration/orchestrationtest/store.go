// Package orchestrationtest holds the adapter-independent contract of the
// orchestration store, so an in-memory run and a PostgreSQL run can never
// disagree about what the store promises.
package orchestrationtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
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
	// SettleRun marks the delivery run of one admission as settled, through the
	// adapter's own authority (the mirror note or the run table itself).
	SettleRun func(*testing.T, string)
	// InputID provisions (when the adapter needs it) and returns one persisted
	// effective input the binding may reference.
	InputID func(*testing.T, StoreEnv) string
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
			InputID: env.InputID(t, env), InputDigest: "sha256:" + repeat("a", 64),
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
		// Every frozen field is part of the binding identity: a replay with any
		// drifted identity, input, repository, capability, provider, scope,
		// artifact or deadline field is a version conflict, never a silent
		// reuse of the old facts.
		otherScopes := []orchestration.Scope{{
			RepositoryID: admission.ProjectRepositoryID, Role: "project", Operation: "push",
			Ref: "refs/heads/task/kowa-other",
		}}
		drifts := map[string]func(*orchestration.Binding){
			"workspace":            func(b *orchestration.Binding) { b.WorkspaceID = "other-workspace" },
			"run":                  func(b *orchestration.Binding) { b.RunID = admission.RunID + "-other" },
			"node":                 func(b *orchestration.Binding) { b.NodeID = "code" },
			"node round":           func(b *orchestration.Binding) { b.NodeRunID = binding.NodeRunID + ":2" },
			"input":                func(b *orchestration.Binding) { b.InputID = "other-input" },
			"project repository":   func(b *orchestration.Binding) { b.ProjectRepositoryID = "4242" },
			"project commit":       func(b *orchestration.Binding) { b.ProjectCommitOID = repeat("f", 40) },
			"knowledge repository": func(b *orchestration.Binding) { b.KnowledgeRepositoryID = "4243" },
			"knowledge commit":     func(b *orchestration.Binding) { b.KnowledgeCommitOID = repeat("e", 40) },
			"capability":           func(b *orchestration.Binding) { b.CapabilityID = "code.implement" },
			"capability version":   func(b *orchestration.Binding) { b.CapabilityVersion = 2 },
			"provider":             func(b *orchestration.Binding) { b.ProviderID = "other-provider" },
			"provider version":     func(b *orchestration.Binding) { b.ProviderVersion = "scripted-2" },
			"scopes":               func(b *orchestration.Binding) { b.Scopes = otherScopes },
			"artifacts":            func(b *orchestration.Binding) { b.ArtifactIDs = []string{"artifact-other"} },
			"deadline":             func(b *orchestration.Binding) { b.DeadlineAt = b.DeadlineAt.Add(time.Second) },
		}
		for name, drift := range drifts {
			changed := binding
			drift(&changed)
			if _, err := env.Store.BindInput(t.Context(), changed); !errors.Is(err, orchestration.ErrVersionConflict) {
				t.Fatalf("binding drifted in %s = %v, want version conflict", name, err)
			}
		}
		read, err := env.Store.Binding(t.Context(), binding.TaskID)
		if err != nil || read.InputDigest != binding.InputDigest || len(read.Scopes) != 1 {
			t.Fatalf("read binding = %+v, err=%v", read, err)
		}
		if _, err := env.Store.Binding(t.Context(), "task-absent"); !errors.Is(err, orchestration.ErrNotFound) {
			t.Fatalf("unknown binding = %v, want not found", err)
		}
	})

	t.Run("suggestions_are_fenced_per_attempt", func(t *testing.T) {
		env := newEnv(t)
		env.PrepareWorkspace(t, env.WorkspaceID)
		admission := env.AdmissionOf(t, env)
		if _, _, err := env.Store.AdmitOnce(t.Context(), admission, "7001", "admit-suggestion", commandDigest()); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		digest := "sha256:" + repeat("a", 64)
		seed := orchestration.SuggestionSeed{
			RunID: admission.RunID, WorkspaceID: env.WorkspaceID, WorkItemID: env.WorkItemID,
			InputDigest: digest, At: now,
		}
		first, err := env.Store.SeedSuggestion(t.Context(), seed)
		if err != nil || first.Attempt != 1 || first.State != orchestration.SuggestionPending ||
			first.InputDigest != digest {
			t.Fatalf("seed = %+v, err=%v", first, err)
		}
		// The seed is idempotent on the very same frozen input and refuses a
		// drifted one.
		replay, err := env.Store.SeedSuggestion(t.Context(), seed)
		if err != nil || replay.AttemptID != first.AttemptID || replay.Version != first.Version {
			t.Fatalf("seed replay = %+v, err=%v", replay, err)
		}
		drifted := seed
		drifted.InputDigest = "sha256:" + repeat("b", 64)
		if _, err := env.Store.SeedSuggestion(t.Context(), drifted); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("drifted seed = %v, want version conflict", err)
		}
		// A failed transition is only reachable through a claim, so the seed
		// cannot be finalised directly either way.
		if _, err := env.Store.FinalizeSuggestion(t.Context(), orchestration.SuggestionOutcome{
			RunID: admission.RunID, Attempt: 1, ExpectedVersion: first.Version, FailureReason: "x", At: now,
		}); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("finalise without a claim = %v, want version conflict", err)
		}
		// Claiming fences the attempt: while the lease runs, a second claim is
		// refused; once it expires, the attempt is claimable again with a
		// bumped version.
		claimed, err := env.Store.ClaimSuggestion(t.Context(), orchestration.SuggestionClaim{
			AttemptID: first.AttemptID, LeaseUntil: now.Add(time.Minute), At: now,
		})
		if err != nil || claimed.State != orchestration.SuggestionProcessing || claimed.Version <= first.Version {
			t.Fatalf("claim = %+v, err=%v", claimed, err)
		}
		if _, err := env.Store.ClaimSuggestion(t.Context(), orchestration.SuggestionClaim{
			AttemptID: first.AttemptID, LeaseUntil: now.Add(2 * time.Minute), At: now.Add(time.Second),
		}); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("claim under a running lease = %v, want version conflict", err)
		}
		reclaimed, err := env.Store.ClaimSuggestion(t.Context(), orchestration.SuggestionClaim{
			AttemptID: first.AttemptID, LeaseUntil: now.Add(3 * time.Minute), At: now.Add(2 * time.Minute),
		})
		if err != nil || reclaimed.Version <= claimed.Version {
			t.Fatalf("re-claim after lease expiry = %+v, err=%v", reclaimed, err)
		}
		// A stale claimant cannot settle: the version moved on.
		if _, err := env.Store.FinalizeSuggestion(t.Context(), orchestration.SuggestionOutcome{
			RunID: admission.RunID, Attempt: 1, ExpectedVersion: claimed.Version,
			ArtifactID: "artifact-late", ArtifactDigest: "sha256:" + repeat("c", 64), At: now,
		}); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("stale finalise = %v, want version conflict", err)
		}
		// Failure settles the attempt, keeps the reason, and a retry opens the
		// next attempt; the newest row is the current state.
		failed, err := env.Store.FinalizeSuggestion(t.Context(), orchestration.SuggestionOutcome{
			RunID: admission.RunID, Attempt: 1, ExpectedVersion: reclaimed.Version,
			FailureReason: "the artifact store is unavailable", At: now.Add(2 * time.Minute),
		})
		if err != nil || failed.State != orchestration.SuggestionFailed ||
			failed.FailureReason != "the artifact store is unavailable" {
			t.Fatalf("failed finalise = %+v, err=%v", failed, err)
		}
		if _, err := env.Store.FinalizeSuggestion(t.Context(), orchestration.SuggestionOutcome{
			RunID: admission.RunID, Attempt: 1, ExpectedVersion: failed.Version,
			ArtifactID: "artifact-late", ArtifactDigest: "sha256:" + repeat("c", 64), At: now,
		}); !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("finalise after settlement = %v, want version conflict", err)
		}
		second, err := env.Store.RequestSuggestion(t.Context(), orchestration.SuggestionRequest{
			RunID: admission.RunID, At: now.Add(3 * time.Minute),
		})
		if err != nil || second.Attempt != 2 || second.State != orchestration.SuggestionPending ||
			second.InputDigest != digest || second.AttemptID == first.AttemptID {
			t.Fatalf("request = %+v, err=%v", second, err)
		}
		// One open attempt per run, and a request only follows a failure.
		if _, err := env.Store.RequestSuggestion(t.Context(), orchestration.SuggestionRequest{
			RunID: admission.RunID, At: now.Add(4 * time.Minute),
		}); !errors.Is(err, orchestration.ErrPolicyBlocked) && !errors.Is(err, orchestration.ErrVersionConflict) {
			t.Fatalf("second open attempt = %v, want a refusal", err)
		}
		// The claimable scan sees the pending attempt and nothing else.
		claimable, err := env.Store.ClaimableSuggestions(t.Context(), now.Add(4*time.Minute), 10)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, attempt := range claimable {
			if attempt.AttemptID == second.AttemptID {
				found = true
			}
			if attempt.AttemptID == first.AttemptID {
				t.Fatalf("a settled attempt is claimable: %+v", attempt)
			}
		}
		if !found {
			t.Fatalf("the pending attempt is not claimable: %+v", claimable)
		}
		// Publish the second attempt and pin the append-only history: the
		// failed attempt stays readable next to the published one.
		claimedSecond, err := env.Store.ClaimSuggestion(t.Context(), orchestration.SuggestionClaim{
			AttemptID: second.AttemptID, LeaseUntil: now.Add(5 * time.Minute), At: now.Add(4 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		published, err := env.Store.FinalizeSuggestion(t.Context(), orchestration.SuggestionOutcome{
			RunID: admission.RunID, Attempt: 2, ExpectedVersion: claimedSecond.Version,
			ArtifactID: "artifact-suggestion", ArtifactDigest: "sha256:" + repeat("d", 64),
			At: now.Add(4 * time.Minute),
		})
		if err != nil || published.State != orchestration.SuggestionPublished ||
			published.ArtifactID != "artifact-suggestion" || published.FailureReason != "" {
			t.Fatalf("published finalise = %+v, err=%v", published, err)
		}
		// A published attempt is never retried.
		if _, err := env.Store.RequestSuggestion(t.Context(), orchestration.SuggestionRequest{
			RunID: admission.RunID, At: now.Add(5 * time.Minute),
		}); !errors.Is(err, orchestration.ErrPolicyBlocked) {
			t.Fatalf("request after publication = %v, want policy blocked", err)
		}
		history, err := env.Store.SuggestionAttempts(t.Context(), admission.RunID)
		if err != nil || len(history) != 2 || history[0].State != orchestration.SuggestionFailed ||
			history[1].State != orchestration.SuggestionPublished {
			t.Fatalf("history = %+v, err=%v", history, err)
		}
		if _, err := env.Store.RequestSuggestion(t.Context(), orchestration.SuggestionRequest{
			RunID: "run-absent", At: now,
		}); !errors.Is(err, orchestration.ErrNotFound) {
			t.Fatalf("request for an unknown run = %v, want not found", err)
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
		// The recorded outcome is compared as JSON: an adapter may store it in a
		// normalising column, and consumers decode it into their own type.
		if err != nil || !found || !sameJSON(t, stored, response) {
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
		// A run whose delivery run settled is not reconcilable on either
		// adapter: the memory mirror and the durable run table agree.
		env.SettleRun(t, admission.RunID)
		active, err = env.Store.ActiveRuns(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, runID := range active {
			if runID == admission.RunID {
				t.Fatalf("settled run %s is still reconcilable: %v", admission.RunID, active)
			}
		}
	})
}

// commandDigest mirrors the bare-hex digest the command-idempotency table
// stores.
func commandDigest() string {
	sum := sha256.Sum256([]byte("orchestration-test"))
	return hex.EncodeToString(sum[:])
}

// sameJSON compares two encoded values as JSON documents.
func sameJSON(t *testing.T, left, right []byte) bool {
	t.Helper()
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatalf("decode the replayed command: %v", err)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatalf("decode the recorded command: %v", err)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func repeat(character string, count int) string {
	repeated := make([]byte, count)
	for index := range repeated {
		repeated[index] = character[0]
	}
	return string(repeated)
}
