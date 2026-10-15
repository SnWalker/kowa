package orchestration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/knowledge"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workitem"
)

// TestKnowledgeGatesTheFixedFlowAndBindingsPrecedeDispatch pins the two
// invariants an admission must never break: no capability attempt exists while
// the knowledge binding is unconfirmed, and an attempt's frozen input binding
// is committed before the attempt exists.
func TestKnowledgeGatesTheFixedFlowAndBindingsPrecedeDispatch(t *testing.T) {
	f := newAdvanceFixture(t)
	item := f.createWorkItem(t)

	result, err := f.service.Admit(t.Context(), orchestration.AdmitCommand{
		WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
		RequestKey: "admit-1", ExpectedVersion: item.Version, Kind: workitem.BindingInitial,
	})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if result.Recommendation == nil || result.Recommendation.HumanTaskID == "" {
		t.Fatalf("admission produced no confirmation request: %+v", result.Recommendation)
	}
	// No attempt and no binding may exist before the confirmation.
	f.expectNoAttempt(t, "a capability attempt exists before the knowledge confirmation")
	if bindings := f.bindingCount(t); bindings != 0 {
		t.Fatalf("%d frozen bindings exist before the knowledge confirmation", bindings)
	}

	f.answer(t, result.Recommendation.HumanTaskID, "approved", map[string]string{
		"selection": knowledgeEntryPath,
	})
	if err := f.service.Advance(t.Context(), result.Admission.RunID); err != nil {
		t.Fatalf("advance after the confirmation: %v", err)
	}

	// The plan attempt now exists, and its frozen binding was committed first.
	lease := f.lease(t)
	if lease.Task.Capability.ID != "plan.create" {
		t.Fatalf("the first dispatched capability is %s", lease.Task.Capability.ID)
	}
	binding, err := f.store.Binding(t.Context(), lease.Task.ID)
	if err != nil {
		t.Fatalf("the dispatched attempt has no frozen binding: %v", err)
	}
	if binding.InputDigest != lease.Task.InputDigest {
		t.Fatalf("binding digest %s differs from the attempt digest %s",
			binding.InputDigest, lease.Task.InputDigest)
	}
	var projectRead, knowledgeRead bool
	for _, scope := range lease.Task.GitScopes {
		if scope.Operation != "clone" {
			t.Fatalf("the plan attempt carries %s, which no plan input needs", scope.Operation)
		}
		if scope.Role == "project" && scope.Ref == binding.ProjectCommitOID {
			projectRead = true
		}
		if scope.Role == "knowledge" && scope.Ref == binding.KnowledgeCommitOID {
			knowledgeRead = true
		}
	}
	if !projectRead || !knowledgeRead {
		t.Fatalf("plan read scopes are not the frozen commit oids: %+v", lease.Task.GitScopes)
	}
	for _, scope := range lease.Task.GitScopes {
		if len(scope.Ref) != 40 {
			t.Fatalf("read scope %s is not a frozen oid", scope.Ref)
		}
	}

	// A further pass reuses the frozen identity: the binding is never
	// duplicated, whatever the pass decides to do about the attempt.
	before := f.bindingCount(t)
	if err := f.service.Advance(t.Context(), result.Admission.RunID); err != nil {
		t.Fatalf("advance over an existing attempt: %v", err)
	}
	if after := f.bindingCount(t); after != before {
		t.Fatalf("a later pass created a second binding: %d -> %d", before, after)
	}
}

// TestAFreshControlPlaneContinuesTheSameRun pins the recovery claim: after a
// restart a new control plane resumes the same run from persisted facts,
// reusing the frozen attempt identity instead of starting a second delivery.
func TestAFreshControlPlaneContinuesTheSameRun(t *testing.T) {
	f := newAdvanceFixture(t)
	item := f.createWorkItem(t)
	result, err := f.service.Admit(t.Context(), orchestration.AdmitCommand{
		WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
		RequestKey: "admit-restart", ExpectedVersion: item.Version, Kind: workitem.BindingInitial,
	})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	f.answer(t, result.Recommendation.HumanTaskID, "approved", map[string]string{
		"selection": knowledgeEntryPath,
	})
	if err := f.service.Advance(t.Context(), result.Admission.RunID); err != nil {
		t.Fatalf("advance before the restart: %v", err)
	}
	before := f.bindingCount(t)

	f.restart(t)
	// The restarted control plane applies the pending decision and dispatches
	// the next node, and it never materialises a second binding for the round
	// that was already frozen.
	f.completeCapability(t, result.Admission.RunID, "plan.create")
	if after := f.bindingCount(t); after <= before {
		t.Fatalf("the restarted control plane did not continue the run: %d -> %d", before, after)
	}
}

// TestRefusedHumanGateBlocksDownstream pins that a human refusal of the plan
// approval never lets the coding node materialise.
func TestRefusedHumanGateBlocksDownstream(t *testing.T) {
	f := newAdvanceFixture(t)
	item := f.createWorkItem(t)
	result, err := f.service.Admit(t.Context(), orchestration.AdmitCommand{
		WorkspaceID: f.workspaceID, WorkItemID: item.ID, ActorID: f.actorID,
		RequestKey: "admit-2", ExpectedVersion: item.Version, Kind: workitem.BindingInitial,
	})
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	f.answer(t, result.Recommendation.HumanTaskID, "approved", map[string]string{
		"selection": knowledgeEntryPath,
	})
	// Plan runs, then the review approves it, and the human refuses the plan.
	f.completeCapability(t, result.Admission.RunID, "plan.create")
	f.completeCapability(t, result.Admission.RunID, "plan.review")
	approval := f.openHumanTask(t, result.Admission.RunID, fixedflow.HumanTaskPlanApproval)
	f.answer(t, approval, "rejected", map[string]string{"feedback": "not this direction"})

	// The refused gate must not materialise the coding node.
	f.expectNoCapability(t, result.Admission.RunID, "code.implement",
		"the coding node materialised although the plan approval was refused")
}

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

type advanceFixture struct {
	t           *testing.T
	options     orchestration.Options
	service     *orchestration.Service
	store       *orchestration.MemoryStore
	counter     *countingExecution
	execution   *execution.Service
	execStore   *execution.MemoryStore
	humans      *humantask.Service
	workitems   *workitem.Service
	workspaceID string
	actorID     string
	now         time.Time
	// clock is the mutable clock behind every service of the fixture: a test
	// may move time forward to probe deadlines.
	clock *fixtureClock
	// recoveryCount gives every recovery command its own request key.
	recoveryCount int
}

func newAdvanceFixture(t *testing.T) *advanceFixture {
	t.Helper()
	clock := &fixtureClock{at: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	now := clock.at
	store := orchestration.NewMemoryStore()
	execStore := execution.NewMemoryStore()
	executionService := execution.NewService(execStore, clock.Now)
	plan, err := fixedflow.Compile()
	if err != nil {
		t.Fatalf("compile the fixed flow: %v", err)
	}
	if err := execStore.PublishPlan(t.Context(), plan); err != nil {
		t.Fatalf("publish the fixed plan: %v", err)
	}
	if err := executionService.RegisterRuntime(t.Context(), execution.RuntimeRegistration{
		ID: "runtime-journey", Epoch: "epoch-1", GitHubUserID: "9001",
		Provider:              execution.ProviderSelection{ID: "scripted", Version: "scripted-1"},
		ProviderAuthenticated: true,
		Capabilities:          capabilityList(),
	}); err != nil {
		t.Fatalf("register the runtime: %v", err)
	}
	workspaceID := "orchestration-workspace"
	ids := &fixedIDs{}
	workItemStore := workitem.NewMemoryStore()
	workItems := workitem.NewService(workItemStore, fixedProject{}, executionRunSettled{executionService},
		noExternalOperations{}, &moduleIDs{}, clock.Now)
	humanStore := humantask.NewMemoryStore()
	humans := humantask.NewService(humanStore, noObservations{}, workItemExists{workItems},
		&moduleIDs{}, clock.Now)
	knowledgeStore := newMemoryKnowledge(t)
	blobs, err := artifact.NewLocalStore(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	catalog := artifact.NewMemoryCatalog()
	// The catalog authorizes by explicit membership; the fixture grants the
	// admission initiator and the answering member.
	catalog.Grant(workspaceID, "7001", true)
	artifacts := artifact.NewService(catalog, blobs, clock.Now)
	// The in-memory orchestration store projects the deliverables the artifact
	// module actually published.
	store.UseArtifacts(artifactDeliverables{catalog: catalog})
	counter := &countingExecution{inner: executionService}
	options := orchestration.Options{
		Store: store, Execution: counter,
		Knowledge: knowledgeStore, Artifacts: artifacts,
		WorkItems: workItems, HumanTasks: humans,
		Runtimes: fixedRuntimes{},
		Bindings: fixedBindings{project: "9001", knowledge: "9002"},
		Access:   allowMember{},
		Materializer: &scriptedMaterializer{
			t: t,
			commits: map[string]string{
				"9001": repeat("1", 40),
				"9002": repeat("2", 40),
			},
			entries: map[string]map[string]string{
				"9002": {"kowa-index.json": knowledgeIndex(), knowledgeEntryPath: knowledgeEntryContent},
			},
		},
		IDs: ids, Now: clock.Now,
	}
	service, err := orchestration.NewService(options)
	if err != nil {
		t.Fatalf("assemble the orchestration service: %v", err)
	}
	return &advanceFixture{
		t: t, options: options, service: service, store: store, execution: executionService,
		counter: counter, execStore: execStore,
		humans: humans, workitems: workItems, workspaceID: workspaceID, actorID: "7001", now: now,
		clock: clock,
	}
}

const (
	knowledgeEntryPath    = "docs/design.md"
	knowledgeEntryContent = "The project uses small deterministic helpers.\n"
)

// knowledgeIndex is the frozen index the recommendation reads; the entry it
// declares is materialised beside it.
func knowledgeIndex() string {
	document := map[string]any{
		"schemaVersion": "kowa.web-application.v1",
		"entries": []map[string]any{{
			"path": knowledgeEntryPath, "digest": artifact.Digest([]byte(knowledgeEntryContent)),
			"scope": "design", "required": true,
		}},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// countingExecution observes dispatch attempts: a pass that merely re-reads
// existing facts must not keep dispatching.
type countingExecution struct {
	inner    orchestration.ExecutionPort
	mu       sync.Mutex
	dispatch int
}

func (c *countingExecution) Run(ctx context.Context, runID string) (execution.Run, error) {
	return c.inner.Run(ctx, runID)
}
func (c *countingExecution) RunView(ctx context.Context, runID string) (execution.RunView, error) {
	return c.inner.RunView(ctx, runID)
}
func (c *countingExecution) TaskStatus(ctx context.Context, taskID string) (execution.TaskStatus, error) {
	return c.inner.TaskStatus(ctx, taskID)
}
func (c *countingExecution) CreateRun(ctx context.Context, command execution.CreateRunCommand) error {
	return c.inner.CreateRun(ctx, command)
}
func (c *countingExecution) DispatchNode(
	ctx context.Context,
	command execution.DispatchNodeCommand,
) (execution.DispatchOutcome, error) {
	c.mu.Lock()
	c.dispatch++
	c.mu.Unlock()
	return c.inner.DispatchNode(ctx, command)
}
func (c *countingExecution) OpenNodeRound(ctx context.Context, command execution.OpenRoundCommand) (execution.NodeRun, error) {
	return c.inner.OpenNodeRound(ctx, command)
}
func (c *countingExecution) DecideNode(
	ctx context.Context,
	command execution.DecideNodeCommand,
) (execution.DecideOutcome, error) {
	return c.inner.DecideNode(ctx, command)
}
func (c *countingExecution) RunAttempts(ctx context.Context, runID string) ([]execution.NodeAttemptRef, error) {
	return c.inner.RunAttempts(ctx, runID)
}
func (c *countingExecution) RunRounds(ctx context.Context, runID string) ([]execution.NodeRoundRef, error) {
	return c.inner.RunRounds(ctx, runID)
}
func (c *countingExecution) RetryTask(ctx context.Context, taskID, newTaskID string) (execution.Task, error) {
	return c.inner.RetryTask(ctx, taskID, newTaskID)
}
func (c *countingExecution) RetryAttempt(ctx context.Context, command execution.RetryAttemptCommand) (execution.Task, error) {
	return c.inner.RetryAttempt(ctx, command)
}
func (c *countingExecution) CancelRun(ctx context.Context, runID string, expectedVersion int64) error {
	return c.inner.CancelRun(ctx, runID, expectedVersion)
}

func (c *countingExecution) dispatchAttempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dispatch
}

func (c *countingExecution) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dispatch = 0
}

// restart replaces the service with a new instance over the same stores: from
// the persisted facts' point of view the control plane process restarted.
func (f *advanceFixture) restart(t *testing.T) {
	t.Helper()
	service, err := orchestration.NewService(f.options)
	if err != nil {
		t.Fatalf("reassemble the orchestration service: %v", err)
	}
	f.service = service
}

func (f *advanceFixture) createWorkItem(t *testing.T) workitem.WorkItem {
	t.Helper()
	item, err := f.workitems.Create(t.Context(), workitem.Actor{GitHubUserID: f.actorID}, workitem.CreateCommand{
		WorkspaceID: f.workspaceID, RequestKey: "intake-1", Title: "Add a deterministic greeting helper",
		Requirement: workitem.VersionedContent{Version: 1, Content: "add a helper"},
		Acceptance:  workitem.VersionedContent{Version: 1, Content: "a passing test"},
	})
	if err != nil {
		t.Fatalf("create the work item: %v", err)
	}
	return item
}

func (f *advanceFixture) answer(t *testing.T, taskID, decision string, values map[string]string) {
	t.Helper()
	task, err := f.humans.Get(t.Context(), f.workspaceID, taskID)
	if err != nil {
		t.Fatalf("read the human task: %v", err)
	}
	submitted := map[string]string{}
	for name, value := range values {
		submitted[name] = value
	}
	for _, field := range task.Request.Fields {
		if field.Name == "decision" {
			submitted["decision"] = decision
		}
	}
	if _, err := f.humans.Answer(t.Context(),
		humantask.Actor{GitHubUserID: f.actorID, Roles: []humantask.Role{humantask.RoleAdmin}},
		humantask.AnswerCommand{
			WorkspaceID: f.workspaceID, TaskID: taskID, RequestKey: "answer-" + taskID,
			RequestVersion: task.Request.Version, Object: task.Request.Object,
			Decision: decision, Values: submitted,
		}); err != nil {
		t.Fatalf("answer the human task: %v", err)
	}
}

func (f *advanceFixture) openHumanTask(t *testing.T, runID, kind string) string {
	t.Helper()
	tasks, err := f.humans.List(t.Context(), f.workspaceID, true)
	if err != nil {
		t.Fatalf("list the human tasks: %v", err)
	}
	for _, task := range tasks {
		if task.Request.Kind == kind {
			return task.ID
		}
	}
	t.Fatalf("no open %s task exists; run %s", kind, runID)
	return ""
}

func (f *advanceFixture) lease(t *testing.T) execution.TaskLease {
	t.Helper()
	lease, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	return lease
}

// completeCapability leases the named capability, reports a passing outcome and
// lets the reconciler apply it.
func (f *advanceFixture) completeCapability(t *testing.T, runID, capability string) {
	t.Helper()
	for step := 0; step < 40; step++ {
		if err := f.service.Advance(t.Context(), runID); err != nil {
			t.Fatalf("advance: %v", err)
		}
		lease, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil {
			// Nothing is leasable in this pass: the reconciler may still be
			// publishing a deliverable or recording a decision. Try again.
			continue
		}
		if lease.Task.Capability.ID != capability {
			t.Fatalf("leased %s while waiting for %s", lease.Task.Capability.ID, capability)
		}
		output := map[string]any{"verdict": "approved", "summary": "scripted"}
		if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
			SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
			TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
			Status: execution.ResultCompleted, ResultDigest: artifact.Digest([]byte(capability)),
			Summary: "scripted", Output: output,
		}); err != nil {
			t.Fatalf("report the %s attempt: %v", capability, err)
		}
		if err := f.service.Advance(t.Context(), runID); err != nil {
			t.Fatalf("advance after %s: %v", capability, err)
		}
		return
	}
	view, err := f.execution.RunView(t.Context(), runID)
	if err != nil {
		t.Fatalf("%s never became leasable and the run is unreadable: %v", capability, err)
	}
	t.Fatalf("%s never became leasable; run state %s nodes %+v", capability, view.State, view.Nodes)
}

func (f *advanceFixture) expectNoAttempt(t *testing.T, message string) {
	t.Helper()
	if _, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	}); err == nil {
		t.Fatal(message)
	}
}

// expectNoCapability drives the reconciler while its gates are unsatisfied and
// proves the forbidden capability never becomes an attempt. Attempts of other
// capabilities are completed, so the flow really does keep running: a bypass
// would have to show up as the forbidden capability itself.
func (f *advanceFixture) expectNoCapability(t *testing.T, runID, forbidden, message string) {
	t.Helper()
	for step := 0; step < 40; step++ {
		if err := f.service.Advance(t.Context(), runID); err != nil {
			t.Fatalf("advance: %v", err)
		}
		lease, err := f.execution.LeaseTask(t.Context(), execution.LeaseRequest{
			RuntimeID: "runtime-journey", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
		})
		if err != nil {
			continue
		}
		if lease.Task.Capability.ID == forbidden {
			t.Fatal(message)
		}
		if _, err := f.execution.ReportResult(t.Context(), execution.ResultReport{
			SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
			TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
			Status: execution.ResultCompleted, ResultDigest: artifact.Digest([]byte(lease.Task.Capability.ID)),
			Summary: "scripted", Output: map[string]any{"verdict": "approved", "summary": "scripted"},
		}); err != nil {
			t.Fatalf("report %s: %v", lease.Task.Capability.ID, err)
		}
	}
}

func (f *advanceFixture) bindingCount(t *testing.T) int {
	t.Helper()
	identifiers, err := f.store.ActiveRuns(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, runID := range identifiers {
		view, err := f.execution.RunView(t.Context(), runID)
		if err != nil {
			continue
		}
		for _, node := range view.Nodes {
			for iteration := 1; iteration <= 4; iteration++ {
				taskID := runID + ":task:" + node.NodeID + ":" + strconv.Itoa(iteration)
				if _, err := f.store.Binding(t.Context(), taskID); err == nil {
					count++
				}
			}
		}
	}
	return count
}

func capabilityList() []execution.Capability {
	capabilities := []execution.Capability{}
	for _, capability := range fixedflow.Capabilities() {
		capabilities = append(capabilities, execution.Capability{
			ID: capability.Ref.ID, Version: capability.Ref.Version,
		})
	}
	return capabilities
}

// artifactDeliverables adapts the artifact module's test catalog to the
// orchestration read the durable store performs with its own SQL.
type artifactDeliverables struct{ catalog *artifact.MemoryCatalog }

func (a artifactDeliverables) ArtifactsForWorkItem(
	_ context.Context,
	workspaceID, workItemID string,
) ([]orchestration.ArtifactRecord, error) {
	records := a.catalog.Deliverables(workspaceID, workItemID)
	projected := make([]orchestration.ArtifactRecord, 0, len(records))
	for _, record := range records {
		if record.State != artifact.Published {
			continue
		}
		projected = append(projected, orchestration.ArtifactRecord{
			ArtifactID: record.Ref.ArtifactID, SubjectID: record.Ref.SubjectID, Kind: record.Ref.Kind,
			Digest: record.Ref.Digest, Size: record.Ref.Size, MediaType: record.Ref.MediaType,
			ProducerKind: record.Ref.Producer.Kind, ProducerID: record.Ref.Producer.ID,
			RequiresApproval: record.Ref.RequiresApproval, State: string(record.State),
			CreatedAt: record.CreatedAt,
		})
	}
	return projected, nil
}

// ---- fakes of the neighbouring modules -------------------------------------

// fixedIDs is a deterministic identity source: the fixture never depends on
// randomness to name a run, a work item or a human task.
// fixtureClock is a movable clock shared by every service the fixture builds.
type fixtureClock struct {
	at time.Time
}

func (c *fixtureClock) Now() time.Time { return c.at }

type fixedIDs struct {
	mu      sync.Mutex
	counter int
}

func (f *fixedIDs) New(prefix string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counter++
	return prefix + "-" + strconv.Itoa(f.counter), nil
}

// moduleIDs is the no-argument identity source the WorkItem and HumanTask
// services consume.
type moduleIDs struct {
	mu      sync.Mutex
	counter int
}

func (m *moduleIDs) New() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counter++
	return "id-" + strconv.Itoa(m.counter), nil
}

type fixedProject struct{}

func (fixedProject) ProjectRepositoryID(context.Context, string) (string, error) { return "9001", nil }

type executionRunSettled struct{ service *execution.Service }

func (a executionRunSettled) RunSettled(ctx context.Context, runID string) (bool, error) {
	run, err := a.service.Run(ctx, runID)
	if err != nil {
		return false, err
	}
	return run.State.Settled(), nil
}

type noExternalOperations struct{}

func (noExternalOperations) UnsettledOperations(context.Context, string, string) (int, error) {
	return 0, nil
}

type workItemExists struct{ service *workitem.Service }

func (w workItemExists) WorkItemExists(ctx context.Context, workspaceID, workItemID string) (bool, error) {
	if _, err := w.service.Get(ctx, workspaceID, workItemID); err != nil {
		return false, nil
	}
	return true, nil
}

type noObservations struct{}

func (noObservations) FactMatches(context.Context, string, string, humantask.ObjectBinding) (bool, error) {
	return false, nil
}

type fixedBindings struct{ project, knowledge string }

func (f fixedBindings) Repositories(context.Context, string, string) (string, string, error) {
	return f.project, f.knowledge, nil
}

type allowMember struct{}

func (allowMember) Authorize(context.Context, string, string, []string) error { return nil }

type fixedRuntimes struct{}

func (fixedRuntimes) ExecutionConfig(context.Context, string) (runtimeops.ExecutionConfig, error) {
	capabilities := []runtimeops.Capability{}
	for _, capability := range fixedflow.Capabilities() {
		capabilities = append(capabilities, runtimeops.Capability{
			ID: capability.Ref.ID, Version: capability.Ref.Version,
		})
	}
	return runtimeops.ExecutionConfig{
		WorkspaceID: "orchestration-workspace", Version: 1,
		Capabilities: capabilities, Runtimes: []string{"runtime-journey"},
	}, nil
}

func (fixedRuntimes) WorkspaceRuntimes(context.Context, string) ([]runtimeops.RuntimeView, error) {
	capabilities := []runtimeops.Capability{}
	for _, capability := range fixedflow.Capabilities() {
		capabilities = append(capabilities, runtimeops.Capability{
			ID: capability.Ref.ID, Version: capability.Ref.Version,
		})
	}
	return []runtimeops.RuntimeView{{
		RuntimeID: "runtime-journey", GitHubUserID: "9001",
		ProviderID: "scripted", ProviderVersion: "scripted-1", ProviderAuthenticated: true,
		Capabilities: capabilities, Freshness: runtimeops.FreshnessFresh,
	}}, nil
}

// scriptedMaterializer resolves one frozen commit per repository from a local
// directory; it never falls back to another revision.
type scriptedMaterializer struct {
	t       *testing.T
	commits map[string]string
	entries map[string]map[string]string
	roots   map[string]string
}

func (m *scriptedMaterializer) BranchHead(_ context.Context, repositoryID, _ string) (string, error) {
	commit, known := m.commits[repositoryID]
	if !known {
		return "", os.ErrNotExist
	}
	return commit, nil
}

func (m *scriptedMaterializer) Open(_ context.Context, repositoryID, commit string) (knowledge.Repository, error) {
	frozen, known := m.commits[repositoryID]
	if !known || commit != frozen {
		return nil, os.ErrNotExist
	}
	if m.roots == nil {
		m.roots = map[string]string{}
	}
	root, materialized := m.roots[repositoryID]
	if !materialized {
		root = m.t.TempDir()
		for relative, content := range m.entries[repositoryID] {
			path := filepath.Join(root, relative)
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return nil, err
			}
		}
		m.roots[repositoryID] = root
	}
	source, err := knowledge.NewDirectorySource(root, repositoryID, commit)
	if err != nil {
		return nil, err
	}
	return directoryRepository{source}, nil
}

type directoryRepository struct{ *knowledge.DirectorySource }

func (d directoryRepository) Source() knowledge.Source { return d.DirectorySource }

// memoryKnowledge freezes content with the module's own domain functions, so
// the fixture cannot teach the orchestrator a private dialect.
type memoryKnowledge struct {
	t         *testing.T
	snapshots map[string]knowledge.Snapshot
	events    map[string]knowledge.BindingEvent
	inputs    map[string]knowledge.EffectiveInput
	current   map[string]string
}

func newMemoryKnowledge(t *testing.T) *memoryKnowledge {
	return &memoryKnowledge{
		t: t, snapshots: map[string]knowledge.Snapshot{}, events: map[string]knowledge.BindingEvent{},
		inputs: map[string]knowledge.EffectiveInput{}, current: map[string]string{},
	}
}

func (m *memoryKnowledge) PublishSnapshot(_ context.Context, _ artifact.Scope, snapshot knowledge.Snapshot) error {
	if err := knowledge.ValidateSnapshot(snapshot); err != nil {
		return err
	}
	m.snapshots[snapshot.ID] = snapshot
	return nil
}

func (m *memoryKnowledge) Confirm(
	_ context.Context,
	scope artifact.Scope,
	command knowledge.ConfirmCommand,
) (knowledge.BindingEvent, error) {
	if err := knowledge.ValidateSnapshot(command.Snapshot); err != nil {
		return knowledge.BindingEvent{}, err
	}
	m.snapshots[command.Snapshot.ID] = command.Snapshot
	event := knowledge.BindingEvent{
		ID: "kb-" + command.HumanResponseID, WorkspaceID: scope.WorkspaceID, SubjectID: command.SubjectID,
		SnapshotID: command.Snapshot.ID, HumanResponseID: command.HumanResponseID,
		ActorID: scope.ActorID, Version: 1, ConfirmedAt: time.Now().UTC(),
	}
	m.events[event.ID] = event
	m.current[command.SubjectID] = event.ID
	return event, nil
}

func (m *memoryKnowledge) AssembleInput(
	_ context.Context,
	scope artifact.Scope,
	request knowledge.InputRequest,
	bindingEventID string,
) (knowledge.EffectiveInput, error) {
	event, found := m.events[bindingEventID]
	if !found || m.current[request.SubjectID] != bindingEventID {
		return knowledge.EffectiveInput{}, knowledge.ErrUnconfirmed
	}
	snapshot, found := m.snapshots[event.SnapshotID]
	if !found {
		return knowledge.EffectiveInput{}, artifact.ErrUnavailable
	}
	input, err := knowledge.Assemble(request, snapshot, event)
	if err != nil {
		return knowledge.EffectiveInput{}, err
	}
	// The assembled input is persisted, exactly like the durable adapter stores
	// it, so the frozen-input read can serve it back.
	m.inputs[input.ID] = input
	return input, nil
}

func (m *memoryKnowledge) ReadInput(_ context.Context, _ artifact.Scope, inputID string) (knowledge.EffectiveInput, error) {
	input, found := m.inputs[inputID]
	if !found {
		return knowledge.EffectiveInput{}, artifact.ErrUnavailable
	}
	return input, nil
}
