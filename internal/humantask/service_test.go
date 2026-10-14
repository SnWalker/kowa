package humantask_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/humantask"
)

func (s stubWorkItems) AdmissionState(context.Context, string, string) (string, string, error) {
	return "OPEN", "", nil
}

type stubWorkItems struct {
	missing map[string]bool
}

func (s stubWorkItems) WorkItemExists(_ context.Context, workspaceID, workItemID string) (bool, error) {
	return !s.missing[workspaceID+"/"+workItemID], nil
}

type stubObservationSource struct {
	matches map[string]bool
}

func (s stubObservationSource) FactMatches(_ context.Context, _ string, observationID string, _ humantask.ObjectBinding) (bool, error) {
	return s.matches[observationID], nil
}

type sequenceIDs struct{ next int }

func (s *sequenceIDs) New() (string, error) {
	s.next++
	return "ht_" + strconv.Itoa(s.next), nil
}

func newService(t *testing.T, matches map[string]bool) *humantask.Service {
	t.Helper()
	return humantask.NewService(
		humantask.NewMemoryStore(),
		stubObservationSource{matches: matches},
		stubWorkItems{},
		&sequenceIDs{},
		func() time.Time { return time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) },
	)
}

func createCommand() humantask.CreateCommand {
	return humantask.CreateCommand{
		WorkspaceID: "ws_1", WorkItemID: "wi_1", RunID: "run_1", NodeRunID: "nr_1",
		ActorID: "101", RequestKey: "create-1", Title: "Approve the plan",
		Kind: "plan_approval", Mode: humantask.ModeKowaAnswer,
		Fields: []humantask.Field{
			{Name: "decision", Type: "decision", Required: true, Options: []string{"approved", "rejected"}},
			{Name: "comment", Type: "comment"},
		},
		Object:     humantask.ObjectBinding{Kind: "plan_artifact", ID: "art_1", Version: "sha256:" + nonce64('a')},
		Candidates: []string{"101", "202"},
	}
}

func nonce64(fill byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = fill
	}
	return string(value)
}

func mustCreate(t *testing.T, service *humantask.Service, command humantask.CreateCommand) humantask.HumanTask {
	t.Helper()
	task, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return task
}

func TestCreateFreezesAOpenVersionedRequest(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	if task.State != humantask.StateOpen || task.Request.Version != 1 || task.RequestDigest == "" {
		t.Fatalf("Create() = %#v", task)
	}
	if task.Request.Object.ID != "art_1" || len(task.Request.Candidates) != 2 {
		t.Fatalf("Create() object = %#v", task.Request.Object)
	}
}

func TestExternalFactTasksAreRefusedAHumanAnswer(t *testing.T) {
	service := newService(t, map[string]bool{"obs_1": true})
	command := createCommand()
	command.Mode = humantask.ModeExternalFact
	command.Kind = "github_review"
	command.Candidates = nil
	command.Roles = nil
	command.Object = humantask.ObjectBinding{
		Kind: "pull_request_review", ID: "42", Version: "b99754c271e7c8b759e9affa7d9fc875625f6d8c",
	}
	task := mustCreate(t, service, command)
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"},
		humantask.AnswerCommand{
			WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-1", RequestVersion: 1,
			Object: task.Request.Object, Decision: humantask.DecisionApproved,
			Values: map[string]string{"decision": "approved"},
		}); !errors.Is(err, humantask.ErrPolicyBlocked) {
		t.Fatalf("Answer() on an external_fact task = %v, want ErrPolicyBlocked", err)
	}
}

func TestAnswerRejectsAnExpiredTask(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	if _, err := service.Expire(context.Background(), humantask.ExpireCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, ActorID: "135722937", Reason: "the PR head moved",
	}); err != nil {
		t.Fatalf("Expire() error = %v", err)
	}
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"},
		humantask.AnswerCommand{
			WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-late", RequestVersion: 1,
			Object: task.Request.Object, Decision: humantask.DecisionApproved,
			Values: map[string]string{"decision": "approved"},
		}); !errors.Is(err, humantask.ErrPolicyBlocked) {
		t.Fatalf("late Answer() = %v, want ErrPolicyBlocked", err)
	}
	current, err := service.Get(context.Background(), "ws_1", task.ID)
	if err != nil || current.State != humantask.StateExpired || current.Response != nil {
		t.Fatalf("late answer changed facts: %#v, %v", current, err)
	}
}

func TestAnswerRejectsADriftedObjectVersion(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	drifted := task.Request.Object
	drifted.Version = "sha256:" + nonce64('b')
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"},
		humantask.AnswerCommand{
			WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-drift", RequestVersion: 1,
			Object: drifted, Decision: humantask.DecisionApproved,
			Values: map[string]string{"decision": "approved"},
		}); !errors.Is(err, humantask.ErrVersionConflict) {
		t.Fatalf("Answer() with a drifted object = %v, want ErrVersionConflict", err)
	}
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"},
		humantask.AnswerCommand{
			WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-old-request", RequestVersion: 0,
			Object: task.Request.Object, Decision: humantask.DecisionApproved,
			Values: map[string]string{"decision": "approved"},
		}); !errors.Is(err, humantask.ErrVersionConflict) {
		t.Fatalf("Answer() with an old request version = %v, want ErrVersionConflict", err)
	}
}

func TestAnswerIsIdempotentAndConflictsOnDifferentContent(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	answer := humantask.AnswerCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-1", RequestVersion: 1,
		Object: task.Request.Object, Decision: humantask.DecisionApproved,
		Values: map[string]string{"decision": "approved", "comment": "lgtm"},
	}
	resolved, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"}, answer)
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if resolved.State != humantask.StateResolved || resolved.Response == nil ||
		resolved.Response.ActorID != "101" || resolved.Response.PayloadDigest == "" {
		t.Fatalf("Answer() = %#v", resolved)
	}
	if resolved.Continuation == nil || resolved.Continuation.State != humantask.ContinuationPending ||
		resolved.Continuation.Intent != humantask.IntentResumeNode {
		t.Fatalf("Answer() continuation = %#v", resolved.Continuation)
	}
	replay, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"}, answer)
	if err != nil || replay.Response == nil || replay.Response.ResponseID != resolved.Response.ResponseID {
		t.Fatalf("idempotent Answer() = %#v, %v", replay, err)
	}
	conflicting := answer
	conflicting.Values = map[string]string{"decision": "rejected"}
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"}, conflicting); !errors.Is(err, humantask.ErrVersionConflict) {
		t.Fatalf("same key different content = %v, want ErrVersionConflict", err)
	}
}

func TestAnswerRequiresAnAuthorizedCandidate(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	answer := humantask.AnswerCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-1", RequestVersion: 1,
		Object: task.Request.Object, Decision: humantask.DecisionApproved,
		Values: map[string]string{"decision": "approved"},
	}
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "303"}, answer); !errors.Is(err, humantask.ErrForbidden) {
		t.Fatalf("Answer() by a non-candidate = %v, want ErrForbidden", err)
	}
	if _, err := service.Answer(context.Background(), humantask.Actor{}, answer); !errors.Is(err, humantask.ErrValidation) {
		t.Fatalf("Answer() without an authenticated actor = %v, want ErrValidation", err)
	}
	// A role assignment authorizes a current member holding that role even when
	// the individual is not listed as a candidate.
	roleCommand := createCommand()
	roleCommand.RequestKey = "create-role"
	roleCommand.Candidates = nil
	roleCommand.Roles = []humantask.Role{humantask.RoleDeveloper}
	roleTask := mustCreate(t, service, roleCommand)
	roleAnswer := answer
	roleAnswer.TaskID = roleTask.ID
	roleAnswer.Object = roleTask.Request.Object
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "303", Roles: []humantask.Role{humantask.RoleDeveloper}}, roleAnswer); err != nil {
		t.Fatalf("Answer() by an authorized role = %v", err)
	}
	if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "303", Roles: []humantask.Role{humantask.RoleViewer}}, answer); !errors.Is(err, humantask.ErrForbidden) {
		t.Fatalf("Answer() by a viewer = %v, want ErrForbidden", err)
	}
}

func TestAnswerValidatesTheStructuredPayload(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	cases := map[string]humantask.AnswerCommand{
		"unknown field": func() humantask.AnswerCommand {
			return humantask.AnswerCommand{
				WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-unknown", RequestVersion: 1,
				Object: task.Request.Object, Decision: humantask.DecisionApproved,
				Values: map[string]string{"decision": "approved", "smuggled": "value"},
			}
		}(),
		"missing required field": func() humantask.AnswerCommand {
			return humantask.AnswerCommand{
				WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-missing", RequestVersion: 1,
				Object: task.Request.Object, Decision: humantask.DecisionApproved,
				Values: map[string]string{"comment": "no decision"},
			}
		}(),
		"invalid decision value": func() humantask.AnswerCommand {
			return humantask.AnswerCommand{
				WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-bad-decision", RequestVersion: 1,
				Object: task.Request.Object, Decision: "maybe",
				Values: map[string]string{"decision": "approved"},
			}
		}(),
		"decision disagrees with values": func() humantask.AnswerCommand {
			return humantask.AnswerCommand{
				WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-mismatch", RequestVersion: 1,
				Object: task.Request.Object, Decision: humantask.DecisionRejected,
				Values: map[string]string{"decision": "approved"},
			}
		}(),
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"}, command); !errors.Is(err, humantask.ErrValidation) {
				t.Fatalf("Answer() error = %v, want ErrValidation", err)
			}
		})
	}
}

func TestExternalFactResolutionRequiresAReconciledFact(t *testing.T) {
	service := newService(t, map[string]bool{"obs_matching": true})
	command := createCommand()
	command.Mode = humantask.ModeExternalFact
	command.Kind = "github_merge"
	command.Object = humantask.ObjectBinding{Kind: "pull_request_merge", ID: "42", Version: "b99754c271e7c8b759e9affa7d9fc875625f6d8c"}
	command.Candidates = nil
	task := mustCreate(t, service, command)

	if _, err := service.ResolveExternalFact(context.Background(), humantask.ResolveExternalFactCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "resolve-unknown", ObservationID: "obs_unknown",
		ActorID: "135722937",
	}); !errors.Is(err, humantask.ErrPolicyBlocked) {
		t.Fatalf("Resolve with an unmatched observation = %v, want ErrPolicyBlocked", err)
	}
	unsettled, err := service.Get(context.Background(), "ws_1", task.ID)
	if err != nil || unsettled.State != humantask.StateOpen {
		t.Fatalf("failed resolve changed facts: %#v, %v", unsettled, err)
	}

	resolved, err := service.ResolveExternalFact(context.Background(), humantask.ResolveExternalFactCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "resolve-1", ObservationID: "obs_matching",
		ActorID: "135722937",
	})
	if err != nil {
		t.Fatalf("ResolveExternalFact() error = %v", err)
	}
	if resolved.State != humantask.StateResolved || resolved.Response != nil ||
		resolved.ResolutionKind != string(humantask.ModeExternalFact) || resolved.ExternalFactID != "obs_matching" {
		t.Fatalf("ResolveExternalFact() = %#v", resolved)
	}
	replay, err := service.ResolveExternalFact(context.Background(), humantask.ResolveExternalFactCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "resolve-1", ObservationID: "obs_matching",
		ActorID: "135722937",
	})
	if err != nil || replay.State != humantask.StateResolved {
		t.Fatalf("idempotent Resolve = %#v, %v", replay, err)
	}
}

func TestExpireBoundToOnlyInvalidatesTheSupersededVersion(t *testing.T) {
	service := newService(t, nil)
	old := mustCreate(t, service, createCommand())
	command := createCommand()
	command.RequestKey = "create-2"
	command.Object.Version = "sha256:" + nonce64('b')
	current := mustCreate(t, service, command)
	expired, err := service.ExpireBoundTo(context.Background(), "ws_1",
		old.Request.Object.Kind, old.Request.Object.ID, current.Request.Object.Version,
		"the object moved to a new version", "135722937")
	if err != nil || expired != 1 {
		t.Fatalf("ExpireBoundTo() = %d, %v", expired, err)
	}
	oldTask, err := service.Get(context.Background(), "ws_1", old.ID)
	if err != nil || oldTask.State != humantask.StateExpired || oldTask.ExpiredReason == "" {
		t.Fatalf("expired task = %#v, %v", oldTask, err)
	}
	currentTask, err := service.Get(context.Background(), "ws_1", current.ID)
	if err != nil || currentTask.State != humantask.StateOpen {
		t.Fatalf("current task = %#v, %v", currentTask, err)
	}
}

func TestCreateRefusesAWorkItemOfAnotherWorkspace(t *testing.T) {
	store := humantask.NewMemoryStore()
	service := humantask.NewService(store, stubObservationSource{},
		stubWorkItems{missing: map[string]bool{"ws_1/wi_1": true}}, &sequenceIDs{}, time.Now)
	if _, err := service.Create(context.Background(), createCommand()); !errors.Is(err, humantask.ErrForbidden) {
		t.Fatalf("Create() for a foreign work item = %v, want ErrForbidden", err)
	}
}

func TestContinuationsAreListedAndConsumedOnce(t *testing.T) {
	service := newService(t, nil)
	task := mustCreate(t, service, createCommand())
	resolved, err := service.Answer(context.Background(), humantask.Actor{GitHubUserID: "101"}, humantask.AnswerCommand{
		WorkspaceID: "ws_1", TaskID: task.ID, RequestKey: "answer-1", RequestVersion: 1,
		Object: task.Request.Object, Decision: humantask.DecisionApproved,
		Values: map[string]string{"decision": "approved"},
	})
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	pending, err := service.PendingContinuations(context.Background(), "ws_1", "run_1")
	if err != nil || len(pending) != 1 || pending[0].ResponseID != resolved.Response.ResponseID {
		t.Fatalf("PendingContinuations() = %#v, %v", pending, err)
	}
	consumed, err := service.ConsumeContinuation(context.Background(), humantask.ConsumeContinuationMutation{
		ContinuationID: pending[0].ID, WorkspaceID: "ws_1", ActorID: "101",
		At: time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC),
	})
	if err != nil || consumed.State != humantask.ContinuationConsumed || consumed.ConsumedAt == nil {
		t.Fatalf("ConsumeContinuation() = %#v, %v", consumed, err)
	}
	remaining, err := service.PendingContinuations(context.Background(), "ws_1", "run_1")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("PendingContinuations() after consume = %#v, %v", remaining, err)
	}
}
