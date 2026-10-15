package fixedflow_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/workflow"
)

// publishedDefinition is the definition example the kowa.fixed-workflow.v1
// contract publishes. The compiled definition is only frozen if the authored
// document, the published example and the embedded document are the same bytes.
const publishedDefinition = "../../api/fixed-workflow/v1/examples/positive-definition.json"

// goldenPlan is the compiled plan of the frozen definition. Refreshing it is
// never a substitute for the semantic assertions in this file.
const goldenPlan = "testdata/compiled-plan.golden.json"

func TestEmbeddedDocumentMatchesThePublishedDefinition(t *testing.T) {
	published, err := os.ReadFile(filepath.Clean(publishedDefinition))
	if err != nil {
		t.Fatalf("read published definition: %v", err)
	}
	embedded := fixedflow.Document()
	if string(published) != string(embedded) {
		t.Fatalf("published definition and embedded document differ: %d vs %d bytes", len(published), len(embedded))
	}
	if _, err := workflow.Parse(embedded); err != nil {
		t.Fatalf("published definition does not parse: %v", err)
	}
}

func TestCompiledPlanMatchesTheGolden(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile fixed workflow: %v", err)
	}
	encoded, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("encode compiled plan: %v", err)
	}
	encoded = append(encoded, '\n')
	if os.Getenv("KOWA_UPDATE_GOLDEN") == "1" {
		// Recording the current compiled plan is a maintenance action only: the
		// semantic assertions below still have to hold, so a refresh can never
		// turn a topology change into a green build by itself.
		if err := os.WriteFile(filepath.Clean(goldenPlan), encoded, 0o644); err != nil {
			t.Fatalf("update compiled plan golden: %v", err)
		}
	}
	golden, err := os.ReadFile(filepath.Clean(goldenPlan))
	if err != nil {
		t.Fatalf("read compiled plan golden: %v", err)
	}
	if string(golden) != string(encoded) {
		t.Fatalf("compiled plan differs from the golden file")
	}
	// The digest the run freezes must be reproducible from the definition.
	definition, err := fixedflow.Load()
	if err != nil {
		t.Fatalf("load definition: %v", err)
	}
	digest, err := fixedflow.DocumentDigest(definition)
	if err != nil {
		t.Fatalf("digest definition: %v", err)
	}
	if digest != plan.Digest {
		t.Fatalf("definition digest %s differs from the compiled plan digest %s", digest, plan.Digest)
	}
	if len(plan.Digest) != len("sha256:")+64 {
		t.Fatalf("compiled digest %q is not a sha256 digest", plan.Digest)
	}
}

// TestFrozenTopology pins the node responsibility of the single MVP flow: the
// ordered delivery chain, its parallel branch, its two bounded revision
// regions and its joins. A silent topology change must fail here, not in a
// production run.
func TestFrozenTopology(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile fixed workflow: %v", err)
	}
	wantOrder := []string{
		"knowledge_recommend", "knowledge_confirm", "knowledge_bind",
		"plan", "plan_review", "plan_approval",
		"code", "design_tests", "verify_change", "security_review",
		"test", "code_review", "publish_pr",
		"observe_review", "observe_merge", "integration", "close",
	}
	if !slices.Equal(plan.Order, wantOrder) {
		t.Fatalf("topological order = %v, want %v", plan.Order, wantOrder)
	}
	if len(plan.Nodes) != len(wantOrder) {
		t.Fatalf("compiled %d nodes, want %d", len(plan.Nodes), len(wantOrder))
	}

	wantTypes := map[string]workflow.NodeType{
		"knowledge_recommend": workflow.NodeSystem,
		"knowledge_confirm":   workflow.NodeHumanTask,
		"knowledge_bind":      workflow.NodeSystem,
		"plan":                workflow.NodeCapability,
		"plan_review":         workflow.NodeCapability,
		"plan_approval":       workflow.NodeHumanTask,
		"design_tests":        workflow.NodeCapability,
		"code":                workflow.NodeCapability,
		"verify_change":       workflow.NodeSystem,
		"test":                workflow.NodeCapability,
		"code_review":         workflow.NodeCapability,
		"security_review":     workflow.NodeCapability,
		"publish_pr":          workflow.NodeCapability,
		"observe_review":      workflow.NodeSystem,
		"observe_merge":       workflow.NodeSystem,
		"integration":         workflow.NodeCapability,
		"close":               workflow.NodeEnd,
	}
	for id, want := range wantTypes {
		node, found := plan.Nodes[id]
		if !found {
			t.Fatalf("frozen node %q is missing", id)
		}
		if node.Type != want {
			t.Fatalf("node %q type = %s, want %s", id, node.Type, want)
		}
		if !node.Mandatory {
			t.Fatalf("node %q is not mandatory", id)
		}
	}

	// Two bounded revision regions: automatic plan revision and automatic code
	// revision, each exactly three rounds.
	revisions := map[string]workflow.RevisionPolicy{}
	for id, node := range plan.Nodes {
		if node.Revision != nil {
			revisions[id] = *node.Revision
		}
	}
	wantRevisions := map[string]workflow.RevisionPolicy{
		"plan_review": {TargetNodeID: "plan", MaxAutoRevisions: fixedflow.MaxAutoRevisions},
		"code_review": {TargetNodeID: "code", MaxAutoRevisions: fixedflow.MaxAutoRevisions},
	}
	if len(revisions) != len(wantRevisions) {
		t.Fatalf("revision regions = %v, want %v", revisions, wantRevisions)
	}
	for id, want := range wantRevisions {
		if revisions[id] != want {
			t.Fatalf("revision region %q = %+v, want %+v", id, revisions[id], want)
		}
	}

	// Joins: the nodes that consolidate parallel or gated branches.
	wantJoins := map[string][]string{
		"test":        {"verify_change", "design_tests"},
		"code_review": {"test", "verify_change"},
		"publish_pr":  {"code_review", "security_review", "verify_change"},
	}
	for id, want := range wantJoins {
		if got := plan.Nodes[id].DependsOn; !slices.Equal(got, want) {
			t.Fatalf("node %q dependencies = %v, want %v", id, got, want)
		}
	}

	// Every control-plane node carries its frozen operation, and every human
	// task carries its kind.
	operations := map[string]string{
		"knowledge_recommend": fixedflow.OperationKnowledgeRecommend,
		"knowledge_bind":      fixedflow.OperationKnowledgeBind,
		"verify_change":       fixedflow.OperationVerifyChange,
		"observe_review":      fixedflow.OperationObserveReview,
		"observe_merge":       fixedflow.OperationObserveMerge,
		"close":               fixedflow.OperationClose,
	}
	for id, want := range operations {
		if got := plan.Nodes[id].Operation; got != want {
			t.Fatalf("node %q operation = %q, want %q", id, got, want)
		}
	}
	humanTasks := map[string]string{
		"knowledge_confirm": fixedflow.HumanTaskKnowledgeConfirmation,
		"plan_approval":     fixedflow.HumanTaskPlanApproval,
	}
	for id, want := range humanTasks {
		if got := plan.Nodes[id].HumanTask; got != want {
			t.Fatalf("node %q human task = %q, want %q", id, got, want)
		}
	}
}

// TestCatalogCoversEveryCapabilityNode keeps the definition and the catalog in
// lockstep: a capability node whose catalog entry disappeared would otherwise
// only fail inside a production admission.
func TestCatalogCoversEveryCapabilityNode(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile fixed workflow: %v", err)
	}
	catalog := fixedflow.Catalog()
	capabilities := map[workflow.CapabilityRef]bool{}
	for _, capability := range fixedflow.Capabilities() {
		capabilities[capability.Ref] = true
	}
	for id, node := range plan.Nodes {
		if node.Type != workflow.NodeCapability {
			continue
		}
		if !capabilities[node.Capability] {
			t.Fatalf("capability node %q uses the undeclared catalog entry %+v", id, node.Capability)
		}
		if _, found := catalog[node.Capability]; !found {
			t.Fatalf("capability node %q is absent from the compiler catalog", id)
		}
	}
	// A catalog entry that no node uses would be dead weight in the published
	// contract.
	used := map[workflow.CapabilityRef]bool{}
	for _, node := range plan.Nodes {
		if node.Type == workflow.NodeCapability {
			used[node.Capability] = true
		}
	}
	for reference := range capabilities {
		if !used[reference] {
			t.Fatalf("catalog entry %+v is never used by the frozen flow", reference)
		}
	}
}

// TestCompilerRejectsBrokenVariants proves the frozen definition is protected
// by real compile rules rather than by inspection: a mutated definition must be
// refused, so a later edit cannot quietly publish a bypass.
func TestCompilerRejectsBrokenVariants(t *testing.T) {
	definition, err := fixedflow.Load()
	if err != nil {
		t.Fatalf("load definition: %v", err)
	}
	mutations := map[string]func(*workflow.Definition){
		"unknown capability": func(d *workflow.Definition) {
			for index := range d.Nodes {
				if d.Nodes[index].ID == "plan" {
					d.Nodes[index].Capability.Version = 2
				}
			}
		},
		"non ancestor binding": func(d *workflow.Definition) {
			for index := range d.Nodes {
				if d.Nodes[index].ID == "plan" {
					d.Nodes[index].Inputs = []workflow.InputBinding{{
						Key: "plan_ref", ProducerNodeID: "close", Output: "verdict",
						Type: workflow.ValueString, Required: true,
					}}
				}
			}
		},
		"cyclic dependency": func(d *workflow.Definition) {
			for index := range d.Nodes {
				if d.Nodes[index].ID == "knowledge_recommend" {
					d.Nodes[index].DependsOn = []string{"close"}
				}
			}
		},
		"revision budget drift": func(d *workflow.Definition) {
			for index := range d.Nodes {
				if d.Nodes[index].ID == "code_review" {
					d.Nodes[index].Revision = &workflow.RevisionPolicy{TargetNodeID: "code", MaxAutoRevisions: 5}
				}
			}
		},
		// The compiler only requires a revision target to be an ancestor; the
		// stronger rule (a review may only revise the region it judges) is an
		// orchestration invariant and is pinned by the orchestration tests.
		"revision target is not an ancestor": func(d *workflow.Definition) {
			for index := range d.Nodes {
				if d.Nodes[index].ID == "code_review" {
					d.Nodes[index].Revision = &workflow.RevisionPolicy{
						TargetNodeID: "close", MaxAutoRevisions: fixedflow.MaxAutoRevisions,
					}
				}
			}
		},
		"type mismatch on an input": func(d *workflow.Definition) {
			for index := range d.Nodes {
				if d.Nodes[index].ID == "test" {
					d.Nodes[index].Inputs[0].Type = workflow.ValueString
				}
			}
		},
		"duplicate node": func(d *workflow.Definition) {
			d.Nodes = append(d.Nodes, d.Nodes[0])
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := cloneDefinition(t, definition)
			mutate(&mutated)
			if _, err := workflow.Compile(mutated, fixedflow.Catalog()); err == nil {
				t.Fatalf("mutated definition %q compiled without error", name)
			}
		})
	}
}

func cloneDefinition(t *testing.T, definition workflow.Definition) workflow.Definition {
	t.Helper()
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatalf("encode definition: %v", err)
	}
	cloned, err := workflow.Parse(encoded)
	if err != nil {
		t.Fatalf("decode definition: %v", err)
	}
	return cloned
}
