package execution

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.Mutex
	runtimes map[string]RuntimeRegistration
	runs     map[string]Run
	nodes    map[string]NodeRun
	tasks    map[string]Task
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runtimes: make(map[string]RuntimeRegistration),
		runs:     make(map[string]Run),
		nodes:    make(map[string]NodeRun),
		tasks:    make(map[string]Task),
	}
}

func (s *MemoryStore) RegisterRuntime(_ context.Context, registration RuntimeRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, exists := s.runtimes[registration.ID]; exists && existing.Epoch == registration.Epoch {
		if !existing.SameDeclaration(registration) {
			return ErrRuntimeConflict
		}
		return nil
	}
	registration.Capabilities = slices.Clone(registration.Capabilities)
	s.runtimes[registration.ID] = registration
	return nil
}

func (s *MemoryStore) CreateRun(_ context.Context, command CreateRunCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.runs[command.Run.ID]; exists {
		return ErrVersionConflict
	}
	for _, node := range command.Nodes {
		if node.RunID != command.Run.ID || node.ID == "" || node.Iteration < 1 || !digestPattern.MatchString(node.InputDigest) {
			return ErrInvalidInput
		}
		if _, exists := s.nodes[node.ID]; exists {
			return ErrVersionConflict
		}
	}
	for _, task := range command.Tasks {
		if _, exists := s.nodes[task.NodeRunID]; exists {
			return ErrVersionConflict
		}
		if !nodeIncluded(command.Nodes, task.NodeRunID) || task.ID == "" || task.State != TaskQueued || task.InputDigest == "" {
			return ErrInvalidInput
		}
	}
	s.runs[command.Run.ID] = command.Run
	for _, node := range command.Nodes {
		s.nodes[node.ID] = cloneNodeRun(node)
	}
	for _, task := range command.Tasks {
		s.tasks[task.ID] = task
	}
	return nil
}

func (s *MemoryStore) LeaseTask(_ context.Context, request LeaseRequest, now time.Time) (TaskLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runtime, exists := s.runtimes[request.RuntimeID]
	if !exists || runtime.Epoch != request.RuntimeEpoch {
		return TaskLease{}, ErrRuntimeConflict
	}
	taskIDs := make([]string, 0, len(s.tasks))
	for taskID := range s.tasks {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	for _, taskID := range taskIDs {
		task := s.tasks[taskID]
		if task.State != TaskQueued || !now.Before(task.DeadlineAt) || !runtimeSupports(runtime, task) {
			continue
		}
		node := s.nodes[task.NodeRunID]
		if !s.isCurrentNode(node) {
			continue
		}
		run := s.runs[node.RunID]
		if run.State != RunActive || (run.GitExecutionUserID != "" && run.GitExecutionUserID != runtime.GitHubUserID) {
			continue
		}
		if run.GitExecutionUserID == "" {
			run.GitExecutionUserID = runtime.GitHubUserID
			run.Version++
			run.UpdatedAt = now
			s.runs[run.ID] = run
		}
		task.RuntimeID = runtime.ID
		task.RuntimeEpoch = runtime.Epoch
		task.WorkflowRunID = run.ID
		task.GitExecutionUserID = runtime.GitHubUserID
		task.FencingToken++
		task.LeaseID = fmt.Sprintf("%s-lease-%d", task.ID, task.FencingToken)
		task.LeaseExpiresAt = now.Add(request.LeaseDuration)
		task.State = TaskLeased
		s.tasks[task.ID] = task
		node.State = NodeRunning
		node.Version++
		s.nodes[node.ID] = node
		return TaskLease{
			SchemaVersion: SchemaVersion, Kind: TaskLeaseKind,
			LeaseID: task.LeaseID, FencingToken: task.FencingToken,
			RuntimeEpoch: runtime.Epoch, ExpiresAt: task.LeaseExpiresAt, Task: taskSpec(task),
		}, nil
	}
	return TaskLease{}, ErrCapabilityUnavailable
}

func (s *MemoryStore) ReportProgress(_ context.Context, report ProgressReport, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, err := s.currentLease(report.TaskID, report.LeaseID, report.FencingToken, now)
	if err != nil {
		return err
	}
	if report.Sequence <= task.ProgressSeq {
		return nil
	}
	task.ProgressSeq = report.Sequence
	task.State = TaskRunning
	s.tasks[task.ID] = task
	return nil
}

func (s *MemoryStore) ReportResult(_ context.Context, report ResultReport, now time.Time) (ResultAcceptance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[report.TaskID]
	if !exists {
		return ResultAcceptance{}, ErrNotFound
	}
	// Accepted reports remain idempotent after expiry, under the same lease identity.
	if task.LeaseID != report.LeaseID || task.FencingToken != report.FencingToken {
		return ResultAcceptance{}, ErrStaleLease
	}
	if task.ResultDigest != "" {
		if task.ResultDigest != report.ResultDigest {
			return ResultAcceptance{}, ErrResultConflict
		}
		return ResultAcceptance{Duplicate: true}, nil
	}
	task, err := s.currentLease(report.TaskID, report.LeaseID, report.FencingToken, now)
	if err != nil {
		return ResultAcceptance{}, err
	}
	node := s.nodes[task.NodeRunID]
	task.ResultDigest = report.ResultDigest
	switch report.Status {
	case ResultCompleted:
		task.State = TaskCompleted
		node.State = NodeDecided
		node.Output = maps.Clone(report.Output)
		if verdict, ok := report.Output["verdict"].(string); ok {
			node.Verdict = verdict
		}
	case ResultFailed:
		task.State = TaskFailed
		node.State = NodeExecutionFailed
		run := s.runs[node.RunID]
		if run.State == RunActive {
			run.State = RunPaused
			run.Version++
			run.UpdatedAt = now
			s.runs[run.ID] = run
		}
	case ResultAwaitingInput:
		task.State = TaskAwaitingInput
		node.State = NodeWaitingHuman
	}
	node.Version++
	s.tasks[task.ID] = task
	s.nodes[node.ID] = node
	return ResultAcceptance{Applied: true}, nil
}

func (s *MemoryStore) RetryTask(_ context.Context, taskID, newTaskID string, now time.Time) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.tasks[taskID]
	if !exists {
		return Task{}, ErrNotFound
	}
	node := s.nodes[previous.NodeRunID]
	run := s.runs[node.RunID]
	cancellationRequested := run.State == RunCanceling || run.State == RunCanceled
	if cancellationRequested {
		return Task{}, ErrRetryNotAllowed
	}
	isRetryable := previous.State == TaskFailed || previous.State == TaskExpired
	if !isRetryable || !s.isCurrentNode(node) {
		return Task{}, ErrRetryNotAllowed
	}
	if _, exists := s.tasks[newTaskID]; exists {
		return Task{}, ErrVersionConflict
	}
	next := Task{
		ID: newTaskID, NodeRunID: previous.NodeRunID, Attempt: previous.Attempt + 1,
		AttemptKind: AttemptRetry, PreviousTaskID: previous.ID, Capability: previous.Capability,
		Provider: previous.Provider, GitScopes: slices.Clone(previous.GitScopes),
		State: TaskQueued, InputDigest: previous.InputDigest, DeadlineAt: previous.DeadlineAt,
	}
	s.tasks[next.ID] = next
	node.State = NodeReady
	node.Version++
	s.nodes[node.ID] = node
	run.State = RunActive
	run.Version++
	run.UpdatedAt = now
	s.runs[run.ID] = run
	return next, nil
}

func (s *MemoryStore) ResumeTask(
	_ context.Context,
	taskID string,
	newTaskID string,
	humanResponseID string,
	inputDigest string,
	resumeRef *ResumeRef,
	now time.Time,
) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.tasks[taskID]
	if !exists {
		return Task{}, ErrNotFound
	}
	node := s.nodes[previous.NodeRunID]
	run := s.runs[node.RunID]
	cancellationRequested := run.State == RunCanceling || run.State == RunCanceled
	if cancellationRequested {
		return Task{}, ErrRetryNotAllowed
	}
	if previous.State != TaskAwaitingInput || !s.isCurrentNode(node) {
		return Task{}, ErrRetryNotAllowed
	}
	if _, exists := s.tasks[newTaskID]; exists {
		return Task{}, ErrVersionConflict
	}
	next := Task{
		ID: newTaskID, NodeRunID: previous.NodeRunID, Attempt: previous.Attempt + 1,
		AttemptKind: AttemptResume, PreviousTaskID: previous.ID, HumanResponseID: humanResponseID,
		ResumeRef: cloneResumeRef(resumeRef), Capability: previous.Capability,
		Provider: previous.Provider, GitScopes: slices.Clone(previous.GitScopes),
		State: TaskQueued, InputDigest: inputDigest, DeadlineAt: previous.DeadlineAt,
	}
	s.tasks[next.ID] = next
	node.State = NodeReady
	node.InputDigest = inputDigest
	node.Version++
	s.nodes[node.ID] = node
	run.State = RunActive
	run.Version++
	run.UpdatedAt = now
	s.runs[run.ID] = run
	return next, nil
}

func (s *MemoryStore) RerunNode(
	_ context.Context,
	runID string,
	nodeID string,
	newNodeRunID string,
	newTaskID string,
	inputDigest string,
	maxIteration int,
	now time.Time,
) (NodeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[runID]
	if !exists {
		return NodeRun{}, ErrNotFound
	}
	cancellationRequested := run.State == RunCanceling || run.State == RunCanceled
	if cancellationRequested {
		return NodeRun{}, ErrRetryNotAllowed
	}
	var current NodeRun
	found := false
	for _, candidate := range s.nodes {
		if candidate.RunID == runID && candidate.NodeID == nodeID && (!found || candidate.Iteration > current.Iteration) {
			current = candidate
			found = true
		}
	}
	if !found {
		return NodeRun{}, ErrNotFound
	}
	if maxIteration > 0 && current.Iteration >= maxIteration {
		return NodeRun{}, ErrPolicyBlocked
	}
	if _, exists := s.nodes[newNodeRunID]; exists {
		return NodeRun{}, ErrVersionConflict
	}
	if _, exists := s.tasks[newTaskID]; exists {
		return NodeRun{}, ErrVersionConflict
	}
	var sourceTask Task
	for _, task := range s.tasks {
		if task.NodeRunID != current.ID {
			continue
		}
		// A new round cannot acquire authority while the current one is in flight.
		if task.State == TaskLeased || task.State == TaskRunning {
			return NodeRun{}, ErrRetryNotAllowed
		}
		if task.Attempt > sourceTask.Attempt {
			sourceTask = task
		}
	}
	for id, task := range s.tasks {
		if task.NodeRunID == current.ID && task.State == TaskQueued {
			task.State = TaskCanceled
			s.tasks[id] = task
		}
	}
	next := NodeRun{ID: newNodeRunID, RunID: runID, NodeID: nodeID, Iteration: current.Iteration + 1, State: NodeReady, InputDigest: inputDigest, Version: 1}
	s.nodes[next.ID] = next
	s.tasks[newTaskID] = Task{
		ID: newTaskID, NodeRunID: next.ID, Attempt: 1, AttemptKind: AttemptInitial,
		Capability: sourceTask.Capability, Provider: sourceTask.Provider,
		GitScopes: slices.Clone(sourceTask.GitScopes), State: TaskQueued,
		InputDigest: inputDigest, DeadlineAt: sourceTask.DeadlineAt,
	}
	run.State = RunActive
	run.Version++
	run.UpdatedAt = now
	s.runs[run.ID] = run
	return next, nil
}

func (s *MemoryStore) CancelRun(_ context.Context, runID string, expectedVersion int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[runID]
	if !exists {
		return ErrNotFound
	}
	if run.Version != expectedVersion {
		return ErrVersionConflict
	}
	hasActiveLease := false
	for taskID, task := range s.tasks {
		node := s.nodes[task.NodeRunID]
		if node.RunID != runID {
			continue
		}
		if task.State == TaskLeased || task.State == TaskRunning {
			hasActiveLease = true
			continue
		}
		if task.State == TaskQueued {
			task.State = TaskCanceled
			s.tasks[taskID] = task
		}
	}
	run.State = RunCanceled
	if hasActiveLease {
		run.State = RunCanceling
	}
	run.Version++
	run.UpdatedAt = now
	s.runs[run.ID] = run
	return nil
}

func (s *MemoryStore) RunView(_ context.Context, runID string) (RunView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[runID]
	if !exists {
		return RunView{}, ErrNotFound
	}
	current := make(map[string]NodeRun)
	for _, node := range s.nodes {
		if node.RunID != runID {
			continue
		}
		if existing, exists := current[node.NodeID]; !exists || node.Iteration > existing.Iteration {
			current[node.NodeID] = node
		}
	}
	nodeIDs := make([]string, 0, len(current))
	for nodeID := range current {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	views := make([]NodeView, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node := current[nodeID]
		views = append(views, NodeView{ID: node.ID, NodeID: node.NodeID, Iteration: node.Iteration, State: node.State, Verdict: node.Verdict, InputDigest: node.InputDigest, Output: maps.Clone(node.Output)})
	}
	return RunView{
		SchemaVersion: SchemaVersion, Kind: RunViewKind,
		ID: run.ID, WorkItemID: run.WorkItemID, StartedByGitHubUserID: run.StartedByGitHubUserID,
		GitExecutionUserID: run.GitExecutionUserID, DefinitionName: run.DefinitionName,
		DefinitionVersion: run.DefinitionVersion, DefinitionDigest: run.DefinitionDigest,
		State: run.State, Version: run.Version, Nodes: views,
	}, nil
}

func (s *MemoryStore) TaskStatus(_ context.Context, taskID string) (TaskStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, exists := s.tasks[taskID]
	if !exists {
		return TaskStatus{}, ErrNotFound
	}
	return TaskStatus{
		ID: task.ID, RunID: s.nodes[task.NodeRunID].RunID, State: task.State,
		InputDigest: task.InputDigest,
		RuntimeID:   task.RuntimeID, RuntimeEpoch: task.RuntimeEpoch,
		LeaseID: task.LeaseID, FencingToken: task.FencingToken,
		LeaseExpiresAt: task.LeaseExpiresAt, ResultAccepted: task.ResultDigest != "",
	}, nil
}

func (s *MemoryStore) RuntimeStatus(_ context.Context, runtimeID string) (RuntimeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	registration, exists := s.runtimes[runtimeID]
	if !exists {
		return RuntimeStatus{}, ErrNotFound
	}
	return RuntimeStatus{
		ID: registration.ID, Epoch: registration.Epoch, GitHubUserID: registration.GitHubUserID,
		Provider: registration.Provider, ProviderAuthenticated: registration.ProviderAuthenticated,
		Capabilities: slices.Clone(registration.Capabilities), RegisteredAt: registration.RegisteredAt,
	}, nil
}

// SweepExpiredLeases expires leased attempts whose persisted deadline has passed and
// finalizes CANCELING runs that no longer hold leased work. Heartbeats never renew
// leases, so this pass is the only automatic expiry transition; it never rewrites
// accepted results and never resumes queued tasks.
func (s *MemoryStore) SweepExpiredLeases(_ context.Context, now time.Time) (SweepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result SweepResult
	for id, task := range s.tasks {
		if (task.State == TaskLeased || task.State == TaskRunning) && !task.LeaseExpiresAt.IsZero() && !now.Before(task.LeaseExpiresAt) {
			task.State = TaskExpired
			s.tasks[id] = task
			result.ExpiredTasks++
		}
	}
	for id, run := range s.runs {
		if run.State != RunCanceling || s.runHasLeasedWork(run.ID) {
			continue
		}
		run.State = RunCanceled
		run.Version++
		run.UpdatedAt = now
		s.runs[id] = run
		result.FinalizedRuns++
	}
	return result, nil
}

// runHasLeasedWork is evaluated while the store mutex holds the snapshot.
func (s *MemoryStore) runHasLeasedWork(runID string) bool {
	for _, task := range s.tasks {
		if task.State != TaskLeased && task.State != TaskRunning {
			continue
		}
		if s.nodes[task.NodeRunID].RunID == runID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) currentLease(taskID, leaseID string, fencingToken int64, now time.Time) (Task, error) {
	task, exists := s.tasks[taskID]
	if !exists {
		return Task{}, ErrNotFound
	}
	validState := task.State == TaskLeased || task.State == TaskRunning
	// A new Runtime epoch replaces the dispatch generation, so leases granted under the old one are no longer current.
	identityMatches := task.LeaseID == leaseID && task.FencingToken == fencingToken &&
		s.runtimes[task.RuntimeID].Epoch == task.RuntimeEpoch
	expired := !now.Before(task.LeaseExpiresAt)
	if !validState || !identityMatches || expired {
		if validState && expired {
			task.State = TaskExpired
			s.tasks[task.ID] = task
		}
		return Task{}, ErrStaleLease
	}
	if !s.isCurrentNode(s.nodes[task.NodeRunID]) {
		return Task{}, ErrStaleLease
	}
	return task, nil
}

// isCurrentNode is evaluated while the store mutex holds the dispatch/report snapshot.
func (s *MemoryStore) isCurrentNode(node NodeRun) bool {
	for _, candidate := range s.nodes {
		if candidate.RunID == node.RunID && candidate.NodeID == node.NodeID && candidate.Iteration > node.Iteration {
			return false
		}
	}
	return true
}

func runtimeSupports(runtime RuntimeRegistration, task Task) bool {
	providerMatches := runtime.ProviderAuthenticated && runtime.Provider == task.Provider
	return providerMatches && slices.Contains(runtime.Capabilities, task.Capability)
}

func nodeIncluded(nodes []NodeRun, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func cloneNodeRun(node NodeRun) NodeRun {
	cloned := node
	cloned.Output = maps.Clone(node.Output)
	return cloned
}

func cloneResumeRef(value *ResumeRef) *ResumeRef {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func taskSpec(task Task) TaskSpec {
	return TaskSpec{
		ID: task.ID, WorkflowRunID: task.WorkflowRunID, NodeRunID: task.NodeRunID,
		Attempt: task.Attempt, AttemptKind: task.AttemptKind,
		PreviousTaskID: task.PreviousTaskID, HumanResponseID: task.HumanResponseID,
		ResumeRef: cloneResumeRef(task.ResumeRef), Capability: task.Capability,
		ProviderSelection: task.Provider, InputDigest: task.InputDigest,
		GitExecutionUserID: task.GitExecutionUserID, GitScopes: slices.Clone(task.GitScopes),
		DeadlineAt: task.DeadlineAt,
	}
}

var _ Store = (*MemoryStore)(nil)
