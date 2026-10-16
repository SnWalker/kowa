package orchestration

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/workflow"
)

// This white-box test pins the fail-closed backstop of upstreamsOf. The state it
// refuses — a declared input producer whose round decided but never published its
// deliverable — is not produced by this build anymore (a decided round is
// validated at settlement and a conforming round always publishes), so it stays
// reachable only from facts that predate the rule (a run decided by an earlier
// build, exactly like the W1 run) or from a store that lost the record. The
// branch must still refuse explicitly: reporting "no action" is what let the W1
// run stop without a diagnostic.
func TestADeclaredInputWithoutAPublishedDeliverableIsRefused(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile the fixed flow: %v", err)
	}
	service := &Service{store: NewMemoryStore(), plan: plan, now: time.Now}
	admission := Admission{RunID: "run-1", WorkspaceID: "ws-1", WorkItemID: "item-1"}
	// The plan round decided, and no deliverable of it was ever published.
	view := execution.RunView{ID: "run-1", Nodes: []execution.NodeView{{
		ID: "run-1:node:plan:1", NodeID: fixedflow.NodePlan, Iteration: 1, State: execution.NodeDecided,
	}}}

	_, err = service.upstreamsOf(t.Context(), admission, plan.Nodes[fixedflow.NodePlanReview], view)
	if err == nil {
		t.Fatal("a declared input without a published deliverable reported no action")
	}
	if !errors.Is(err, ErrPolicyBlocked) {
		t.Fatalf("the refusal is not a policy block: %v", err)
	}
	for _, want := range []string{fixedflow.NodePlanReview, fixedflow.NodePlan, "required input"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// An optional binding that was never published stays absent instead of
	// refusing the dispatch.
	optional := workflow.Node{
		ID: "optional_consumer", Type: workflow.NodeCapability,
		Inputs: []workflow.InputBinding{{
			Key: "extra", ProducerNodeID: fixedflow.NodePlan, Output: "plan_ref",
			Type: workflow.ValueArtifactRef, Required: false,
		}},
	}
	upstreams, err := service.upstreamsOf(t.Context(), admission, optional, view)
	if err != nil {
		t.Fatalf("an optional input without a deliverable refused the dispatch: %v", err)
	}
	if len(upstreams) != 0 {
		t.Fatalf("an unpublished optional input produced upstreams: %+v", upstreams)
	}
}

// TestTheDeclaredOutputRuleMatchesWhatTheFlowConsumes pins the exact strictness
// of validateDecidedOutput: a decision the flow's own consumers can read is
// never refused, and one they cannot read always is. The two named outputs are
// the ones whose producer may be the control plane: the Runner injects the
// controlled delivery record for the pushed commit, and the pull request number
// is read under either spelling.
func TestTheDeclaredOutputRuleMatchesWhatTheFlowConsumes(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile the fixed flow: %v", err)
	}
	service := &Service{store: NewMemoryStore(), plan: plan, now: time.Now}
	cases := []struct {
		name    string
		nodeID  string
		output  map[string]any
		refused bool
	}{
		{
			name:   "the runner's controlled delivery satisfies the declared commit",
			nodeID: fixedflow.NodeCode, output: map[string]any{
				"summary":  "implemented",
				"delivery": []any{map[string]any{"commitOid": "aaaabbbb"}},
			},
		},
		{
			name:   "a code round that delivered nothing is refused",
			nodeID: fixedflow.NodeCode, output: map[string]any{"summary": "implemented"}, refused: true,
		},
		{
			name:   "a publish_pr round reporting the observation spelling is accepted",
			nodeID: fixedflow.NodePublishPR, output: map[string]any{"pullRequestNumber": "42"},
		},
		{
			name:   "a publish_pr round reporting no pull request is refused",
			nodeID: fixedflow.NodePublishPR, output: map[string]any{"summary": "opened"}, refused: true,
		},
		{
			name:   "a gate asking for a revision is a decision, not a defect",
			nodeID: fixedflow.NodePlanReview, output: map[string]any{"verdict": "needs_revision"},
		},
		{
			name:   "a gate word outside the vocabulary is a defect",
			nodeID: fixedflow.NodePlanReview, output: map[string]any{"verdict": "lgtm"}, refused: true,
		},
		{
			name:   "a gate without any verdict is a defect",
			nodeID: fixedflow.NodePlanReview, output: map[string]any{"summary": "fine"}, refused: true,
		},
		{
			name:   "a content node must carry some result",
			nodeID: fixedflow.NodePlan, output: map[string]any{}, refused: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			round := execution.NodeView{
				ID: "run-1:" + testCase.nodeID + ":1", NodeID: testCase.nodeID,
				Iteration: 1, State: execution.NodeDecided, Output: testCase.output,
			}
			err := service.validateDecidedOutput(plan.Nodes[testCase.nodeID], round)
			if testCase.refused && err == nil {
				t.Fatalf("the decision %+v was accepted, want a refusal", testCase.output)
			}
			if !testCase.refused && err != nil {
				t.Fatalf("the decision %+v was refused: %v", testCase.output, err)
			}
		})
	}
}
