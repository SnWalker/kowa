package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/githubobs"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/platform/proc"
	"github.com/SnWalker/kowa/internal/platform/random"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

// The S05 delivery surface is enabled with the identity surface: it needs the
// S01 session and membership authorities on every request.

func newWorkItemService(
	identityConfig config.Identity,
	db *sql.DB,
	workspaces *workspace.Service,
	executionService *execution.Service,
) *workitem.Service {
	if !identityConfig.Enabled || db == nil || workspaces == nil || executionService == nil {
		return nil
	}
	store := postgres.NewStore(db)
	return workitem.NewService(
		pgWorkItems{store}, workspaceProjects{workspaces}, executionRuns{executionService},
		workItemOperationsGuard{store}, random.Generator{}, time.Now,
	)
}

// workItemOperationsGuard counts the unsettled external operations straight
// from the delivery store; the GitHub delivery service owns that table's
// meaning, and cancellation must fail closed while an outcome is open.
type workItemOperationsGuard struct{ store *postgres.Store }

func (g workItemOperationsGuard) UnsettledOperations(ctx context.Context, workspaceID, workItemID string) (int, error) {
	return g.store.UnsettledExternalOperations(ctx, workspaceID, workItemID)
}

func newHumanTaskService(
	identityConfig config.Identity,
	db *sql.DB,
) *humantask.Service {
	if !identityConfig.Enabled || db == nil {
		return nil
	}
	store := postgres.NewStore(db)
	return humantask.NewService(pgHumanTasks{store}, humanTaskObservations{store},
		workItemOwnership{store}, random.Generator{}, time.Now)
}

// workItemOwnership is the HumanTask service's view of the WorkItem authority:
// a task may only be created for a work item of the same workspace. It reads
// the work item row through the shared store so the two services do not depend
// on each other.
type workItemOwnership struct{ store *postgres.Store }

func (o workItemOwnership) WorkItemExists(ctx context.Context, workspaceID, workItemID string) (bool, error) {
	if _, err := o.store.WorkItem(ctx, workspaceID, workItemID); err != nil {
		if errors.Is(err, workitem.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func newGitHubDeliveryService(
	identityConfig config.Identity,
	db *sql.DB,
	observer githubobs.Observer,
	evidence *evaluation.Service,
	runs *execution.Service,
	workItems *workitem.Service,
	humans *humantask.Service,
) *githubdelivery.Service {
	if !identityConfig.Enabled || db == nil || runs == nil || workItems == nil {
		return nil
	}
	store := postgres.NewStore(db)
	var factSource githubdelivery.FactSource
	deliveryObserver, _ := observer.(githubobs.DeliveryObserver)
	if deliveryObserver != nil {
		factSource = githubDeliveryFacts{deliveryObserver}
	}
	var settler githubdelivery.FactSettler
	if humans != nil {
		settler = humanFactSettler{humans}
	}
	var evidenceSource githubdelivery.EvidenceSource
	if evidence != nil {
		evidenceSource = deliveryEvidence{evidence}
	}
	return githubdelivery.NewService(pgDelivery{store}, factSource, evidenceSource,
		executionRuns{runs}, workItemFacts{workItems}, settler, random.Generator{}, time.Now)
}

func newEvaluationService(
	identityConfig config.Identity,
	db *sql.DB,
	runs *execution.Service,
	workItems *workitem.Service,
) *evaluation.Service {
	if !identityConfig.Enabled || db == nil || runs == nil || workItems == nil {
		return nil
	}
	store := postgres.NewStore(db)
	// The PostgreSQL adapter is the only one that closes: it owns the single
	// transaction over the WorkItem, the delivery run and the closure record.
	adapter := pgEvaluation{store}
	return evaluation.NewService(adapter, adapter, adapter, workItemFacts{workItems},
		executionRuns{runs}, evaluationFacts{store}, adapter, adapter, random.Generator{}, time.Now)
}

// newGitRemoteService exposes the frozen git remote projection over real
// read-only observations; the gh observation seam supplies identity and
// permission facts, and Git itself supplies the remote read fact.
func newGitRemoteService(
	identityConfig config.Identity,
	observer githubobs.Observer,
	runtimeOps *runtimeops.Service,
) *gitremote.Service {
	// The runtime-operations service being present implies the runtime surface
	// is enabled, which is also what supplies the registered machine identity.
	if !identityConfig.Enabled || observer == nil || runtimeOps == nil {
		return nil
	}
	deliveryObserver, _ := observer.(githubobs.DeliveryObserver)
	if deliveryObserver == nil {
		return nil
	}
	return gitremote.NewService(
		gitRemoteFacts{deliveryObserver}, gitRemoteIdentities{runtimeOps},
		func(ctx context.Context, spec gitremote.CommandSpec) (gitremote.CommandOutcome, error) {
			outcome, err := proc.Run(ctx, proc.Spec{
				Command: spec.Command, Dir: spec.Dir, Env: spec.Env,
				StdoutPath: spec.StdoutPath, StderrPath: spec.StderrPath,
				Marker: spec.Marker, Grace: spec.Grace,
			})
			return gitremote.CommandOutcome{
				ExitCode: outcome.ExitCode, Canceled: outcome.Canceled, TimedOut: outcome.TimedOut,
			}, err
		},
		os.Environ(), time.Now,
	)
}

// newDeliverySurface assembles the kowa.workitem.v1 and
// kowa.delivery-governance.v1 HTTP handlers.
func newDeliverySurface(
	identityConfig config.Identity,
	auth *identity.Service,
	workspaces *workspace.Service,
	workItems *workitem.Service,
	humans *humantask.Service,
	deliveries *githubdelivery.Service,
	gitRemote *gitremote.Service,
) (*httpapi.WorkItemHandler, *httpapi.DeliveryHandler) {
	if !identityConfig.Enabled || auth == nil || workspaces == nil {
		return nil, nil
	}
	var workItemHandler *httpapi.WorkItemHandler
	if workItems != nil {
		workItemHandler = httpapi.NewWorkItemHandler(auth, workspaces, workItems, identityConfig.TrustedOrigin)
	}
	var deliveryHandler *httpapi.DeliveryHandler
	if humans != nil && deliveries != nil {
		deliveryHandler = httpapi.NewDeliveryHandler(auth, workspaces, humans,
			deliveryTargets{deliveries: deliveries, gitRemote: gitRemote}, identityConfig.TrustedOrigin)
	}
	return workItemHandler, deliveryHandler
}

// deliveryTargets adapts the delivery and git-remote services to the handler's
// read-only projection interface.
type deliveryTargets struct {
	deliveries *githubdelivery.Service
	gitRemote  *gitremote.Service
}

func (t deliveryTargets) DeliveryView(ctx context.Context, workspaceID, workItemID string) (githubdelivery.DeliveryView, error) {
	return t.deliveries.DeliveryView(ctx, workspaceID, workItemID)
}

func (t deliveryTargets) Status(ctx context.Context, command gitremote.StatusCommand) (gitremote.Status, error) {
	if t.gitRemote == nil {
		// The git remote projection needs the verified machine gh identity to
		// observe anything; without it the route fails closed.
		return gitremote.Status{}, workspace.ErrResourceUnavailable
	}
	return t.gitRemote.Status(ctx, command)
}

// mountDeliverySurface is intentionally empty: the S05 handlers are mounted by
// mountIdentityWorkspace, which routes the frozen path families to them.
func mountDeliverySurface(*httpapi.WorkItemHandler, *httpapi.DeliveryHandler) {}

// ---- store adapters -------------------------------------------------------

// pgWorkItems adapts the shared PostgreSQL store to the WorkItem store port.
type pgWorkItems struct{ store *postgres.Store }

func (a pgWorkItems) Create(ctx context.Context, mutation workitem.CreateMutation) (workitem.WorkItem, error) {
	return a.store.CreateWorkItem(ctx, mutation)
}

func (a pgWorkItems) Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error) {
	return a.store.WorkItem(ctx, workspaceID, workItemID)
}

func (a pgWorkItems) List(ctx context.Context, workspaceID string) ([]workitem.WorkItem, error) {
	return a.store.WorkItems(ctx, workspaceID)
}

func (a pgWorkItems) Cancel(ctx context.Context, mutation workitem.CancelMutation) (workitem.WorkItem, error) {
	return a.store.CancelWorkItem(ctx, mutation)
}

func (a pgWorkItems) AssociateRun(ctx context.Context, mutation workitem.AssociateRunMutation) (workitem.WorkItem, error) {
	return a.store.AssociateRun(ctx, mutation)
}

func (a pgWorkItems) RunBindings(ctx context.Context, workspaceID, workItemID string) ([]workitem.RunBinding, error) {
	return a.store.WorkItemRunBindings(ctx, workspaceID, workItemID)
}

// pgHumanTasks adapts the shared PostgreSQL store to the HumanTask store port.
type pgHumanTasks struct{ store *postgres.Store }

func (a pgHumanTasks) ReplayTask(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (humantask.HumanTask, bool, error) {
	return a.store.ReplayHumanTaskCommand(ctx, actor, commandType, targetID, requestKey, digest)
}

func (a pgHumanTasks) ReplayContinuation(ctx context.Context, actor, commandType, targetID, requestKey, digest string) (humantask.Continuation, bool, error) {
	return a.store.ReplayHumanContinuation(ctx, actor, commandType, targetID, requestKey, digest)
}

func (a pgHumanTasks) Create(ctx context.Context, mutation humantask.CreateMutation) (humantask.HumanTask, error) {
	return a.store.CreateHumanTask(ctx, mutation)
}

func (a pgHumanTasks) Get(ctx context.Context, workspaceID, taskID string) (humantask.HumanTask, error) {
	return a.store.HumanTask(ctx, workspaceID, taskID)
}

func (a pgHumanTasks) List(ctx context.Context, workspaceID string, openOnly bool) ([]humantask.HumanTask, error) {
	return a.store.ListHumanTasks(ctx, workspaceID, openOnly)
}

func (a pgHumanTasks) Answer(ctx context.Context, mutation humantask.AnswerMutation) (humantask.HumanTask, error) {
	return a.store.AnswerHumanTask(ctx, mutation)
}

func (a pgHumanTasks) ResolveExternalFact(ctx context.Context, mutation humantask.ResolveExternalFactMutation) (humantask.HumanTask, error) {
	return a.store.ResolveHumanTaskFromFact(ctx, mutation)
}

func (a pgHumanTasks) SetState(ctx context.Context, mutation humantask.StateMutation) (humantask.HumanTask, error) {
	return a.store.SetHumanTaskState(ctx, mutation)
}

func (a pgHumanTasks) ExpireBoundTo(
	ctx context.Context,
	workspaceID, objectKind, objectID, exceptVersion, reason, actorID string,
	at time.Time,
) (int, error) {
	return a.store.ExpireHumanTasksBoundTo(ctx, workspaceID, objectKind, objectID, exceptVersion, reason, actorID, at)
}

func (a pgHumanTasks) PendingContinuations(ctx context.Context, workspaceID, runID string) ([]humantask.Continuation, error) {
	return a.store.PendingHumanContinuations(ctx, workspaceID, runID)
}

func (a pgHumanTasks) ConsumeContinuation(ctx context.Context, mutation humantask.ConsumeContinuationMutation) (humantask.Continuation, error) {
	return a.store.ConsumeHumanContinuation(ctx, mutation)
}

// pgDelivery adapts the shared PostgreSQL store to the delivery store port.
type pgDelivery struct{ store *postgres.Store }

func (a pgDelivery) CreateOperation(ctx context.Context, mutation githubdelivery.AdmitMutation) (githubdelivery.ExternalOperation, error) {
	return a.store.CreateExternalOperation(ctx, mutation)
}

func (a pgDelivery) Operation(ctx context.Context, workspaceID, operationID string) (githubdelivery.ExternalOperation, error) {
	return a.store.ExternalOperation(ctx, workspaceID, operationID)
}

func (a pgDelivery) Operations(ctx context.Context, workspaceID, workItemID string) ([]githubdelivery.ExternalOperation, error) {
	return a.store.ExternalOperations(ctx, workspaceID, workItemID)
}

func (a pgDelivery) OperationForRequest(ctx context.Context, workspaceID string, kind githubdelivery.OperationKind, repositoryID, ref, commitOID string) (githubdelivery.ExternalOperation, bool, error) {
	return a.store.ExternalOperationForRequest(ctx, workspaceID, kind, repositoryID, ref, commitOID)
}

func (a pgDelivery) UpdateOperation(ctx context.Context, mutation githubdelivery.UpdateMutation) (githubdelivery.ExternalOperation, error) {
	return a.store.UpdateExternalOperation(ctx, mutation)
}

func (a pgDelivery) RecordObservation(ctx context.Context, mutation githubdelivery.RecordObservationMutation) (githubdelivery.Observation, error) {
	return a.store.RecordGitHubObservation(ctx, mutation)
}

func (a pgDelivery) Observation(ctx context.Context, workspaceID, observationID string) (githubdelivery.Observation, bool, error) {
	return a.store.GitHubObservation(ctx, workspaceID, observationID)
}

func (a pgDelivery) LatestObservations(ctx context.Context, workspaceID, repositoryID string) ([]githubdelivery.Observation, error) {
	return a.store.LatestGitHubObservations(ctx, workspaceID, repositoryID)
}

func (a pgDelivery) UnsettledOperations(ctx context.Context, workspaceID, workItemID string) (int, error) {
	return a.store.UnsettledExternalOperations(ctx, workspaceID, workItemID)
}

// pgEvaluation adapts the shared PostgreSQL store to the evaluation store port.
type pgEvaluation struct{ store *postgres.Store }

func (a pgEvaluation) RecordVerification(ctx context.Context, verification evaluation.Verification, requestKey, requestDigest string) (evaluation.Verification, error) {
	return a.store.RecordVerificationEvidence(ctx, verification, requestKey, requestDigest)
}

func (a pgEvaluation) Verification(ctx context.Context, workspaceID, verificationID string) (evaluation.Verification, bool, error) {
	return a.store.VerificationEvidence(ctx, workspaceID, verificationID)
}

func (a pgEvaluation) VerificationsFor(ctx context.Context, workspaceID, workItemID string) ([]evaluation.Verification, error) {
	return a.store.VerificationsForWorkItem(ctx, workspaceID, workItemID)
}

func (a pgEvaluation) Closure(ctx context.Context, workspaceID, workItemID string) (evaluation.ClosureRecord, bool, error) {
	return a.store.ClosureEvidenceFor(ctx, workspaceID, workItemID)
}

func (a pgEvaluation) KnowledgeBindingExists(ctx context.Context, workspaceID, workItemID, snapshotID, bindingEventID string) (bool, error) {
	return a.store.KnowledgeBindingExists(ctx, workspaceID, workItemID, snapshotID, bindingEventID)
}

func (a pgEvaluation) SettledPublishPullRequest(ctx context.Context, workspaceID, workItemID, runID, repositoryID, commitOID string) (string, bool, error) {
	return a.store.SettledPublishPullRequest(ctx, workspaceID, workItemID, runID, repositoryID, commitOID)
}

func (a pgEvaluation) ReplayedClose(ctx context.Context, actor, workItemID, requestKey, digest string) (evaluation.ClosureRecord, bool, error) {
	return a.store.ReplayedClose(ctx, actor, workItemID, requestKey, digest)
}

func (a pgEvaluation) Close(ctx context.Context, mutation evaluation.CloseMutation) (evaluation.ClosureRecord, error) {
	return a.store.CloseDeliveryWorkItem(ctx, mutation)
}

// ---- source adapters ------------------------------------------------------

// workspaceProjects resolves the single writable project repository of one
// workspace; the Identity/Workspace service owns the binding.
type workspaceProjects struct{ workspaces *workspace.Service }

func (a workspaceProjects) ProjectRepositoryID(ctx context.Context, workspaceID string) (string, error) {
	binding, err := a.workspaces.ProjectBinding(ctx, workspaceID)
	if err != nil {
		return "", workitem.ErrPolicyBlocked
	}
	return binding.GitHubRepositoryID, nil
}

// executionRuns adapts the execution service to the run ports.
type executionRuns struct{ service *execution.Service }

func (a executionRuns) RunView(ctx context.Context, runID string) (execution.RunView, error) {
	return a.service.RunView(ctx, runID)
}

func (a executionRuns) Run(ctx context.Context, runID string) (execution.Run, error) {
	return a.service.Run(ctx, runID)
}

func (a executionRuns) RunSettled(ctx context.Context, runID string) (bool, error) {
	run, err := a.service.Run(ctx, runID)
	if err != nil {
		return false, err
	}
	return run.State.Settled(), nil
}

// workItemFacts adapts the WorkItem service to the read ports.
type workItemFacts struct{ service *workitem.Service }

func (a workItemFacts) Get(ctx context.Context, workspaceID, workItemID string) (workitem.WorkItem, error) {
	return a.service.Get(ctx, workspaceID, workItemID)
}

func (a workItemFacts) RunBindings(ctx context.Context, workspaceID, workItemID string) ([]workitem.RunBinding, error) {
	return a.service.RunBindings(ctx, workspaceID, workItemID)
}

// deliveryEvidence adapts the evaluation service to the delivery gate source.
type deliveryEvidence struct{ service *evaluation.Service }

func (a deliveryEvidence) Verification(ctx context.Context, workspaceID, verificationID string) (evaluation.Verification, bool, error) {
	return a.service.Verification(ctx, workspaceID, verificationID)
}

func (a deliveryEvidence) VerificationsFor(ctx context.Context, workspaceID, workItemID string) ([]evaluation.Verification, error) {
	return a.service.Verifications(ctx, workspaceID, workItemID)
}

func (a deliveryEvidence) Closure(ctx context.Context, workspaceID, workItemID string) (evaluation.ClosureRecord, bool, error) {
	return a.service.Closure(ctx, workspaceID, workItemID)
}

// evaluationFacts reads the closure's bound GitHub facts from the delivery
// store; the GitHub delivery service owns how those rows are produced.
type evaluationFacts struct{ store *postgres.Store }

func (a evaluationFacts) Observation(ctx context.Context, workspaceID, observationID string) (evaluation.ObservationFact, bool, error) {
	observation, found, err := a.store.GitHubObservation(ctx, workspaceID, observationID)
	if err != nil || !found {
		return evaluation.ObservationFact{}, found, err
	}
	fact := evaluation.ObservationFact{
		ObservationID: observation.ObservationID, RepositoryID: observation.RepositoryID,
		SubjectKind: observation.SubjectKind, SubjectKey: observation.SubjectKey,
		Unknown: observation.Unknown,
	}
	if observation.PullRequest != nil {
		fact.PullRequestAuthorID = observation.PullRequest.AuthorID
		fact.HeadOID = observation.PullRequest.HeadOID
		fact.BaseRef = observation.PullRequest.BaseRef
		fact.State = observation.PullRequest.State
		fact.Merged = observation.PullRequest.Merged
		fact.MergeCommitOID = observation.PullRequest.MergeCommitOID
		fact.MergedByID = observation.PullRequest.MergedByID
		fact.MergedByType = observation.PullRequest.MergedByType
	}
	if observation.Review != nil {
		fact.ReviewID = observation.Review.ReviewID
		fact.ReviewerID = observation.Review.ReviewerID
		fact.ReviewAuthorAssociation = observation.Review.AuthorAssociation
		fact.ReviewerType = observation.Review.ReviewerType
		fact.ReviewerPermission = observation.Review.ReviewerPermission
		fact.ReviewState = observation.Review.State
		fact.ReviewCommitOID = observation.Review.CommitOID
	}
	return fact, true, nil
}

// humanTaskObservations reads one recorded observation for the HumanTask
// external-fact check; the settlement rule itself lives in the delivery package.
type humanTaskObservations struct{ store *postgres.Store }

func (a humanTaskObservations) FactMatches(
	ctx context.Context,
	workspaceID, observationID string,
	object humantask.ObjectBinding,
) (bool, error) {
	observation, found, err := a.store.GitHubObservation(ctx, workspaceID, observationID)
	if err != nil || !found {
		return false, err
	}
	return githubdelivery.FactSettlesObject(observation, object.Kind, object.ID, object.Version), nil
}

// humanFactSettler adapts the HumanTask service to the delivery settler port.
type humanFactSettler struct{ service *humantask.Service }

func (a humanFactSettler) ResolveForObject(
	ctx context.Context,
	workspaceID, objectKind, objectID, objectVersion, observationID, actorID string,
) (bool, error) {
	tasks, err := a.service.List(ctx, workspaceID, true)
	if err != nil {
		return false, err
	}
	settled := false
	for _, task := range tasks {
		if task.Request.Mode != humantask.ModeExternalFact {
			continue
		}
		if task.Request.Object.Kind != objectKind || task.Request.Object.ID != objectID ||
			task.Request.Object.Version != objectVersion {
			continue
		}
		if _, err := a.service.ResolveExternalFact(ctx, humantask.ResolveExternalFactCommand{
			WorkspaceID: workspaceID, TaskID: task.ID,
			RequestKey: "fact-" + observationID, ObservationID: observationID, ActorID: actorID,
		}); err != nil {
			return settled, err
		}
		settled = true
	}
	return settled, nil
}

func (a humanFactSettler) ExpireBoundTo(
	ctx context.Context,
	workspaceID, objectKind, objectID, exceptVersion, reason, actorID string,
) (int, error) {
	return a.service.ExpireBoundTo(ctx, workspaceID, objectKind, objectID, exceptVersion, reason, actorID)
}

// githubDeliveryFacts adapts the verified gh observation seam to the delivery
// fact source. Unknown reads are reported as unknown, never as facts.
type githubDeliveryFacts struct{ observer githubobs.DeliveryObserver }

func (a githubDeliveryFacts) PullRequest(ctx context.Context, repositoryID, number string) (githubdelivery.PullRequestProjection, error) {
	observation, err := a.observer.PullRequest(ctx, repositoryID, number)
	projection := githubdelivery.PullRequestProjection{
		RepositoryID: repositoryID, Number: number, ActorID: observation.ActorID,
		Unknown: observation.Unknown, UnknownReason: observation.UnknownReason,
	}
	if err != nil || observation.Unknown {
		return projection, nil
	}
	projection.PullRequest = githubdelivery.PullRequestFact{
		Number: observation.Number, AuthorID: observation.AuthorID, HeadOID: observation.HeadOID,
		HeadRef: observation.HeadRef, BaseRef: observation.BaseRef, State: observation.State,
		Merged: observation.Merged, MergeCommitOID: observation.MergeCommitOID,
		MergedByID: observation.MergedByID, MergedByType: observation.MergedByType,
		MergedAt: observation.MergedAt,
	}
	reviews, err := a.observer.Reviews(ctx, repositoryID, number)
	if err != nil {
		return projection, nil
	}
	permissions := map[string]string{}
	for _, review := range reviews {
		if review.Unknown {
			continue
		}
		fact := githubdelivery.ReviewFact{
			ReviewID: review.ReviewID, ReviewerID: review.ReviewerID,
			ReviewerLogin: review.ReviewerLogin, ReviewerType: review.ReviewerType,
			AuthorAssociation: review.AuthorAssociation, State: review.State,
			CommitOID: review.CommitOID, Submitted: review.SubmittedAt,
		}
		// Only approvals that could satisfy the gate need the permission fact,
		// and the read is bounded: one call per distinct reviewer account.
		if fact.State == "APPROVED" && fact.CommitOID == observation.HeadOID {
			permission, known := permissions[fact.ReviewerLogin]
			if !known && fact.ReviewerLogin != "" {
				found, err := a.observer.ReviewerPermission(ctx, repositoryID, fact.ReviewerLogin, fact.ReviewerID)
				if err != nil {
					found = ""
				}
				permission = found
				permissions[fact.ReviewerLogin] = found
			}
			fact.ReviewerPermission = permission
		}
		projection.Reviews = append(projection.Reviews, fact)
	}
	checks, err := a.observer.Checks(ctx, repositoryID, observation.HeadOID)
	if err == nil && !checks.Unknown {
		projection.Checks = &githubdelivery.ChecksFact{
			CommitOID: checks.CommitOID, State: checks.State,
			Total: checks.Total, Failed: checks.Failed, Pending: checks.Pending,
		}
	}
	return projection, nil
}

func (a githubDeliveryFacts) PullRequestsByHead(ctx context.Context, repositoryID, headBranch string) ([]githubdelivery.PullRequestProjection, error) {
	observations, err := a.observer.PullRequestsByHead(ctx, repositoryID, headBranch)
	if err != nil {
		return nil, err
	}
	projections := make([]githubdelivery.PullRequestProjection, 0, len(observations))
	for _, observation := range observations {
		if observation.Unknown {
			continue
		}
		projections = append(projections, githubdelivery.PullRequestProjection{
			RepositoryID: repositoryID, Number: observation.Number, ActorID: observation.ActorID,
			PullRequest: githubdelivery.PullRequestFact{
				Number: observation.Number, AuthorID: observation.AuthorID, HeadOID: observation.HeadOID,
				HeadRef: observation.HeadRef, BaseRef: observation.BaseRef, State: observation.State,
				Merged: observation.Merged, MergeCommitOID: observation.MergeCommitOID,
				MergedByID: observation.MergedByID, MergedByType: observation.MergedByType,
				MergedAt: observation.MergedAt,
			},
		})
	}
	return projections, nil
}

func (a githubDeliveryFacts) BranchHead(ctx context.Context, repositoryID, ref string) (string, bool, error) {
	observation, err := a.observer.BranchHead(ctx, repositoryID, ref)
	if err != nil || observation.Unknown || observation.OID == "" {
		return "", false, err
	}
	return observation.OID, true, nil
}

// gitRemoteFacts adapts the verified gh observation seam to the git remote
// projection.
type gitRemoteFacts struct{ observer githubobs.DeliveryObserver }

func (a gitRemoteFacts) RepositoryFullName(ctx context.Context, repositoryID string) (string, string, error) {
	observation, err := a.observer.Repository(ctx, repositoryID)
	if err != nil || observation.Unknown || observation.FullName == "" {
		return "", "", err
	}
	return observation.FullName, observation.ActorID, nil
}

func (a gitRemoteFacts) PushPermission(ctx context.Context, repositoryID string) (bool, string, error) {
	observation, err := a.observer.Permission(ctx, repositoryID)
	if err != nil || observation.Unknown {
		return false, "", err
	}
	return observation.Push, observation.ActorID, nil
}

// gitRemoteIdentities reads the registered machine identities of the runtimes
// an admin authorized for the workspace.
type gitRemoteIdentities struct{ runtimeOps *runtimeops.Service }

func (a gitRemoteIdentities) MachineIdentities(ctx context.Context, workspaceID string) ([]string, []string, error) {
	views, err := a.runtimeOps.WorkspaceRuntimes(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	runtimeIDs := make([]string, 0, len(views))
	userIDs := make([]string, 0, len(views))
	seen := map[string]bool{}
	for _, view := range views {
		runtimeIDs = append(runtimeIDs, view.RuntimeID)
		if view.GitHubUserID != "" && !seen[view.GitHubUserID] {
			seen[view.GitHubUserID] = true
			userIDs = append(userIDs, view.GitHubUserID)
		}
	}
	return runtimeIDs, userIDs, nil
}
