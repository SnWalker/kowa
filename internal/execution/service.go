package execution

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var decimalIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

type Store interface {
	RegisterRuntime(context.Context, RuntimeRegistration) error
	CreateRun(context.Context, CreateRunCommand) error
	LeaseTask(context.Context, LeaseRequest, time.Time) (TaskLease, error)
	ReportProgress(context.Context, ProgressReport, time.Time) error
	ReportResult(context.Context, ResultReport, time.Time) (ResultAcceptance, error)
	RetryTask(context.Context, string, string, time.Time) (Task, error)
	ResumeTask(context.Context, string, string, string, string, *ResumeRef, time.Time) (Task, error)
	RerunNode(context.Context, string, string, string, string, string, int, time.Time) (NodeRun, error)
	CancelRun(context.Context, string, int64, time.Time) error
	RunView(context.Context, string) (RunView, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store, now func() time.Time) *Service {
	return &Service{store: store, now: now}
}

func (s *Service) RegisterRuntime(ctx context.Context, registration RuntimeRegistration) error {
	if registration.ID == "" || registration.Epoch == "" ||
		!decimalIDPattern.MatchString(registration.GitHubUserID) ||
		registration.Provider.ID == "" || registration.Provider.Version == "" ||
		len(registration.Capabilities) == 0 {
		return ErrInvalidInput
	}
	seenCapabilities := make(map[Capability]struct{}, len(registration.Capabilities))
	for _, capability := range registration.Capabilities {
		if capability.ID == "" || capability.Version < 1 {
			return ErrInvalidInput
		}
		if _, duplicate := seenCapabilities[capability]; duplicate {
			return ErrInvalidInput
		}
		seenCapabilities[capability] = struct{}{}
	}
	registration.RegisteredAt = s.now().UTC()
	registration.SchemaVersion = SchemaVersion
	registration.Kind = RuntimeRegistrationKind
	return s.store.RegisterRuntime(ctx, registration)
}

func (s *Service) CreateRun(ctx context.Context, command CreateRunCommand) error {
	if command.Run.ID == "" || command.Run.WorkspaceID == "" || command.Run.WorkItemID == "" ||
		!decimalIDPattern.MatchString(command.Run.StartedByGitHubUserID) ||
		command.Run.DefinitionName == "" || command.Run.DefinitionVersion < 1 ||
		!digestPattern.MatchString(command.Run.DefinitionDigest) || command.Run.State != RunActive ||
		command.Run.Version != 1 || len(command.Nodes) == 0 {
		return ErrInvalidInput
	}
	for _, task := range command.Tasks {
		if task.ID == "" || task.NodeRunID == "" || task.Attempt < 1 ||
			task.AttemptKind != AttemptInitial || task.Capability.ID == "" ||
			task.Capability.Version < 1 || task.Provider.ID == "" ||
			task.Provider.Version == "" || task.State != TaskQueued ||
			!digestPattern.MatchString(task.InputDigest) || task.DeadlineAt.IsZero() {
			return ErrInvalidInput
		}
		for _, scope := range task.GitScopes {
			if !validGitScope(scope) {
				return ErrInvalidInput
			}
		}
	}
	command.Run.CreatedAt = s.now().UTC()
	command.Run.UpdatedAt = command.Run.CreatedAt
	return s.store.CreateRun(ctx, command)
}

func validGitScope(scope GitScope) bool {
	if !decimalIDPattern.MatchString(scope.RepositoryID) || scope.Ref == "" {
		return false
	}
	validRole := scope.Role == "project" || scope.Role == "knowledge"
	validOperation := scope.Operation == "clone" || scope.Operation == "fetch" ||
		scope.Operation == "pull" || scope.Operation == "commit" ||
		scope.Operation == "push" || scope.Operation == "publish_pr"
	isKnowledgeWrite := scope.Role == "knowledge" &&
		(scope.Operation == "commit" || scope.Operation == "push" || scope.Operation == "publish_pr")
	return validRole && validOperation && !isKnowledgeWrite
}

func (s *Service) LeaseTask(ctx context.Context, request LeaseRequest) (TaskLease, error) {
	if request.RuntimeID == "" || request.RuntimeEpoch == "" || request.LeaseDuration <= 0 {
		return TaskLease{}, ErrInvalidInput
	}
	return s.store.LeaseTask(ctx, request, s.now().UTC())
}

func (s *Service) ReportProgress(ctx context.Context, report ProgressReport) error {
	if report.SchemaVersion != SchemaVersion || report.Kind != ProgressEventKind ||
		report.TaskID == "" || report.LeaseID == "" || report.FencingToken < 1 ||
		report.Sequence < 1 || report.Phase == "" || report.Message == "" || report.ObservedAt.IsZero() {
		return ErrInvalidInput
	}
	return s.store.ReportProgress(ctx, report, s.now().UTC())
}

func (s *Service) ReportResult(ctx context.Context, report ResultReport) (ResultAcceptance, error) {
	validStatus := report.Status == ResultCompleted || report.Status == ResultFailed || report.Status == ResultAwaitingInput
	if report.SchemaVersion != SchemaVersion || report.Kind != TaskResultKind ||
		report.TaskID == "" || report.LeaseID == "" || report.FencingToken < 1 ||
		!validStatus || !digestPattern.MatchString(report.ResultDigest) {
		return ResultAcceptance{}, ErrInvalidInput
	}
	switch report.Status {
	case ResultCompleted:
		if report.Output == nil || report.Error != nil || report.InputRequest != nil {
			return ResultAcceptance{}, ErrInvalidInput
		}
	case ResultFailed:
		if report.Error == nil || report.Error.Code == "" || report.Error.Message == "" || report.Output != nil || report.InputRequest != nil {
			return ResultAcceptance{}, ErrInvalidInput
		}
	case ResultAwaitingInput:
		if report.InputRequest == nil || report.Error != nil {
			return ResultAcceptance{}, ErrInvalidInput
		}
	}
	return s.store.ReportResult(ctx, report, s.now().UTC())
}

func (s *Service) RetryTask(ctx context.Context, taskID, newTaskID string) (Task, error) {
	if taskID == "" || newTaskID == "" || taskID == newTaskID {
		return Task{}, ErrInvalidInput
	}
	return s.store.RetryTask(ctx, taskID, newTaskID, s.now().UTC())
}

func (s *Service) ResumeTask(
	ctx context.Context,
	taskID string,
	newTaskID string,
	humanResponseID string,
	inputDigest string,
	resumeRef *ResumeRef,
) (Task, error) {
	if taskID == "" || newTaskID == "" || taskID == newTaskID || humanResponseID == "" || !digestPattern.MatchString(inputDigest) {
		return Task{}, ErrInvalidInput
	}
	if resumeRef != nil && (resumeRef.RuntimeID == "" || resumeRef.ProviderID == "" || resumeRef.SessionRef == "") {
		return Task{}, ErrInvalidInput
	}
	return s.store.ResumeTask(ctx, taskID, newTaskID, humanResponseID, inputDigest, resumeRef, s.now().UTC())
}

func (s *Service) RerunNode(
	ctx context.Context,
	runID string,
	nodeID string,
	newNodeRunID string,
	newTaskID string,
	inputDigest string,
) (NodeRun, error) {
	if runID == "" || nodeID == "" || newNodeRunID == "" || newTaskID == "" || !digestPattern.MatchString(inputDigest) {
		return NodeRun{}, ErrInvalidInput
	}
	return s.store.RerunNode(ctx, runID, nodeID, newNodeRunID, newTaskID, inputDigest, 0, s.now().UTC())
}

func (s *Service) ReviseNode(
	ctx context.Context,
	runID string,
	nodeID string,
	newNodeRunID string,
	newTaskID string,
	inputDigest string,
) (NodeRun, error) {
	if runID == "" || nodeID == "" || newNodeRunID == "" || newTaskID == "" || !digestPattern.MatchString(inputDigest) {
		return NodeRun{}, ErrInvalidInput
	}
	const initialPlusThreeRevisions = 4
	return s.store.RerunNode(
		ctx,
		runID,
		nodeID,
		newNodeRunID,
		newTaskID,
		inputDigest,
		initialPlusThreeRevisions,
		s.now().UTC(),
	)
}

func (s *Service) CancelRun(ctx context.Context, runID string, expectedVersion int64) error {
	if runID == "" || expectedVersion < 1 {
		return ErrInvalidInput
	}
	return s.store.CancelRun(ctx, runID, expectedVersion, s.now().UTC())
}

func (s *Service) RunView(ctx context.Context, runID string) (RunView, error) {
	if runID == "" {
		return RunView{}, fmt.Errorf("%w: run id is required", ErrInvalidInput)
	}
	return s.store.RunView(ctx, runID)
}
