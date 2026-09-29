// Package execution owns durable runs, node rounds, task attempts, runtimes, and leases.
package execution

import (
	"errors"
	"time"
)

const (
	SchemaVersion           = "kowa.workflow-execution.v1"
	RuntimeRegistrationKind = "runtimeRegistration"
	TaskLeaseKind           = "taskLease"
	ProgressEventKind       = "progressEvent"
	TaskResultKind          = "taskResult"
	RunViewKind             = "runView"
)

var (
	ErrInvalidInput          = errors.New("execution: invalid input")
	ErrNotFound              = errors.New("execution: not found")
	ErrVersionConflict       = errors.New("execution: version conflict")
	ErrRuntimeConflict       = errors.New("execution: runtime conflict")
	ErrCapabilityUnavailable = errors.New("execution: capability unavailable")
	ErrStaleLease            = errors.New("execution: stale lease")
	ErrResultConflict        = errors.New("execution: result conflict")
	ErrRetryNotAllowed       = errors.New("execution: retry not allowed")
	ErrPolicyBlocked         = errors.New("execution: policy blocked")
)

type RunState string

const (
	RunActive    RunState = "ACTIVE"
	RunPaused    RunState = "PAUSED"
	RunCanceling RunState = "CANCELING"
	RunSucceeded RunState = "SUCCEEDED"
	RunFailed    RunState = "FAILED"
	RunCanceled  RunState = "CANCELED"
)

type NodeState string

const (
	NodePending         NodeState = "PENDING"
	NodeReady           NodeState = "READY"
	NodeRunning         NodeState = "RUNNING"
	NodeWaitingHuman    NodeState = "WAITING_HUMAN"
	NodeDecided         NodeState = "DECIDED"
	NodeExecutionFailed NodeState = "EXECUTION_FAILED"
	NodeSkipped         NodeState = "SKIPPED"
	NodeCanceled        NodeState = "CANCELED"
)

type TaskState string

const (
	TaskQueued        TaskState = "QUEUED"
	TaskLeased        TaskState = "LEASED"
	TaskRunning       TaskState = "RUNNING"
	TaskAwaitingInput TaskState = "AWAITING_INPUT"
	TaskCompleted     TaskState = "COMPLETED"
	TaskFailed        TaskState = "FAILED"
	TaskExpired       TaskState = "EXPIRED"
	TaskCanceled      TaskState = "CANCELED"
)

type AttemptKind string

const (
	AttemptInitial AttemptKind = "initial"
	AttemptRetry   AttemptKind = "retry"
	AttemptResume  AttemptKind = "resume"
)

type ResultStatus string

const (
	ResultCompleted     ResultStatus = "completed"
	ResultFailed        ResultStatus = "failed"
	ResultAwaitingInput ResultStatus = "awaiting_input"
)

type Capability struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type ProviderSelection struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type GitScope struct {
	RepositoryID string `json:"repository_id"`
	Role         string `json:"role"`
	Operation    string `json:"operation"`
	Ref          string `json:"ref"`
}

type RuntimeRegistration struct {
	SchemaVersion         string            `json:"schemaVersion"`
	Kind                  string            `json:"kind"`
	ID                    string            `json:"runtimeId"`
	Epoch                 string            `json:"runtimeEpoch"`
	GitHubUserID          string            `json:"gitHubUserId"`
	Provider              ProviderSelection `json:"provider"`
	ProviderAuthenticated bool              `json:"providerAuthenticated"`
	Capabilities          []Capability      `json:"capabilities"`
	RegisteredAt          time.Time         `json:"registeredAt"`
}

type Run struct {
	ID                    string    `json:"workflow_run_id"`
	WorkspaceID           string    `json:"workspace_id"`
	WorkItemID            string    `json:"work_item_id"`
	StartedByGitHubUserID string    `json:"started_by_github_user_id"`
	GitExecutionUserID    string    `json:"git_execution_user_id,omitempty"`
	DefinitionName        string    `json:"definition_name"`
	DefinitionVersion     int       `json:"definition_version"`
	DefinitionDigest      string    `json:"definition_digest"`
	State                 RunState  `json:"state"`
	Version               int64     `json:"version"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type NodeRun struct {
	ID          string         `json:"node_run_id"`
	RunID       string         `json:"workflow_run_id"`
	NodeID      string         `json:"node_id"`
	Iteration   int            `json:"iteration"`
	State       NodeState      `json:"state"`
	Verdict     string         `json:"verdict,omitempty"`
	InputDigest string         `json:"input_digest"`
	Output      map[string]any `json:"output,omitempty"`
	Version     int64          `json:"version"`
}

type Task struct {
	ID                 string            `json:"taskId"`
	WorkflowRunID      string            `json:"workflowRunId"`
	NodeRunID          string            `json:"nodeRunId"`
	Attempt            int               `json:"attempt"`
	AttemptKind        AttemptKind       `json:"attemptKind"`
	PreviousTaskID     string            `json:"previousTaskId,omitempty"`
	HumanResponseID    string            `json:"humanResponseId,omitempty"`
	ResumeRef          *ResumeRef        `json:"resumeRef,omitempty"`
	Capability         Capability        `json:"capability"`
	Provider           ProviderSelection `json:"provider"`
	GitScopes          []GitScope        `json:"gitScopes"`
	GitExecutionUserID string            `json:"gitExecutionUserId,omitempty"`
	State              TaskState         `json:"state"`
	InputDigest        string            `json:"inputDigest"`
	RuntimeID          string            `json:"runtimeId,omitempty"`
	RuntimeEpoch       string            `json:"runtimeEpoch,omitempty"`
	LeaseID            string            `json:"leaseId,omitempty"`
	FencingToken       int64             `json:"fencingToken,omitempty"`
	LeaseExpiresAt     time.Time         `json:"leaseExpiresAt,omitempty"`
	ResultDigest       string            `json:"resultDigest,omitempty"`
	ProgressSeq        int64             `json:"progressSequence,omitempty"`
	DeadlineAt         time.Time         `json:"deadlineAt"`
}

type ResumeRef struct {
	RuntimeID  string `json:"runtimeId"`
	ProviderID string `json:"providerId"`
	SessionRef string `json:"sessionRef"`
}

type CreateRunCommand struct {
	Run   Run
	Nodes []NodeRun
	Tasks []Task
}

type LeaseRequest struct {
	RuntimeID     string
	RuntimeEpoch  string
	LeaseDuration time.Duration
}

type TaskLease struct {
	SchemaVersion string    `json:"schemaVersion"`
	Kind          string    `json:"kind"`
	LeaseID       string    `json:"leaseId"`
	FencingToken  int64     `json:"fencingToken"`
	RuntimeEpoch  string    `json:"runtimeEpoch"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Task          TaskSpec  `json:"task"`
}

type TaskSpec struct {
	ID                 string            `json:"taskId"`
	WorkflowRunID      string            `json:"workflowRunId"`
	NodeRunID          string            `json:"nodeRunId"`
	Attempt            int               `json:"attempt"`
	AttemptKind        AttemptKind       `json:"attemptKind"`
	PreviousTaskID     string            `json:"previousTaskId,omitempty"`
	HumanResponseID    string            `json:"humanResponseId,omitempty"`
	ResumeRef          *ResumeRef        `json:"resumeRef,omitempty"`
	Capability         Capability        `json:"capability"`
	ProviderSelection  ProviderSelection `json:"providerSelection"`
	InputDigest        string            `json:"inputDigest"`
	GitExecutionUserID string            `json:"gitExecutionUserId"`
	GitScopes          []GitScope        `json:"gitScopes"`
	DeadlineAt         time.Time         `json:"deadlineAt"`
}

type ProgressReport struct {
	SchemaVersion string    `json:"schemaVersion"`
	Kind          string    `json:"kind"`
	TaskID        string    `json:"taskId"`
	LeaseID       string    `json:"leaseId"`
	FencingToken  int64     `json:"fencingToken"`
	Sequence      int64     `json:"sequence"`
	Phase         string    `json:"phase"`
	Message       string    `json:"message"`
	ObservedAt    time.Time `json:"observedAt"`
}

type ResultReport struct {
	SchemaVersion string         `json:"schemaVersion"`
	Kind          string         `json:"kind"`
	TaskID        string         `json:"taskId"`
	LeaseID       string         `json:"leaseId"`
	FencingToken  int64          `json:"fencingToken"`
	Status        ResultStatus   `json:"status"`
	ResultDigest  string         `json:"resultDigest"`
	Summary       string         `json:"summary"`
	Output        map[string]any `json:"output,omitempty"`
	Error         *TaskError     `json:"error,omitempty"`
	InputRequest  map[string]any `json:"inputRequest,omitempty"`
}

type TaskError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type ResultAcceptance struct {
	Applied   bool `json:"applied"`
	Duplicate bool `json:"duplicate"`
}

type NodeView struct {
	ID          string         `json:"nodeRunId"`
	NodeID      string         `json:"nodeId"`
	Iteration   int            `json:"iteration"`
	State       NodeState      `json:"state"`
	Verdict     string         `json:"verdict,omitempty"`
	InputDigest string         `json:"inputDigest"`
	Output      map[string]any `json:"output,omitempty"`
}

type RunView struct {
	SchemaVersion         string     `json:"schemaVersion"`
	Kind                  string     `json:"kind"`
	ID                    string     `json:"workflowRunId"`
	WorkItemID            string     `json:"workItemId"`
	StartedByGitHubUserID string     `json:"startedByGitHubUserId"`
	GitExecutionUserID    string     `json:"gitExecutionUserId,omitempty"`
	DefinitionName        string     `json:"definitionName"`
	DefinitionVersion     int        `json:"definitionVersion"`
	DefinitionDigest      string     `json:"definitionDigest"`
	State                 RunState   `json:"state"`
	Version               int64      `json:"version"`
	Nodes                 []NodeView `json:"nodes"`
}
