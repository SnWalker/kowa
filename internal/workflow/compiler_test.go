package workflow

import (
	"errors"
	"testing"
)

func TestCompile(t *testing.T) {
	t.Parallel()

	catalog := Catalog{
		{ID: "plan.create", Version: 1}: {
			Input:  Schema{Fields: map[string]ValueType{}},
			Output: Schema{Fields: map[string]ValueType{"result_ref": ValueArtifactRef}},
		},
		{ID: "plan.review", Version: 1}: {
			Input:  Schema{Fields: map[string]ValueType{"plan": ValueArtifactRef}},
			Output: Schema{Fields: map[string]ValueType{"verdict": ValueString}},
		},
	}

	tests := []struct {
		name       string
		definition Definition
		wantErr    error
	}{
		{
			name:       "valid dag",
			definition: validDefinition(),
		},
		{
			name: "duplicate node",
			definition: Definition{
				SchemaVersion: SchemaVersion, Kind: DefinitionKind,
				Name: "delivery", Version: 1,
				Nodes: []Node{{ID: "plan", Type: NodeCapability}, {ID: "plan", Type: NodeCapability}},
			},
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "unknown node type",
			definition: Definition{
				SchemaVersion: SchemaVersion, Kind: DefinitionKind,
				Name: "delivery", Version: 1,
				Nodes: []Node{{ID: "plan", Type: NodeType("mystery")}},
			},
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "missing dependency",
			definition: Definition{
				SchemaVersion: SchemaVersion, Kind: DefinitionKind,
				Name: "delivery", Version: 1,
				Nodes: []Node{{ID: "plan", Type: NodeCapability, DependsOn: []string{"missing"}}},
			},
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "ordinary cycle",
			definition: Definition{
				SchemaVersion: SchemaVersion, Kind: DefinitionKind,
				Name: "delivery", Version: 1,
				Nodes: []Node{
					{ID: "a", Type: NodeJoin, DependsOn: []string{"b"}},
					{ID: "b", Type: NodeJoin, DependsOn: []string{"a"}},
				},
			},
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "non ancestor binding",
			definition: Definition{
				SchemaVersion: SchemaVersion, Kind: DefinitionKind,
				Name: "delivery", Version: 1,
				Nodes: []Node{
					{ID: "left", Type: NodeJoin},
					{ID: "right", Type: NodeJoin},
					{ID: "end", Type: NodeEnd, DependsOn: []string{"left"}, Inputs: []InputBinding{{Key: "x", ProducerNodeID: "right", Output: "result_ref", Type: ValueArtifactRef, Required: true}}},
				},
			},
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "binding type mismatch",
			definition: func() Definition {
				definition := validDefinition()
				definition.Nodes[1].Inputs[0].Type = ValueBoolean
				return definition
			}(),
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "condition missing field",
			definition: func() Definition {
				definition := validDefinition()
				definition.Nodes[1].When = &Condition{ProducerNodeID: "plan", Output: "missing", Equals: "approved"}
				return definition
			}(),
			wantErr: ErrInvalidDefinition,
		},
		{
			name: "unknown capability version",
			definition: func() Definition {
				definition := validDefinition()
				definition.Nodes[0].Capability.Version = 2
				return definition
			}(),
			wantErr: ErrCapabilityUnavailable,
		},
		{
			name: "revision budget must be three",
			definition: func() Definition {
				definition := validDefinition()
				definition.Nodes[1].Revision = &RevisionPolicy{TargetNodeID: "plan", MaxAutoRevisions: 4}
				return definition
			}(),
			wantErr: ErrInvalidDefinition,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, err := Compile(test.definition, catalog)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("Compile() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if plan.Digest == "" || len(plan.Order) != len(test.definition.Nodes) {
				t.Fatalf("Compile() plan = %#v", plan)
			}
		})
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := Parse([]byte(`{
		"schemaVersion":"kowa.workflow-execution.v1",
		"kind":"workflowDefinition",
		"name":"delivery",
		"version":1,
		"nodes":[],
		"unknown":true
	}`))
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Parse() error = %v, want ErrInvalidDefinition", err)
	}
}

func validDefinition() Definition {
	return Definition{
		SchemaVersion: SchemaVersion,
		Kind:          DefinitionKind,
		Name:          "delivery",
		Version:       1,
		Nodes: []Node{
			{
				ID: "plan", Type: NodeCapability, Mandatory: true,
				Capability: CapabilityRef{ID: "plan.create", Version: 1},
			},
			{
				ID: "review", Type: NodeCapability, Mandatory: true,
				DependsOn:  []string{"plan"},
				Capability: CapabilityRef{ID: "plan.review", Version: 1},
				Inputs:     []InputBinding{{Key: "plan", ProducerNodeID: "plan", Output: "result_ref", Type: ValueArtifactRef, Required: true}},
				Revision:   &RevisionPolicy{TargetNodeID: "plan", MaxAutoRevisions: 3},
			},
		},
	}
}
