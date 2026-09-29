// Package workflow compiles immutable workflow definitions into executable plans.
package workflow

import "errors"

var (
	ErrInvalidDefinition     = errors.New("workflow: invalid definition")
	ErrCapabilityUnavailable = errors.New("workflow: capability unavailable")
	ErrDefinitionConflict    = errors.New("workflow: definition conflict")
	ErrDefinitionNotFound    = errors.New("workflow: definition not found")
)

type NodeType string

const (
	NodeCapability NodeType = "capability"
	NodeHumanTask  NodeType = "human_task"
	NodeSystem     NodeType = "system"
	NodeJoin       NodeType = "join"
	NodeEnd        NodeType = "end"
)

type ValueType string

const (
	ValueString      ValueType = "string"
	ValueBoolean     ValueType = "boolean"
	ValueInteger     ValueType = "integer"
	ValueArtifactRef ValueType = "artifact_ref"
)

type CapabilityRef struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type Schema struct {
	Fields map[string]ValueType `json:"fields"`
}

type CapabilityDescriptor struct {
	Input  Schema `json:"input"`
	Output Schema `json:"output"`
}

type Catalog map[CapabilityRef]CapabilityDescriptor

type InputBinding struct {
	Key            string    `json:"key"`
	ProducerNodeID string    `json:"producerNodeId"`
	Output         string    `json:"output"`
	Type           ValueType `json:"type"`
	Required       bool      `json:"required"`
}

type Condition struct {
	ProducerNodeID string `json:"producerNodeId"`
	Output         string `json:"output"`
	Equals         any    `json:"equals"`
}

type RevisionPolicy struct {
	TargetNodeID     string `json:"targetNodeId"`
	MaxAutoRevisions int    `json:"maxAutoRevisions"`
}

type Node struct {
	ID         string          `json:"id"`
	Type       NodeType        `json:"type"`
	DependsOn  []string        `json:"dependsOn"`
	Inputs     []InputBinding  `json:"inputs"`
	Capability CapabilityRef   `json:"capability,omitempty"`
	Operation  string          `json:"operation,omitempty"`
	HumanTask  string          `json:"humanTask,omitempty"`
	When       *Condition      `json:"when,omitempty"`
	Revision   *RevisionPolicy `json:"revision,omitempty"`
	Mandatory  bool            `json:"mandatory"`
}

type Definition struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Version       int    `json:"version"`
	Nodes         []Node `json:"nodes"`
}

type CompiledPlan struct {
	SchemaVersion string          `json:"schemaVersion"`
	Kind          string          `json:"kind"`
	Name          string          `json:"name"`
	Version       int             `json:"version"`
	Digest        string          `json:"digest"`
	Definition    Definition      `json:"definition"`
	Order         []string        `json:"order"`
	Nodes         map[string]Node `json:"nodes"`
}
