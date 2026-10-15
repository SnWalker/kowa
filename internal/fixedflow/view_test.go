package fixedflow_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/workflow"
)

// publishedView is the view example the kowa.fixed-workflow.v1 contract
// publishes. It must be the real producer output, not a hand-written copy.
const publishedView = "../../api/fixed-workflow/v1/examples/positive-fixed-workflow-view.json"

func TestPublishedViewIsTheRealProducerOutput(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile fixed workflow: %v", err)
	}
	produced, err := json.MarshalIndent(fixedflow.BuildView(plan), "", "  ")
	if err != nil {
		t.Fatalf("encode fixed workflow view: %v", err)
	}
	produced = append(produced, '\n')
	if os.Getenv("KOWA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(filepath.Clean(publishedView), produced, 0o644); err != nil {
			t.Fatalf("update published view: %v", err)
		}
	}
	published, err := os.ReadFile(filepath.Clean(publishedView))
	if err != nil {
		t.Fatalf("read published view: %v", err)
	}
	if string(published) != string(produced) {
		t.Fatalf("published view differs from the real producer output")
	}
}

// TestViewKeepsTheFrozenResponsibilities proves the projection does not invent
// or hide node responsibility: the DAG the Web renders is the compiled plan.
func TestViewKeepsTheFrozenResponsibilities(t *testing.T) {
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile fixed workflow: %v", err)
	}
	view := fixedflow.BuildView(plan)
	if view.SchemaVersion != fixedflow.SchemaVersion || view.Kind != "fixedWorkflowView" {
		t.Fatalf("view envelope = %s/%s", view.SchemaVersion, view.Kind)
	}
	if view.Name != fixedflow.DefinitionName || view.Version != fixedflow.DefinitionVersion {
		t.Fatalf("view identity = %s@%d", view.Name, view.Version)
	}
	if view.DefinitionSchemaVersion != workflow.SchemaVersion {
		t.Fatalf("view definition schema = %s", view.DefinitionSchemaVersion)
	}
	if view.DefinitionDigest != plan.Digest {
		t.Fatalf("view digest %s differs from the compiled digest %s", view.DefinitionDigest, plan.Digest)
	}
	if len(view.Order) != len(plan.Order) || len(view.Nodes) != len(plan.Nodes) {
		t.Fatalf("view carries %d order entries and %d nodes, plan has %d and %d",
			len(view.Order), len(view.Nodes), len(plan.Order), len(plan.Nodes))
	}
	for index, id := range plan.Order {
		if view.Order[index] != id {
			t.Fatalf("view order[%d] = %s, want %s", index, view.Order[index], id)
		}
	}
	for _, node := range view.Nodes {
		compiled, found := plan.Nodes[node.ID]
		if !found {
			t.Fatalf("view node %q is not in the compiled plan", node.ID)
		}
		if node.Type != compiled.Type || node.Mandatory != compiled.Mandatory {
			t.Fatalf("view node %q differs from the compiled node", node.ID)
		}
		if node.Operation != compiled.Operation || node.HumanTask != compiled.HumanTask {
			t.Fatalf("view node %q carries another operation or human task", node.ID)
		}
		switch compiled.Type {
		case workflow.NodeCapability:
			if node.Capability == nil || *node.Capability != compiled.Capability {
				t.Fatalf("view node %q capability = %v, want %+v", node.ID, node.Capability, compiled.Capability)
			}
			if node.Operation != "" || node.HumanTask != "" {
				t.Fatalf("capability node %q advertises a control-plane field", node.ID)
			}
		case workflow.NodeHumanTask:
			if node.Capability != nil {
				t.Fatalf("human task node %q advertises a capability", node.ID)
			}
		}
		if len(node.Inputs) != len(compiled.Inputs) {
			t.Fatalf("view node %q has %d inputs, compiled node has %d", node.ID, len(node.Inputs), len(compiled.Inputs))
		}
	}
}
