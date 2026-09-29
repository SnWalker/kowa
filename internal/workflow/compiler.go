package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
)

const (
	SchemaVersion    = "kowa.workflow-execution.v1"
	DefinitionKind   = "workflowDefinition"
	CompiledPlanKind = "compiledPlan"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func Compile(definition Definition, catalog Catalog) (CompiledPlan, error) {
	if definition.SchemaVersion != SchemaVersion || definition.Kind != DefinitionKind ||
		!identifierPattern.MatchString(definition.Name) || definition.Version < 1 || len(definition.Nodes) == 0 {
		return CompiledPlan{}, invalidDefinition("name, version, and nodes are required")
	}

	nodes := make(map[string]Node, len(definition.Nodes))
	indegree := make(map[string]int, len(definition.Nodes))
	children := make(map[string][]string, len(definition.Nodes))
	for _, node := range definition.Nodes {
		if err := validateNodeShape(node, catalog); err != nil {
			return CompiledPlan{}, err
		}
		if _, exists := nodes[node.ID]; exists {
			return CompiledPlan{}, invalidDefinition("duplicate node %q", node.ID)
		}
		nodes[node.ID] = cloneNode(node)
		indegree[node.ID] = len(node.DependsOn)
	}
	for _, node := range definition.Nodes {
		seen := make(map[string]struct{}, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			if dependency == node.ID {
				return CompiledPlan{}, invalidDefinition("node %q depends on itself", node.ID)
			}
			if _, exists := nodes[dependency]; !exists {
				return CompiledPlan{}, invalidDefinition("node %q depends on unknown node %q", node.ID, dependency)
			}
			if _, duplicate := seen[dependency]; duplicate {
				return CompiledPlan{}, invalidDefinition("node %q repeats dependency %q", node.ID, dependency)
			}
			seen[dependency] = struct{}{}
			children[dependency] = append(children[dependency], node.ID)
		}
	}

	order, err := topologicalOrder(definition.Nodes, indegree, children)
	if err != nil {
		return CompiledPlan{}, err
	}
	for _, node := range definition.Nodes {
		if err := validateBindings(node, nodes, catalog); err != nil {
			return CompiledPlan{}, err
		}
	}

	encoded, err := json.Marshal(definition)
	if err != nil {
		return CompiledPlan{}, fmt.Errorf("encode definition: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return CompiledPlan{
		SchemaVersion: SchemaVersion,
		Kind:          CompiledPlanKind,
		Name:          definition.Name,
		Version:       definition.Version,
		Digest:        "sha256:" + hex.EncodeToString(digest[:]),
		Definition:    cloneDefinition(definition),
		Order:         slices.Clone(order),
		Nodes:         nodes,
	}, nil
}

func Parse(encoded []byte) (Definition, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var definition Definition
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, invalidDefinition("decode definition: %v", err)
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		return Definition{}, invalidDefinition("definition contains trailing data")
	}
	return definition, nil
}

func validateNodeShape(node Node, catalog Catalog) error {
	if !identifierPattern.MatchString(node.ID) {
		return invalidDefinition("invalid node id %q", node.ID)
	}
	switch node.Type {
	case NodeCapability:
		if node.Capability.ID == "" || node.Capability.Version < 1 {
			return invalidDefinition("node %q requires a capability", node.ID)
		}
		if _, exists := catalog[node.Capability]; !exists {
			return fmt.Errorf("%w: %s@%d", ErrCapabilityUnavailable, node.Capability.ID, node.Capability.Version)
		}
		if node.Operation != "" || node.HumanTask != "" {
			return invalidDefinition("node %q mixes capability and control fields", node.ID)
		}
	case NodeHumanTask:
		if node.HumanTask == "" || node.Capability.ID != "" || node.Operation != "" || node.Revision != nil {
			return invalidDefinition("node %q has invalid human task fields", node.ID)
		}
	case NodeSystem, NodeEnd:
		if node.Capability.ID != "" || node.HumanTask != "" || node.Revision != nil {
			return invalidDefinition("node %q has invalid control fields", node.ID)
		}
	case NodeJoin:
		if node.Capability.ID != "" || node.HumanTask != "" || node.Operation != "" || node.Revision != nil {
			return invalidDefinition("node %q has invalid join fields", node.ID)
		}
	default:
		return invalidDefinition("node %q has unknown type %q", node.ID, node.Type)
	}
	if node.Revision != nil && node.Revision.MaxAutoRevisions != 3 {
		return invalidDefinition("node %q revision budget must be 3", node.ID)
	}
	return nil
}

func topologicalOrder(nodes []Node, indegree map[string]int, children map[string][]string) ([]string, error) {
	ready := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if indegree[node.ID] == 0 {
			ready = append(ready, node.ID)
		}
	}
	slices.Sort(ready)
	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)
		for _, child := range children[current] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				slices.Sort(ready)
			}
		}
	}
	if len(order) != len(nodes) {
		return nil, invalidDefinition("ordinary dependency graph contains a cycle")
	}
	return order, nil
}

func validateBindings(node Node, nodes map[string]Node, catalog Catalog) error {
	ancestors := collectAncestors(node.ID, nodes)
	consumerSchema := Schema{Fields: map[string]ValueType{}}
	if node.Type == NodeCapability {
		consumerSchema = catalog[node.Capability].Input
	}
	seenKeys := make(map[string]struct{}, len(node.Inputs))
	for _, binding := range node.Inputs {
		if _, duplicate := seenKeys[binding.Key]; duplicate {
			return invalidDefinition("node %q repeats input key %q", node.ID, binding.Key)
		}
		seenKeys[binding.Key] = struct{}{}
		if _, ancestor := ancestors[binding.ProducerNodeID]; !ancestor {
			return invalidDefinition("node %q input %q does not reference an ancestor", node.ID, binding.Key)
		}
		producer := nodes[binding.ProducerNodeID]
		producerType, exists := outputType(producer, binding.Output, catalog)
		if !exists || producerType != binding.Type {
			return invalidDefinition("node %q input %q has incompatible producer output", node.ID, binding.Key)
		}
		if expected, exists := consumerSchema.Fields[binding.Key]; exists && expected != binding.Type {
			return invalidDefinition("node %q input %q has incompatible consumer type", node.ID, binding.Key)
		}
		if binding.Required && producer.When != nil && node.When == nil {
			return invalidDefinition("node %q requires output from conditional node %q", node.ID, producer.ID)
		}
	}
	if node.When != nil {
		if _, ancestor := ancestors[node.When.ProducerNodeID]; !ancestor {
			return invalidDefinition("node %q condition does not reference an ancestor", node.ID)
		}
		producerType, exists := outputType(nodes[node.When.ProducerNodeID], node.When.Output, catalog)
		if !exists || !conditionValueMatches(producerType, node.When.Equals) {
			return invalidDefinition("node %q condition has an invalid field or value", node.ID)
		}
	}
	if node.Revision != nil {
		if _, ancestor := ancestors[node.Revision.TargetNodeID]; !ancestor {
			return invalidDefinition("node %q revision target is not an ancestor", node.ID)
		}
	}
	return nil
}

func collectAncestors(nodeID string, nodes map[string]Node) map[string]struct{} {
	result := make(map[string]struct{})
	stack := slices.Clone(nodes[nodeID].DependsOn)
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if _, seen := result[current]; seen {
			continue
		}
		result[current] = struct{}{}
		stack = append(stack, nodes[current].DependsOn...)
	}
	return result
}

func outputType(node Node, output string, catalog Catalog) (ValueType, bool) {
	if node.Type != NodeCapability {
		return "", false
	}
	valueType, exists := catalog[node.Capability].Output.Fields[output]
	return valueType, exists
}

func conditionValueMatches(valueType ValueType, value any) bool {
	switch valueType {
	case ValueString, ValueArtifactRef:
		_, ok := value.(string)
		return ok
	case ValueBoolean:
		_, ok := value.(bool)
		return ok
	case ValueInteger:
		kind := reflect.TypeOf(value)
		return kind != nil && kind.Kind() >= reflect.Int && kind.Kind() <= reflect.Int64
	default:
		return false
	}
}

func invalidDefinition(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDefinition, fmt.Sprintf(format, values...))
}

func cloneDefinition(definition Definition) Definition {
	cloned := definition
	cloned.Nodes = make([]Node, len(definition.Nodes))
	for index, node := range definition.Nodes {
		cloned.Nodes[index] = cloneNode(node)
	}
	return cloned
}

func cloneNode(node Node) Node {
	cloned := node
	cloned.DependsOn = slices.Clone(node.DependsOn)
	cloned.Inputs = slices.Clone(node.Inputs)
	if node.When != nil {
		condition := *node.When
		cloned.When = &condition
	}
	if node.Revision != nil {
		revision := *node.Revision
		cloned.Revision = &revision
	}
	return cloned
}
