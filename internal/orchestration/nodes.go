package orchestration

import (
	"context"
	"fmt"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/knowledge"
	"github.com/SnWalker/kowa/internal/workflow"
)

// knowledgeUpstream and knowledgeField mirror the S03 input shapes so this
// package composes them without redefining them.
type knowledgeUpstream = knowledge.Upstream

type knowledgeField = knowledge.Field

// identity helpers of this module's frozen identities. Node-run and task ids
// use ':' separators so they are valid artifact and verification identifiers.
func nodeRoundID(runID, nodeID string, iteration int) string {
	return fmt.Sprintf("%s:node:%s:%d", runID, nodeID, iteration)
}

func attemptID(runID, nodeID string, iteration int) string {
	return fmt.Sprintf("%s:task:%s:%d", runID, nodeID, iteration)
}

// roundIntent derives the round policy of one node's next execution from the
// persisted round: the first round is the initial round of the node, and only a
// settled round may be superseded by a bounded revision or rerun.
func (s *Service) roundIntent(
	ctx context.Context,
	view execution.RunView,
	nodeID string,
	kind execution.DispatchRoundKind,
	maxRounds int,
) (execution.DispatchRound, bool) {
	current, found := s.roundOf(view, nodeID)
	if !found {
		return execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: maxRounds}, true
	}
	switch current.State {
	case execution.NodePending, execution.NodeReady:
		// A round that already carries an attempt is not actionable: the
		// reconciler only waits for the execution plane to pick it up.
		if s.roundHasAttempt(ctx, view.ID, current.ID) {
			return execution.DispatchRound{}, false
		}
		return execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: maxRounds}, true
	case execution.NodeDecided, execution.NodeExecutionFailed, execution.NodeWaitingHuman:
		// A waiting round is settled for this purpose: the question it asks
		// belongs to an upstream round that has moved on.
		if kind == execution.RoundInitial {
			kind = execution.RoundRevision
		}
		return execution.DispatchRound{
			Kind: kind, SupersedesNodeRunID: current.ID, MaxRounds: maxRounds,
		}, true
	default:
		return execution.DispatchRound{}, false
	}
}

// roundHasAttempt reports whether one node round already carries an attempt;
// the execution module owns the attempts, this is a read of them.
func (s *Service) roundHasAttempt(ctx context.Context, runID, nodeRunID string) bool {
	attempts, err := s.execution.RunAttempts(ctx, runID)
	if err != nil {
		return false
	}
	for _, attempt := range attempts {
		if attempt.NodeRunID == nodeRunID {
			return true
		}
	}
	return false
}

// dispatchCapability binds a capability round's frozen input and then
// dispatches it. The binding is committed before the attempt exists, so a
// dispatched attempt is never without its frozen input.
func (s *Service) dispatchCapability(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	kind execution.DispatchRoundKind,
	view execution.RunView,
) (bool, error) {
	return s.dispatchCapabilityBudget(ctx, admission, node, kind, view, MaxNodeRounds)
}

// dispatchCapabilityBudget is dispatchCapability under an explicit round budget
// (the region's frozen budget plus any human grant).
func (s *Service) dispatchCapabilityBudget(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	kind execution.DispatchRoundKind,
	view execution.RunView,
	maxRounds int,
) (bool, error) {
	round, actionable := s.roundIntent(ctx, view, node.ID, kind, maxRounds)
	if !actionable {
		return false, nil
	}
	newIteration, resolvable := s.plannedIteration(node.ID, round, view)
	if !resolvable {
		return false, nil
	}
	if node.ID == fixedflow.NodePublishPR {
		prepared, err := s.preparePublishPullRequest(ctx, admission, view, round, newIteration)
		if err != nil || !prepared {
			return false, err
		}
	}
	if !resolvable {
		return false, nil
	}
	upstreams, err := s.upstreamsOf(ctx, admission, node, view)
	if err != nil || upstreams == nil {
		return false, err
	}
	fields, err := s.fieldsOf(ctx, admission, node)
	if err != nil {
		return false, err
	}
	input, err := s.assembleInput(ctx, admission, node, nodeRoundID(admission.RunID, node.ID, newIteration), upstreams, fields)
	if err != nil {
		return false, err
	}
	projectCommit, err := s.projectCommitOf(ctx, admission, node.ID)
	if err != nil {
		return false, err
	}
	artifactIDs := make([]string, 0, len(upstreams))
	for _, upstream := range upstreams {
		artifactIDs = append(artifactIDs, upstream.Ref.ArtifactID)
	}
	// The binding freezes the complete causal reference set: every dependency
	// gate's round and every declared input producer's round, with the
	// deliverable identity where one exists. Staleness is judged from these
	// alone.
	causalRefs := make([]CausalRef, 0, len(node.DependsOn)+len(upstreams))
	referenceIndex := map[string]int{}
	addReference := func(nodeID, nodeRunID, artifactID string) {
		if position, found := referenceIndex[nodeID]; found {
			if artifactID != "" {
				causalRefs[position].ArtifactID = artifactID
			}
			return
		}
		referenceIndex[nodeID] = len(causalRefs)
		causalRefs = append(causalRefs, CausalRef{NodeID: nodeID, NodeRunID: nodeRunID, ArtifactID: artifactID})
	}
	for _, gate := range node.DependsOn {
		gateRound, found := s.roundOf(view, gate)
		if !found {
			return false, fmt.Errorf("%w: gate %s has no round to freeze", ErrPolicyBlocked, gate)
		}
		addReference(gate, gateRound.ID, "")
	}
	for _, upstream := range upstreams {
		addReference(upstream.Name, upstream.NodeRunID, upstream.Ref.ArtifactID)
	}
	// A binding is immutable: an attempt that was already frozen (an interrupted
	// dispatch, a retry of the same round) reuses its identity, digest, scopes
	// and deadline instead of materialising a second intent.
	taskID := attemptID(admission.RunID, node.ID, newIteration)
	stored, err := s.store.Binding(ctx, taskID)
	switch {
	case err == nil:
	case errorsIs(err, ErrNotFound):
		stored, err = s.store.BindInput(ctx, Binding{
			TaskID: taskID, WorkspaceID: admission.WorkspaceID, RunID: admission.RunID, NodeID: node.ID,
			NodeRunID: nodeRoundID(admission.RunID, node.ID, newIteration),
			InputID:   input.ID, InputDigest: input.Digest,
			ProjectRepositoryID: admission.ProjectRepositoryID, ProjectCommitOID: projectCommit,
			KnowledgeRepositoryID: admission.KnowledgeRepositoryID, KnowledgeCommitOID: admission.KnowledgeCommitOID,
			CapabilityID: node.Capability.ID, CapabilityVersion: node.Capability.Version,
			ProviderID: admission.ProviderID, ProviderVersion: admission.ProviderVersion,
			Scopes: s.scopes(admission, node.ID, projectCommit), ArtifactIDs: artifactIDs,
			CausalRefs: causalRefs,
			DeadlineAt: s.deadline(),
		})
		if err != nil {
			return false, fmt.Errorf("freeze the input of node %s: %w", node.ID, err)
		}
	default:
		return false, fmt.Errorf("read the frozen input of node %s: %w", node.ID, err)
	}
	scopes := make([]execution.GitScope, 0, len(stored.Scopes))
	for _, scope := range stored.Scopes {
		scopes = append(scopes, execution.GitScope{
			RepositoryID: scope.RepositoryID, Role: scope.Role, Operation: scope.Operation, Ref: scope.Ref,
		})
	}
	run, err := s.execution.Run(ctx, admission.RunID)
	if err != nil {
		return false, err
	}
	_, err = s.execution.DispatchNode(ctx, execution.DispatchNodeCommand{
		RunID: admission.RunID, NodeID: node.ID, NodeRunID: stored.NodeRunID, TaskID: stored.TaskID,
		ExpectedRunVersion: run.Version, InputDigest: stored.InputDigest,
		Capability: execution.Capability{ID: stored.CapabilityID, Version: stored.CapabilityVersion},
		Provider:   execution.ProviderSelection{ID: stored.ProviderID, Version: stored.ProviderVersion},
		GitScopes:  scopes, DeadlineAt: stored.DeadlineAt,
		Round: round,
	})
	if err != nil {
		if errorsIs(err, execution.ErrRetryNotAllowed) {
			// The current round already carries an attempt (a retry, for
			// example): the reconciler has nothing to add.
			return false, nil
		}
		return false, fmt.Errorf("dispatch node %s: %w", node.ID, err)
	}
	return true, nil
}

// executeControlNode runs the first (or next) round of a system or human node.
func (s *Service) executeControlNode(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	view execution.RunView,
) (bool, error) {
	return s.executeControlNodeBudget(ctx, admission, node, view, MaxNodeRounds)
}

// executeControlNodeBudget is executeControlNode under an explicit round budget.
func (s *Service) executeControlNodeBudget(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	view execution.RunView,
	maxRounds int,
) (bool, error) {
	round, actionable := s.roundIntent(ctx, view, node.ID, execution.RoundRevision, maxRounds)
	if !actionable {
		return false, nil
	}
	newIteration := 1
	if round.SupersedesNodeRunID != "" {
		current, found := s.roundOf(view, node.ID)
		if !found {
			return false, nil
		}
		newIteration = current.Iteration + 1
	}
	return s.runControlNode(ctx, admission, node, newIteration, round, view)
}

// runControlNode executes the control-plane operation of one node round.
func (s *Service) runControlNode(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
	view execution.RunView,
) (bool, error) {
	switch node.ID {
	case fixedflow.NodeKnowledgeRecommend:
		recommendation, err := s.recommendKnowledge(ctx, admission)
		if err != nil {
			return false, err
		}
		return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved,
			map[string]any{
				"indexDigest":     recommendation.IndexDigest,
				"knowledgeCommit": recommendation.KnowledgeCommitOID,
				"candidateCount":  len(recommendation.Candidates),
			})
	case fixedflow.NodeKnowledgeConfirm:
		return s.askKnowledgeConfirmation(ctx, admission, node, iteration, round)
	case fixedflow.NodePlanApproval:
		return s.askPlanApproval(ctx, admission, node, iteration, round, view)
	case fixedflow.NodeKnowledgeBind:
		recommendation, err := s.store.Recommendation(ctx, admission.RunID)
		if err != nil {
			return false, err
		}
		if !recommendation.Confirmed() {
			return false, nil
		}
		return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved,
			map[string]any{"snapshotId": recommendation.SnapshotID, "bindingEventId": recommendation.BindingEventID})
	case fixedflow.NodeVerifyChange:
		return s.runVerifyChange(ctx, admission, node, iteration, round, view)
	case fixedflow.NodeObserveReview:
		return s.runObserve(ctx, admission, node, iteration, round, view, "pull_request_review")
	case fixedflow.NodeObserveMerge:
		return s.runObserve(ctx, admission, node, iteration, round, view, "pull_request_merge")
	case fixedflow.NodeClose:
		return s.runClose(ctx, admission, node, iteration, round, view)
	default:
		return false, fmt.Errorf("%w: node %s has no control-plane handler", ErrValidation, node.ID)
	}
}

// continuationOf finds the accepted answer a human round still has to consume.
func (s *Service) continuationOf(
	nodeRunID string,
	continuations []humantask.Continuation,
) (humantask.Continuation, bool) {
	for _, continuation := range continuations {
		if continuation.NodeRunID == nodeRunID {
			return continuation, true
		}
	}
	return humantask.Continuation{}, false
}

// humanRequestExists reports whether one round already has a human request.
func (s *Service) humanRequestExists(ctx context.Context, admission Admission, nodeRunID string) (bool, error) {
	tasks, err := s.humans.List(ctx, admission.WorkspaceID, false)
	if err != nil {
		return false, err
	}
	for _, task := range tasks {
		if task.NodeRunID == nodeRunID {
			return true, nil
		}
	}
	return false, nil
}

// askKnowledgeConfirmation creates the versioned confirmation request bound to
// the frozen recommendation.
func (s *Service) askKnowledgeConfirmation(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
) (bool, error) {
	recommendation, err := s.recommendKnowledge(ctx, admission)
	if err != nil {
		return false, err
	}
	if recommendation.Confirmed() {
		return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved,
			map[string]any{"bindingEventId": recommendation.BindingEventID, "alreadyConfirmed": true})
	}
	// The round is recorded before its request exists, so the request always
	// names a materialised round; an interrupted creation is re-created
	// idempotently by the reconciler.
	if _, err := s.decide(ctx, admission, node, iteration, round, execution.NodeWaitingHuman, "", nil); err != nil {
		return false, err
	}
	requestKey := fmt.Sprintf("fixedflow.knowledge-confirm:%s:%d", admission.RunID, iteration)
	task, err := s.humans.Create(ctx, humantask.CreateCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID, RunID: admission.RunID,
		NodeRunID: nodeRoundID(admission.RunID, node.ID, iteration),
		ActorID:   admission.AdmittedByUserID, RequestKey: requestKey,
		Title: "Confirm the frozen knowledge selection",
		Kind:  HumanTaskKnowledgeConfirmation, Mode: humantask.ModeKowaAnswer,
		Fields: []humantask.Field{
			{Name: "decision", Type: "decision", Required: true},
			{Name: "selection", Type: "text"},
			{Name: "feedback", Type: "comment"},
		},
		Object: humantask.ObjectBinding{
			Kind: ObjectKindKnowledge, ID: admission.RunID,
			Version: recommendation.IndexDigest, Digest: recommendation.IndexDigest,
		},
		Roles: []humantask.Role{humantask.RoleAdmin, humantask.RoleDeveloper},
	})
	if err != nil {
		return false, fmt.Errorf("create the knowledge confirmation task: %w", err)
	}
	if _, err := s.store.AttachConfirmationTask(ctx, admission.RunID, task.ID); err != nil {
		return false, err
	}
	return true, nil
}

// askPlanApproval records the human decision of one reviewed plan and creates
// its versioned request. The request binds the exact reviewed deliverable, so a
// later plan revision cannot inherit this approval.
func (s *Service) askPlanApproval(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
	view execution.RunView,
) (bool, error) {
	planRound, found := s.roundOf(view, fixedflow.NodePlan)
	if !found || planRound.State != execution.NodeDecided {
		return false, nil
	}
	ref, found, err := s.deliverableRef(ctx, admission, fixedflow.NodePlan, planRound.Iteration)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if _, err := s.decide(ctx, admission, node, iteration, round, execution.NodeWaitingHuman, "", nil); err != nil {
		return false, err
	}
	requestKey := fmt.Sprintf("fixedflow.plan-approval:%s:%d", admission.RunID, iteration)
	if _, err := s.humans.Create(ctx, humantask.CreateCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID, RunID: admission.RunID,
		NodeRunID: nodeRoundID(admission.RunID, node.ID, iteration),
		ActorID:   admission.AdmittedByUserID, RequestKey: requestKey,
		Title: "Approve the reviewed plan",
		Kind:  fixedflow.HumanTaskPlanApproval, Mode: humantask.ModeKowaAnswer,
		Fields: []humantask.Field{
			{Name: "decision", Type: "decision", Required: true},
			{Name: "feedback", Type: "comment"},
		},
		Object: humantask.ObjectBinding{
			Kind: ObjectKindPlan, ID: admission.RunID, Version: ref.Digest, Digest: ref.Digest,
		},
		Roles: []humantask.Role{humantask.RoleAdmin, humantask.RoleDeveloper},
	}); err != nil {
		return false, fmt.Errorf("create the plan approval task: %w", err)
	}
	// The superseded request of the previous plan round is expired through the
	// owner's own version-scoped binding rule, so the projection can never show
	// a stale approval next to the pending one. A first request has nothing to
	// expire, and the pending request itself is excluded by its version.
	if _, err := s.humans.ExpireBoundTo(ctx, admission.WorkspaceID, ObjectKindPlan, admission.RunID,
		ref.Digest, "superseded by a new plan approval request", admission.AdmittedByUserID); err != nil {
		return false, fmt.Errorf("expire the superseded plan approval: %w", err)
	}
	return true, nil
}

// decide records one control-plane decision with its frozen input digest.
func (s *Service) decide(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
	state execution.NodeState,
	verdict string,
	output map[string]any,
) (bool, error) {
	run, err := s.execution.Run(ctx, admission.RunID)
	if err != nil {
		return false, err
	}
	digest, err := s.controlInputDigest(ctx, admission, node.ID, iteration)
	if err != nil {
		return false, err
	}
	_, err = s.execution.DecideNode(ctx, execution.DecideNodeCommand{
		RunID: admission.RunID, NodeID: node.ID, NodeRunID: nodeRoundID(admission.RunID, node.ID, iteration),
		ExpectedRunVersion: run.Version, InputDigest: digest,
		State: state, Verdict: verdict, Output: output, Round: round,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// consumeHumanAnswer applies the accepted answer of one human round. The answer
// is consumed exactly once, and an answer whose continuation was already taken
// up is still applied: the decision itself is idempotent.
func (s *Service) consumeHumanAnswer(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) (bool, error) {
	continuations, err := s.humans.PendingContinuations(ctx, admission.WorkspaceID, admission.RunID)
	if err != nil {
		return false, err
	}
	if continuation, found := s.continuationOf(round.ID, continuations); found {
		task, err := s.humans.Get(ctx, admission.WorkspaceID, continuation.TaskID)
		if err != nil {
			return false, err
		}
		if task.Response == nil {
			return false, nil
		}
		if _, err := s.humans.ConsumeContinuation(ctx, humantask.ConsumeContinuationMutation{
			ContinuationID: continuation.ID, WorkspaceID: admission.WorkspaceID,
			ActorID: admission.AdmittedByUserID, At: s.now().UTC(),
		}); err != nil {
			return false, err
		}
		return s.applyAcceptedAnswer(ctx, admission, node, round, task, view)
	}
	if task, found, err := s.resolvedRequest(ctx, admission, round.ID); err != nil || found {
		if err != nil {
			return false, err
		}
		return s.applyAcceptedAnswer(ctx, admission, node, round, task, view)
	}
	// An interrupted request creation leaves a waiting round without its
	// request; re-asking is idempotent, and a present request is left alone.
	exists, err := s.humanRequestExists(ctx, admission, round.ID)
	if err != nil || exists {
		return false, err
	}
	switch node.ID {
	case fixedflow.NodeKnowledgeConfirm:
		return s.askKnowledgeConfirmation(ctx, admission, node, round.Iteration,
			execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: MaxNodeRounds})
	case fixedflow.NodePlanApproval:
		return s.askPlanApproval(ctx, admission, node, round.Iteration,
			execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: MaxNodeRounds}, view)
	default:
		return false, nil
	}
}

// resolvedRequest finds the accepted answer of one round, whatever consumed it.
func (s *Service) resolvedRequest(
	ctx context.Context,
	admission Admission,
	nodeRunID string,
) (humantask.HumanTask, bool, error) {
	tasks, err := s.humans.List(ctx, admission.WorkspaceID, false)
	if err != nil {
		return humantask.HumanTask{}, false, err
	}
	for _, task := range tasks {
		if task.NodeRunID == nodeRunID && task.State == humantask.StateResolved && task.Response != nil {
			return task, true, nil
		}
	}
	return humantask.HumanTask{}, false, nil
}

// applyAcceptedAnswer records the decision one accepted answer implies. It is
// idempotent: applying it twice writes the same decision.
func (s *Service) applyAcceptedAnswer(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	task humantask.HumanTask,
	view execution.RunView,
) (bool, error) {
	_ = view
	revision := execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: MaxNodeRounds}
	iteration := round.Iteration
	switch node.ID {
	case fixedflow.NodeKnowledgeConfirm:
		if task.Response.Decision != humantask.DecisionApproved {
			return s.decide(ctx, admission, node, iteration, revision, execution.NodeDecided,
				VerdictRejected, map[string]any{"decision": task.Response.Decision})
		}
		recommendation, err := s.store.Recommendation(ctx, admission.RunID)
		if err != nil {
			return false, err
		}
		confirmation := humantask.Continuation{TaskID: task.ID}
		confirmed, err := s.confirmKnowledge(ctx, admission, recommendation, confirmation)
		if err != nil {
			if errorsIs(err, errKnowledgeRejected) {
				return s.decide(ctx, admission, node, iteration, revision, execution.NodeDecided,
					VerdictRejected, map[string]any{"decision": task.Response.Decision})
			}
			return false, err
		}
		return s.decide(ctx, admission, node, iteration, revision, execution.NodeDecided, VerdictApproved,
			map[string]any{"snapshotId": confirmed.SnapshotID, "bindingEventId": confirmed.BindingEventID})
	case fixedflow.NodePlanApproval:
		verdict := VerdictRejected
		if task.Response.Decision == humantask.DecisionApproved {
			verdict = VerdictApproved
		}
		return s.decide(ctx, admission, node, iteration, revision, execution.NodeDecided, verdict,
			map[string]any{"decision": task.Response.Decision})
	default:
		return false, fmt.Errorf("%w: node %s has no answer handler", ErrValidation, node.ID)
	}
}

// retryControlNode re-runs one waiting control-plane node, which either
// advances it or leaves it waiting for the external fact.
func (s *Service) retryControlNode(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) (bool, error) {
	switch node.ID {
	case fixedflow.NodeVerifyChange:
		return s.runVerifyChange(ctx, admission, node, round.Iteration, execution.DispatchRound{
			Kind: execution.RoundInitial, MaxRounds: MaxNodeRounds,
		}, view)
	case fixedflow.NodeObserveReview:
		return s.runObserve(ctx, admission, node, round.Iteration, execution.DispatchRound{
			Kind: execution.RoundInitial, MaxRounds: MaxNodeRounds,
		}, view, "pull_request_review")
	case fixedflow.NodeObserveMerge:
		return s.runObserve(ctx, admission, node, round.Iteration, execution.DispatchRound{
			Kind: execution.RoundInitial, MaxRounds: MaxNodeRounds,
		}, view, "pull_request_merge")
	default:
		return false, nil
	}
}

// preparePublishPullRequest admits the gated publish_pr operation before the
// pull-request attempt exists. S05 owns the gate: required tests and an
// independent review of the delivered commit must already be valid, and the
// operation is the only write intent this run may issue.
func (s *Service) preparePublishPullRequest(
	ctx context.Context,
	admission Admission,
	view execution.RunView,
	round execution.DispatchRound,
	iteration int,
) (bool, error) {
	commit, ok := s.codeCommit(view)
	if !ok {
		return false, nil
	}
	operation, found, err := s.operationFor(ctx, admission, githubdelivery.OperationPublishPR,
		admission.WorkingBranchRef, commit)
	if err != nil || found {
		return found, err
	}
	// The write intent references the round the dispatch below will attach its
	// attempt to: the planned round is opened first when it does not exist yet
	// (a rebuild supersedes the previous round), and a replay that finds it
	// proceeds without opening anything.
	targetRoundID := nodeRoundID(admission.RunID, fixedflow.NodePublishPR, iteration)
	if _, materialised := s.existingRound(view, targetRoundID); !materialised {
		run, err := s.execution.Run(ctx, admission.RunID)
		if err != nil {
			return false, err
		}
		digest, err := s.controlInputDigest(ctx, admission, fixedflow.NodePublishPR, iteration)
		if err != nil {
			return false, err
		}
		if _, err := s.execution.OpenNodeRound(ctx, execution.OpenRoundCommand{
			RunID: admission.RunID, NodeID: fixedflow.NodePublishPR,
			NodeRunID:          targetRoundID,
			ExpectedRunVersion: run.Version, InputDigest: digest,
			Round: round,
		}); err != nil && !errorsIs(err, execution.ErrVersionConflict) {
			return false, fmt.Errorf("open the publish_pr round: %w", err)
		}
	}
	_ = operation
	verifications, err := s.evaluation.Verifications(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return false, err
	}
	requiredTests, independentReview, ok := publishGateEvidence(view, verifications)
	if !ok {
		// The gates are not satisfied yet; the reconciler waits instead of
		// admitting a write intent that S05 would refuse.
		return false, nil
	}
	admitted, err := s.deliveries.AdmitOperation(ctx, githubdelivery.AdmitOperationCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID, RunID: admission.RunID,
		NodeRunID:    nodeRoundID(admission.RunID, fixedflow.NodePublishPR, iteration),
		Kind:         githubdelivery.OperationPublishPR,
		RepositoryID: admission.ProjectRepositoryID, Ref: admission.WorkingBranchRef,
		BaseRef: admission.ProjectBranchRef, CommitOID: commit,
		RequestedByUserID: admission.AdmittedByUserID,
		VerificationIDs:   requiredTests, IndependentReviewIDs: independentReview,
		// The write intent belongs to the delivered commit: a revised delivery
		// asks for its own pull request instead of colliding with the previous one.
		RequestKey: fmt.Sprintf("fixedflow.publish-pr:%s:%s", admission.RunID, commit),
	})
	if err != nil {
		return false, fmt.Errorf("admit publish_pr: %w", err)
	}
	if _, err := s.deliveries.MarkAttempted(ctx, githubdelivery.MarkAttemptedCommand{
		WorkspaceID: admission.WorkspaceID, OperationID: admitted.OperationID,
		ExpectedVersion: admitted.Version, ActorID: admission.AdmittedByUserID,
		RequestKey: fmt.Sprintf("fixedflow.mark-publish-pr:%s:%s", admission.RunID, commit),
	}); err != nil {
		return false, err
	}
	return true, nil
}

// publishGateEvidence binds the verification identities the publish_pr gate
// requires from the current rounds of the fixed flow.
func publishGateEvidence(
	view execution.RunView,
	verifications []evaluation.Verification,
) (requiredTests, independentReview []string, ok bool) {
	find := func(kind, nodeRunID string) (string, bool) {
		for _, verification := range verifications {
			if verification.Kind == kind && verification.NodeRunID == nodeRunID {
				return verification.VerificationID, true
			}
		}
		return "", false
	}
	testRound, found := roundFor(view, fixedflow.NodeTest)
	if !found {
		return nil, nil, false
	}
	testID, found := find(evaluation.KindRequiredTests, testRound.ID)
	if !found {
		return nil, nil, false
	}
	reviewRound, found := roundFor(view, fixedflow.NodeCodeReview)
	if !found {
		return nil, nil, false
	}
	reviewID, found := find(evaluation.KindIndependentReview, reviewRound.ID)
	if !found {
		return nil, nil, false
	}
	return []string{testID}, []string{reviewID}, true
}

// runVerifyChange reconciles the delivered commit against the remote branch.
func (s *Service) runVerifyChange(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
	view execution.RunView,
) (bool, error) {
	commit, ok := s.codeCommit(view)
	if !ok {
		return false, nil
	}
	// The external operation references this node round, so the round exists
	// first: a waiting round is recorded, then the operation is admitted.
	if _, err := s.decide(ctx, admission, node, iteration, round, execution.NodeWaitingHuman, "", nil); err != nil {
		return false, err
	}
	operation, found, err := s.operationFor(ctx, admission, githubdelivery.OperationVerifyChange,
		admission.WorkingBranchRef, commit)
	if err != nil {
		return false, err
	}
	if !found {
		if _, err := s.deliveries.AdmitOperation(ctx, githubdelivery.AdmitOperationCommand{
			WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID, RunID: admission.RunID,
			NodeRunID:    nodeRoundID(admission.RunID, node.ID, iteration),
			Kind:         githubdelivery.OperationVerifyChange,
			RepositoryID: admission.ProjectRepositoryID, Ref: admission.WorkingBranchRef, CommitOID: commit,
			RequestedByUserID: admission.AdmittedByUserID,
			RequestKey:        fmt.Sprintf("fixedflow.verify-change:%s:%d", admission.RunID, iteration),
		}); err != nil {
			return false, fmt.Errorf("admit verify_change: %w", err)
		}
		return true, nil
	}
	switch operation.State {
	case githubdelivery.StatePending:
		if _, err := s.deliveries.MarkAttempted(ctx, githubdelivery.MarkAttemptedCommand{
			WorkspaceID: admission.WorkspaceID, OperationID: operation.OperationID,
			ExpectedVersion: operation.Version, ActorID: admission.AdmittedByUserID,
			RequestKey: fmt.Sprintf("fixedflow.mark-attempted:%s:%d", admission.RunID, iteration),
		}); err != nil {
			return false, err
		}
		return true, nil
	case githubdelivery.StateAttempted, githubdelivery.StateUnknown:
		observed, err := s.deliveries.ObserveOperation(ctx, admission.WorkspaceID, operation.OperationID)
		if err != nil {
			return false, err
		}
		switch observed.State {
		case githubdelivery.StateSucceeded:
			return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved,
				map[string]any{"commitOid": commit, "operationId": observed.OperationID})
		case githubdelivery.StateFailed:
			return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictFailed,
				map[string]any{"commitOid": commit, "operationId": observed.OperationID})
		}
		return false, nil
	default:
		return false, nil
	}
}

// operationFor finds one already admitted operation of a run by its frozen
// request identity, so a reconciliation never admits a second write intent.
func (s *Service) operationFor(
	ctx context.Context,
	admission Admission,
	kind githubdelivery.OperationKind,
	ref, commitOID string,
) (githubdelivery.ExternalOperation, bool, error) {
	operations, err := s.deliveries.Operations(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return githubdelivery.ExternalOperation{}, false, err
	}
	for _, operation := range operations {
		if operation.Kind != kind || operation.RunID != admission.RunID {
			continue
		}
		if ref != "" && operation.Request.Ref != ref {
			continue
		}
		if commitOID != "" && operation.Request.CommitOID != commitOID {
			continue
		}
		return operation, true, nil
	}
	return githubdelivery.ExternalOperation{}, false, nil
}

// runObserve waits for one external GitHub fact: the review approval or the
// merge. The external-fact request is a reminder; only the reconciled fact
// settles it, and the node decides only when the fact exists.
func (s *Service) runObserve(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
	view execution.RunView,
	kind string,
) (bool, error) {
	prNumber, ok := s.publishedPullRequest(view)
	if !ok {
		return false, nil
	}
	// The reviewed and merged head is the delivered commit of the code round.
	headOID, ok := s.codeCommit(view)
	if !ok {
		return false, nil
	}
	if _, err := s.decide(ctx, admission, node, iteration, round, execution.NodeWaitingHuman, "", nil); err != nil {
		return false, err
	}
	requestKey := fmt.Sprintf("fixedflow.observe.%s:%s:%d", kind, admission.RunID, iteration)
	if _, err := s.humans.Create(ctx, humantask.CreateCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID, RunID: admission.RunID,
		NodeRunID: nodeRoundID(admission.RunID, node.ID, iteration),
		ActorID:   admission.AdmittedByUserID, RequestKey: requestKey,
		Title: externalFactTitle(kind),
		Kind:  kind, Mode: humantask.ModeExternalFact,
		Object: humantask.ObjectBinding{Kind: kind, ID: prNumber, Version: headOID},
	}); err != nil {
		return false, fmt.Errorf("create the external-fact task: %w", err)
	}
	if _, err := s.deliveries.ObservePullRequest(ctx, githubdelivery.ObservePullRequestCommand{
		WorkspaceID: admission.WorkspaceID, RepositoryID: admission.ProjectRepositoryID, Number: prNumber,
	}); err != nil {
		return false, err
	}
	// The pull-request write intent settles against the same reconciled facts.
	// The intent of the current delivery is the one bound to the delivered
	// commit: a revised delivery has its own intent, and the earlier one must
	// not shadow it.
	if operation, found, err := s.operationFor(ctx, admission,
		githubdelivery.OperationPublishPR, admission.WorkingBranchRef, headOID); err == nil && found &&
		(operation.State == githubdelivery.StateAttempted || operation.State == githubdelivery.StateUnknown) {
		if _, err := s.deliveries.ObserveOperation(ctx, admission.WorkspaceID, operation.OperationID); err != nil {
			return false, err
		}
	} else if err != nil {
		return false, err
	}
	observation, found, err := s.observationFor(ctx, admission, kind, prNumber, headOID)
	if err != nil || !found {
		return false, err
	}
	output := map[string]any{"observationId": observation.ObservationID, "pullRequestNumber": prNumber}
	if kind == "pull_request_merge" {
		output["mergeCommitOid"] = observation.PullRequest.MergeCommitOID
		return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved, output)
	}
	return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved, output)
}

func externalFactTitle(kind string) string {
	if kind == "pull_request_merge" {
		return "Review and merge the delivery pull request on GitHub"
	}
	return "Submit an approving GitHub review on the delivery pull request"
}

// observationFor finds the reconciled observation of one fact.
func (s *Service) observationFor(
	ctx context.Context,
	admission Admission,
	kind, prNumber, headOID string,
) (githubdelivery.Observation, bool, error) {
	observations, err := s.deliveries.LatestObservations(ctx, admission.WorkspaceID, admission.ProjectRepositoryID)
	if err != nil {
		return githubdelivery.Observation{}, false, err
	}
	for _, observation := range observations {
		if observation.Unknown {
			continue
		}
		switch kind {
		case "pull_request_review":
			if observation.SubjectKind == githubdelivery.SubjectReview &&
				observation.SubjectKey == prNumber+"/"+reviewID(observation) &&
				observation.Review != nil && observation.Review.State == "APPROVED" &&
				observation.Review.CommitOID == headOID &&
				observation.Review.ReviewerID != s.prAuthorID(ctx, admission, prNumber) {
				return observation, true, nil
			}
		case "pull_request_merge":
			if observation.SubjectKind == githubdelivery.SubjectPullRequest &&
				observation.SubjectKey == prNumber && observation.PullRequest != nil &&
				observation.PullRequest.Merged && oidPattern.MatchString(observation.PullRequest.MergeCommitOID) {
				return observation, true, nil
			}
		}
	}
	return githubdelivery.Observation{}, false, nil
}

func reviewID(observation githubdelivery.Observation) string {
	if observation.Review == nil {
		return ""
	}
	return observation.Review.ReviewID
}

// prAuthorID reports the PR author the observations recorded, so a self
// approval never satisfies the review gate.
func (s *Service) prAuthorID(ctx context.Context, admission Admission, prNumber string) string {
	observations, err := s.deliveries.LatestObservations(ctx, admission.WorkspaceID, admission.ProjectRepositoryID)
	if err != nil {
		return ""
	}
	for _, observation := range observations {
		if observation.SubjectKind == githubdelivery.SubjectPullRequest && observation.SubjectKey == prNumber &&
			observation.PullRequest != nil {
			return observation.PullRequest.AuthorID
		}
	}
	return ""
}

// mergeObservation is the reconciled merge fact the closing checklist binds.
func (s *Service) mergeObservation(ctx context.Context, admission Admission) (githubdelivery.Observation, bool, error) {
	view, err := s.execution.RunView(ctx, admission.RunID)
	if err != nil {
		return githubdelivery.Observation{}, false, err
	}
	round, found := s.roundOf(view, fixedflow.NodeObserveMerge)
	if !found || round.State != execution.NodeDecided || !gateAccepting(round.Verdict) {
		return githubdelivery.Observation{}, false, nil
	}
	observationID, _ := round.Output["observationId"].(string)
	if observationID == "" {
		return githubdelivery.Observation{}, false, nil
	}
	observation, found, err := s.deliveries.Observation(ctx, admission.WorkspaceID, observationID)
	if err != nil || !found {
		return githubdelivery.Observation{}, false, err
	}
	return observation, true, nil
}

// publishedPullRequest reads the pull request one settled publish_pr round
// produced. The reviewed head is the delivered commit, not a value the pull
// request itself reports.
func (s *Service) publishedPullRequest(view execution.RunView) (string, bool) {
	round, found := s.roundOf(view, fixedflow.NodePublishPR)
	if !found || !s.accepting(fixedflow.NodePublishPR, round) {
		return "", false
	}
	if number, ok := outputString(round.Output, "pr_number"); ok {
		return number, true
	}
	return outputString(round.Output, "pullRequestNumber")
}

// codeCommit reads the delivered commit oid of the code round.
func (s *Service) codeCommit(view execution.RunView) (string, bool) {
	round, found := s.roundOf(view, fixedflow.NodeCode)
	if !found || !s.accepting(fixedflow.NodeCode, round) {
		return "", false
	}
	if deliveries, ok := round.Output["delivery"].([]any); ok && len(deliveries) > 0 {
		if record, ok := deliveries[0].(map[string]any); ok {
			if commit, ok := outputString(record, "commitOid"); ok {
				return commit, true
			}
		}
	}
	return outputString(round.Output, "commit_oid")
}

func outputString(output map[string]any, key string) (string, bool) {
	value, ok := output[key].(string)
	return value, ok && value != ""
}

// recordVerification records the gate evidence of one decided node.
func (s *Service) recordVerification(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	round execution.NodeView,
	view execution.RunView,
) (bool, error) {
	kind, found := verificationKind(node.ID)
	if !found {
		return false, nil
	}
	existing, err := s.evaluation.Verifications(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return false, err
	}
	for _, verification := range existing {
		if verification.Kind == kind && verification.NodeRunID == round.ID {
			return false, nil
		}
	}
	commit, ok := s.codeCommit(view)
	if !ok {
		return false, nil
	}
	if node.ID == fixedflow.NodeIntegration {
		merge, found, err := s.mergeObservation(ctx, admission)
		if err != nil || !found {
			return false, err
		}
		commit = merge.PullRequest.MergeCommitOID
	}
	result := evaluation.ResultFailed
	if gateAccepting(round.Verdict) {
		result = evaluation.ResultPassed
	}
	ref, found, err := s.deliverableRef(ctx, admission, node.ID, round.Iteration)
	if err != nil {
		return false, err
	}
	command := evaluation.RecordVerificationCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID,
		RunID: admission.RunID, NodeRunID: round.ID, Kind: kind, SubjectKind: "commit",
		RepositoryID: admission.ProjectRepositoryID, CommitOID: commit,
		ConfigurationVersion: s.configVersion(admission),
		ProducerKind:         evaluation.ProducerTask, ProducerID: attemptID(admission.RunID, node.ID, round.Iteration),
		Source: evaluation.SourceReal, Result: result,
		RequestKey: fmt.Sprintf("fixedflow.verification.%s:%s:%d", kind, admission.RunID, round.Iteration),
		ActorID:    admission.AdmittedByUserID,
	}
	if found && ref.ArtifactID != "" {
		command.ReportArtifactID = ref.ArtifactID
		command.ReportDigest = ref.Digest
	}
	if _, err := s.evaluation.RecordVerification(ctx, command); err != nil {
		return false, fmt.Errorf("record %s evidence: %w", kind, err)
	}
	return true, nil
}

func verificationKind(nodeID string) (string, bool) {
	switch nodeID {
	case fixedflow.NodeTest:
		return evaluation.KindRequiredTests, true
	case fixedflow.NodeCodeReview:
		return evaluation.KindIndependentReview, true
	case fixedflow.NodeSecurityReview:
		return evaluation.KindSecurityReview, true
	case fixedflow.NodeIntegration:
		return evaluation.KindPostMergeIntegration, true
	default:
		return "", false
	}
}

// runClose closes the work item through the single authoritative transaction.
// Only the delivery closure can complete the run successfully.
func (s *Service) runClose(
	ctx context.Context,
	admission Admission,
	node workflow.Node,
	iteration int,
	round execution.DispatchRound,
	view execution.RunView,
) (bool, error) {
	recommendation, err := s.store.Recommendation(ctx, admission.RunID)
	if err != nil {
		return false, err
	}
	if !recommendation.Confirmed() {
		return false, nil
	}
	merge, found, err := s.mergeObservation(ctx, admission)
	if err != nil || !found {
		return false, err
	}
	reviewRound, found := s.roundOf(view, fixedflow.NodeObserveReview)
	if !found {
		return false, nil
	}
	reviewObservationID, _ := reviewRound.Output["observationId"].(string)
	pullRequestNumber, ok := s.publishedPullRequest(view)
	if reviewObservationID == "" || !ok {
		return false, nil
	}
	item, err := s.workitems.Get(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return false, err
	}
	run, err := s.execution.Run(ctx, admission.RunID)
	if err != nil {
		return false, err
	}
	verifications, err := s.evaluation.Verifications(ctx, admission.WorkspaceID, admission.WorkItemID)
	if err != nil {
		return false, err
	}
	requiredTests, independentReview, postMerge, ok := closingEvidence(view, verifications)
	if !ok {
		return false, nil
	}
	// The auxiliary suggestion branch is frozen as a durable pending fact right
	// before the closure commits: the closure never waits for any artifact
	// work, and a crash right after the closure still finds the pending branch.
	if err := s.seedKnowledgeSuggestion(ctx, admission); err != nil {
		return false, err
	}
	closure, err := s.evaluation.Close(ctx, evaluation.CloseCommand{
		WorkspaceID: admission.WorkspaceID, WorkItemID: admission.WorkItemID, RunID: admission.RunID,
		ExpectedWorkItemVersion: item.Version, ExpectedRunVersion: run.Version,
		ActorID:             admission.AdmittedByUserID,
		RequestKey:          fmt.Sprintf("fixedflow.close:%s:%d", admission.RunID, iteration),
		PolicyVersion:       admission.PolicyVersion,
		KnowledgeSnapshotID: recommendation.SnapshotID, KnowledgeBindingEventID: recommendation.BindingEventID,
		PullRequestNumber:  pullRequestNumber,
		MergeObservationID: merge.ObservationID, ReviewObservationID: reviewObservationID,
		RequiredTestIDs: requiredTests, IndependentReviewID: independentReview,
		PostMergeVerificationID: postMerge,
	})
	if err != nil {
		return false, fmt.Errorf("close the work item: %w", err)
	}
	return s.decide(ctx, admission, node, iteration, round, execution.NodeDecided, VerdictApproved,
		map[string]any{"closureId": closure.ClosureID, "mergeCommitOid": closure.MergeCommitOID})
}

// closingEvidence binds the verification identities the closing checklist
// requires, from the current rounds of the fixed flow.
func closingEvidence(
	view execution.RunView,
	verifications []evaluation.Verification,
) (requiredTests []string, independentReview, postMerge string, ok bool) {
	find := func(kind, nodeRunID string) (string, bool) {
		for _, verification := range verifications {
			if verification.Kind == kind && verification.NodeRunID == nodeRunID {
				return verification.VerificationID, true
			}
		}
		return "", false
	}
	testRound, found := roundFor(view, fixedflow.NodeTest)
	if !found {
		return nil, "", "", false
	}
	if id, found := find(evaluation.KindRequiredTests, testRound.ID); found {
		requiredTests = []string{id}
	}
	reviewRound, found := roundFor(view, fixedflow.NodeCodeReview)
	if !found {
		return nil, "", "", false
	}
	independentReview, found = find(evaluation.KindIndependentReview, reviewRound.ID)
	if !found {
		return nil, "", "", false
	}
	integrationRound, found := roundFor(view, fixedflow.NodeIntegration)
	if !found {
		return nil, "", "", false
	}
	postMerge, found = find(evaluation.KindPostMergeIntegration, integrationRound.ID)
	if !found {
		return nil, "", "", false
	}
	if len(requiredTests) == 0 {
		return nil, "", "", false
	}
	return requiredTests, independentReview, postMerge, true
}

func roundFor(view execution.RunView, nodeID string) (execution.NodeView, bool) {
	for _, node := range view.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return execution.NodeView{}, false
}

var _ = artifact.Published
var _ = knowledge.InputRequest{}
var _ = execution.TaskQueued
