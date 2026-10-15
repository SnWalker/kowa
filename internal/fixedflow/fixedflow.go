// Package fixedflow owns the single built-in MVP workflow definition
// (kowa.fixed-workflow, version 1) and the capability catalog it compiles
// against. The definition document in definition.json is the frozen authoring
// form; Compile turns it into the immutable compiled plan a run executes.
//
// The definition is the only place that declares the MVP node topology; the
// orchestration package interprets it and never hard-codes a second copy.
package fixedflow

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/SnWalker/kowa/internal/workflow"
)

// SchemaVersion is the contract version of the fixed-workflow documents.
const SchemaVersion = "kowa.fixed-workflow.v1"

// DefinitionName and DefinitionVersion identify the single frozen MVP flow.
// The name obeys the workflow compiler's identifier grammar
// (^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$), so the contract's dotted version string
// (kowa.fixed-workflow.v1) is the document schema, not the definition identity.
const (
	DefinitionName    = "kowa-fixed-workflow"
	DefinitionVersion = 1
)

// PolicyVersion names the frozen policy set a run executes under. It enters the
// closing checklist, so a policy change is visible in the closure record.
const PolicyVersion = "kowa.mvp-policy.v1"

// Node identifiers of the frozen flow. The orchestrator dispatches by node id,
// so every handler refers to these constants instead of repeating literals.
const (
	NodeKnowledgeRecommend = "knowledge_recommend"
	NodeKnowledgeConfirm   = "knowledge_confirm"
	NodeKnowledgeBind      = "knowledge_bind"
	NodePlan               = "plan"
	NodePlanReview         = "plan_review"
	NodePlanApproval       = "plan_approval"
	NodeDesignTests        = "design_tests"
	NodeCode               = "code"
	NodeVerifyChange       = "verify_change"
	NodeTest               = "test"
	NodeCodeReview         = "code_review"
	NodeSecurityReview     = "security_review"
	NodePublishPR          = "publish_pr"
	NodeObserveReview      = "observe_review"
	NodeObserveMerge       = "observe_merge"
	NodeIntegration        = "integration"
	NodeClose              = "close"
)

// Control-plane operations of the frozen flow. A system node's operation names
// a control-plane use case; it is never executed by a Runner.
const (
	OperationKnowledgeRecommend = "knowledge.recommend"
	OperationKnowledgeBind      = "knowledge.bind"
	OperationVerifyChange       = "delivery.verify_change"
	OperationObserveReview      = "github.observe_review"
	OperationObserveMerge       = "github.observe_merge"
	OperationClose              = "delivery.close"
)

// Human task kinds the frozen flow creates.
const (
	HumanTaskKnowledgeConfirmation = "knowledge_confirmation"
	HumanTaskPlanApproval          = "plan_approval"
)

// Capability return values every capability node reports in its structured
// output. A node's verdict decides gating; the orchestrator never derives it
// from the task's completion state.
const (
	// VerdictApproved and VerdictPassed are the two gate-satisfying values: a
	// review approves, a test passes. Every other value leaves the gate closed.
	VerdictApproved      = "approved"
	VerdictPassed        = "passed"
	VerdictNeedsRevision = "needs_revision"
	VerdictRejected      = "rejected"
)

// Revision budget of the automatic review regions. The workflow compiler
// freezes this value for every revision policy (one initial round plus three
// automatic revisions).
const MaxAutoRevisions = 3

//go:embed definition.json
var document []byte

// Document returns the exact frozen authoring bytes of the definition. The
// published digest is computed over the parsed definition, exactly as the
// compiler does; these bytes are what a reviewer can diff.
func Document() []byte { return append([]byte{}, document...) }

// Load parses the frozen definition document with the compiler's strict rules.
func Load() (workflow.Definition, error) {
	definition, err := workflow.Parse(Document())
	if err != nil {
		return workflow.Definition{}, fmt.Errorf("parse fixed workflow definition: %w", err)
	}
	if definition.Name != DefinitionName || definition.Version != DefinitionVersion {
		return workflow.Definition{}, fmt.Errorf(
			"fixed workflow definition identity is %s@%d, want %s@%d",
			definition.Name, definition.Version, DefinitionName, DefinitionVersion)
	}
	return definition, nil
}

// Compile returns the immutable compiled plan every admission runs against.
func Compile() (workflow.CompiledPlan, error) {
	definition, err := Load()
	if err != nil {
		return workflow.CompiledPlan{}, err
	}
	plan, err := workflow.Compile(definition, Catalog())
	if err != nil {
		return workflow.CompiledPlan{}, fmt.Errorf("compile fixed workflow definition: %w", err)
	}
	return plan, nil
}

// DocumentDigest is the content digest rule of the definition: the SHA-256 of
// the compiler's canonical JSON encoding, exactly like workflow.Compile.
func DocumentDigest(definition workflow.Definition) (string, error) {
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("encode fixed workflow definition: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
