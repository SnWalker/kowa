package fixedflow

import "github.com/SnWalker/kowa/internal/workflow"

// Capability is one Runner-executed capability of the frozen MVP catalog. The
// catalog declares what the flow may dispatch; a Runtime that does not register
// a capability cannot execute its task.
type Capability struct {
	Ref    workflow.CapabilityRef
	Input  workflow.Schema
	Output workflow.Schema
}

// Capabilities is the frozen capability catalog of the fixed MVP flow. Every
// capability node of definition.json appears here with the exact input/output
// field vocabulary its inputs and the downstream bindings use.
//
// The catalog is the single authority: the compiler rejects a definition node
// whose capability is missing here, and the orchestrator resolves node outputs
// through the same entries.
func Capabilities() []Capability {
	artifact := workflow.ValueArtifactRef
	text := workflow.ValueString
	return []Capability{
		{
			Ref:    workflow.CapabilityRef{ID: "plan.create", Version: 1},
			Input:  workflow.Schema{Fields: map[string]workflow.ValueType{}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{"plan_ref": artifact}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "plan.review", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"plan_ref": artifact,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"verdict": text, "review_ref": artifact,
			}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "testcases.design", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"plan_ref": artifact,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{"cases_ref": artifact}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "code.implement", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"plan_ref": artifact,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"change_ref": artifact, "commit_oid": text,
			}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "test.execute", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"cases_ref": artifact, "commit_oid": text,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"verdict": text, "report_ref": artifact,
			}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "code.review", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"change_ref": artifact, "commit_oid": text, "report_ref": artifact,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"verdict": text, "review_ref": artifact,
			}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "security.review", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"change_ref": artifact, "commit_oid": text,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"verdict": text, "report_ref": artifact,
			}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "delivery.publish_pr", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"change_ref": artifact, "review_ref": artifact, "report_ref": artifact, "commit_oid": text,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"pr_number": text, "pr_ref": artifact,
			}},
		},
		{
			Ref: workflow.CapabilityRef{ID: "delivery.integration_verify", Version: 1},
			Input: workflow.Schema{Fields: map[string]workflow.ValueType{
				"pr_number": text,
			}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{
				"verdict": text, "report_ref": artifact,
			}},
		},
	}
}

// Catalog is the compiler-shaped lookup of Capabilities.
func Catalog() workflow.Catalog {
	catalog := make(workflow.Catalog, len(Capabilities()))
	for _, capability := range Capabilities() {
		catalog[capability.Ref] = workflow.CapabilityDescriptor{
			Input: capability.Input, Output: capability.Output,
		}
	}
	return catalog
}
