package orchestration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
)

// The S06 rework pins the two facts the W1 real run exposed: every capability
// node receives the frozen requirement/acceptance text and knowledge binding it
// needs to work on the actual task, and a decided round that does not carry the
// capability's declared output fails closed instead of letting the advancement
// stop without a diagnostic.

// TestTheFrozenCapabilityInputCarriesTheRequirementAndKnowledge pins the input
// delivery chain: plan and code must read the WorkItem's requirement and
// acceptance text, the frozen knowledge binding and the declared output
// contract from their own frozen EffectiveInput.
func TestTheFrozenCapabilityInputCarriesTheRequirementAndKnowledge(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.advance(t, result.Admission.RunID, 2)

	plan := f.lease(t)
	if plan.Task.Capability.ID != "plan.create" {
		t.Fatalf("the first dispatched capability is %s", plan.Task.Capability.ID)
	}
	requireCapabilityFields(t, f, plan.Task.ID, map[string]string{
		"requirement_title":       "Add a deterministic greeting helper",
		"requirement_content":     "add a helper",
		"acceptance_content":      "a passing test",
		"knowledge_repository_id": "9002",
		"knowledge_commit":        repeat("2", 40),
		"declared_outputs":        `{"plan_ref":"artifact_ref"}`,
	})

	// The lease taken above carries the plan result; the review follows, and its
	// gate must know which verdicts the flow can interpret.
	f.reportCapabilityOutput(t, plan, map[string]any{"summary": "the plan for the helper"})
	f.advance(t, result.Admission.RunID, 2)
	review := f.leaseCapability(t, "plan.review")
	requireCapabilityFields(t, f, review.Task.ID, map[string]string{
		"declared_outputs":    `{"review_ref":"artifact_ref","verdict":"string"}`,
		"verdict_vocabulary":  `["approved","passed","rejected","needs_revision","failed"]`,
		"requirement_content": "add a helper",
	})

	// The same delivery reaches the coding node, the second node the rework's
	// ruling names.
	f.completeCapabilityOutput(t, result.Admission.RunID, review,
		map[string]any{"verdict": "approved", "summary": "reviewed"})
	approval := f.openHumanTask(t, result.Admission.RunID, fixedflow.HumanTaskPlanApproval)
	f.answer(t, approval, "approved", map[string]string{"feedback": "go ahead"})
	f.advance(t, result.Admission.RunID, 4)

	code := f.lease(t)
	if code.Task.Capability.ID != "code.implement" {
		t.Fatalf("the coding capability is %s", code.Task.Capability.ID)
	}
	requireCapabilityFields(t, f, code.Task.ID, map[string]string{
		"requirement_content": "add a helper",
		"acceptance_content":  "a passing test",
		"declared_outputs":    `{"change_ref":"artifact_ref","commit_oid":"string"}`,
	})
}

// completeCapabilityOutput reports one completed result for an already leased
// attempt and lets the reconciler apply it.
func (f *advanceFixture) completeCapabilityOutput(
	t *testing.T,
	runID string,
	lease execution.TaskLease,
	output map[string]any,
) {
	t.Helper()
	f.reportCapabilityOutput(t, lease, output)
	f.advance(t, runID, 2)
}

// reportCapabilityOutput reports the completed result of an attempt the test
// already leased.
func (f *advanceFixture) reportCapabilityOutput(t *testing.T, lease execution.TaskLease, output map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("encode the scripted output: %v", err)
	}
	if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: artifact.Digest(encoded),
		Summary: "scripted", Output: output,
	}); err != nil {
		t.Fatalf("report the %s attempt: %v", lease.Task.Capability.ID, err)
	}
}

// requireCapabilityFields proves the frozen input of one attempt carries the
// named string fields with the given values.
func requireCapabilityFields(t *testing.T, f *advanceFixture, taskID string, expected map[string]string) {
	t.Helper()
	fields := frozenInputFields(t, f, taskID)
	for name, want := range expected {
		if got := fields[name]; got != want {
			t.Errorf("frozen input field %s of attempt %s = %q, want %q", name, taskID, got, want)
		}
	}
}

// frozenInputFields reads back the delivered fields of one attempt's frozen
// EffectiveInput through the knowledge port that persisted it.
func frozenInputFields(t *testing.T, f *advanceFixture, taskID string) map[string]string {
	t.Helper()
	binding, err := f.store.Binding(t.Context(), taskID)
	if err != nil {
		t.Fatalf("the attempt has no frozen binding: %v", err)
	}
	port, ok := f.options.Knowledge.(*memoryKnowledge)
	if !ok {
		t.Fatal("the fixture knowledge port is not the memory port")
	}
	input, err := port.ReadInput(t.Context(),
		artifact.Scope{WorkspaceID: f.workspaceID, ActorID: f.actorID}, binding.InputID)
	if err != nil {
		t.Fatalf("read the frozen input %s: %v", binding.InputID, err)
	}
	fields := map[string]string{}
	for _, field := range input.Content.Fields {
		var value string
		if err := json.Unmarshal(field.Value, &value); err != nil {
			t.Fatalf("frozen input field %s is not a string: %v", field.Name, err)
		}
		fields[field.Name] = value
	}
	return fields
}

// TestADecidedCapabilityRoundWithoutItsDeclaredOutputFailsClosed pins the
// fail-closed rule: a completed attempt that carries no declared output must be
// rejected with a diagnostic instead of letting the reconciler stop silently.
func TestADecidedCapabilityRoundWithoutItsDeclaredOutputFailsClosed(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.advance(t, result.Admission.RunID, 2)

	lease := f.lease(t)
	// The frozen S02 acceptance only requires the output object to exist, so an
	// empty result reaches the decision on its own; the capability contract is
	// what refuses it.
	if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: artifact.Digest([]byte("no declared output")),
		Summary: "no declared output", Output: map[string]any{},
	}); err != nil {
		t.Fatalf("report the empty attempt: %v", err)
	}

	err := f.service.Advance(t.Context(), result.Admission.RunID)
	if err == nil {
		t.Fatal("a decided capability round without its declared output advanced the run silently")
	}
	for _, want := range []string{"plan", "plan_ref"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// Nothing may be published for the refused round, and no downstream node may
	// run on it.
	if records, err := f.store.ArtifactsForWorkItem(t.Context(), f.workspaceID,
		f.workitemOf(t, result.Admission.RunID)); err != nil {
		t.Fatalf("read the published artifacts: %v", err)
	} else if len(records) != 0 {
		t.Fatalf("the refused round published %d deliverables", len(records))
	}
	// No downstream node ran on the refused decision: the flow stays on the
	// defective round until a human-controlled recovery replaces it.
	view, err := f.execution.RunView(t.Context(), result.Admission.RunID)
	if err != nil {
		t.Fatalf("read the run view: %v", err)
	}
	for _, round := range view.Nodes {
		if round.NodeID == fixedflow.NodePlanReview {
			t.Fatalf("a downstream round ran on a decision that carries no declared output: %+v", round)
		}
	}

	// The refusal is not a dead end: a controlled rerun of the round with a
	// conforming result continues the flow.
	f.recoverRerun(t, result.Admission.RunID, fixedflow.NodePlan)
	f.completeCapability(t, result.Admission.RunID, "plan.create")
	review := f.leaseCapability(t, "plan.review")
	if review.Task.Capability.ID != "plan.review" {
		t.Fatalf("the rebuilt flow dispatched %s", review.Task.Capability.ID)
	}
}

// TestADecidedGateWithoutItsVerdictFailsClosed pins that a gate which decides
// without the verdict its capability declares is refused explicitly: a closed
// gate with no diagnostic would keep every downstream node waiting forever.
func TestADecidedGateWithoutItsVerdictFailsClosed(t *testing.T) {
	f := newAdvanceFixture(t)
	result := f.admittedRun(t)
	f.answerKnowledge(t, result)
	f.completeCapability(t, result.Admission.RunID, "plan.create")

	lease := f.leaseCapability(t, "plan.review")
	if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: artifact.Digest([]byte("no verdict")),
		Summary: "no verdict", Output: map[string]any{"summary": "the plan looks fine"},
	}); err != nil {
		t.Fatalf("report the verdict-less review: %v", err)
	}

	err := f.service.Advance(t.Context(), result.Admission.RunID)
	if err == nil {
		t.Fatal("a decided gate without its declared verdict was accepted silently")
	}
	for _, want := range []string{"plan_review", "verdict"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// The misreported verdict is refused as a defect of the attempt, never
	// reinterpreted as a closed gate: the run does not advance, and the fast
	// diagnostic is what the operator sees on every pass.
	if _, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	}); err == nil {
		t.Error("a downstream attempt was dispatched for a gate the capability never answered")
	}
}
