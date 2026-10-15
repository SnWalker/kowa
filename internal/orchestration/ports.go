package orchestration

import (
	"context"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/knowledge"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workitem"
)

// ExecutionPort is the frozen execution surface plus the additive dispatch and
// decision contract of backend:S06. Execution stays the only producer of
// NodeRun and Task state.
type ExecutionPort interface {
	Run(ctx context.Context, runID string) (execution.Run, error)
	RunView(ctx context.Context, runID string) (execution.RunView, error)
	TaskStatus(ctx context.Context, taskID string) (execution.TaskStatus, error)
	CreateRun(ctx context.Context, command execution.CreateRunCommand) error
	DispatchNode(ctx context.Context, command execution.DispatchNodeCommand) (execution.DispatchOutcome, error)
	OpenNodeRound(ctx context.Context, command execution.OpenRoundCommand) (execution.NodeRun, error)
	RunAttempts(ctx context.Context, runID string) ([]execution.NodeAttemptRef, error)
	RunRounds(ctx context.Context, runID string) ([]execution.NodeRoundRef, error)
	DecideNode(ctx context.Context, command execution.DecideNodeCommand) (execution.DecideOutcome, error)
	RetryTask(ctx context.Context, taskID, newTaskID string) (execution.Task, error)
	CancelRun(ctx context.Context, runID string, expectedVersion int64) error
}

// KnowledgePort freezes verified content and assembles effective inputs.
type KnowledgePort interface {
	PublishSnapshot(ctx context.Context, scope artifact.Scope, snapshot knowledge.Snapshot) error
	Confirm(ctx context.Context, scope artifact.Scope, command knowledge.ConfirmCommand) (knowledge.BindingEvent, error)
	AssembleInput(
		ctx context.Context,
		scope artifact.Scope,
		request knowledge.InputRequest,
		bindingEventID string,
	) (knowledge.EffectiveInput, error)
	ReadInput(ctx context.Context, scope artifact.Scope, inputID string) (knowledge.EffectiveInput, error)
}

// ArtifactPort publishes, reads and approves immutable node deliverables.
type ArtifactPort interface {
	Upload(ctx context.Context, scope artifact.Scope, upload artifact.Upload, data []byte) (artifact.Ref, error)
	Read(ctx context.Context, scope artifact.Scope, ref artifact.Ref) ([]byte, error)
	Publish(ctx context.Context, scope artifact.Scope, ref artifact.Ref) error
	Approve(ctx context.Context, scope artifact.Scope, ref artifact.Ref, humanResponseID string) error
}

// WorkItemPort reads and binds the goal of the run.
type WorkItemPort interface {
	Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error)
	AssociateRun(ctx context.Context, command workitem.AssociateRunCommand) (workitem.WorkItem, error)
	RunBindings(ctx context.Context, workspaceID, workItemID string) ([]workitem.RunBinding, error)
}

// HumanTaskPort owns versioned human requests and their accepted answers.
type HumanTaskPort interface {
	Create(ctx context.Context, command humantask.CreateCommand) (humantask.HumanTask, error)
	Get(ctx context.Context, workspaceID, taskID string) (humantask.HumanTask, error)
	List(ctx context.Context, workspaceID string, openOnly bool) ([]humantask.HumanTask, error)
	PendingContinuations(ctx context.Context, workspaceID, runID string) ([]humantask.Continuation, error)
	ConsumeContinuation(ctx context.Context, command humantask.ConsumeContinuationMutation) (humantask.Continuation, error)
}

// DeliveryPort owns external GitHub operations and their reconciliation.
type DeliveryPort interface {
	AdmitOperation(ctx context.Context, command githubdelivery.AdmitOperationCommand) (githubdelivery.ExternalOperation, error)
	MarkAttempted(ctx context.Context, command githubdelivery.MarkAttemptedCommand) (githubdelivery.ExternalOperation, error)
	ObserveOperation(ctx context.Context, workspaceID, operationID string) (githubdelivery.ExternalOperation, error)
	ObservePullRequest(ctx context.Context, command githubdelivery.ObservePullRequestCommand) (githubdelivery.PullRequestProjection, error)
	Observation(ctx context.Context, workspaceID, observationID string) (githubdelivery.Observation, bool, error)
	LatestObservations(ctx context.Context, workspaceID, repositoryID string) ([]githubdelivery.Observation, error)
	Operations(ctx context.Context, workspaceID, workItemID string) ([]githubdelivery.ExternalOperation, error)
}

// EvaluationPort records verification evidence and closes through the single
// authoritative transaction.
type EvaluationPort interface {
	RecordVerification(ctx context.Context, command evaluation.RecordVerificationCommand) (evaluation.Verification, error)
	Verifications(ctx context.Context, workspaceID, workItemID string) ([]evaluation.Verification, error)
	Closure(ctx context.Context, workspaceID, workItemID string) (evaluation.ClosureRecord, bool, error)
	Close(ctx context.Context, command evaluation.CloseCommand) (evaluation.ClosureRecord, error)
}

// RuntimePort reads the workspace execution configuration and its runtimes.
type RuntimePort interface {
	ExecutionConfig(ctx context.Context, workspaceID string) (runtimeops.ExecutionConfig, error)
	WorkspaceRuntimes(ctx context.Context, workspaceID string) ([]runtimeops.RuntimeView, error)
}

// Materializer resolves frozen repository read views for the control plane. A
// commit read must return exactly that commit; a branch head may only be used
// to freeze a baseline once at admission.
type Materializer interface {
	// BranchHead resolves one branch reference to its current commit oid.
	BranchHead(ctx context.Context, repositoryID, branchRef string) (string, error)
	// Open returns a verified read view of one frozen commit.
	Open(ctx context.Context, repositoryID, commitOID string) (Repository, error)
}

// Repository is a control-plane read view of one frozen commit.
type Repository = knowledge.Repository

// ProjectBindingSource reports the single writable project repository and the
// knowledge repository of a Workspace; backend:S01 owns those facts and decides
// whether the acting member may read them.
type ProjectBindingSource interface {
	Repositories(
		ctx context.Context,
		workspaceID, actorID string,
	) (projectRepositoryID, knowledgeRepositoryID string, err error)
}

// WorkspaceAuthorizer re-checks the caller's current membership and roles; the
// Web application never trusts a request body for identity.
type WorkspaceAuthorizer interface {
	Authorize(ctx context.Context, workspaceID, githubUserID string, roles []string) error
}

// Clock and IDGenerator keep the service deterministic in tests.
type Clock func() time.Time

type IDGenerator interface {
	New(prefix string) (string, error)
}

// DefaultIDs is the production identity generator.
type DefaultIDs struct{}

func (DefaultIDs) New(prefix string) (string, error) { return newID(prefix) }
