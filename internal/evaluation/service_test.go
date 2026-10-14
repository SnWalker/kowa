package evaluation_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workitem"
)

const (
	workspaceID = "ws_1"
	workItemID  = "wi_1"
	runID       = "run_1"
	projectRepo = "1401511579"
	headOID     = "de62a4324cad57908376f8c5f3e14bd3bd3dab95"
	mergeOID    = "ca6c6ec8446ec687213d58064a7fd8363050321a"
	reviewerID  = "277460407"
	authorID    = "135722937"
	prNumber    = "42"
	mergeObsID  = "obs_merge_1"
	reviewObsID = "obs_review_1"
)

type fakeWorkItems struct {
	item workitem.WorkItem
}

func (f fakeWorkItems) Get(context.Context, string, string) (workitem.WorkItem, error) {
	return f.item, nil
}

type fakeRuns struct {
	run execution.Run
}

func (f fakeRuns) Run(context.Context, string) (execution.Run, error) { return f.run, nil }

type fakeFacts struct {
	rows map[string]evaluation.ObservationFact
}

func (f fakeFacts) Observation(_ context.Context, _, observationID string) (evaluation.ObservationFact, bool, error) {
	row, found := f.rows[observationID]
	return row, found, nil
}

type stubRunCompleter struct {
	completed []string
}

func (s *stubRunCompleter) CompleteRun(_ context.Context, runID string, _ int64, _ time.Time) (execution.Run, error) {
	s.completed = append(s.completed, runID)
	return execution.Run{ID: runID, State: execution.RunSucceeded}, nil
}

type sequenceIDs struct{ next int }

func (s *sequenceIDs) New() (string, error) {
	s.next++
	return "ev_" + strconv.Itoa(s.next), nil
}

func openWorkItem() workitem.WorkItem {
	return workitem.WorkItem{
		ID: workItemID, WorkspaceID: workspaceID, Title: "Fix the failing test",
		TargetRepositoryID: projectRepo, State: workitem.StateOpen, Version: 3, CurrentRunID: runID,
		CreatedByUserID: "101",
		Requirement: workitem.VersionedContent{
			Version: 1, Content: "make calc add correctly",
			Digest: workitem.ContentDigest("make calc add correctly"),
		},
	}
}

func activeRun() execution.Run {
	return execution.Run{
		ID: runID, WorkspaceID: workspaceID, WorkItemID: workItemID, StartedByGitHubUserID: "101",
		GitExecutionUserID: authorID, State: execution.RunActive, Version: 4,
		DefinitionName: "mvp-delivery", DefinitionVersion: 1,
		DefinitionDigest: workitem.ContentDigest("definition")[:71],
	}
}

func mergedFacts() fakeFacts {
	return fakeFacts{rows: map[string]evaluation.ObservationFact{
		mergeObsID: {
			ObservationID: mergeObsID, RepositoryID: projectRepo, SubjectKind: "pull_request",
			SubjectKey: prNumber, PullRequestAuthorID: authorID, HeadOID: headOID,
			BaseRef: "refs/heads/main", State: "closed", Merged: true, MergeCommitOID: mergeOID,
			MergedByID: reviewerID,
		},
		reviewObsID: {
			ObservationID: reviewObsID, RepositoryID: projectRepo, SubjectKind: "pull_request_review",
			SubjectKey: prNumber + "/5001", ReviewID: "5001", ReviewerID: reviewerID,
			ReviewState: "APPROVED", ReviewCommitOID: headOID,
		},
	}}
}

type harness struct {
	service      *evaluation.Service
	store        *evaluation.MemoryStore
	workItems    *workitem.MemoryStore
	runCompleter *stubRunCompleter
}

func newHarness(t *testing.T, facts evaluation.FactSource) harness {
	t.Helper()
	workItems := workitem.NewMemoryStore()
	created := openWorkItem()
	if _, err := workItems.Create(context.Background(), workitem.CreateMutation{
		WorkItem: created, Actor: workitem.Actor{GitHubUserID: "101"},
		RequestKey: "create", RequestDigest: workitem.Digest("create"),
	}); err != nil {
		t.Fatalf("seed work item: %v", err)
	}
	// The seeded facts carry the versions the closure command expects.
	runCompleter := &stubRunCompleter{}
	store := evaluation.NewMemoryStore().WithCloser(evaluation.NewMemoryCloser(workItems, runCompleter))
	service := evaluation.NewService(store, fakeWorkItems{item: created}, fakeRuns{run: activeRun()},
		facts, &sequenceIDs{}, func() time.Time { return time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC) })
	return harness{service: service, store: store, workItems: workItems, runCompleter: runCompleter}
}

func recordVerification(t *testing.T, service *evaluation.Service, kind, commitOID, result, source string) string {
	t.Helper()
	verification, err := service.RecordVerification(context.Background(), evaluation.RecordVerificationCommand{
		WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID, Kind: kind,
		SubjectKind: "commit", RepositoryID: projectRepo, CommitOID: commitOID,
		ConfigurationVersion: "build-v1", ProducerKind: evaluation.ProducerTask, ProducerID: "task_1",
		Source: source, Result: result, RequestKey: "record-" + kind + "-" + commitOID[:8] + "-" + result + "-" + source,
		ActorID: "101",
	})
	if err != nil {
		t.Fatalf("RecordVerification(%s) error = %v", kind, err)
	}
	return verification.VerificationID
}

func closeCommand(tests, review, postMerge string) evaluation.CloseCommand {
	return evaluation.CloseCommand{
		WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID,
		ExpectedWorkItemVersion: 3, ExpectedRunVersion: 4, ActorID: "101", RequestKey: "close-1",
		PolicyVersion: "delivery-policy-v1", PullRequestNumber: prNumber,
		MergeObservationID: mergeObsID, ReviewObservationID: reviewObsID,
		RequiredTestIDs: []string{tests}, IndependentReviewID: review,
		PostMergeVerificationID: postMerge,
	}
}

func passedChecklist(t *testing.T, harness harness) evaluation.CloseCommand {
	t.Helper()
	tests := recordVerification(t, harness.service, evaluation.KindRequiredTests, headOID, evaluation.ResultPassed, evaluation.SourceReal)
	review := recordVerification(t, harness.service, evaluation.KindIndependentReview, headOID, evaluation.ResultPassed, evaluation.SourceReal)
	postMerge := recordVerification(t, harness.service, evaluation.KindPostMergeIntegration, mergeOID, evaluation.ResultPassed, evaluation.SourceReal)
	return closeCommand(tests, review, postMerge)
}

func TestClosureClosesTheWorkItemAndCompletesTheRunTogether(t *testing.T) {
	harness := newHarness(t, mergedFacts())
	command := passedChecklist(t, harness)
	record, err := harness.service.Close(context.Background(), command)
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if record.MergeCommitOID != mergeOID || record.ReviewHeadOID != headOID ||
		record.ReviewerGitHubUserID != reviewerID || record.ChecklistDigest == "" ||
		record.RequirementDigest != openWorkItem().Requirement.Digest {
		t.Fatalf("Close() record = %#v", record)
	}
	item, err := harness.workItems.Get(context.Background(), workspaceID, workItemID)
	if err != nil || item.State != workitem.StateClosed || item.ClosedAt == nil {
		t.Fatalf("work item after close = %#v, %v", item, err)
	}
	if len(harness.runCompleter.completed) != 1 || harness.runCompleter.completed[0] != runID {
		t.Fatalf("completed runs = %#v", harness.runCompleter.completed)
	}
	replay, err := harness.service.Close(context.Background(), command)
	if err != nil || replay.ClosureID != record.ClosureID {
		t.Fatalf("idempotent Close() = %#v, %v", replay, err)
	}
	stored, found, err := harness.service.Closure(context.Background(), workspaceID, workItemID)
	if err != nil || !found || stored.ClosureID != record.ClosureID {
		t.Fatalf("Closure() = %#v, %v, %v", stored, found, err)
	}
}

// closeCase is one rejected-closure scenario with the harness it ran against.
type closeCase struct {
	harness harness
	command evaluation.CloseCommand
}

func TestClosureRejectsIncompleteOrSimulatedEvidenceAndKeepsTheItemOpen(t *testing.T) {
	cases := map[string]func(t *testing.T) closeCase{
		"missing required test": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.RequiredTestIDs = []string{"unknown-evidence"}
			return closeCase{harness: built, command: command}
		},
		"simulated required test": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.RequiredTestIDs = []string{recordVerification(t, built.service,
				evaluation.KindRequiredTests, headOID, evaluation.ResultPassed, evaluation.SourceSimulated)}
			return closeCase{harness: built, command: command}
		},
		"failed required test": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.RequiredTestIDs = []string{recordVerification(t, built.service,
				evaluation.KindRequiredTests, headOID, evaluation.ResultFailed, evaluation.SourceReal)}
			return closeCase{harness: built, command: command}
		},
		"test from another commit": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.RequiredTestIDs = []string{recordVerification(t, built.service,
				evaluation.KindRequiredTests, mergeOID, evaluation.ResultPassed, evaluation.SourceReal)}
			return closeCase{harness: built, command: command}
		},
		"reviewer is the pull request author": func(t *testing.T) closeCase {
			facts := mergedFacts()
			row := facts.rows[reviewObsID]
			row.ReviewerID = authorID
			facts.rows[reviewObsID] = row
			built := newHarness(t, facts)
			return closeCase{harness: built, command: passedChecklist(t, built)}
		},
		"review applies to an older head": func(t *testing.T) closeCase {
			facts := mergedFacts()
			row := facts.rows[reviewObsID]
			row.ReviewCommitOID = mergeOID
			facts.rows[reviewObsID] = row
			built := newHarness(t, facts)
			return closeCase{harness: built, command: passedChecklist(t, built)}
		},
		"the delivery was not merged": func(t *testing.T) closeCase {
			facts := mergedFacts()
			row := facts.rows[mergeObsID]
			row.Merged = false
			row.MergeCommitOID = ""
			facts.rows[mergeObsID] = row
			built := newHarness(t, facts)
			return closeCase{harness: built, command: passedChecklist(t, built)}
		},
		"stale work item version": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.ExpectedWorkItemVersion = 2
			return closeCase{harness: built, command: command}
		},
		"stale run version": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.ExpectedRunVersion = 3
			return closeCase{harness: built, command: command}
		},
		"run does not hold advancement authority": func(t *testing.T) closeCase {
			built := newHarness(t, mergedFacts())
			command := passedChecklist(t, built)
			command.RunID = "run_other"
			return closeCase{harness: built, command: command}
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			built := build(t)
			if _, err := built.harness.service.Close(context.Background(), built.command); err == nil {
				t.Fatal("Close() succeeded with an invalid checklist")
			}
			item, err := built.harness.workItems.Get(context.Background(), workspaceID, workItemID)
			if err != nil || item.State != workitem.StateOpen || item.Version != 3 {
				t.Fatalf("rejected closure changed facts: %#v, %v", item, err)
			}
			if len(built.harness.runCompleter.completed) != 0 {
				t.Fatalf("rejected closure completed a run: %#v", built.harness.runCompleter.completed)
			}
		})
	}
}

func TestClosureAfterARejectedAttemptStillSucceeds(t *testing.T) {
	harness := newHarness(t, mergedFacts())
	failed := passedChecklist(t, harness)
	failed.RequiredTestIDs = []string{recordVerification(t, harness.service,
		evaluation.KindRequiredTests, headOID, evaluation.ResultFailed, evaluation.SourceReal)}
	if _, err := harness.service.Close(context.Background(), failed); !errors.Is(err, evaluation.ErrPolicyBlocked) {
		t.Fatalf("Close() with a failed test = %v, want ErrPolicyBlocked", err)
	}
	legal := closeCommand("", failed.IndependentReviewID, failed.PostMergeVerificationID)
	legal.RequiredTestIDs = []string{recordVerification(t, harness.service,
		evaluation.KindRequiredTests, headOID, evaluation.ResultPassed, evaluation.SourceReal)}
	legal.RequestKey = "close-2"
	if _, err := harness.service.Close(context.Background(), legal); err != nil {
		t.Fatalf("legal Close() after a rejection error = %v", err)
	}
}

func TestAuxiliaryFailuresDoNotBlockClosure(t *testing.T) {
	harness := newHarness(t, mergedFacts())
	recordVerification(t, harness.service, evaluation.KindKnowledgeSuggestion, headOID, evaluation.ResultFailed, evaluation.SourceReal)
	recordVerification(t, harness.service, evaluation.KindOfflineEvaluation, headOID, evaluation.ResultFailed, evaluation.SourceReal)
	command := passedChecklist(t, harness)
	if _, err := harness.service.Close(context.Background(), command); err != nil {
		t.Fatalf("Close() with auxiliary failures error = %v", err)
	}
	verifications, err := harness.service.Verifications(context.Background(), workspaceID, workItemID)
	if err != nil || len(verifications) != 5 {
		t.Fatalf("Verifications() = %d rows, %v", len(verifications), err)
	}
}

func TestClosureRaceHasASingleWinner(t *testing.T) {
	harness := newHarness(t, mergedFacts())
	command := passedChecklist(t, harness)
	if _, err := harness.service.Close(context.Background(), command); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	racers := []evaluation.CloseCommand{}
	for index, key := range []string{"close-race-1", "close-race-2"} {
		racer := command
		racer.RequestKey = key
		racer.ActorID = strconv.Itoa(300 + index)
		racers = append(racers, racer)
	}
	for _, racer := range racers {
		if _, err := harness.service.Close(context.Background(), racer); !errors.Is(err, evaluation.ErrPolicyBlocked) &&
			!errors.Is(err, evaluation.ErrVersionConflict) {
			t.Fatalf("racing Close(%s) = %v, want a policy or version rejection", racer.RequestKey, err)
		}
	}
	if len(harness.runCompleter.completed) != 1 {
		t.Fatalf("racing closures completed the run %d times", len(harness.runCompleter.completed))
	}
}

func TestRecordVerificationValidatesAndReplays(t *testing.T) {
	harness := newHarness(t, mergedFacts())
	command := evaluation.RecordVerificationCommand{
		WorkspaceID: workspaceID, WorkItemID: workItemID, RunID: runID, Kind: evaluation.KindRequiredTests,
		SubjectKind: "commit", RepositoryID: projectRepo, CommitOID: headOID,
		ConfigurationVersion: "build-v1", ProducerKind: evaluation.ProducerTask, ProducerID: "task_1",
		Source: evaluation.SourceReal, Result: evaluation.ResultPassed, RequestKey: "record-1", ActorID: "101",
	}
	first, err := harness.service.RecordVerification(context.Background(), command)
	if err != nil {
		t.Fatalf("RecordVerification() error = %v", err)
	}
	replay, err := harness.service.RecordVerification(context.Background(), command)
	if err != nil || replay.VerificationID != first.VerificationID {
		t.Fatalf("idempotent RecordVerification() = %#v, %v", replay, err)
	}
	conflict := command
	conflict.Result = evaluation.ResultFailed
	if _, err := harness.service.RecordVerification(context.Background(), conflict); !errors.Is(err, evaluation.ErrVersionConflict) {
		t.Fatalf("same key different content = %v, want ErrVersionConflict", err)
	}
	invalid := map[string]func(*evaluation.RecordVerificationCommand){
		"unknown kind":     func(c *evaluation.RecordVerificationCommand) { c.Kind = "screenshots" },
		"bad commit oid":   func(c *evaluation.RecordVerificationCommand) { c.CommitOID = "HEAD" },
		"unknown source":   func(c *evaluation.RecordVerificationCommand) { c.Source = "maybe" },
		"unknown result":   func(c *evaluation.RecordVerificationCommand) { c.Result = "green" },
		"mixed subject":    func(c *evaluation.RecordVerificationCommand) { c.ArtifactID = "art_1" },
		"no configuration": func(c *evaluation.RecordVerificationCommand) { c.ConfigurationVersion = "" },
		"unknown producer": func(c *evaluation.RecordVerificationCommand) { c.ProducerKind = "agent" },
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			broken := command
			broken.RequestKey = "record-broken"
			mutate(&broken)
			if _, err := harness.service.RecordVerification(context.Background(), broken); !errors.Is(err, evaluation.ErrValidation) {
				t.Fatalf("RecordVerification() error = %v, want ErrValidation", err)
			}
		})
	}
}
