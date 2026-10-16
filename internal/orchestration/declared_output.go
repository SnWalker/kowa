package orchestration

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/workflow"
	"github.com/SnWalker/kowa/internal/workitem"
)

// This file owns the two halves of one contract: what a capability node must
// read to work on the actual task, and what it must produce for the flow to
// consume its result. Both are decided by the frozen capability catalog; the
// orchestrator only delivers the declaration and enforces it.

// Field names of the delivery fields every capability node's frozen input
// carries. They are part of the EffectiveInput the binding freezes, so a
// Provider reads them from the same bytes the control plane bound.
const (
	fieldRequirementTitle    = "requirement_title"
	fieldRequirementContent  = "requirement_content"
	fieldAcceptanceContent   = "acceptance_content"
	fieldKnowledgeRepository = "knowledge_repository_id"
	fieldKnowledgeCommit     = "knowledge_commit"
	fieldDeclaredOutputs     = "declared_outputs"
	fieldVerdictVocabulary   = "verdict_vocabulary"
)

// verdictField is the frozen output field a capability declares to gate its
// downstream nodes. commitField and prNumberField are the two declared outputs
// whose producer may be the control plane rather than the attempt.
const (
	verdictField  = "verdict"
	commitField   = "commit_oid"
	prNumberField = "pr_number"
)

// verdictVocabulary is the single authority over the verdicts this flow can
// interpret: the delivery fields carry it to a gate, and enforcement accepts
// exactly these values.
var verdictVocabulary = []string{
	VerdictApproved, VerdictPassed, VerdictRejected, VerdictNeedsRevision, VerdictFailed,
}

// deliveryFields assembles the frozen delivery fields of one capability node:
// the WorkItem's own requirement and acceptance text, the frozen knowledge
// binding the round reads, and the capability's declared output contract.
//
// The requirement text belongs here rather than in a separate lookup: a node
// whose frozen input only names the requirement version digest cannot work on
// the requirement at all, and the Provider can only read bytes that were bound
// before the attempt existed. The knowledge content itself is not copied — the
// frozen knowledge commit is materialised beside the attempt, and these fields
// name exactly which repository and commit that is.
func (s *Service) deliveryFields(
	admission Admission,
	item workitem.WorkItem,
	node workflow.Node,
) ([]knowledgeField, error) {
	capability, found := fixedflow.Descriptor(node.Capability)
	if !found {
		// The compiler refuses a definition node whose capability is unknown, so
		// this is a defect in the compiled plan, never a runtime choice.
		return nil, fmt.Errorf("%w: capability %s@%d of node %s is not in the frozen catalog",
			ErrPolicyBlocked, node.Capability.ID, node.Capability.Version, node.ID)
	}
	declared, err := json.Marshal(declaredOutputs(capability.Output))
	if err != nil {
		return nil, fmt.Errorf("encode the declared outputs of node %s: %w", node.ID, err)
	}
	fields := []knowledgeField{
		{Name: fieldRequirementTitle, Type: "string", Value: mustJSON(item.Title)},
		{Name: fieldRequirementContent, Type: "string", Value: mustJSON(item.Requirement.Content)},
		{Name: fieldAcceptanceContent, Type: "string", Value: mustJSON(item.Acceptance.Content)},
		{Name: fieldKnowledgeRepository, Type: "string", Value: mustJSON(admission.KnowledgeRepositoryID)},
		{Name: fieldKnowledgeCommit, Type: "string", Value: mustJSON(admission.KnowledgeCommitOID)},
		{Name: fieldDeclaredOutputs, Type: "string", Value: mustJSON(string(declared))},
	}
	// A node whose declaration gates its downstream nodes must also know which
	// verdicts the flow can interpret: a gate word the flow cannot read is a
	// refusal, and the attempt must be able to know that before it reports.
	if _, declaresVerdict := capability.Output.Fields[verdictField]; declaresVerdict {
		vocabulary, err := json.Marshal(verdictVocabulary)
		if err != nil {
			return nil, fmt.Errorf("encode the verdict vocabulary of node %s: %w", node.ID, err)
		}
		fields = append(fields, knowledgeField{
			Name: fieldVerdictVocabulary, Type: "string", Value: mustJSON(string(vocabulary)),
		})
	}
	return fields, nil
}

// declaredOutputs renders one declared output schema as the name-to-type object
// the delivery field carries. Encoding a map sorts the keys, so the field is
// canonical for a given declaration.
func declaredOutputs(schema workflow.Schema) map[string]string {
	declared := make(map[string]string, len(schema.Fields))
	for name, valueType := range schema.Fields {
		declared[name] = string(valueType)
	}
	return declared
}

// declaredOutputList renders the declared output names in a stable order for
// diagnostics.
func declaredOutputList(schema workflow.Schema) string {
	names := make([]string, 0, len(schema.Fields))
	for name := range schema.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Sprint(names)
}

// validateDecidedOutput enforces the capability contract on a decided round: a
// completed attempt must carry the outputs its capability declares. The frozen
// execution acceptance only requires an output object to exist — it holds no
// capability catalog — so this is the layer that can refuse a decision the flow
// could otherwise keep waiting on without a diagnostic.
//
// Declared scalar outputs are the attempt's own result and must be present and
// well typed. A declared artifact reference is produced by this control plane
// (the round's published deliverable, see deliverableRef): a Provider cannot
// upload or name an artifact in this MVP, so an attempt-supplied value is only
// checked for shape.
func (s *Service) validateDecidedOutput(node workflow.Node, round execution.NodeView) error {
	capability, found := fixedflow.Descriptor(node.Capability)
	if !found {
		return fmt.Errorf("%w: capability %s@%d of node %s is not in the frozen catalog",
			ErrPolicyBlocked, node.Capability.ID, node.Capability.Version, node.ID)
	}
	if len(round.Output) == 0 {
		return fmt.Errorf(
			"%w: node %s round %d completed without any declared output (declared: %s); "+
				"there is no result to publish and no downstream round may run on it",
			ErrPolicyBlocked, node.ID, round.Iteration, declaredOutputList(capability.Output))
	}
	for _, name := range sortedFieldNames(capability.Output) {
		valueType := capability.Output.Fields[name]
		value, present := declaredValue(round.Output, name)
		if valueType == workflow.ValueArtifactRef {
			if present && !nonEmptyString(value) {
				return fmt.Errorf(
					"%w: node %s round %d declares the artifact output %q, but the attempt reported %v",
					ErrPolicyBlocked, node.ID, round.Iteration, name, value)
			}
			continue
		}
		if !present || !declaredValueMatches(valueType, value) {
			return fmt.Errorf(
				"%w: node %s round %d completed without its declared output %q (%s)",
				ErrPolicyBlocked, node.ID, round.Iteration, name, valueType)
		}
		if name == verdictField && !knownVerdict(value.(string)) {
			return fmt.Errorf(
				"%w: node %s round %d reported the verdict %q, which is outside the frozen verdict vocabulary",
				ErrPolicyBlocked, node.ID, round.Iteration, value)
		}
	}
	return nil
}

// declaredValue reads one declared output the way the flow's own consumers read
// it. A capability reports the declared name, but the same fact may also arrive
// under the name its producer owns: the Runner's controlled delivery carries
// the commit oid of the pushed work branch, and a pull request number is read
// from either spelling. Validating through the consumers' resolution keeps the
// rule exactly as strict as consumption, so a decision the flow can consume is
// never refused.
func declaredValue(output map[string]any, name string) (any, bool) {
	if value, found := output[name]; found {
		return value, true
	}
	switch name {
	case commitField:
		commit, found := deliveredCommit(output)
		return commit, found
	case prNumberField:
		number, found := reportedPullRequestNumber(output)
		return number, found
	default:
		return nil, false
	}
}

// sortedFieldNames lists one declared output schema's field names in a stable
// order, so a refusal reports the same field first every time.
func sortedFieldNames(schema workflow.Schema) []string {
	names := make([]string, 0, len(schema.Fields))
	for name := range schema.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// declaredValueMatches reports whether one decided value satisfies the declared
// type of its output field. Numbers are compared as the JSON decoder delivers
// them.
func declaredValueMatches(valueType workflow.ValueType, value any) bool {
	switch valueType {
	case workflow.ValueString:
		return nonEmptyString(value)
	case workflow.ValueBoolean:
		_, ok := value.(bool)
		return ok
	case workflow.ValueInteger:
		number, ok := value.(float64)
		return ok && number == math.Trunc(number)
	default:
		return false
	}
}

func nonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && text != ""
}

// knownVerdict reports whether one reported verdict is a member of the frozen
// vocabulary. A verdict the flow cannot interpret is a defect, not a closed
// gate: it would otherwise keep every downstream node waiting with no fact that
// says why.
func knownVerdict(verdict string) bool {
	for _, known := range verdictVocabulary {
		if verdict == known {
			return true
		}
	}
	return false
}
