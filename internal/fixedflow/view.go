package fixedflow

import "github.com/SnWalker/kowa/internal/workflow"

// View is the frozen Web projection of the built-in flow: the identity of the
// compiled definition plus every node's declared responsibility. It is the
// payload of the `fixed.workflow` operation and the structure authority the Web
// merges a run's materialised rounds into.
type View struct {
	SchemaVersion           string     `json:"schemaVersion"`
	Kind                    string     `json:"kind"`
	Name                    string     `json:"name"`
	Version                 int        `json:"version"`
	DefinitionSchemaVersion string     `json:"definitionSchemaVersion"`
	DefinitionDigest        string     `json:"definitionDigest"`
	Order                   []string   `json:"order"`
	Nodes                   []NodeView `json:"nodes"`
}

// NodeView is one node of the frozen definition. Only the fields the node type
// actually carries are emitted, so a control-plane node never advertises a
// capability and vice versa.
type NodeView struct {
	ID         string                   `json:"id"`
	Type       workflow.NodeType        `json:"type"`
	DependsOn  []string                 `json:"dependsOn"`
	Inputs     []workflow.InputBinding  `json:"inputs"`
	Capability *workflow.CapabilityRef  `json:"capability,omitempty"`
	Operation  string                   `json:"operation,omitempty"`
	HumanTask  string                   `json:"humanTask,omitempty"`
	Revision   *workflow.RevisionPolicy `json:"revision,omitempty"`
	Mandatory  bool                     `json:"mandatory"`
}

// ViewKind is the frozen envelope kind of the fixed workflow projection.
const ViewKind = "fixedWorkflowView"

// BuildView projects a compiled plan into the frozen view. The projection is
// pure: it never reloads a definition or a catalog, so an already admitted run
// always sees the plan it was admitted with.
func BuildView(plan workflow.CompiledPlan) View {
	nodes := make([]NodeView, 0, len(plan.Order))
	for _, id := range plan.Order {
		node := plan.Nodes[id]
		view := NodeView{
			ID:        node.ID,
			Type:      node.Type,
			DependsOn: append([]string{}, node.DependsOn...),
			Inputs:    append([]workflow.InputBinding{}, node.Inputs...),
			Mandatory: node.Mandatory,
		}
		if node.Type == workflow.NodeCapability {
			capability := node.Capability
			view.Capability = &capability
		}
		view.Operation = node.Operation
		view.HumanTask = node.HumanTask
		if node.Revision != nil {
			revision := *node.Revision
			view.Revision = &revision
		}
		nodes = append(nodes, view)
	}
	return View{
		SchemaVersion:           SchemaVersion,
		Kind:                    ViewKind,
		Name:                    plan.Name,
		Version:                 plan.Version,
		DefinitionSchemaVersion: plan.SchemaVersion,
		DefinitionDigest:        plan.Digest,
		Order:                   append([]string{}, plan.Order...),
		Nodes:                   nodes,
	}
}
