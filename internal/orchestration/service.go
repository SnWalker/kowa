package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/workflow"
	"github.com/SnWalker/kowa/internal/workitem"
)

// Command types of this module's idempotent commands.
const (
	CommandAdmit    = "orchestration.admit"
	CommandRecovery = "orchestration.recovery"
)

// Service implements run admission, advancement and recovery of the single
// built-in workflow.
type Service struct {
	store      Store
	execution  ExecutionPort
	knowledge  KnowledgePort
	artifacts  ArtifactPort
	workitems  WorkItemPort
	humans     HumanTaskPort
	deliveries DeliveryPort
	evaluation EvaluationPort
	runtimes   RuntimePort
	bindings   ProjectBindingSource
	access     WorkspaceAuthorizer
	fetch      Materializer
	ids        IDGenerator
	now        Clock
	plan       workflow.CompiledPlan
	// advanceMu serialises reconciliation inside this process, so two drivers
	// (a command and the background pass) never race on the same facts.
	advanceMu sync.Mutex
}

// NewService assembles the orchestration service over the frozen ports.
func NewService(options Options) (*Service, error) {
	plan, err := fixedflow.Compile()
	if err != nil {
		return nil, fmt.Errorf("compile the built-in workflow: %w", err)
	}
	service := &Service{
		store: options.Store, execution: options.Execution, knowledge: options.Knowledge,
		artifacts: options.Artifacts, workitems: options.WorkItems, humans: options.HumanTasks,
		deliveries: options.Deliveries, evaluation: options.Evaluation, runtimes: options.Runtimes,
		bindings: options.Bindings, access: options.Access, fetch: options.Materializer,
		ids: options.IDs, now: options.Now, plan: plan,
	}
	if service.access == nil || service.bindings == nil || service.fetch == nil {
		// The composition always supplies the workspace authorizer, the
		// repository binding source and the materializer: without them the
		// service cannot authorize an admission or freeze a baseline, so it
		// refuses to exist instead of defaulting to allow.
		return nil, fmt.Errorf("%w: authorization, repository bindings and the materializer are required", ErrValidation)
	}
	if service.ids == nil {
		service.ids = DefaultIDs{}
	}
	if service.now == nil {
		service.now = time.Now
	}
	return service, nil
}

// Options are the frozen ports the service consumes.
type Options struct {
	Store        Store
	Execution    ExecutionPort
	Knowledge    KnowledgePort
	Artifacts    ArtifactPort
	WorkItems    WorkItemPort
	HumanTasks   HumanTaskPort
	Deliveries   DeliveryPort
	Evaluation   EvaluationPort
	Runtimes     RuntimePort
	Bindings     ProjectBindingSource
	Access       WorkspaceAuthorizer
	Materializer Materializer
	IDs          IDGenerator
	Now          Clock
}

// Plan exposes the frozen compiled plan this service runs.
func (s *Service) Plan() workflow.CompiledPlan { return s.plan }

// AdmitCommand is one authorized run admission.
type AdmitCommand struct {
	WorkspaceID     string
	WorkItemID      string
	ActorID         string
	RequestKey      string
	ExpectedVersion int64
	Kind            workitem.RunBindingKind
	SourceRunID     string
	Reason          string
	MergedBaseline  string
}

// AdmissionResult is the frozen outcome of one admission.
type AdmissionResult struct {
	Admission       Admission       `json:"admission"`
	WorkItemVersion int64           `json:"workItemVersion"`
	Recommendation  *Recommendation `json:"knowledgeRecommendation,omitempty"`
	Replayed        bool            `json:"replayed"`
}

// Admit freezes the workflow baseline of one WorkItem, materialises its
// delivery run and starts the knowledge step. Every frozen fact is read from
// the owning module; the request body supplies identities only.
func (s *Service) Admit(ctx context.Context, command AdmitCommand) (AdmissionResult, error) {
	if command.WorkspaceID == "" || command.WorkItemID == "" || !validActor(command.ActorID) ||
		!requestKeyRule.MatchString(command.RequestKey) || command.ExpectedVersion < 1 ||
		(command.Kind != workitem.BindingInitial && command.Kind != workitem.BindingRepair) {
		return AdmissionResult{}, ErrValidation
	}
	if command.Kind == workitem.BindingRepair {
		if command.SourceRunID == "" || command.Reason == "" || !oidPattern.MatchString(command.MergedBaseline) {
			return AdmissionResult{}, ErrValidation
		}
	} else if command.SourceRunID != "" || command.Reason != "" || command.MergedBaseline != "" {
		return AdmissionResult{}, ErrValidation
	}
	if err := s.authorizeWriter(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return AdmissionResult{}, err
	}
	item, err := s.workitems.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return AdmissionResult{}, err
	}
	if item.State != workitem.StateOpen {
		return AdmissionResult{}, fmt.Errorf("%w: the work item is %s", ErrPolicyBlocked, item.State)
	}
	if item.Version != command.ExpectedVersion {
		return AdmissionResult{}, ErrVersionConflict
	}
	// Advancement authority is a precondition, not a side effect: a work item
	// that already has a delivery run never admits a second one, so a refused
	// admission leaves no run behind.
	switch command.Kind {
	case workitem.BindingInitial:
		if item.CurrentRunID != "" {
			return AdmissionResult{}, fmt.Errorf(
				"%w: the work item already has a delivery run", ErrPolicyBlocked)
		}
	case workitem.BindingRepair:
		if item.CurrentRunID != command.SourceRunID {
			return AdmissionResult{}, fmt.Errorf(
				"%w: a repair run must replace the current delivery run", ErrPolicyBlocked)
		}
	}
	admissionDigest := commandDigest("orchestration.admit", command.WorkspaceID, command.WorkItemID,
		string(command.Kind), command.SourceRunID, command.MergedBaseline, fmt.Sprint(command.ExpectedVersion))
	if replayed, ok, err := s.replayedAdmission(ctx, command, admissionDigest); err != nil || ok {
		return replayed, err
	}

	projectRepositoryID, knowledgeRepositoryID, err := s.bindings.Repositories(ctx, command.WorkspaceID, command.ActorID)
	if err != nil {
		return AdmissionResult{}, fmt.Errorf("%w: the workspace has no project and knowledge repository", ErrResourceUnavailable)
	}
	projectBranch, projectCommit, err := s.resolveBaseline(ctx, projectRepositoryID, command)
	if err != nil {
		return AdmissionResult{}, err
	}
	_, knowledgeCommit, err := s.resolveBranchHead(ctx, knowledgeRepositoryID)
	if err != nil {
		return AdmissionResult{}, fmt.Errorf("%w: the knowledge repository baseline is unavailable: %v", ErrResourceUnavailable, err)
	}
	provider, machineUserID, err := s.selectRuntime(ctx, command.WorkspaceID)
	if err != nil {
		return AdmissionResult{}, err
	}
	runID, err := s.ids.New("run")
	if err != nil {
		return AdmissionResult{}, err
	}
	admission := Admission{
		RunID: runID, WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		DefinitionName: s.plan.Name, DefinitionVersion: s.plan.Version,
		DefinitionDigest:    s.plan.Digest,
		ProjectRepositoryID: projectRepositoryID, KnowledgeRepositoryID: knowledgeRepositoryID,
		ProjectCommitOID: projectCommit, KnowledgeCommitOID: knowledgeCommit,
		ProjectBranchRef: projectBranch, WorkingBranchRef: workingBranch(runID),
		ProviderID: provider.ID, ProviderVersion: provider.Version,
		PolicyVersion: fixedflow.PolicyVersion, AdmittedByUserID: command.ActorID,
		AdmittedAt: s.now().UTC(),
	}
	firstNode := s.plan.Order[0]
	if err := s.execution.CreateRun(ctx, execution.CreateRunCommand{
		Run: execution.Run{
			ID: runID, WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
			StartedByGitHubUserID: command.ActorID, GitExecutionUserID: machineUserID,
			DefinitionName: s.plan.Name, DefinitionVersion: s.plan.Version,
			DefinitionDigest: s.plan.Digest, State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: nodeRoundID(runID, firstNode, 1), RunID: runID, NodeID: firstNode, Iteration: 1,
			State: execution.NodeReady, InputDigest: s.controlInputDigest(ctx, admission, firstNode, 1), Version: 1,
		}},
	}); err != nil {
		return AdmissionResult{}, fmt.Errorf("create the delivery run: %w", err)
	}
	if err := s.store.NoteRunState(ctx, admission.RunID, string(execution.RunActive)); err != nil {
		return AdmissionResult{}, err
	}
	created, replayed, err := s.store.AdmitOnce(ctx, admission, command.ActorID, command.RequestKey, admissionDigest)
	if err != nil {
		return AdmissionResult{}, err
	}
	if replayed && created.RunID != runID {
		// Another attempt already admitted this work item under the same request
		// key; the frozen facts of that admission are the answer.
		admission = created
	}
	if _, err := s.workitems.AssociateRun(ctx, workitem.AssociateRunCommand{
		WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID, RunID: admission.RunID,
		Kind: command.Kind, SourceRunID: command.SourceRunID, Reason: command.Reason,
		MergedBaselineOID: command.MergedBaseline, ExpectedVersion: command.ExpectedVersion,
		ActorID: command.ActorID, RequestKey: command.RequestKey,
	}); err != nil {
		// The authority was refused after the run exists: withdraw every fact of
		// this attempt, so no run without advancement authority survives and no
		// replay can answer with the withdrawn admission.
		s.withdrawAdmission(ctx, admission, command)
		return AdmissionResult{}, fmt.Errorf("bind the delivery run to the work item: %w", err)
	}
	result := AdmissionResult{Admission: admission, WorkItemVersion: command.ExpectedVersion + 1, Replayed: replayed}
	if err := s.Advance(ctx, admission.RunID); err != nil {
		return AdmissionResult{}, err
	}
	recommendation, err := s.store.Recommendation(ctx, admission.RunID)
	if err != nil && !isNotFound(err) {
		return AdmissionResult{}, err
	}
	if err == nil {
		// Admission must leave the flow at a well-defined point: the knowledge
		// step either waits for its confirmation request or is already
		// confirmed. Anything else is an internal defect, not a client error.
		if !recommendation.Confirmed() && recommendation.HumanTaskID == "" {
			return AdmissionResult{}, fmt.Errorf(
				"%w: the knowledge recommendation %s has no confirmation request",
				ErrPolicyBlocked, recommendation.RunID)
		}
		result.Recommendation = &recommendation
	}
	return result, nil
}

// withdrawAdmission cancels a run whose advancement authority was refused and
// forgets its admission record. A failure here is logged by the caller's
// reconciliation: the authority filter is what keeps the run inert meanwhile.
func (s *Service) withdrawAdmission(ctx context.Context, admission Admission, command AdmitCommand) {
	run, err := s.execution.Run(ctx, admission.RunID)
	if err == nil && !run.State.Settled() {
		_ = s.execution.CancelRun(ctx, admission.RunID, run.Version)
	}
	_ = s.store.VoidAdmission(ctx, admission.RunID, command.ActorID, command.WorkItemID, command.RequestKey)
}

// replayedAdmission answers an identical admission command from the frozen
// admission record, and refuses the same request key with different content.
func (s *Service) replayedAdmission(
	ctx context.Context,
	command AdmitCommand,
	digest string,
) (AdmissionResult, bool, error) {
	existing, found, err := s.store.ReplayedAdmission(ctx, command.ActorID, command.WorkItemID, command.RequestKey, digest)
	if err != nil || !found {
		return AdmissionResult{}, false, err
	}
	item, err := s.workitems.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return AdmissionResult{}, false, err
	}
	result := AdmissionResult{Admission: existing, WorkItemVersion: item.Version, Replayed: true}
	if recommendation, err := s.store.Recommendation(ctx, existing.RunID); err == nil {
		result.Recommendation = &recommendation
	} else if !isNotFound(err) {
		return AdmissionResult{}, false, err
	}
	return result, true, nil
}

// resolveBaseline freezes the project baseline of the run. A first run freezes
// the current head of the delivery branch once; a repair run freezes the merged
// baseline the human confirmed instead of a moving branch head.
func (s *Service) resolveBaseline(
	ctx context.Context,
	projectRepositoryID string,
	command AdmitCommand,
) (string, string, error) {
	branch, head, err := s.resolveBranchHead(ctx, projectRepositoryID)
	if err != nil {
		return "", "", fmt.Errorf("%w: the project repository baseline is unavailable: %v", ErrResourceUnavailable, err)
	}
	if command.Kind == workitem.BindingRepair {
		return branch, command.MergedBaseline, nil
	}
	return branch, head, nil
}

// resolveBranchHead resolves the frozen delivery branch of one repository: the
// MVP only admits a repository whose delivery branch is main or master.
func (s *Service) resolveBranchHead(ctx context.Context, repositoryID string) (string, string, error) {
	var lastErr error
	for _, branch := range []string{ProjectBranchMain, ProjectBranchMaster} {
		head, err := s.fetch.BranchHead(ctx, repositoryID, branch)
		if err == nil && oidPattern.MatchString(head) {
			return branch, head, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no %s or %s branch", ProjectBranchMain, ProjectBranchMaster)
	}
	return "", "", lastErr
}

// selectRuntime freezes the Provider and machine GitHub identity of the run
// from the workspace execution configuration and the registered runtimes.
func (s *Service) selectRuntime(ctx context.Context, workspaceID string) (ProviderSelection, string, error) {
	config, err := s.runtimes.ExecutionConfig(ctx, workspaceID)
	if err != nil {
		return ProviderSelection{}, "", err
	}
	authorized := map[string]bool{}
	for _, runtimeID := range config.Runtimes {
		authorized[runtimeID] = true
	}
	configured := map[execution.Capability]bool{}
	for _, capability := range config.Capabilities {
		configured[execution.Capability{ID: capability.ID, Version: capability.Version}] = true
	}
	required := s.planCapabilities()
	for _, capability := range required {
		if !configured[capability] {
			return ProviderSelection{}, "", fmt.Errorf(
				"%w: the workspace execution configuration does not authorize capability %s@%d",
				ErrPolicyBlocked, capability.ID, capability.Version)
		}
	}
	views, err := s.runtimes.WorkspaceRuntimes(ctx, workspaceID)
	if err != nil {
		return ProviderSelection{}, "", err
	}
	candidates := make([]string, 0, len(views))
	byID := map[string]struct {
		provider ProviderSelection
		userID   string
	}{}
	for _, view := range views {
		if !authorized[view.RuntimeID] || !view.ProviderAuthenticated || view.GitHubUserID == "" {
			continue
		}
		declared := map[execution.Capability]bool{}
		for _, capability := range view.Capabilities {
			declared[execution.Capability{ID: capability.ID, Version: capability.Version}] = true
		}
		covers := true
		for _, capability := range required {
			if !declared[capability] {
				covers = false
				break
			}
		}
		if !covers {
			continue
		}
		candidates = append(candidates, view.RuntimeID)
		byID[view.RuntimeID] = struct {
			provider ProviderSelection
			userID   string
		}{ProviderSelection{ID: view.ProviderID, Version: view.ProviderVersion}, view.GitHubUserID}
	}
	if len(candidates) == 0 {
		return ProviderSelection{}, "", fmt.Errorf(
			"%w: no authorized runtime declares every capability of the fixed workflow", ErrResourceUnavailable)
	}
	slices.Sort(candidates)
	chosen := byID[candidates[0]]
	return chosen.provider, chosen.userID, nil
}

func (s *Service) planCapabilities() []execution.Capability {
	seen := map[execution.Capability]bool{}
	capabilities := make([]execution.Capability, 0, len(s.plan.Nodes))
	for _, node := range s.plan.Nodes {
		if node.Type != workflow.NodeCapability {
			continue
		}
		capability := execution.Capability{ID: node.Capability.ID, Version: node.Capability.Version}
		if seen[capability] {
			continue
		}
		seen[capability] = true
		capabilities = append(capabilities, capability)
	}
	return capabilities
}

// ProviderSelection mirrors the execution wire shape.
type ProviderSelection struct {
	ID      string
	Version string
}

func (s *Service) authorizeWriter(ctx context.Context, workspaceID, actorID string) error {
	if s.access == nil {
		return nil
	}
	return s.access.Authorize(ctx, workspaceID, actorID, []string{"admin", "developer"})
}

// workingBranch derives the frozen write authorization of one run. It is an
// operation authorization only: no frozen read input ever uses this ref.
func workingBranch(runID string) string {
	suffix := runID
	if index := strings.LastIndex(runID, "-"); index >= 0 {
		suffix = runID[index+1:]
	}
	return "refs/heads/task/kowa-" + suffix
}

// controlInputDigest is the frozen input digest of a control-plane round: the
// canonical admission facts plus, per dependency, the round and deliverable
// identity the node evaluated. The upstream references are what makes staleness
// decidable by identity instead of by comparing per-node counters.
func (s *Service) controlInputDigest(
	ctx context.Context,
	admission Admission,
	nodeID string,
	iteration int,
) string {
	type upstreamRef struct {
		NodeID     string `json:"nodeId"`
		NodeRunID  string `json:"nodeRunId,omitempty"`
		ArtifactID string `json:"artifactId,omitempty"`
	}
	upstreams := []upstreamRef{}
	if node, known := s.plan.Nodes[nodeID]; known {
		if view, err := s.execution.RunView(ctx, admission.RunID); err == nil {
			// Every dependency gate and every declared input producer carries
			// the frozen upstream identity this round evaluated.
			identities := append([]string{}, node.DependsOn...)
			identities = append(identities, inputProducers(node)...)
			seen := map[string]bool{}
			for _, dependency := range identities {
				if seen[dependency] {
					continue
				}
				seen[dependency] = true
				reference := upstreamRef{NodeID: dependency}
				if round, found := s.roundOf(view, dependency); found {
					reference.NodeRunID = round.ID
					if ref, published, err := s.deliverableRef(ctx, admission, dependency, round.Iteration); err == nil && published {
						reference.ArtifactID = ref.ArtifactID
					}
				}
				upstreams = append(upstreams, reference)
			}
		}
	}
	document := struct {
		SchemaVersion      string        `json:"schemaVersion"`
		RunID              string        `json:"workflowRunId"`
		NodeID             string        `json:"nodeId"`
		Iteration          int           `json:"iteration"`
		DefinitionDigest   string        `json:"definitionDigest"`
		ProjectRepository  string        `json:"projectRepositoryId"`
		ProjectCommitOID   string        `json:"projectCommitOid"`
		KnowledgeCommitOID string        `json:"knowledgeCommitOid"`
		PolicyVersion      string        `json:"policyVersion"`
		Upstreams          []upstreamRef `json:"upstreams"`
	}{
		SchemaVersion: SchemaVersion, RunID: admission.RunID, NodeID: nodeID, Iteration: iteration,
		DefinitionDigest: admission.DefinitionDigest, ProjectRepository: admission.ProjectRepositoryID,
		ProjectCommitOID: admission.ProjectCommitOID, KnowledgeCommitOID: admission.KnowledgeCommitOID,
		PolicyVersion: admission.PolicyVersion, Upstreams: upstreams,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		// The document is a closed shape of strings and integers.
		panic(fmt.Sprintf("encode control node input: %v", err))
	}
	return artifact.Digest(encoded)
}

func isNotFound(err error) bool {
	return errorsIs(err, ErrNotFound)
}
