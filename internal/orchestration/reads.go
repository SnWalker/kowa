package orchestration

import (
	"context"
	"fmt"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/runnertransport"
	"github.com/SnWalker/kowa/internal/workitem"
)

// TaskInput resolves the frozen input bundle of one attempt. Reads are matched
// against the real attempt: a binding of another run, node or digest fails
// closed instead of serving unverified content, and a binding whose attempt was
// never materialised is not an executable fact.
func (s *Service) TaskInput(ctx context.Context, taskID string) (runnertransport.InputManifestResponse, error) {
	binding, err := s.store.Binding(ctx, taskID)
	if err != nil {
		return runnertransport.InputManifestResponse{}, err
	}
	status, err := s.execution.TaskStatus(ctx, taskID)
	if err != nil {
		return runnertransport.InputManifestResponse{}, err
	}
	if status.RunID != binding.RunID || status.InputDigest != binding.InputDigest {
		return runnertransport.InputManifestResponse{}, fmt.Errorf(
			"%w: the frozen binding belongs to another attempt", ErrPolicyBlocked)
	}
	run, err := s.execution.Run(ctx, binding.RunID)
	if err != nil {
		return runnertransport.InputManifestResponse{}, err
	}
	input, err := s.knowledge.ReadInput(ctx,
		artifact.Scope{WorkspaceID: binding.WorkspaceID, ActorID: run.StartedByGitHubUserID}, binding.InputID)
	if err != nil {
		return runnertransport.InputManifestResponse{}, err
	}
	if input.Digest != binding.InputDigest || artifact.Digest(input.Canonical) != binding.InputDigest {
		return runnertransport.InputManifestResponse{}, fmt.Errorf(
			"%w: the stored effective input no longer matches its frozen digest", ErrPolicyBlocked)
	}
	references, err := s.bindingArtifacts(ctx, binding)
	if err != nil {
		return runnertransport.InputManifestResponse{}, err
	}
	return runnertransport.InputManifestResponse{
		Bundle: input.Canonical, BundleDigest: input.Digest, Artifacts: references,
	}, nil
}

// TaskInputArtifact serves the raw frozen content of one referenced artifact.
// An artifact outside the binding is never exposed.
func (s *Service) TaskInputArtifact(ctx context.Context, taskID, artifactID string) ([]byte, error) {
	binding, err := s.store.Binding(ctx, taskID)
	if err != nil {
		return nil, err
	}
	references, err := s.bindingArtifacts(ctx, binding)
	if err != nil {
		return nil, err
	}
	run, err := s.execution.Run(ctx, binding.RunID)
	if err != nil {
		return nil, err
	}
	for _, reference := range references {
		if reference.ArtifactID != artifactID {
			continue
		}
		// The artifact module authorizes the read for the run's authenticated
		// initiator, never for a value taken from the wire.
		return s.artifacts.Read(ctx,
			artifact.Scope{WorkspaceID: binding.WorkspaceID, ActorID: run.StartedByGitHubUserID}, reference)
	}
	return nil, fmt.Errorf("%w: artifact %s is not referenced by this attempt", ErrForbidden, artifactID)
}

// bindingArtifacts resolves the frozen references of one binding from the
// artifact module's own records.
func (s *Service) bindingArtifacts(ctx context.Context, binding Binding) ([]artifact.Ref, error) {
	if len(binding.ArtifactIDs) == 0 {
		return nil, nil
	}
	records, err := s.store.ArtifactsForWorkItem(ctx, binding.WorkspaceID, workItemIDOf(ctx, s, binding.RunID))
	if err != nil {
		return nil, err
	}
	references := make([]artifact.Ref, 0, len(binding.ArtifactIDs))
	for _, artifactID := range binding.ArtifactIDs {
		for _, record := range records {
			if record.ArtifactID != artifactID {
				continue
			}
			references = append(references, artifact.Ref{
				SchemaVersion: artifact.SchemaVersion, ArtifactID: record.ArtifactID,
				WorkspaceID: binding.WorkspaceID, SubjectID: record.SubjectID, Kind: record.Kind,
				Producer: artifact.Producer{Kind: record.ProducerKind, ID: record.ProducerID},
				Digest:   record.Digest, Size: record.Size, MediaType: record.MediaType,
				StorageRef: "artifact:" + record.ArtifactID, RequiresApproval: record.RequiresApproval,
			})
			break
		}
	}
	if len(references) != len(binding.ArtifactIDs) {
		return nil, fmt.Errorf("%w: a frozen artifact reference no longer exists", ErrResourceUnavailable)
	}
	return references, nil
}

// workItemIDOf reads the work item of one run.
func workItemIDOf(ctx context.Context, s *Service, runID string) string {
	run, err := s.execution.Run(ctx, runID)
	if err != nil {
		return ""
	}
	return run.WorkItemID
}

// Watermark tells a reader when the server produced a projection and when the
// run it describes last changed, so a browser can tell "nothing new" from
// "stale" without inventing its own clock comparison.
type Watermark struct {
	ObservedAt   time.Time `json:"observedAt"`
	RunUpdatedAt time.Time `json:"runUpdatedAt"`
}

// NodeHistory is one materialised node round of a run.
type NodeHistory struct {
	NodeID      string         `json:"nodeId"`
	Type        string         `json:"type"`
	Iteration   int            `json:"iteration"`
	State       string         `json:"state"`
	Verdict     string         `json:"verdict,omitempty"`
	InputDigest string         `json:"inputDigest"`
	Output      map[string]any `json:"output,omitempty"`
	// Attempts are the execution attempts of this round, oldest first.
	Attempts []NodeAttempt `json:"attempts"`
}

// NodeAttempt is one persisted execution attempt of a node round. The execution
// module owns these facts; this is a read-only projection of them.
type NodeAttempt struct {
	TaskID       string `json:"taskId"`
	Attempt      int    `json:"attempt"`
	AttemptKind  string `json:"attemptKind"`
	State        string `json:"state"`
	InputDigest  string `json:"inputDigest"`
	ResultDigest string `json:"resultDigest,omitempty"`
	RuntimeID    string `json:"runtimeId,omitempty"`
	RuntimeEpoch string `json:"runtimeEpoch,omitempty"`
	FencingToken int64  `json:"fencingToken,omitempty"`
	LeaseID      string `json:"leaseId,omitempty"`
	// ArtifactIDs are the frozen artifacts this attempt consumed.
	ArtifactIDs []string `json:"artifactIds"`
}

// RunDetailView is the composite read of one delivery run: the frozen
// definition, every materialised round with its verdict, the repair lineage and
// the observation watermark. It is a projection only; no state lives here.
type RunDetailView struct {
	SchemaVersion       string                `json:"schemaVersion"`
	Kind                string                `json:"kind"`
	WorkItemID          string                `json:"workItemId"`
	WorkflowRunID       string                `json:"workflowRunId"`
	State               string                `json:"state"`
	Version             int64                 `json:"version"`
	DefinitionName      string                `json:"definitionName"`
	DefinitionVersion   int                   `json:"definitionVersion"`
	DefinitionDigest    string                `json:"definitionDigest"`
	StartedByUserID     string                `json:"startedByGitHubUserId"`
	MachineUserID       string                `json:"gitExecutionUserId,omitempty"`
	Nodes               []NodeHistory         `json:"nodes"`
	RunBindings         []workitem.RunBinding `json:"runBindings"`
	Knowledge           *Recommendation       `json:"knowledgeRecommendation,omitempty"`
	KnowledgeSuggestion *SuggestionView       `json:"knowledgeSuggestion,omitempty"`
	Watermark           Watermark             `json:"watermark"`
}

// SuggestionView is the read-only projection of the auxiliary
// knowledge-suggestion branch: the current summary plus every attempt, oldest
// first. The attempts are the audit trail; the summary is the newest one.
type SuggestionView struct {
	RunID         string                  `json:"workflowRunId"`
	State         string                  `json:"state"`
	Attempts      int                     `json:"attempts"`
	ArtifactID    string                  `json:"artifactId,omitempty"`
	FailureReason string                  `json:"failureReason,omitempty"`
	UpdatedAt     time.Time               `json:"updatedAt"`
	History       []SuggestionAttemptView `json:"history"`
}

// SuggestionAttemptView is one attempt of the auxiliary branch. A failure
// reason is reported as a short classification, never as raw internals.
type SuggestionAttemptView struct {
	AttemptID      string    `json:"attemptId"`
	Attempt        int       `json:"attempt"`
	State          string    `json:"state"`
	InputDigest    string    `json:"inputDigest"`
	ArtifactID     string    `json:"artifactId,omitempty"`
	ArtifactDigest string    `json:"artifactDigest,omitempty"`
	FailureReason  string    `json:"failureReason,omitempty"`
	RequestedAt    time.Time `json:"requestedAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// ActiveRunIDs lists the runs the reconciler still has to advance.
func (s *Service) ActiveRunIDs(ctx context.Context) ([]string, error) {
	return s.store.ActiveRuns(ctx)
}

// RunDetail reads one run of a work item: the frozen definition, every
// materialised round with its attempts, the repair lineage, the knowledge
// confirmation and the observation watermark.
func (s *Service) RunDetail(ctx context.Context, workspaceID, workItemID, runID string) (RunDetailView, error) {
	run, err := s.execution.Run(ctx, runID)
	if err != nil {
		return RunDetailView{}, err
	}
	if run.WorkspaceID != workspaceID || run.WorkItemID != workItemID {
		return RunDetailView{}, fmt.Errorf("%w: the run does not belong to this work item", ErrForbidden)
	}
	rounds, err := s.execution.RunRounds(ctx, runID)
	if err != nil {
		return RunDetailView{}, err
	}
	attempts, err := s.execution.RunAttempts(ctx, runID)
	if err != nil {
		return RunDetailView{}, err
	}
	bindings, err := s.workitems.RunBindings(ctx, workspaceID, workItemID)
	if err != nil {
		return RunDetailView{}, err
	}
	detail := RunDetailView{
		SchemaVersion: SchemaVersion, Kind: "runDetail", WorkItemID: workItemID, WorkflowRunID: run.ID,
		State: string(run.State), Version: run.Version, DefinitionName: run.DefinitionName,
		DefinitionVersion: run.DefinitionVersion, DefinitionDigest: run.DefinitionDigest,
		StartedByUserID: run.StartedByGitHubUserID, MachineUserID: run.GitExecutionUserID,
		RunBindings: bindings,
		Watermark:   Watermark{ObservedAt: s.now().UTC(), RunUpdatedAt: run.UpdatedAt.UTC()},
	}
	// A declared array is always an array: a browser never has to treat null as
	// an empty list.
	detail.Nodes = []NodeHistory{}
	if bindings == nil {
		detail.RunBindings = []workitem.RunBinding{}
	}
	for _, round := range rounds {
		node, known := s.plan.Nodes[round.NodeID]
		if !known {
			// A round the frozen plan cannot name is a server-side inconsistency:
			// the read fails closed instead of inventing a type the frozen
			// schema does not carry.
			return RunDetailView{}, fmt.Errorf(
				"the round %s names node %q, which is not in the frozen plan", round.NodeRunID, round.NodeID)
		}
		history := NodeHistory{
			NodeID: round.NodeID, Type: string(node.Type), Iteration: round.Iteration,
			State: string(round.State), Verdict: round.Verdict, InputDigest: round.InputDigest,
			Output: cloneOutput(round.Output), Attempts: []NodeAttempt{},
		}
		for _, attempt := range attempts {
			if attempt.NodeRunID != round.NodeRunID {
				continue
			}
			entry := NodeAttempt{
				TaskID: attempt.TaskID, Attempt: attempt.Attempt, AttemptKind: string(attempt.AttemptKind),
				State: string(attempt.State), InputDigest: attempt.InputDigest,
				ResultDigest: attempt.ResultDigest, RuntimeID: attempt.RuntimeID,
				RuntimeEpoch: attempt.RuntimeEpoch, FencingToken: attempt.FencingToken,
				LeaseID: attempt.LeaseID, ArtifactIDs: []string{},
			}
			if binding, err := s.store.Binding(ctx, attempt.TaskID); err == nil && binding.ArtifactIDs != nil {
				entry.ArtifactIDs = binding.ArtifactIDs
			}
			history.Attempts = append(history.Attempts, entry)
		}
		detail.Nodes = append(detail.Nodes, history)
	}
	if recommendation, err := s.store.Recommendation(ctx, runID); err == nil {
		detail.Knowledge = &recommendation
	} else if !isNotFound(err) {
		return RunDetailView{}, err
	}
	if suggestion, err := s.Suggestion(ctx, runID); err != nil {
		return RunDetailView{}, err
	} else if suggestion != nil {
		detail.KnowledgeSuggestion = suggestion
	}
	return detail, nil
}

// Suggestion projects the auxiliary knowledge-suggestion branch of one run:
// the current summary and every attempt, oldest first. It is nil when the run
// never materialised the branch.
func (s *Service) Suggestion(ctx context.Context, runID string) (*SuggestionView, error) {
	attempts, err := s.store.SuggestionAttempts(ctx, runID)
	if err != nil {
		return nil, err
	}
	if len(attempts) == 0 {
		return nil, nil
	}
	view := &SuggestionView{
		RunID: runID, Attempts: len(attempts), History: []SuggestionAttemptView{},
	}
	for _, attempt := range attempts {
		view.History = append(view.History, SuggestionAttemptView{
			AttemptID: attempt.AttemptID, Attempt: attempt.Attempt, State: string(attempt.State),
			InputDigest: attempt.InputDigest, ArtifactID: attempt.ArtifactID,
			ArtifactDigest: attempt.ArtifactDigest, FailureReason: attempt.FailureReason,
			RequestedAt: attempt.RequestedAt, UpdatedAt: attempt.UpdatedAt,
		})
	}
	newest := attempts[len(attempts)-1]
	view.State = string(newest.State)
	view.ArtifactID = newest.ArtifactID
	view.FailureReason = newest.FailureReason
	view.UpdatedAt = newest.UpdatedAt
	return view, nil
}

// ArtifactListView is the composite read of one work item's deliverables and
// its repair lineage.
type ArtifactListView struct {
	SchemaVersion string                `json:"schemaVersion"`
	Kind          string                `json:"kind"`
	WorkItemID    string                `json:"workItemId"`
	Artifacts     []ArtifactRecord      `json:"artifacts"`
	RunBindings   []workitem.RunBinding `json:"runBindings"`
}

// Artifacts reads the published deliverables of one work item.
func (s *Service) Artifacts(ctx context.Context, workspaceID, workItemID string) (ArtifactListView, error) {
	if _, err := s.workitems.Get(ctx, workspaceID, workItemID); err != nil {
		return ArtifactListView{}, err
	}
	records, err := s.store.ArtifactsForWorkItem(ctx, workspaceID, workItemID)
	if err != nil {
		return ArtifactListView{}, err
	}
	bindings, err := s.workitems.RunBindings(ctx, workspaceID, workItemID)
	if err != nil {
		return ArtifactListView{}, err
	}
	if records == nil {
		records = []ArtifactRecord{}
	}
	if bindings == nil {
		bindings = []workitem.RunBinding{}
	}
	return ArtifactListView{
		SchemaVersion: SchemaVersion, Kind: "artifactList", WorkItemID: workItemID,
		Artifacts: records, RunBindings: bindings,
	}, nil
}

// JourneyView is the composite read of one work item's whole delivery journey:
// the frozen definition, the current run, the open human tasks, the delivery
// state and the closure. It is a projection only.
type JourneyView struct {
	SchemaVersion    string                    `json:"schemaVersion"`
	Kind             string                    `json:"kind"`
	WorkItem         workitem.WorkItem         `json:"workItem"`
	DefinitionName   string                    `json:"definitionName"`
	DefinitionDigest string                    `json:"definitionDigest"`
	Run              *JourneyRun               `json:"run,omitempty"`
	OpenHumanTasks   []JourneyHumanTask        `json:"openHumanTasks"`
	Artifacts        int                       `json:"artifactCount"`
	Closure          *evaluation.ClosureRecord `json:"closure,omitempty"`
	// Watermark carries the read time and the last change of the current run,
	// so a browser can distinguish "nothing new" from "stale".
	Watermark Watermark `json:"watermark"`
}

// JourneyRun is the run summary of one journey.
type JourneyRun struct {
	WorkflowRunID string `json:"workflowRunId"`
	State         string `json:"state"`
	Version       int64  `json:"version"`
	DecidedNodes  int    `json:"decidedNodes"`
	TotalNodes    int    `json:"totalNodes"`
}

// JourneyHumanTask is one open human task of a journey.
type JourneyHumanTask struct {
	HumanTaskID string `json:"humanTaskId"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Mode        string `json:"completionMode"`
	State       string `json:"state"`
	Version     int64  `json:"version"`
}

// Journey reads the composite journey view of one work item.
func (s *Service) Journey(ctx context.Context, workspaceID, workItemID string) (JourneyView, error) {
	item, err := s.workitems.Get(ctx, workspaceID, workItemID)
	if err != nil {
		return JourneyView{}, err
	}
	journey := JourneyView{
		SchemaVersion: SchemaVersion, Kind: "journey", WorkItem: item,
		DefinitionName: s.plan.Name, DefinitionDigest: s.plan.Digest,
		OpenHumanTasks: []JourneyHumanTask{},
		Watermark:      Watermark{ObservedAt: s.now().UTC()},
	}
	if item.CurrentRunID != "" {
		run, err := s.execution.Run(ctx, item.CurrentRunID)
		if err != nil && !isNotFound(err) {
			return JourneyView{}, err
		}
		if err == nil {
			view, err := s.execution.RunView(ctx, run.ID)
			if err != nil {
				return JourneyView{}, err
			}
			summary := JourneyRun{
				WorkflowRunID: run.ID, State: string(run.State), Version: run.Version,
				TotalNodes: len(s.plan.Order),
			}
			for _, node := range view.Nodes {
				if node.State == execution.NodeDecided {
					summary.DecidedNodes++
				}
			}
			journey.Run = &summary
			journey.Watermark.RunUpdatedAt = run.UpdatedAt.UTC()
		}
	}
	tasks, err := s.humans.List(ctx, workspaceID, true)
	if err != nil {
		return JourneyView{}, err
	}
	for _, task := range tasks {
		if task.WorkItemID != workItemID {
			continue
		}
		journey.OpenHumanTasks = append(journey.OpenHumanTasks, JourneyHumanTask{
			HumanTaskID: task.ID, Kind: task.Request.Kind, Title: task.Request.Title,
			Mode: string(task.Request.Mode), State: string(task.State), Version: task.Version,
		})
	}
	records, err := s.store.ArtifactsForWorkItem(ctx, workspaceID, workItemID)
	if err != nil {
		return JourneyView{}, err
	}
	journey.Artifacts = len(records)
	if closure, found, err := s.evaluation.Closure(ctx, workspaceID, workItemID); err != nil {
		return JourneyView{}, err
	} else if found {
		journey.Closure = &closure
	}
	return journey, nil
}

var _ = fixedflow.DefinitionName
