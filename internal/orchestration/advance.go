package orchestration

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/workflow"
)

// advanceMaxSteps bounds one reconciliation pass loop; every step applies one
// persisted transition, so a bounded loop cannot spin forever.
const advanceMaxSteps = 128

// AdvanceResult reports what one reconciliation applied.
type AdvanceResult struct {
	RunID string
	Steps int
	State execution.RunState
	// Conflicts counts the passes that stopped because another writer moved the
	// same run; they are expected under concurrent commands and are retried.
	Conflicts int
	// ConflictDetail is the last conflict's message, for diagnostics only.
	ConflictDetail string
}

// Advance reconciles one run from persisted facts: it materialises the rounds
// whose gates are satisfied, applies accepted results, opens bounded revisions
// and escalates exhausted budgets to a human. It is idempotent and safe to
// re-run after a restart.
func (s *Service) Advance(ctx context.Context, runID string) error {
	_, err := s.advance(ctx, runID)
	return err
}

func (s *Service) advance(ctx context.Context, runID string) (AdvanceResult, error) {
	s.advanceMu.Lock()
	defer s.advanceMu.Unlock()
	result := AdvanceResult{RunID: runID}
	run, err := s.execution.Run(ctx, runID)
	if err != nil {
		return result, err
	}
	result.State = run.State
	// The mirror keeps an adapter without the run table reporting the same
	// reconcilable set as the durable adapter.
	_ = s.store.NoteRunState(ctx, runID, string(run.State))
	if run.State.Settled() || run.State == execution.RunCanceling || run.State == execution.RunCanceled {
		return result, nil
	}
	if run.State == execution.RunPaused {
		// A failed or awaiting-input attempt needs an explicit recovery; the
		// reconciler never resumes a paused run by itself.
		return result, nil
	}
	admission, err := s.store.Admission(ctx, runID)
	if err != nil {
		return result, err
	}
	// Only the run that holds advancement authority is reconciled; a run whose
	// admission never obtained it stays inert.
	item, err := s.workitems.Get(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return result, err
	}
	if item.CurrentRunID != runID {
		return result, nil
	}
	for step := 0; step < advanceMaxSteps; step++ {
		progressed, err := s.advanceStep(ctx, admission)
		if err != nil {
			if errorsIs(err, execution.ErrVersionConflict) {
				// Another writer moved the run; the next pass re-reads it, so the
				// conflict is reported without aborting the reconciliation.
				result.Conflicts++
				result.ConflictDetail = err.Error()
				break
			}
			return result, err
		}
		if !progressed {
			break
		}
		result.Steps++
	}
	if current, err := s.execution.Run(ctx, runID); err == nil {
		result.State = current.State
	}
	return result, nil
}

// advanceStep applies at most one transition and reports whether it did.
func (s *Service) advanceStep(ctx context.Context, admission Admission) (bool, error) {
	view, err := s.execution.RunView(ctx, admission.RunID)
	if err != nil {
		return false, err
	}
	if view.State != execution.RunActive {
		return false, nil
	}
	// A human grant from an approved escalation opens one extra round first: the
	// grant is a persisted decision, not a silent budget reset.
	if granted, err := s.applyGrants(ctx, admission, view); err != nil {
		return false, err
	} else if granted {
		return true, nil
	}
	rounds := map[string]execution.NodeView{}
	for _, node := range view.Nodes {
		rounds[node.NodeID] = node
	}
	for _, nodeID := range s.plan.Order {
		node := s.plan.Nodes[nodeID]
		round, materialised := rounds[nodeID]
		if !materialised {
			ready, err := s.nodeReady(ctx, admission, node, rounds, view)
			if err != nil {
				return false, err
			}
			if !ready {
				continue
			}
			opened, err := s.openFirstRound(ctx, admission, node, view)
			if err != nil {
				return false, fmt.Errorf("open the first round of node %s: %w", node.ID, err)
			}
			return opened, nil
		}
		// A round whose inputs moved on is superseded: a decision taken against
		// an older upstream round may not keep gating the flow. The rebuild may
		// only proceed while every dependency still gates it truthfully — a
		// re-asked human approval closes its gate until it is answered again, so
		// the rebuilt downstream never runs on a new version nobody approved.
		if s.staleRound(ctx, admission, node, round, view) {
			if !s.dependenciesCurrent(ctx, admission, node, rounds, view) {
				continue
			}
			rebuilt, err := s.rebuildStaleRound(ctx, admission, node, round, view)
			if err != nil {
				return false, fmt.Errorf("rebuild round %d of node %s: %w", round.Iteration, node.ID, err)
			}
			if rebuilt {
				return true, nil
			}
		}
		action, err := s.applyRound(ctx, admission, node, round, rounds, view)
		if err != nil {
			return false, fmt.Errorf("apply round %d of node %s: %w", round.Iteration, node.ID, err)
		}
		if action {
			return true, nil
		}
	}
	return false, nil
}

// nodeReady reports whether every dependency of one node is satisfied: all of
// them decided and their verdict accepting. A dependency that asks for a
// revision never satisfies a gate, so no downstream node can bypass it.
func (s *Service) nodeReady(
	_ context.Context,
	_ Admission,
	node workflow.Node,
	rounds map[string]execution.NodeView,
	view execution.RunView,
) (bool, error) {
	for _, dependency := range node.DependsOn {
		round, found := rounds[dependency]
		if !found {
			// The dependency has no round yet: a revision of it may be due.
			return false, nil
		}
		if !s.accepting(dependency, round) {
			return false, nil
		}
	}
	_ = view
	return true, nil
}

// accepting reports whether one node's current round satisfies everything that
// reads it: it must be decided, and — for a node whose capability declares a
// verdict — the verdict must approve. A node that only produces content has no
// gate verdict, so being decided is what its readers need.
func (s *Service) accepting(nodeID string, round execution.NodeView) bool {
	if round.State != execution.NodeDecided {
		return false
	}
	node, found := s.plan.Nodes[nodeID]
	if !found {
		return false
	}
	if !s.gating(node) {
		return true
	}
	return gateAccepting(round.Verdict)
}

// dependenciesCurrent reports whether every dependency of one node still gates
// it truthfully: the dependency is decided, accepts, and is not itself waiting
// for a rebuild of newer inputs. A re-asked human approval therefore keeps its
// downstream closed until the new answer exists.
func (s *Service) dependenciesCurrent(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	rounds map[string]execution.NodeView,
	view execution.RunView,
) bool {
	for _, dependency := range node.DependsOn {
		dependencyNode, known := s.plan.Nodes[dependency]
		if !known {
			return false
		}
		round, materialised := rounds[dependency]
		if !materialised || round.State != execution.NodeDecided || !s.accepting(dependency, round) {
			return false
		}
		if s.staleRound(ctx, admission, dependencyNode, round, view) {
			return false
		}
	}
	return true
}

// staleRound reports whether one round was built from references an upstream
// has since replaced. The decision is by the frozen causal references, never by
// comparing per-node counters: a node locally rerun past its dependency's count
// would otherwise look fresh while its frozen input still names the superseded
// round. A capability round is stale when the current deliverable of any
// declared upstream is not among the artifacts its own frozen binding carries;
// a control round is stale when its frozen input digest no longer reproduces
// from the current upstream rounds. The counter comparison stays as an
// additional trigger, never as proof of freshness. Only settled or waiting
// rounds are judged: a round that never decided has no stale decision to
// supersede.
func (s *Service) staleRound(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) bool {
	if round.State != execution.NodeDecided && round.State != execution.NodeWaitingHuman {
		return false
	}
	for _, dependency := range node.DependsOn {
		if s.currentIteration(view, dependency) > round.Iteration {
			return true
		}
	}
	if node.Type == workflow.NodeCapability {
		return s.capabilityRoundSuperseded(ctx, admission, node, round, view)
	}
	return s.controlRoundSuperseded(ctx, admission, node, round)
}

// capabilityRoundSuperseded compares the artifacts one capability round froze
// with the current deliverable of every declared upstream. An upstream whose
// deliverable is not published yet cannot be judged and is left to its own
// rebuild path.
func (s *Service) capabilityRoundSuperseded(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) bool {
	binding, err := s.store.Binding(ctx, attemptID(admission.RunID, node.ID, round.Iteration))
	if err != nil {
		// A round without its frozen binding cannot be proven fresh by
		// references; only the counter trigger above may rebuild it.
		return false
	}
	// The frozen references are the node's declared input producers — not
	// necessarily its dependency gates — so each upstream deliverable the
	// round consumed is compared with the producer's current round.
	for _, producer := range inputProducers(node) {
		current, found := s.roundOf(view, producer)
		if !found {
			continue
		}
		ref, published, err := s.deliverableRef(ctx, admission, producer, current.Iteration)
		if err != nil || !published {
			continue
		}
		if !slices.Contains(binding.ArtifactIDs, ref.ArtifactID) {
			return true
		}
	}
	return false
}

// inputProducers lists the declared input producers of one node, deduplicated
// in declaration order.
func inputProducers(node workflow.Node) []string {
	producers := make([]string, 0, len(node.Inputs))
	seen := map[string]bool{}
	for _, binding := range node.Inputs {
		if seen[binding.ProducerNodeID] {
			continue
		}
		seen[binding.ProducerNodeID] = true
		producers = append(producers, binding.ProducerNodeID)
	}
	return producers
}

// controlRoundSuperseded recomputes the frozen input digest of a control round
// from the current upstream rounds: any moved upstream identity changes it.
func (s *Service) controlRoundSuperseded(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
) bool {
	return s.controlInputDigest(ctx, admission, node.ID, round.Iteration) != round.InputDigest
}

// rebuildStaleRound opens the next round of a node whose inputs moved on. A
// capability round is dispatched again, a control-plane round is decided again,
// and a human round asks its question again for the new upstream deliverable.
func (s *Service) rebuildStaleRound(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) (bool, error) {
	budget := s.regionBudget(ctx, admission, node.ID, round.Iteration)
	switch node.Type {
	case workflow.NodeCapability:
		return s.dispatchCapabilityBudget(ctx, admission, node, execution.RoundRevision, view, budget)
	default:
		return s.executeControlNodeBudget(ctx, admission, node, view, budget)
	}
}

// regionTargetOf reports the node one decider's region revises: the declared
// revision target, or the plan a refused approval revises, or the decider
// itself when a human gate is simply asked again.
func (s *Service) regionTargetOf(deciderNodeID string) (string, bool) {
	switch deciderNodeID {
	case fixedflow.NodePlanApproval:
		return fixedflow.NodePlan, true
	case fixedflow.NodeKnowledgeConfirm:
		return fixedflow.NodeKnowledgeConfirm, true
	}
	node, found := s.plan.Nodes[deciderNodeID]
	if !found {
		return "", false
	}
	if node.Revision != nil {
		return node.Revision.TargetNodeID, true
	}
	// A verifier without a declared automatic revision region governs itself:
	// an approved answer grants it one explicit extra round, and a refusal
	// grants nothing. No task is ever created without this consumer.
	return deciderNodeID, true
}

// regionBudget bounds the rounds of one node: the frozen automatic budget plus
// one extra round per escalation a human approved. A grant belongs to the region
// its decider governs, so every node of the affected set gets the same extra
// round, each grant carrying the reason it was approved for.
func (s *Service) regionBudget(ctx context.Context, admission Admission, nodeID string, current int) int {
	budget := MaxNodeRounds
	if current > budget {
		budget = current
	}
	tasks, err := s.humans.List(ctx, admission.WorkspaceID, false)
	if err != nil {
		return budget
	}
	for _, task := range tasks {
		if task.RunID != admission.RunID ||
			task.Request.Kind != HumanTaskRevisionEscalation || task.State != humantask.StateResolved {
			continue
		}
		if decision, accepted := s.acceptedDecision(ctx, admission.WorkspaceID, task); !accepted ||
			decision != humantask.DecisionApproved {
			continue
		}
		decider := strings.TrimPrefix(task.Request.Object.ID, admission.RunID+":")
		target, found := s.regionTargetOf(decider)
		if !found {
			continue
		}
		if target == nodeID || s.descendsFrom(target, nodeID, map[string]bool{}) {
			budget++
		}
	}
	return budget
}

// descendsFrom reports whether one node lies downstream of another.
func (s *Service) descendsFrom(ancestor, nodeID string, seen map[string]bool) bool {
	if seen[nodeID] {
		return false
	}
	seen[nodeID] = true
	node, found := s.plan.Nodes[nodeID]
	if !found {
		return false
	}
	for _, dependency := range node.DependsOn {
		if dependency == ancestor || s.descendsFrom(ancestor, dependency, seen) {
			return true
		}
	}
	return false
}

// applyGrants takes up every approved escalation that has not been acted on
// yet: it opens one extra round of the region's target.
func (s *Service) applyGrants(
	ctx context.Context,
	admission Admission,
	view execution.RunView,
) (bool, error) {
	tasks, err := s.humans.List(ctx, admission.WorkspaceID, false)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.RunID != admission.RunID || task.Request.Kind != HumanTaskRevisionEscalation {
			continue
		}
		if decision, accepted := s.acceptedDecision(ctx, admission.WorkspaceID, task); !accepted ||
			decision != humantask.DecisionApproved {
			continue
		}
		deciderNodeID := strings.TrimPrefix(task.Request.Object.ID, admission.RunID+":")
		targetNodeID, found := s.regionTargetOf(deciderNodeID)
		if !found {
			continue
		}
		grantedAt, err := strconv.Atoi(task.Request.Object.Version)
		if err != nil {
			continue
		}
		if s.currentIteration(view, targetNodeID) > grantedAt {
			continue
		}
		target, found := s.plan.Nodes[targetNodeID]
		if !found {
			continue
		}
		budget := grantedAt + 1
		if budget < MaxNodeRounds {
			budget = MaxNodeRounds
		}
		if target.Type == workflow.NodeCapability {
			return s.dispatchCapabilityBudget(ctx, admission, target, execution.RoundRevision, view, budget)
		}
		return s.executeControlNodeBudget(ctx, admission, target, view, budget)
	}
	return false, nil
}

// gating reports whether one node's verdict decides its downstream gates. A
// capability that declares a verdict output must approve; a capability that
// only publishes content, and every control-plane node, is judged on its
// decision instead.
func (s *Service) gating(node workflow.Node) bool {
	if node.Type != workflow.NodeCapability {
		return true
	}
	descriptor, found := fixedflow.Catalog()[node.Capability]
	if !found {
		return true
	}
	_, declaresVerdict := descriptor.Output.Fields["verdict"]
	return declaresVerdict
}

// openFirstRound materialises the first round of one node.
func (s *Service) openFirstRound(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	view execution.RunView,
) (bool, error) {
	switch node.Type {
	case workflow.NodeCapability:
		return s.dispatchCapability(ctx, admission, node, execution.RoundInitial, view)
	default:
		return s.executeControlNode(ctx, admission, node, view)
	}
}

// applyRound applies the transition one existing round is waiting for.
func (s *Service) applyRound(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	rounds map[string]execution.NodeView,
	view execution.RunView,
) (bool, error) {
	switch round.State {
	case execution.NodeReady:
		if node.Type == workflow.NodeCapability {
			return s.dispatchCapability(ctx, admission, node, execution.RoundRevision, view)
		}
		return s.executeControlNode(ctx, admission, node, view)
	case execution.NodeDecided:
		return s.settleDecided(ctx, admission, node, round, rounds, view)
	case execution.NodeWaitingHuman:
		if node.Type == workflow.NodeHumanTask {
			return s.consumeHumanAnswer(ctx, admission, node, round, view)
		}
		return s.retryControlNode(ctx, admission, node, round, view)
	default:
		return false, nil
	}
}

// settleDecided applies the once-only effects of a decided round: the published
// deliverable, the recorded verification and the bounded revision a review asks
// for. Each effect is idempotent, so a restart re-applies nothing.
func (s *Service) settleDecided(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	rounds map[string]execution.NodeView,
	view execution.RunView,
) (bool, error) {
	switch node.ID {
	case fixedflow.NodeKnowledgeBind:
		// The confirmation already assembled and persisted the run input; the
		// node only records that the binding is frozen.
		return false, nil
	case fixedflow.NodePlanApproval:
		if round.Verdict == VerdictRejected {
			opened, exhausted, err := s.askForRevision(ctx, admission, fixedflow.NodePlan, round.Iteration)
			if err != nil || opened {
				return opened, err
			}
			if exhausted {
				return s.escalateExhaustedBudget(ctx, admission, node, round)
			}
		}
		return false, nil
	case fixedflow.NodeKnowledgeConfirm:
		if round.Verdict == VerdictRejected {
			opened, exhausted, err := s.askForRevision(ctx, admission, fixedflow.NodeKnowledgeConfirm, round.Iteration)
			if err != nil || opened {
				return opened, err
			}
			if exhausted {
				return s.escalateExhaustedBudget(ctx, admission, node, round)
			}
		}
		return false, nil
	}
	if node.Type == workflow.NodeCapability {
		if published, err := s.publishDeliverable(ctx, admission, node, round, view); err != nil || published {
			return published, err
		}
		if recorded, err := s.recordVerification(ctx, admission, node, round, view); err != nil || recorded {
			return recorded, err
		}
	}
	if round.Verdict == VerdictNeedsRevision {
		if target, ok := s.revisionTarget(node); ok {
			opened, exhausted, err := s.askForRevision(ctx, admission, target, round.Iteration)
			if err != nil || opened {
				return opened, err
			}
			if exhausted {
				return s.escalateExhaustedBudget(ctx, admission, node, round)
			}
			return false, nil
		}
		return s.escalateExhaustedBudget(ctx, admission, node, round)
	}
	return false, nil
}

// revisionTarget reports the node a review verdict revises. The frozen MVP
// policy declares the automatic regions of the fixed flow.
func (s *Service) revisionTarget(node workflow.Node) (string, bool) {
	if node.Revision != nil {
		return node.Revision.TargetNodeID, true
	}
	return "", false
}

// askForRevision opens the next round of one node inside its frozen budget. A
// revision is opened exactly once per deciding round: the target's current
// iteration must not already be ahead of the round that asked for it. An
// exhausted budget is reported so the caller can escalate to a human instead of
// extending it silently.
func (s *Service) askForRevision(
	ctx context.Context,
	admission Admission,
	targetNodeID string,
	deciderIteration int,
) (opened bool, exhausted bool, err error) {
	plan, found := s.plan.Nodes[targetNodeID]
	if !found {
		return false, false, fmt.Errorf("%w: unknown revision target %s", ErrValidation, targetNodeID)
	}
	view, err := s.execution.RunView(ctx, admission.RunID)
	if err != nil {
		return false, false, err
	}
	round, materialised := s.roundOf(view, targetNodeID)
	if !materialised {
		opened, err := s.openFirstRound(ctx, admission, plan, view)
		return opened, false, err
	}
	if round.Iteration > deciderIteration {
		// The revision this decision asked for already exists.
		return false, false, nil
	}
	if round.State != execution.NodeDecided && round.State != execution.NodeExecutionFailed {
		return false, false, nil
	}
	budget := s.regionBudget(ctx, admission, targetNodeID, deciderIteration)
	var advanced bool
	if plan.Type == workflow.NodeCapability {
		advanced, err = s.dispatchCapabilityBudget(ctx, admission, plan, execution.RoundRevision, view, budget)
	} else {
		advanced, err = s.executeControlNodeBudget(ctx, admission, plan, view, budget)
	}
	if err != nil {
		if errorsIs(err, execution.ErrPolicyBlocked) {
			// The node's round bound is spent: the region needs a human.
			return false, true, nil
		}
		return false, false, err
	}
	return advanced, false, nil
}

// acceptedDecision reads the accepted answer of one resolved request through
// the owner's single-task read. The list projection does not carry answers on
// every adapter, so the answer is always fetched where it is consumed.
func (s *Service) acceptedDecision(
	ctx context.Context,
	workspaceID string,
	task humantask.HumanTask,
) (string, bool) {
	if task.Response != nil {
		return task.Response.Decision, true
	}
	if task.State != humantask.StateResolved {
		return "", false
	}
	full, err := s.humans.Get(ctx, workspaceID, task.ID)
	if err != nil || full.Response == nil {
		return "", false
	}
	return full.Response.Decision, true
}

// escalateExhaustedBudget records the human handling a spent automatic
// revision budget needs. The gate stays unsatisfied until a human acts.
func (s *Service) escalateExhaustedBudget(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
) (bool, error) {
	tasks, err := s.humans.List(ctx, admission.WorkspaceID, false)
	if err != nil {
		return false, err
	}
	// The escalation names the region it governs, so its answer is a decision
	// about one region and never a silent global budget reset.
	region := admission.RunID + ":" + node.ID
	requestKey := fmt.Sprintf("fixedflow.escalation.%s.%s.%d", admission.RunID, node.ID, round.Iteration)
	for _, task := range tasks {
		if task.RunID != admission.RunID || task.Request.Kind != HumanTaskRevisionEscalation {
			continue
		}
		if task.Request.Object.ID != region {
			continue
		}
		if task.State == humantask.StateOpen {
			return false, nil
		}
		if task.State == humantask.StateResolved {
			decision, _ := s.acceptedDecision(ctx, admission.WorkspaceID, task)
			approved := decision == humantask.DecisionApproved
			if approved {
				// A consumed self-target grant no longer suppresses the region:
				// a further exhaustion after the granted round asks again. The
				// grant is consumed once the node's own round moved past the
				// exhaustion round.
				if grantedAt, err := strconv.Atoi(task.Request.Object.Version); err == nil &&
					round.Iteration <= grantedAt {
					return false, nil
				}
				continue
			}
			// A refused escalation ends the region's automatic re-asking: the
			// explicit cancellation or a repair run is the human exit.
			return false, nil
		}
	}
	_, err = s.humans.Create(ctx, humantask.CreateCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID,
		RunID: admission.RunID, NodeRunID: round.ID,
		ActorID: admission.AdmittedByUserID, RequestKey: requestKey,
		Title: "Automatic revision budget of " + node.ID + " is exhausted",
		Kind:  HumanTaskRevisionEscalation, Mode: humantask.ModeKowaAnswer,
		Fields: []humantask.Field{
			{Name: "decision", Type: "decision", Required: true},
			{Name: "feedback", Type: "comment"},
		},
		Object: humantask.ObjectBinding{
			Kind: "revision_escalation", ID: region, Version: fmt.Sprint(round.Iteration),
		},
		Roles: []humantask.Role{humantask.RoleAdmin, humantask.RoleDeveloper},
	})
	if err != nil {
		return false, fmt.Errorf("create the revision escalation task: %w", err)
	}
	return true, nil
}

// deliverableRef finds the published deliverable one decided round produced.
func (s *Service) deliverableRef(
	ctx context.Context,
	admission Admission,
	nodeID string,
	iteration int,
) (artifact.Ref, bool, error) {
	records, err := s.store.ArtifactsForWorkItem(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return artifact.Ref{}, false, err
	}
	producer := deliverableProducer(nodeID, iteration)
	for _, record := range records {
		if record.ProducerID != producer || record.State != string(artifact.Published) {
			continue
		}
		return artifact.Ref{
			SchemaVersion: artifact.SchemaVersion, ArtifactID: record.ArtifactID,
			WorkspaceID: admission.WorkspaceID, SubjectID: admission.WorkItemID,
			Kind: record.Kind, Producer: artifact.Producer{Kind: "operation", ID: record.ProducerID},
			Digest: record.Digest, Size: record.Size, MediaType: record.MediaType,
			StorageRef: "artifact:" + record.ArtifactID,
		}, true, nil
	}
	return artifact.Ref{}, false, nil
}

// publishDeliverable publishes the structured result of one decided capability
// round as an immutable artifact, so downstream rounds consume frozen bytes.
func (s *Service) publishDeliverable(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) (bool, error) {
	if _, found, err := s.deliverableRef(ctx, admission, node.ID, round.Iteration); err != nil || found {
		return false, err
	}
	content, err := s.deliverableContent(ctx, admission, node, round, view)
	if err != nil || content == nil {
		return false, err
	}
	scope := artifact.Scope{WorkspaceID: admission.WorkspaceID, ActorID: admission.AdmittedByUserID}
	// A reviewed deliverable is a normal publication: the review node consumes
	// it as an input, and S03 refuses to assemble an input from an unapproved
	// candidate. The human gate is the approval node's own verdict, which every
	// downstream node depends on.
	ref, err := s.artifacts.Upload(ctx, scope, artifact.Upload{
		RequiresApproval: false, SubjectID: admission.WorkItemID, Kind: deliverableKind(node.ID),
		MediaType: "application/json", Digest: artifact.Digest(content),
		Producer: artifact.Producer{Kind: "operation", ID: deliverableProducer(node.ID, round.Iteration)},
	}, content)
	if err != nil {
		return false, fmt.Errorf("upload the deliverable of node %s: %w", node.ID, err)
	}
	if err := s.artifacts.Publish(ctx, scope, ref); err != nil {
		return false, fmt.Errorf("publish the deliverable of node %s: %w", node.ID, err)
	}
	return true, nil
}

// deliverableContent is the canonical content of one node's deliverable: the
// exact structured output the accepted attempt reported.
func (s *Service) deliverableContent(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) ([]byte, error) {
	if len(round.Output) == 0 {
		return nil, nil
	}
	document := struct {
		SchemaVersion string         `json:"schemaVersion"`
		RunID         string         `json:"workflowRunId"`
		WorkItemID    string         `json:"workItemId"`
		NodeID        string         `json:"nodeId"`
		Iteration     int            `json:"iteration"`
		NodeRunID     string         `json:"nodeRunId"`
		Verdict       string         `json:"verdict"`
		Output        map[string]any `json:"output"`
	}{
		SchemaVersion: SchemaVersion, RunID: admission.RunID, WorkItemID: admission.WorkItemID,
		NodeID: node.ID, Iteration: round.Iteration, NodeRunID: round.ID,
		Verdict: round.Verdict, Output: cloneOutput(round.Output),
	}
	return mustJSON(document), nil
}

func deliverableProducer(nodeID string, iteration int) string {
	return fmt.Sprintf("fixedflow.node-output:%s:%d", nodeID, iteration)
}

func deliverableKind(nodeID string) string {
	return "node_output_" + nodeID
}

// existingRound reports whether one round identity is already materialised.
func (s *Service) existingRound(view execution.RunView, nodeRunID string) (execution.NodeView, bool) {
	for _, node := range view.Nodes {
		if node.ID == nodeRunID {
			return node, true
		}
	}
	return execution.NodeView{}, false
}

// plannedIteration reports the round the next dispatch of one node materialises:
// the first round, the superseding round of a revision or rerun, or the round a
// crashed dispatch already opened.
func (s *Service) plannedIteration(
	nodeID string,
	round execution.DispatchRound,
	view execution.RunView,
) (int, bool) {
	current, materialised := s.roundOf(view, nodeID)
	if round.SupersedesNodeRunID != "" {
		if !materialised || current.ID != round.SupersedesNodeRunID {
			return 0, false
		}
		return current.Iteration + 1, true
	}
	if materialised && (current.State == execution.NodePending || current.State == execution.NodeReady) {
		// A crash can leave the planned round without its attempt; the dispatch
		// resumes on that very round.
		return current.Iteration, true
	}
	return 1, true
}

// roundOf returns one node's current round of a run view.
func (s *Service) roundOf(view execution.RunView, nodeID string) (execution.NodeView, bool) {
	for _, node := range view.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return execution.NodeView{}, false
}

// currentIteration reports the highest materialised round of one node.
func (s *Service) currentIteration(view execution.RunView, nodeID string) int {
	round, found := s.roundOf(view, nodeID)
	if !found {
		return 0
	}
	return round.Iteration
}

// scopeForNodes keeps the frozen read scopes of one capability round.
func (s *Service) scopes(admission Admission, nodeID string, projectCommit string) []Scope {
	scopes := []Scope{
		{RepositoryID: admission.ProjectRepositoryID, Role: "project", Operation: "clone", Ref: projectCommit},
		{RepositoryID: admission.KnowledgeRepositoryID, Role: "knowledge", Operation: "clone", Ref: admission.KnowledgeCommitOID},
	}
	if nodeID == fixedflow.NodeCode {
		scopes = append(scopes, Scope{
			RepositoryID: admission.ProjectRepositoryID, Role: "project",
			Operation: "push", Ref: admission.WorkingBranchRef,
		})
	}
	return scopes
}

// projectCommitOf chooses the commit a node reads: the frozen baseline, or the
// actual merge commit for the post-merge verification.
func (s *Service) projectCommitOf(ctx context.Context, admission Admission, nodeID string) (string, error) {
	if nodeID != fixedflow.NodeIntegration {
		return admission.ProjectCommitOID, nil
	}
	merge, found, err := s.mergeObservation(ctx, admission)
	if err != nil {
		return "", err
	}
	if !found {
		return admission.ProjectCommitOID, nil
	}
	return merge.PullRequest.MergeCommitOID, nil
}

// deadlineHorizon is the attempted deadline of one dispatch.
func (s *Service) deadline() time.Time { return s.now().UTC().Add(AttemptDeadlineHorizon) }

// upstreamsOf builds the frozen upstream references of one node from its
// decided dependencies and their published deliverables.
func (s *Service) upstreamsOf(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	view execution.RunView,
) ([]knowledgeUpstream, error) {
	// One upstream per producer node: several declared bindings may read the
	// same producer, but the input contract names each upstream exactly once.
	upstreams := make([]knowledgeUpstream, 0, len(node.Inputs))
	seen := map[string]bool{}
	for _, binding := range node.Inputs {
		if seen[binding.ProducerNodeID] {
			continue
		}
		seen[binding.ProducerNodeID] = true
		producer, found := s.roundOf(view, binding.ProducerNodeID)
		if !found {
			return nil, fmt.Errorf("%w: upstream node %s has no round", ErrPolicyBlocked, binding.ProducerNodeID)
		}
		ref, found, err := s.deliverableRef(ctx, admission, binding.ProducerNodeID, producer.Iteration)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		upstreams = append(upstreams, knowledgeUpstream{
			Name: binding.ProducerNodeID, NodeRunID: producer.ID, Ref: ref,
		})
	}
	return upstreams, nil
}

// fieldsOf assembles the control-plane fields one capability node receives.
func (s *Service) fieldsOf(ctx context.Context, admission Admission, node workflow.Node) ([]knowledgeField, error) {
	fields := make([]knowledgeField, 0, 3)
	switch node.ID {
	case fixedflow.NodeCode:
		fields = append(fields, knowledgeField{Name: "working_branch_ref", Type: "string", Value: mustJSON(admission.WorkingBranchRef)})
	case fixedflow.NodePublishPR:
		fields = append(fields,
			knowledgeField{Name: "base_ref", Type: "string", Value: mustJSON(admission.ProjectBranchRef)},
			knowledgeField{Name: "head_ref", Type: "string", Value: mustJSON(admission.WorkingBranchRef)},
		)
	case fixedflow.NodeIntegration:
		merge, found, err := s.mergeObservation(ctx, admission)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%w: the merge commit is not observed yet", ErrPolicyBlocked)
		}
		fields = append(fields,
			knowledgeField{Name: "merge_commit_oid", Type: "string", Value: mustJSON(merge.PullRequest.MergeCommitOID)},
			knowledgeField{Name: "merge_observation_id", Type: "string", Value: mustJSON(merge.ObservationID)},
		)
	}
	return fields, nil
}

// cloneOutput is defined in store.go; this alias keeps node output copies
// explicit at the call sites in this file.
var _ = maps.Clone[map[string]any, string]
