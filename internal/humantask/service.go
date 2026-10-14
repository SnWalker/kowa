package humantask

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Actor is the authenticated caller for answer commands. Roles are the current
// workspace roles of that member, re-read by the HTTP layer on every request.
type Actor struct {
	GitHubUserID string
	Roles        []Role
}

// Service implements the HumanTask use cases.
type Service struct {
	store        Store
	observations ObservationSource
	ids          IDGenerator
	now          func() time.Time
}

func NewService(store Store, observations ObservationSource, ids IDGenerator, now func() time.Time) *Service {
	return &Service{store: store, observations: observations, ids: ids, now: now}
}

// Create records one OPEN, versioned human request. External-fact tasks bind a
// GitHub object; answering them from a browser is impossible by construction.
func (s *Service) Create(ctx context.Context, command CreateCommand) (HumanTask, error) {
	request, err := s.frozenRequest(command)
	if err != nil {
		return HumanTask{}, err
	}
	if !validRequestKey(command.RequestKey) || !validActor(command.ActorID) ||
		command.WorkspaceID == "" || command.WorkItemID == "" {
		return HumanTask{}, ErrValidation
	}
	createDigest := Digest("humantask.create", command.WorkspaceID, command.WorkItemID,
		command.RequestKey, requestDigest(request))
	if replay, found, err := s.store.ReplayTask(ctx, command.ActorID, "humantask.create",
		command.WorkspaceID, command.RequestKey, createDigest); err != nil || found {
		return replay, err
	}
	id, err := s.ids.New()
	if err != nil {
		return HumanTask{}, fmt.Errorf("generate human task id: %w", err)
	}
	now := s.now().UTC()
	task := HumanTask{
		ID: id, WorkspaceID: command.WorkspaceID, WorkItemID: command.WorkItemID,
		RunID: command.RunID, NodeRunID: command.NodeRunID,
		Request: request, RequestDigest: requestDigest(request),
		State: StateOpen, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	return s.store.Create(ctx, CreateMutation{
		Task: task, ActorID: command.ActorID, RequestKey: command.RequestKey,
		RequestDigest: createDigest,
	})
}

// Get returns one task of the workspace.
func (s *Service) Get(ctx context.Context, workspaceID, taskID string) (HumanTask, error) {
	if workspaceID == "" || taskID == "" {
		return HumanTask{}, ErrValidation
	}
	return s.store.Get(ctx, workspaceID, taskID)
}

// List returns the workspace tasks, newest first; openOnly filters to OPEN.
func (s *Service) List(ctx context.Context, workspaceID string, openOnly bool) ([]HumanTask, error) {
	if workspaceID == "" {
		return nil, ErrValidation
	}
	tasks, err := s.store.List(ctx, workspaceID, openOnly)
	if err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []HumanTask{}
	}
	return tasks, nil
}

// Answer accepts one authenticated kowa_answer: the actor must be a current
// candidate, the echoed request and object versions must still be the frozen
// ones, and the structured payload must satisfy the frozen field definition.
// The response, the resolution and the continuation intent commit together.
func (s *Service) Answer(ctx context.Context, actor Actor, command AnswerCommand) (HumanTask, error) {
	if !validActor(actor.GitHubUserID) || !validRequestKey(command.RequestKey) ||
		command.WorkspaceID == "" || command.TaskID == "" {
		return HumanTask{}, ErrValidation
	}
	if command.Decision != DecisionApproved && command.Decision != DecisionRejected &&
		command.Decision != DecisionSubmitted {
		return HumanTask{}, ErrValidation
	}
	task, err := s.store.Get(ctx, command.WorkspaceID, command.TaskID)
	if err != nil {
		return HumanTask{}, err
	}
	answerDigest := Digest("humantask.answer", task.ID, fmt.Sprint(command.RequestVersion),
		command.Object.Kind, command.Object.ID, command.Object.Version,
		command.Decision, valuesDigest(command.Decision, command.Values))
	if replay, found, err := s.store.ReplayTask(ctx, actor.GitHubUserID, "humantask.answer",
		task.ID, command.RequestKey, answerDigest); err != nil || found {
		return replay, err
	}
	if task.Request.Mode != ModeKowaAnswer {
		return HumanTask{}, fmt.Errorf(
			"%w: an external_fact request is settled only by a reconciled fact", ErrPolicyBlocked)
	}
	if task.State != StateOpen {
		return HumanTask{}, fmt.Errorf("%w: the request is %s", ErrPolicyBlocked, task.State)
	}
	if command.RequestVersion != task.Request.Version || command.Object != task.Request.Object {
		return HumanTask{}, fmt.Errorf(
			"%w: the answer targets request version %d object %s@%s, the current request is version %d object %s@%s",
			ErrVersionConflict, command.RequestVersion, command.Object.ID, command.Object.Version,
			task.Request.Version, task.Request.Object.ID, task.Request.Object.Version)
	}
	if !Assigned(task.Request, actor.GitHubUserID, actor.Roles) {
		return HumanTask{}, fmt.Errorf("%w: the member is not assigned to this request", ErrForbidden)
	}
	values, err := validateValues(task.Request, command.Decision, command.Values)
	if err != nil {
		return HumanTask{}, err
	}
	responseID, err := s.ids.New()
	if err != nil {
		return HumanTask{}, fmt.Errorf("generate human response id: %w", err)
	}
	continuationID, err := s.ids.New()
	if err != nil {
		return HumanTask{}, fmt.Errorf("generate continuation id: %w", err)
	}
	now := s.now().UTC()
	payloadDigest := valuesDigest(command.Decision, values)
	response := Response{
		ResponseID: responseID, TaskID: task.ID, WorkspaceID: task.WorkspaceID,
		RequestVersion: task.Request.Version, Object: task.Request.Object,
		ActorID: actor.GitHubUserID, Decision: command.Decision, Values: values,
		PayloadDigest: payloadDigest, CreatedAt: now,
	}
	continuation := Continuation{
		ID: continuationID, ResponseID: responseID, TaskID: task.ID,
		WorkspaceID: task.WorkspaceID, RunID: task.RunID, NodeRunID: task.NodeRunID,
		Intent: IntentResumeNode, State: ContinuationPending, CreatedAt: now,
	}
	return s.store.Answer(ctx, AnswerMutation{
		TaskID: task.ID, WorkspaceID: task.WorkspaceID, ActorID: actor.GitHubUserID,
		RequestKey: command.RequestKey, RequestDigest: answerDigest,
		Response: response, Continuation: continuation,
	})
}

// ResolveExternalFact settles an external_fact task from a reconciled
// observation. The observation must exist, belong to the workspace and match
// the task's bound object; no HumanResponse is produced.
func (s *Service) ResolveExternalFact(ctx context.Context, command ResolveExternalFactCommand) (HumanTask, error) {
	if !validRequestKey(command.RequestKey) || !validActor(command.ActorID) ||
		command.WorkspaceID == "" || command.TaskID == "" || command.ObservationID == "" {
		return HumanTask{}, ErrValidation
	}
	task, err := s.store.Get(ctx, command.WorkspaceID, command.TaskID)
	if err != nil {
		return HumanTask{}, err
	}
	resolveDigest := Digest("humantask.resolve_external", task.ID,
		fmt.Sprint(task.Request.Version), command.ObservationID)
	if replay, found, err := s.store.ReplayTask(ctx, command.ActorID, "humantask.resolve_external",
		task.ID, command.RequestKey, resolveDigest); err != nil || found {
		return replay, err
	}
	if task.Request.Mode != ModeExternalFact {
		return HumanTask{}, fmt.Errorf("%w: only an external_fact request is settled by a fact", ErrPolicyBlocked)
	}
	if task.State != StateOpen {
		return HumanTask{}, fmt.Errorf("%w: the request is %s", ErrPolicyBlocked, task.State)
	}
	if s.observations == nil {
		return HumanTask{}, fmt.Errorf("%w: no observation source is configured", ErrPolicyBlocked)
	}
	matches, err := s.observations.FactMatches(ctx, command.WorkspaceID, command.ObservationID, task.Request.Object)
	if err != nil {
		return HumanTask{}, err
	}
	if !matches {
		return HumanTask{}, fmt.Errorf(
			"%w: observation %s does not establish the bound object %s@%s",
			ErrPolicyBlocked, command.ObservationID, task.Request.Object.ID, task.Request.Object.Version)
	}
	return s.store.ResolveExternalFact(ctx, ResolveExternalFactMutation{
		TaskID: task.ID, WorkspaceID: task.WorkspaceID, ActorID: command.ActorID,
		RequestKey: command.RequestKey, RequestDigest: resolveDigest,
		ObservationID: command.ObservationID, ResolvedAt: s.now().UTC(),
	})
}

// Expire invalidates one task whose bound object or request version moved on.
// A late answer to an expired task is refused by Answer.
func (s *Service) Expire(ctx context.Context, command ExpireCommand) (HumanTask, error) {
	return s.setState(ctx, command.WorkspaceID, command.TaskID, command.ActorID, StateExpired, command.Reason)
}

// Cancel cancels a task whose target was canceled.
func (s *Service) Cancel(ctx context.Context, command CancelCommand) (HumanTask, error) {
	return s.setState(ctx, command.WorkspaceID, command.TaskID, command.ActorID, StateCanceled, command.Reason)
}

func (s *Service) setState(
	ctx context.Context,
	workspaceID, taskID, actorID string,
	state State,
	reason string,
) (HumanTask, error) {
	if workspaceID == "" || taskID == "" || reason == "" || !validActor(actorID) {
		return HumanTask{}, ErrValidation
	}
	return s.store.SetState(ctx, StateMutation{
		TaskID: taskID, WorkspaceID: workspaceID, State: state, Reason: reason,
		ActorID: actorID, At: s.now().UTC(),
	})
}

// ExpireBoundTo invalidates every OPEN task bound to one exact object version.
// The GitHub delivery path calls it when an observed head moves, so an old
// approval can never authorize a newer version.
func (s *Service) ExpireBoundTo(ctx context.Context, workspaceID string, object ObjectBinding, reason string) (int, error) {
	if workspaceID == "" || reason == "" || !validObject(object) {
		return 0, ErrValidation
	}
	return s.store.ExpireBoundTo(ctx, workspaceID, object, reason, s.now().UTC())
}

// PendingContinuations lists the accepted answers one run still has to consume.
func (s *Service) PendingContinuations(ctx context.Context, workspaceID, runID string) ([]Continuation, error) {
	if workspaceID == "" {
		return nil, ErrValidation
	}
	continuations, err := s.store.PendingContinuations(ctx, workspaceID, runID)
	if err != nil {
		return nil, err
	}
	if continuations == nil {
		continuations = []Continuation{}
	}
	return continuations, nil
}

// ConsumeContinuation marks one continuation intent as consumed by the workflow
// that dispatched the follow-up task. The workflow stays the only party that
// starts a new task; this package only records that the intent was taken up.
func (s *Service) ConsumeContinuation(ctx context.Context, command ConsumeContinuationMutation) (Continuation, error) {
	if command.WorkspaceID == "" || command.ContinuationID == "" || !validActor(command.ActorID) {
		return Continuation{}, ErrValidation
	}
	if command.At.IsZero() {
		command.At = s.now().UTC()
	}
	digest := Digest(command.ContinuationID)
	if replay, found, err := s.store.ReplayContinuation(ctx, command.ActorID, "humantask.consume_continuation",
		command.ContinuationID, "consume", digest); err != nil || found {
		return replay, err
	}
	return s.store.ConsumeContinuation(ctx, command)
}

// frozenRequest validates one request definition and freezes version 1.
func (s *Service) frozenRequest(command CreateCommand) (Request, error) {
	title := strings.TrimSpace(command.Title)
	if title == "" || len(title) > 300 ||
		!kindPattern.MatchString(command.Kind) ||
		(command.Mode != ModeKowaAnswer && command.Mode != ModeExternalFact) {
		return Request{}, ErrValidation
	}
	fields := make([]Field, 0, len(command.Fields))
	seen := map[string]bool{}
	for _, field := range command.Fields {
		if !fieldNamePattern.MatchString(field.Name) || seen[field.Name] ||
			(field.Type != "text" && field.Type != "decision" && field.Type != "choice" && field.Type != "comment") {
			return Request{}, ErrValidation
		}
		if field.Type == "choice" && len(field.Options) == 0 {
			return Request{}, ErrValidation
		}
		for _, option := range field.Options {
			if option == "" || len(option) > 200 {
				return Request{}, ErrValidation
			}
		}
		seen[field.Name] = true
		fields = append(fields, Field{
			Name: field.Name, Type: field.Type, Required: field.Required,
			Options: append([]string{}, field.Options...),
		})
	}
	if !validObject(command.Object) {
		return Request{}, ErrValidation
	}
	candidates := make([]string, 0, len(command.Candidates))
	candidateSeen := map[string]bool{}
	for _, candidate := range command.Candidates {
		if !validActor(candidate) || candidateSeen[candidate] {
			return Request{}, ErrValidation
		}
		candidateSeen[candidate] = true
		candidates = append(candidates, candidate)
	}
	slices.Sort(candidates)
	roles := make([]Role, 0, len(command.Roles))
	roleSeen := map[Role]bool{}
	for _, role := range command.Roles {
		if !validRole(role) || roleSeen[role] {
			return Request{}, ErrValidation
		}
		roleSeen[role] = true
		roles = append(roles, role)
	}
	slices.Sort(roles)
	if command.Mode == ModeKowaAnswer && len(candidates) == 0 && len(roles) == 0 {
		return Request{}, ErrValidation
	}
	if command.Mode == ModeExternalFact {
		// External facts are GitHub pull-request facts bound to one head oid.
		if command.Object.Digest != "" || !externalFactObjectKinds[command.Object.Kind] ||
			!oidPattern.MatchString(command.Object.Version) || !externalIDPattern.MatchString(command.Object.ID) {
			return Request{}, ErrValidation
		}
	}
	return Request{
		Version: 1, Title: title, Kind: command.Kind, Mode: command.Mode,
		Fields: fields, Object: command.Object, Candidates: candidates, Roles: roles,
	}, nil
}

// validateValues checks the submitted structure against the frozen fields.
func validateValues(request Request, decision string, values map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(values))
	for name, value := range values {
		field, found := findField(request, name)
		if !found {
			return nil, fmt.Errorf("%w: unknown answer field %q", ErrValidation, name)
		}
		if value == "" {
			continue
		}
		if len(value) > 4000 {
			return nil, ErrValidation
		}
		switch field.Type {
		case "decision":
			if value != DecisionApproved && value != DecisionRejected {
				return nil, fmt.Errorf("%w: field %q only accepts approved or rejected", ErrValidation, name)
			}
		case "choice":
			if !slices.Contains(field.Options, value) {
				return nil, fmt.Errorf("%w: field %q does not accept %q", ErrValidation, name, value)
			}
		}
		normalized[name] = value
	}
	for _, field := range request.Fields {
		if field.Required && normalized[field.Name] == "" {
			return nil, fmt.Errorf("%w: field %q is required", ErrValidation, field.Name)
		}
	}
	if decisionField, found := findField(request, "decision"); found && decisionField.Type == "decision" {
		if normalized["decision"] != decision {
			return nil, fmt.Errorf("%w: decision %q disagrees with field value %q", ErrValidation, decision, normalized["decision"])
		}
	}
	return normalized, nil
}

func findField(request Request, name string) (Field, bool) {
	for _, field := range request.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// Assigned is the frozen assignment rule: the caller must be a listed
// candidate or hold one of the assigned workspace roles.
func Assigned(request Request, githubUserID string, roles []Role) bool {
	if slices.Contains(request.Candidates, githubUserID) {
		return true
	}
	for _, role := range request.Roles {
		if slices.Contains(roles, role) {
			return true
		}
	}
	return false
}

func requestDigest(request Request) string {
	parts := []string{"humantask.request", fmt.Sprint(request.Version), request.Kind, string(request.Mode), request.Title,
		request.Object.Kind, request.Object.ID, request.Object.Version, request.Object.Digest}
	for _, field := range request.Fields {
		parts = append(parts, field.Name, field.Type, fmt.Sprint(field.Required), strings.Join(field.Options, ","))
	}
	parts = append(parts, strings.Join(request.Candidates, ","))
	roles := make([]string, 0, len(request.Roles))
	for _, role := range request.Roles {
		roles = append(roles, string(role))
	}
	parts = append(parts, strings.Join(roles, ","))
	return "sha256:" + Digest(parts...)
}

// valuesDigest freezes the payload identity so an identical replay is
// idempotent and different content under the same request key conflicts. Wire
// digests carry the frozen sha256 prefix; only the command-idempotency table
// stores the bare hex form.
func valuesDigest(decision string, values map[string]string) string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.SortFunc(names, cmp.Compare[string])
	parts := []string{"humantask.values", decision}
	for _, name := range names {
		parts = append(parts, name, values[name])
	}
	return "sha256:" + Digest(parts...)
}

// externalFactObjectKinds are the frozen GitHub objects an external_fact task
// may bind: a review or a merge of one pull request at one head oid.
var externalFactObjectKinds = map[string]bool{
	"pull_request_review": true,
	"pull_request_merge":  true,
}

func validObject(object ObjectBinding) bool {
	if !kindPattern.MatchString(object.Kind) || object.ID == "" || len(object.ID) > 200 ||
		object.Version == "" || len(object.Version) > 200 {
		return false
	}
	if object.Digest != "" && !digestPattern.MatchString(object.Digest) {
		return false
	}
	return true
}

func validRole(role Role) bool {
	return role == RoleAdmin || role == RoleDeveloper || role == RoleViewer
}
