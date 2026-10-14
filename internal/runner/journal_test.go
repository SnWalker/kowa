package runner_test

import (
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/runner"
)

func TestJournalAttemptRoundTrip(t *testing.T) {
	journal, err := runner.NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := runner.AttemptRecord{
		TaskID: "task/a b", LeaseID: "lease-1", FencingToken: 1,
		RuntimeID: "runtime-1", RuntimeEpoch: "epoch-1",
		WorkflowRunID: "run-1", NodeRunID: "node-1",
		AttemptKind: execution.AttemptInitial,
		Capability:  execution.Capability{ID: "plan.create", Version: 1},
		Phase:       runner.PhaseExecuting, Workspace: "/tmp/ws", Marker: "marker-1",
		StartedAt: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 10, 14, 0, 0, 1, 0, time.UTC),
	}
	if err := journal.SaveAttempt(record); err != nil {
		t.Fatal(err)
	}
	records, err := journal.Attempts()
	if err != nil || len(records) != 1 {
		t.Fatalf("attempts = %+v, %v", records, err)
	}
	if records[0].TaskID != record.TaskID || records[0].Marker != record.Marker ||
		records[0].Capability != record.Capability {
		t.Fatalf("round trip = %+v", records[0])
	}
	if err := journal.ClearAttempt(record.TaskID); err != nil {
		t.Fatal(err)
	}
	records, err = journal.Attempts()
	if err != nil || len(records) != 0 {
		t.Fatalf("attempts after clear = %+v, %v", records, err)
	}
	// Clearing an unknown attempt is a no-op.
	if err := journal.ClearAttempt("never-saved"); err != nil {
		t.Fatal(err)
	}
}

func TestJournalResultRoundTripPreservesBytes(t *testing.T) {
	journal, err := runner.NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"schemaVersion":"kowa.workflow-execution.v1","kind":"taskResult","taskId":"task-1"}`)
	if _, pending, err := journal.Result("task-1"); err != nil || pending {
		t.Fatalf("missing result = pending=%v, %v", pending, err)
	}
	if err := journal.SaveResult("task-1", payload); err != nil {
		t.Fatal(err)
	}
	stored, pending, err := journal.Result("task-1")
	if err != nil || !pending || string(stored) != string(payload) {
		t.Fatalf("result = %s, pending=%v, %v", stored, pending, err)
	}
	if runner.ResultDigest(stored) != runner.ResultDigest(payload) {
		t.Fatal("digest is not stable across the journal round trip")
	}
	if err := journal.ClearResult("task-1"); err != nil {
		t.Fatal(err)
	}
	if _, pending, _ := journal.Result("task-1"); pending {
		t.Fatal("result survived clear")
	}
}

func TestJournalEpochPersistsAcrossJournalInstances(t *testing.T) {
	root := t.TempDir()
	journal, err := runner.NewJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := journal.Epoch()
	if err != nil || first == "" {
		t.Fatalf("epoch = %q, %v", first, err)
	}
	again, err := journal.Epoch()
	if err != nil || again != first {
		t.Fatalf("epoch is not stable within one state: %q vs %q", again, first)
	}
	// A restart of the process must keep the generation so in-flight leases
	// stay current until the control plane supersedes them explicitly.
	restarted, err := runner.NewJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := restarted.Epoch()
	if err != nil || reloaded != first {
		t.Fatalf("epoch after restart = %q, want %q", reloaded, first)
	}
	rotated, err := restarted.RotateEpoch()
	if err != nil || rotated == first {
		t.Fatalf("rotated epoch = %q, want a fresh generation", rotated)
	}
	final, err := restarted.Epoch()
	if err != nil || final != rotated {
		t.Fatalf("rotated epoch not persisted: %q vs %q", final, rotated)
	}
}

func TestJournalSessionsKeepTheNewestPerNode(t *testing.T) {
	journal, err := runner.NewJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := runner.SessionRecord{
		WorkflowRunID: "run-1", NodeRunID: "node-1", ProviderID: "codex", SessionRef: "session-1",
	}
	first := base
	first.TaskID = "task-1"
	if err := journal.SaveSession(first); err != nil {
		t.Fatal(err)
	}
	second := base
	second.TaskID = "task-2"
	second.SessionRef = "session-2"
	if err := journal.SaveSession(second); err != nil {
		t.Fatal(err)
	}
	if _, found, err := journal.Session("task-1"); err != nil || found {
		t.Fatalf("superseded session still present: found=%v, %v", found, err)
	}
	record, found, err := journal.Session("task-2")
	if err != nil || !found || record.SessionRef != "session-2" {
		t.Fatalf("latest session = %+v, found=%v, %v", record, found, err)
	}
	// Sessions of a different node are untouched.
	other := base
	other.TaskID = "task-3"
	other.NodeRunID = "node-2"
	other.SessionRef = "session-3"
	if err := journal.SaveSession(other); err != nil {
		t.Fatal(err)
	}
	if _, found, err := journal.Session("task-2"); err != nil || !found {
		t.Fatalf("unrelated save pruned another node: found=%v, %v", found, err)
	}
}

func TestSafeNameIsFilesystemSafeAndDeterministic(t *testing.T) {
	dangerous := "../../etc/passwd"
	safe := runner.SafeName(dangerous)
	if safe == dangerous {
		t.Fatal("dangerous name was not sanitized")
	}
	for _, character := range safe {
		if character == '/' || character == '\\' || character == '.' && len(safe) < 3 {
			t.Fatalf("unsafe character in %q", safe)
		}
	}
	if runner.SafeName(dangerous) != safe {
		t.Fatal("SafeName is not deterministic")
	}
	if runner.SafeName("a/b") == runner.SafeName("a_b") {
		t.Fatal("sanitized collisions are not disambiguated")
	}
}
