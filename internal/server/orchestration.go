package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"regexp"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/knowledge"
	"github.com/SnWalker/kowa/internal/materialize"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/runnertransport"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workflow"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

// newOrchestrationService assembles the fixed-flow orchestration over the
// frozen ports of S01—S05 and the additive execution dispatch contract.
func newOrchestrationService(
	identityConfig config.Identity,
	runtimeConfig config.Runtime,
	db *sql.DB,
	executionService *execution.Service,
	runtimes *runtimeops.Service,
	workItems *workitem.Service,
	humans *humantask.Service,
	deliveries *githubdelivery.Service,
	evaluations *evaluation.Service,
	auth *identity.Service,
	workspaces *workspace.Service,
) (*orchestration.Service, error) {
	if !identityConfig.Enabled || db == nil || executionService == nil || runtimes == nil ||
		workItems == nil || humans == nil || evaluations == nil || auth == nil || workspaces == nil {
		return nil, nil
	}
	store := postgres.NewStore(db)
	if err := store.PublishPlan(context.Background(), fixedflowPlan()); err != nil {
		return nil, err
	}
	blobs, err := artifact.NewLocalStore(filepath.Join(runtimeConfig.MaterializeRoot, "artifacts"))
	if err != nil {
		return nil, err
	}
	fetcher, err := materialize.New(runtimeConfig.RepositoryRegistry, filepath.Join(runtimeConfig.MaterializeRoot, "repos"))
	if err != nil {
		return nil, err
	}
	return orchestration.NewService(orchestration.Options{
		Store:        store,
		Execution:    executionService,
		Knowledge:    knowledgePort{store},
		Artifacts:    artifact.NewService(store, blobs, time.Now),
		WorkItems:    workItems,
		HumanTasks:   humans,
		Deliveries:   orchestrationDelivery{deliveries: deliveries, store: store},
		Evaluation:   evaluations,
		Runtimes:     runtimes,
		Bindings:     workspaceRepositories{workspaces},
		Access:       workspaceAuthorizer{workspaces},
		Materializer: fetcher,
	})
}

// fixedflowPlan compiles the single built-in definition. A failure here is a
// deployment error: the built-in definition is frozen with the binary.
func fixedflowPlan() workflow.CompiledPlan {
	compiled, err := fixedflow.Compile()
	if err != nil {
		panic("the built-in workflow definition does not compile: " + err.Error())
	}
	return compiled
}

// knowledgePort adapts the shared PostgreSQL store to the orchestration
// knowledge port; backend:S03 owns every rule behind it.
type knowledgePort struct{ store *postgres.Store }

func (p knowledgePort) PublishSnapshot(ctx context.Context, scope artifact.Scope, snapshot knowledge.Snapshot) error {
	return p.store.PublishKnowledgeSnapshot(ctx, scope, snapshot)
}

func (p knowledgePort) Confirm(
	ctx context.Context,
	scope artifact.Scope,
	command knowledge.ConfirmCommand,
) (knowledge.BindingEvent, error) {
	return p.store.ConfirmKnowledge(ctx, scope, command)
}

func (p knowledgePort) AssembleInput(
	ctx context.Context,
	scope artifact.Scope,
	request knowledge.InputRequest,
	bindingEventID string,
) (knowledge.EffectiveInput, error) {
	return p.store.AssembleKnowledgeInput(ctx, scope, request, bindingEventID)
}

func (p knowledgePort) ReadInput(ctx context.Context, scope artifact.Scope, inputID string) (knowledge.EffectiveInput, error) {
	return p.store.ReadEffectiveInput(ctx, scope, inputID)
}

// workspaceAuthorizer re-checks the caller's current membership and role.
type workspaceAuthorizer struct{ workspaces *workspace.Service }

func (a workspaceAuthorizer) Authorize(ctx context.Context, workspaceID, githubUserID string, roles []string) error {
	current, err := a.workspaces.Get(ctx, workspace.Actor{GitHubUserID: githubUserID}, workspaceID)
	if err != nil {
		return workspace.ErrForbidden
	}
	for _, member := range current.Members {
		if member.GitHubUserID != githubUserID || member.RevokedAt != nil {
			continue
		}
		for _, role := range roles {
			if string(member.Role) == role {
				return nil
			}
		}
	}
	return workspace.ErrForbidden
}

// workspaceRepositories resolves the single project repository and the
// knowledge repository of a workspace through the S01 authority.
type workspaceRepositories struct{ workspaces *workspace.Service }

func (a workspaceRepositories) Repositories(
	ctx context.Context,
	workspaceID, actorID string,
) (string, string, error) {
	current, err := a.workspaces.Get(ctx, workspace.Actor{GitHubUserID: actorID}, workspaceID)
	if err != nil {
		return "", "", err
	}
	if current.Project == nil || current.Knowledge == nil {
		return "", "", workspace.ErrResourceUnavailable
	}
	return current.Project.GitHubRepositoryID, current.Knowledge.GitHubRepositoryID, nil
}

// orchestrationDelivery adapts the GitHub delivery service and its stored
// observations to the orchestration delivery port.
type orchestrationDelivery struct {
	deliveries *githubdelivery.Service
	store      *postgres.Store
}

func (t orchestrationDelivery) AdmitOperation(
	ctx context.Context,
	command githubdelivery.AdmitOperationCommand,
) (githubdelivery.ExternalOperation, error) {
	return t.deliveries.AdmitOperation(ctx, command)
}

func (t orchestrationDelivery) MarkAttempted(
	ctx context.Context,
	command githubdelivery.MarkAttemptedCommand,
) (githubdelivery.ExternalOperation, error) {
	return t.deliveries.MarkAttempted(ctx, command)
}

func (t orchestrationDelivery) ObserveOperation(
	ctx context.Context,
	workspaceID, operationID string,
) (githubdelivery.ExternalOperation, error) {
	return t.deliveries.ObserveOperation(ctx, workspaceID, operationID)
}

func (t orchestrationDelivery) ObservePullRequest(
	ctx context.Context,
	command githubdelivery.ObservePullRequestCommand,
) (githubdelivery.PullRequestProjection, error) {
	return t.deliveries.ObservePullRequest(ctx, command)
}

func (t orchestrationDelivery) Observation(
	ctx context.Context,
	workspaceID, observationID string,
) (githubdelivery.Observation, bool, error) {
	return t.store.GitHubObservation(ctx, workspaceID, observationID)
}

func (t orchestrationDelivery) LatestObservations(
	ctx context.Context,
	workspaceID, repositoryID string,
) ([]githubdelivery.Observation, error) {
	return t.store.LatestGitHubObservations(ctx, workspaceID, repositoryID)
}

func (t orchestrationDelivery) Operations(
	ctx context.Context,
	workspaceID, workItemID string,
) ([]githubdelivery.ExternalOperation, error) {
	return t.store.ExternalOperations(ctx, workspaceID, workItemID)
}

// workspaceDirectoryAdapter lists the workspaces of one authenticated member.
type workspaceDirectoryAdapter struct{ store *postgres.Store }

func (a workspaceDirectoryAdapter) WorkspacesForMember(ctx context.Context, githubUserID string) ([]workspace.Workspace, error) {
	ids, err := a.store.WorkspaceMembershipsFor(ctx, githubUserID)
	if err != nil {
		return nil, err
	}
	workspaces := make([]workspace.Workspace, 0, len(ids))
	for _, workspaceID := range ids {
		current, err := a.store.Get(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, current)
	}
	return workspaces, nil
}

// newWebApplication assembles the kowa.web-application.v1 handler.
func newWebApplication(
	identityConfig config.Identity,
	db *sql.DB,
	auth *identity.Service,
	workspaces *workspace.Service,
	service *orchestration.Service,
	logger *slog.Logger,
) *httpapi.WebApplicationHandler {
	if !identityConfig.Enabled || db == nil || auth == nil || workspaces == nil || service == nil {
		return nil
	}
	return httpapi.NewWebApplicationHandler(auth, workspaces,
		workspaceDirectoryAdapter{store: postgres.NewStore(db)},
		webApplicationTargets{service: service}, identityConfig.TrustedOrigin, logger)
}

// webApplicationTargets adapts the orchestration service to the handler.
type webApplicationTargets struct{ service *orchestration.Service }

func (t webApplicationTargets) Plan() (fixedflow.View, error) {
	plan, err := fixedflow.Compile()
	if err != nil {
		return fixedflow.View{}, err
	}
	return fixedflow.BuildView(plan), nil
}

func (t webApplicationTargets) Admit(
	ctx context.Context,
	command orchestration.AdmitCommand,
) (orchestration.AdmissionResult, error) {
	return t.service.Admit(ctx, command)
}

func (t webApplicationTargets) RunDetail(
	ctx context.Context,
	workspaceID, workItemID, runID string,
) (orchestration.RunDetailView, error) {
	return t.service.RunDetail(ctx, workspaceID, workItemID, runID)
}

func (t webApplicationTargets) Recover(
	ctx context.Context,
	command orchestration.RecoveryCommand,
) (orchestration.RunRecovery, error) {
	return t.service.Recover(ctx, command)
}

func (t webApplicationTargets) Artifacts(
	ctx context.Context,
	workspaceID, workItemID string,
) (orchestration.ArtifactListView, error) {
	return t.service.Artifacts(ctx, workspaceID, workItemID)
}

func (t webApplicationTargets) Journey(
	ctx context.Context,
	workspaceID, workItemID string,
) (orchestration.JourneyView, error) {
	return t.service.Journey(ctx, workspaceID, workItemID)
}

// taskInputProvider serves the frozen input manifest from the orchestration
// bindings. The disabled state keeps the closed shim.
type taskInputProvider struct{ service *orchestration.Service }

func (p taskInputProvider) TaskInput(ctx context.Context, taskID string) (runnertransport.InputManifestResponse, error) {
	manifest, err := p.service.TaskInput(ctx, taskID)
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) || errors.Is(err, orchestration.ErrPolicyBlocked) {
			// An unbound or mismatched attempt is not executable: the transport
			// answers with the retryable unbound category.
			return runnertransport.InputManifestResponse{}, httpapi.ErrTaskInputUnbound
		}
		return runnertransport.InputManifestResponse{}, err
	}
	return manifest, nil
}

func (p taskInputProvider) TaskInputArtifact(ctx context.Context, taskID, artifactID string) ([]byte, error) {
	data, err := p.service.TaskInputArtifact(ctx, taskID, artifactID)
	if err != nil {
		if errors.Is(err, orchestration.ErrNotFound) || errors.Is(err, orchestration.ErrPolicyBlocked) {
			return nil, httpapi.ErrTaskInputUnbound
		}
		return nil, err
	}
	return data, nil
}

// taskInputFor returns the real frozen-input binding when the orchestration
// service is assembled, and the fail-closed shim otherwise.
func taskInputFor(service *orchestration.Service) httpapi.TaskInputProvider {
	if service == nil {
		return unboundTaskInput{}
	}
	return taskInputProvider{service: service}
}

// mountWebApplicationSurface adds the web-application paths to the mounted
// dispatcher. Every other path keeps its owning handler.
func mountWebApplicationSurface(srv *http.Server, handler *httpapi.WebApplicationHandler) {
	if handler == nil {
		return
	}
	existing := srv.Handler
	webApplication := http.Handler(handler)
	srv.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isWebApplicationPath(request) {
			webApplication.ServeHTTP(writer, request)
			return
		}
		existing.ServeHTTP(writer, request)
	})
}

// isWebApplicationPath matches exactly the frozen web-application operations,
// including the workspace collection read that the S01 surface does not own.
func isWebApplicationPath(request *http.Request) bool {
	path := request.URL.Path
	if path == "/api/v1/workspaces" {
		return request.Method == http.MethodGet
	}
	return webApplicationPath.MatchString(path)
}

// webApplicationPath matches the frozen web-application path families.
var webApplicationPath = regexp.MustCompile(
	`^/api/v1/workspaces/[^/]+/(fixed-workflow|work-items/[^/]+/(runs(/[^/]+)?(/recovery)?|artifacts|journey))$`)

// startOrchestrationReconciler advances every admitted run the delivery run
// still holds ACTIVE. It is the restart-safe driver of the fixed flow: all
// progress is derived from persisted facts, so a control-plane restart simply
// continues the same reconciliation.
func startOrchestrationReconciler(
	lifecycle fx.Lifecycle,
	service *orchestration.Service,
	runtimeConfig config.Runtime,
	logger *slog.Logger,
) {
	if service == nil {
		return
	}
	// A zero-valued configuration (a test graph, an older environment file)
	// must never panic the process: the reconciler falls back to its default
	// cadence.
	interval := runtimeConfig.ReconcileInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
					}
					reconcileOnce(service, logger)
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			close(stop)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				logger.Warn("orchestration reconciler did not stop within the shutdown timeout")
			}
			return nil
		},
	})
	startSuggestionWorker(lifecycle, service, interval, logger)
}

// startSuggestionWorker processes the auxiliary knowledge-suggestion branch on
// its own loop, so a slow upload cannot stall the run reconciler itself. The
// artifact store still serialises same-workspace artifact and knowledge
// reads/writes (and workspace-keyed inserts) on its workspace lock while an
// attempt holds it, so every attempt's artifact work runs under its own short
// bound (suggestionPublishBound) and a bound that fires settles the attempt as
// FAILED, retryable only by the explicit command.
func startSuggestionWorker(
	lifecycle fx.Lifecycle,
	service *orchestration.Service,
	interval time.Duration,
	logger *slog.Logger,
) {
	stop := make(chan struct{})
	done := make(chan struct{})
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
					}
					// The pass itself is short: the artifact work inside it
					// carries its own, tighter bound.
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					if _, err := service.ProcessSuggestions(ctx); err != nil {
						logger.Error("process knowledge suggestions", "error", err)
					}
					cancel()
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			close(stop)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				logger.Warn("knowledge suggestion worker did not stop within the shutdown timeout")
			}
			return nil
		},
	})
}

func reconcileOnce(service *orchestration.Service, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	identifiers, err := service.ActiveRunIDs(ctx)
	if err != nil {
		logger.Error("list active runs", "error", err)
		return
	}
	for _, runID := range identifiers {
		if err := service.Advance(ctx, runID); err != nil {
			logger.Error("advance run", "run", runID, "error", err)
		}
	}
}
