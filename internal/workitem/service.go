package workitem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Store performs the atomic, idempotent, versioned writes and reads of the
// WorkItem aggregate. Implementations must enforce version checks, single
// effective cancellation and single-run advancement authority under one
// transaction or one lock.
type Store interface {
	Create(ctx context.Context, mutation CreateMutation) (WorkItem, error)
	Get(ctx context.Context, workspaceID, workItemID string) (WorkItem, error)
	List(ctx context.Context, workspaceID string) ([]WorkItem, error)
	Cancel(ctx context.Context, mutation CancelMutation) (WorkItem, error)
	AssociateRun(ctx context.Context, mutation AssociateRunMutation) (WorkItem, error)
	RunBindings(ctx context.Context, workspaceID, workItemID string) ([]RunBinding, error)
}

// Service implements the WorkItem use cases. Creating a WorkItem records the
// frozen intake and stops there: no workflow run is started.
type Service struct {
	store      Store
	projects   ProjectBindingSource
	runs       RunSource
	operations ExternalOperationGuard
	ids        IDGenerator
	now        func() time.Time
}

func NewService(
	store Store,
	projects ProjectBindingSource,
	runs RunSource,
	operations ExternalOperationGuard,
	ids IDGenerator,
	now func() time.Time,
) *Service {
	return &Service{
		store: store, projects: projects, runs: runs,
		operations: operations, ids: ids, now: now,
	}
}

// Create records one OPEN WorkItem with frozen goal, requirement and
// acceptance versions. The target repository comes from the Workspace project
// binding, so a second writable repository can never enter here.
func (s *Service) Create(
	ctx context.Context,
	actor Actor,
	command CreateCommand,
) (WorkItem, error) {
	if !validActor(actor) || !validRequestKey(command.RequestKey) {
		return WorkItem{}, ErrValidation
	}
	title := strings.TrimSpace(command.Title)
	if title == "" || len(title) > maxTitleLength ||
		!validVersionedContent(command.Requirement) || !validVersionedContent(command.Acceptance) {
		return WorkItem{}, ErrValidation
	}
	projectRepositoryID, err := s.projects.ProjectRepositoryID(ctx, command.WorkspaceID)
	if err != nil {
		return WorkItem{}, err
	}
	if !externalIDPattern.MatchString(projectRepositoryID) {
		return WorkItem{}, fmt.Errorf("%w: workspace project binding is unavailable", ErrPolicyBlocked)
	}
	id, err := s.ids.New()
	if err != nil {
		return WorkItem{}, fmt.Errorf("generate work item id: %w", err)
	}
	now := s.now().UTC()
	item := WorkItem{
		ID: id, WorkspaceID: command.WorkspaceID, Title: title,
		TargetRepositoryID: projectRepositoryID,
		Requirement:        freeze(command.Requirement),
		Acceptance:         freeze(command.Acceptance),
		State:              StateOpen, Version: 1,
		CreatedByUserID: actor.GitHubUserID, CreatedAt: now, UpdatedAt: now,
	}
	return s.store.Create(ctx, CreateMutation{
		WorkItem: item, Actor: actor, RequestKey: command.RequestKey,
		RequestDigest: Digest("workitem.create", command.WorkspaceID, title,
			fmt.Sprint(item.Requirement.Version), item.Requirement.Digest,
			fmt.Sprint(item.Acceptance.Version), item.Acceptance.Digest),
		TargetProjectID: projectRepositoryID,
	})
}

// Get returns one WorkItem of the given Workspace. Membership is the caller's
// authority and is re-read per request by the HTTP layer; a WorkItem of another
// Workspace is indistinguishable from a missing one.
func (s *Service) Get(ctx context.Context, workspaceID, workItemID string) (WorkItem, error) {
	if workspaceID == "" || workItemID == "" {
		return WorkItem{}, ErrValidation
	}
	return s.store.Get(ctx, workspaceID, workItemID)
}

// List returns the WorkItems of one Workspace, newest first.
func (s *Service) List(ctx context.Context, workspaceID string) ([]WorkItem, error) {
	if workspaceID == "" {
		return nil, ErrValidation
	}
	items, err := s.store.List(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []WorkItem{}
	}
	return items, nil
}

// Cancel terminates the goal. Cancellation is refused while any external
// operation of this WorkItem is still pending or unknown, and it never claims
// that already-performed external side effects were rolled back.
func (s *Service) Cancel(ctx context.Context, actor Actor, command CancelCommand) (WorkItem, error) {
	if !validActor(actor) || !validRequestKey(command.RequestKey) ||
		command.WorkspaceID == "" || command.WorkItemID == "" || command.ExpectedVersion < 1 {
		return WorkItem{}, ErrValidation
	}
	item, err := s.store.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Version != command.ExpectedVersion {
		return WorkItem{}, ErrVersionConflict
	}
	if item.State != StateOpen {
		return WorkItem{}, fmt.Errorf("%w: only an OPEN work item can be canceled", ErrPolicyBlocked)
	}
	if s.operations != nil {
		unsettled, err := s.operations.UnsettledOperations(ctx, command.WorkspaceID, command.WorkItemID)
		if err != nil {
			return WorkItem{}, err
		}
		if unsettled > 0 {
			return WorkItem{}, fmt.Errorf("%w: %d external operations are not reconciled", ErrExternalUnknown, unsettled)
		}
	}
	return s.store.Cancel(ctx, CancelMutation{
		WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		ExpectedVersion: command.ExpectedVersion, Actor: actor,
		RequestKey: command.RequestKey, Reason: strings.TrimSpace(command.Reason),
		RequestDigest: Digest("workitem.cancel", command.WorkspaceID, command.WorkItemID,
			fmt.Sprint(command.ExpectedVersion)),
		CanceledAt: s.now().UTC(),
	})
}

// AssociateRun binds the run that holds advancement authority. The initial
// binding requires an unbound WorkItem; a repair binding must name the current
// run as its source and the source run must have ended, so a new run can never
// take over while the previous one is still live.
func (s *Service) AssociateRun(ctx context.Context, command AssociateRunCommand) (WorkItem, error) {
	if !validRequestKey(command.RequestKey) || !externalIDPattern.MatchString(command.ActorID) ||
		command.WorkspaceID == "" || command.WorkItemID == "" || command.RunID == "" ||
		command.ExpectedVersion < 1 {
		return WorkItem{}, ErrValidation
	}
	if command.Kind != BindingInitial && command.Kind != BindingRepair {
		return WorkItem{}, ErrValidation
	}
	item, err := s.store.Get(ctx, command.WorkspaceID, command.WorkItemID)
	if err != nil {
		return WorkItem{}, err
	}
	if item.Version != command.ExpectedVersion {
		return WorkItem{}, ErrVersionConflict
	}
	if item.State != StateOpen {
		return WorkItem{}, fmt.Errorf("%w: a %s work item cannot bind a run", ErrPolicyBlocked, item.State)
	}
	now := s.now().UTC()
	mutation := AssociateRunMutation{
		WorkItemID: command.WorkItemID, ExpectedVersion: command.ExpectedVersion,
		Binding: RunBinding{
			WorkItemID: command.WorkItemID, RunID: command.RunID, Kind: command.Kind,
			BoundByUserID: command.ActorID, BoundAt: now,
		},
		RequestKey: command.RequestKey, BoundAt: now,
		RequestDigest: Digest("workitem.associate_run", command.WorkspaceID, command.WorkItemID,
			command.RunID, string(command.Kind), fmt.Sprint(command.ExpectedVersion)),
	}
	switch command.Kind {
	case BindingInitial:
		if item.CurrentRunID != "" && item.CurrentRunID != command.RunID {
			return WorkItem{}, fmt.Errorf("%w: the work item already has a current run", ErrPolicyBlocked)
		}
	case BindingRepair:
		if command.SourceRunID == "" || item.CurrentRunID != command.SourceRunID {
			return WorkItem{}, fmt.Errorf("%w: a repair run must replace the current run", ErrPolicyBlocked)
		}
		if command.Reason == "" {
			return WorkItem{}, ErrValidation
		}
		settled, err := s.runs.RunSettled(ctx, command.SourceRunID)
		if err != nil {
			return WorkItem{}, err
		}
		if !settled {
			return WorkItem{}, fmt.Errorf("%w: the source run has not ended", ErrPolicyBlocked)
		}
		if command.MergedBaselineOID != "" && !oidPattern.MatchString(command.MergedBaselineOID) {
			return WorkItem{}, ErrValidation
		}
		mutation.Binding.SourceRunID = command.SourceRunID
		mutation.Binding.Reason = command.Reason
		mutation.Binding.MergedBaselineOID = command.MergedBaselineOID
	}
	return s.store.AssociateRun(ctx, mutation)
}

// RunBindings returns the run lineage of one WorkItem.
func (s *Service) RunBindings(ctx context.Context, workspaceID, workItemID string) ([]RunBinding, error) {
	if workspaceID == "" || workItemID == "" {
		return nil, ErrValidation
	}
	bindings, err := s.store.RunBindings(ctx, workspaceID, workItemID)
	if err != nil {
		return nil, err
	}
	if bindings == nil {
		bindings = []RunBinding{}
	}
	return bindings, nil
}

// freeze stamps the server-computed digest on one intake version.
func freeze(value VersionedContent) VersionedContent {
	return VersionedContent{Version: value.Version, Content: value.Content, Digest: ContentDigest(value.Content)}
}

func validActor(actor Actor) bool {
	return externalIDPattern.MatchString(actor.GitHubUserID)
}

func validRequestKey(key string) bool {
	return requestKeyPattern.MatchString(key)
}

// Digest is the frozen request-digest rule for command idempotency: the sha256
// of the NUL-joined canonical parts, without the "sha256:" display prefix.
func Digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
