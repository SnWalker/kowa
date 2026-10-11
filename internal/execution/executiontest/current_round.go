package executiontest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

// CurrentRoundEnv supplies a Store and read-only snapshots of every persisted
// run, node and task field. The suite drives all transitions through Service.
type CurrentRoundEnv struct {
	Store            execution.Store
	DefinitionDigest string
	WorkspaceID      string
	Facts            func(*testing.T, string) any
	TaskState        func(*testing.T, string) execution.TaskState
}

// CurrentRound applies the same supersession and late-report invariants to each adapter.
func CurrentRound(t *testing.T, newEnv func(*testing.T) CurrentRoundEnv) {
	t.Helper()
	t.Run("admitted_history_dispatch", func(t *testing.T) {
		// CreateRun permits frozen rounds together. Dispatch must independently
		// enforce current-round identity, even without a preceding Rerun call.
		f := newRoundFixtureWithHistory(t, newEnv(t), true)
		current := f.lease(t)
		if current.Task.ID != f.newTask {
			t.Fatalf("dispatch selected old admitted round: %+v", current)
		}
		f.complete(t, current)
	})
	for _, kind := range []string{"retry", "resume"} {
		t.Run("superseded_recovery/"+kind, func(t *testing.T) {
			f := newRoundFixture(t, newEnv(t))
			old := f.lease(t)
			report := roundResult(old, "failed")
			if kind == "resume" {
				report.Status = execution.ResultAwaitingInput
				report.Error = nil
				report.InputRequest = map[string]any{"requestKey": "clarify"}
			}
			if _, err := f.service.ReportResult(t.Context(), report); err != nil {
				t.Fatal(err)
			}
			f.next(t, false)
			current := f.lease(t)
			currentReport := roundResult(current, "failed")
			if kind == "resume" {
				currentReport.Status = execution.ResultAwaitingInput
				currentReport.Error = nil
				currentReport.InputRequest = map[string]any{"requestKey": "current-clarify"}
			}
			if _, err := f.service.ReportResult(t.Context(), currentReport); err != nil {
				t.Fatal(err)
			}
			before := f.env.Facts(t, f.runID)
			_, err := f.recoverOld(t, kind)
			if !errors.Is(err, execution.ErrRetryNotAllowed) {
				t.Errorf("superseded %s = %v, want retry not allowed", kind, err)
			}
			f.unchanged(t, before)
			recoveryID := f.runID + "/current-recovery"
			var recovery execution.Task
			if kind == "retry" {
				recovery, err = f.service.RetryTask(t.Context(), current.Task.ID, recoveryID)
			} else {
				recovery, err = f.service.ResumeTask(
					t.Context(), current.Task.ID, recoveryID, "current-answer", resultDigestB, nil,
				)
			}
			validRound := recovery.NodeRunID == f.newNode && recovery.InputDigest == resultDigestB
			if err != nil || !validRound {
				t.Fatalf("current %s = %+v, %v", kind, recovery, err)
			}
			lease := f.lease(t)
			if lease.Task.ID != recoveryID {
				t.Fatalf("current recovery dispatched wrong attempt: %+v", lease)
			}
			f.complete(t, lease)
		})
	}
	for _, revise := range []bool{false, true} {
		mode := "rerun"
		if revise {
			mode = "revise"
		}
		t.Run(mode, func(t *testing.T) {
			t.Run("task_id_collision", func(t *testing.T) {
				f := newRoundFixture(t, newEnv(t))
				old := f.lease(t)
				f.complete(t, old)
				before := f.env.Facts(t, f.runID)
				f.newTask = f.oldTask
				_, err := f.rerun(t, revise)
				if !errors.Is(err, execution.ErrVersionConflict) {
					t.Errorf("existing task ID = %v, want version conflict", err)
				}
				f.unchanged(t, before)
				accepted, err := f.service.ReportResult(t.Context(), roundResult(old, "completed"))
				if err != nil || !accepted.Duplicate || accepted.Applied {
					t.Errorf("accepted history after collision = %+v, %v", accepted, err)
				}
				f.unchanged(t, before)
				f.newTask = f.runID + "/fresh-task"
				f.next(t, revise)
				lease := f.lease(t)
				if lease.Task.ID != f.newTask {
					t.Fatalf("fresh attempt = %+v", lease)
				}
				f.complete(t, lease)
			})
			for _, kind := range []string{"failed", "completed", "progress"} {
				t.Run("queued/"+kind, func(t *testing.T) {
					f := newRoundFixture(t, newEnv(t))
					f.next(t, revise)
					if state := f.env.TaskState(t, f.oldTask); state != execution.TaskCanceled {
						t.Errorf("superseded queued task = %s, want CANCELED", state)
					}
					current := f.lease(t)
					isCurrentRound := current.Task.ID == f.newTask && current.Task.NodeRunID == f.newNode
					if !isCurrentRound || current.Task.InputDigest != resultDigestB {
						t.Fatalf("lease belongs to superseded round: %+v", current)
					}
					// No lease was ever issued to this canceled task. A guessed identity is not authority.
					old := execution.TaskLease{LeaseID: "unissued", FencingToken: 1, Task: execution.TaskSpec{ID: f.oldTask}}
					f.rejectLate(t, old, kind)
					f.complete(t, current)
				})
			}
			for _, running := range []bool{false, true} {
				state := "leased"
				if running {
					state = "running"
				}
				t.Run("inflight/"+state, func(t *testing.T) {
					f := newRoundFixture(t, newEnv(t))
					lease := f.lease(t)
					if running {
						if err := f.progress(t, lease); err != nil {
							t.Fatal(err)
						}
					}
					before := f.env.Facts(t, f.runID)
					_, err := f.rerun(t, revise)
					if !errors.Is(err, execution.ErrRetryNotAllowed) {
						t.Errorf("inflight supersession = %v, want retry not allowed", err)
					}
					f.unchanged(t, before)
					f.complete(t, lease)
				})
			}
		})
	}
	for _, kind := range []string{"failed", "completed", "progress"} {
		t.Run("expired_old/"+kind, func(t *testing.T) {
			f := newRoundFixture(t, newEnv(t))
			old := f.lease(t)
			f.now = old.ExpiresAt
			if err := f.progress(t, old); !errors.Is(err, execution.ErrStaleLease) {
				t.Fatalf("expiry = %v", err)
			}
			if state := f.env.TaskState(t, f.oldTask); state != execution.TaskExpired {
				t.Fatalf("expiry state = %s", state)
			}
			f.next(t, false)
			current := f.lease(t)
			f.rejectLate(t, old, kind)
			f.complete(t, current)
		})
	}
	t.Run("accepted_old_identity", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		old := f.lease(t)
		f.complete(t, old)
		f.next(t, false)
		current := f.lease(t)
		before := f.env.Facts(t, f.runID)
		report := roundResult(old, "completed")
		accepted, err := f.service.ReportResult(t.Context(), report)
		if err != nil || !accepted.Duplicate || accepted.Applied {
			t.Errorf("accepted old duplicate = %+v, %v", accepted, err)
		}
		f.unchanged(t, before)
		report.ResultDigest = resultDigestB
		if _, err := f.service.ReportResult(t.Context(), report); !errors.Is(err, execution.ErrResultConflict) {
			t.Errorf("old conflict = %v", err)
		}
		f.unchanged(t, before)
		for _, field := range []string{"lease", "fencing"} {
			wrong := roundResult(old, "completed")
			if field == "lease" {
				wrong.LeaseID = "wrong"
			} else {
				wrong.FencingToken++
			}
			if _, err := f.service.ReportResult(t.Context(), wrong); !errors.Is(err, execution.ErrStaleLease) {
				t.Errorf("old %s = %v", field, err)
			}
			f.unchanged(t, before)
		}
		f.complete(t, current)
	})
	t.Run("retry_queued_superseded", func(t *testing.T) {
		f := newRoundFixture(t, newEnv(t))
		old := f.lease(t)
		if _, err := f.service.ReportResult(t.Context(), roundResult(old, "failed")); err != nil {
			t.Fatal(err)
		}
		retryID := f.runID + "/retry"
		retry, err := f.service.RetryTask(t.Context(), f.oldTask, retryID)
		if err != nil || retry.InputDigest != resultDigestA {
			t.Fatalf("retry = %+v, %v", retry, err)
		}
		f.next(t, false)
		if state := f.env.TaskState(t, retryID); state != execution.TaskCanceled {
			t.Errorf("superseded retry = %s", state)
		}
		current := f.lease(t)
		if current.Task.ID != f.newTask {
			t.Fatalf("leased superseded retry: %+v", current)
		}
		f.complete(t, current)
	})
}

type roundFixture struct {
	env                                         CurrentRoundEnv
	service                                     *execution.Service
	now                                         time.Time
	runID, oldTask, newTask, newNode, runtimeID string
}

func newRoundFixture(t *testing.T, env CurrentRoundEnv) *roundFixture {
	return newRoundFixtureWithHistory(t, env, false)
}

func newRoundFixtureWithHistory(t *testing.T, env CurrentRoundEnv, history bool) *roundFixture {
	t.Helper()
	name := t.Name()
	f := &roundFixture{
		env: env, now: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC),
		runID: name + "/run", oldTask: name + "/task-1", newTask: name + "/task-2",
		newNode: name + "/node-2", runtimeID: name + "/runtime",
	}
	f.service = execution.NewService(env.Store, func() time.Time { return f.now })
	provider := execution.ProviderSelection{ID: "codex", Version: name}
	registration := execution.RuntimeRegistration{
		ID: f.runtimeID, Epoch: "epoch-1", GitHubUserID: "9001",
		Provider: provider, ProviderAuthenticated: true,
		Capabilities: []execution.Capability{{ID: "plan.create", Version: 1}},
	}
	if err := f.service.RegisterRuntime(t.Context(), registration); err != nil {
		t.Fatal(err)
	}
	command := execution.CreateRunCommand{
		Run: execution.Run{
			ID: f.runID, WorkspaceID: env.WorkspaceID, WorkItemID: name + "/work", StartedByGitHubUserID: "101",
			DefinitionName: "delivery", DefinitionVersion: 1, DefinitionDigest: env.DefinitionDigest,
			State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: name + "/node-1", RunID: f.runID, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: resultDigestA, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: f.oldTask, NodeRunID: name + "/node-1", Attempt: 1, AttemptKind: execution.AttemptInitial,
			Capability: execution.Capability{ID: "plan.create", Version: 1}, Provider: provider,
			GitScopes: []execution.GitScope{}, State: execution.TaskQueued,
			InputDigest: resultDigestA, DeadlineAt: f.now.Add(time.Hour),
		}},
	}
	if history {
		command.Nodes = append(command.Nodes, execution.NodeRun{
			ID: f.newNode, RunID: f.runID, NodeID: "plan", Iteration: 2,
			State: execution.NodeReady, InputDigest: resultDigestB, Version: 1,
		})
		task := command.Tasks[0]
		task.ID = f.newTask
		task.NodeRunID = f.newNode
		task.InputDigest = resultDigestB
		command.Tasks = append(command.Tasks, task)
	}
	if err := f.service.CreateRun(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *roundFixture) rerun(t *testing.T, revise bool) (execution.NodeRun, error) {
	t.Helper()
	if revise {
		return f.service.ReviseNode(
			t.Context(), f.runID, "plan", f.newNode, f.newTask, resultDigestB,
		)
	}
	return f.service.RerunNode(
		t.Context(), f.runID, "plan", f.newNode, f.newTask, resultDigestB,
	)
}
func (f *roundFixture) next(t *testing.T, revise bool) {
	t.Helper()
	next, err := f.rerun(t, revise)
	if err != nil || next.Iteration != 2 {
		t.Fatalf("new round = %+v, %v", next, err)
	}
}
func (f *roundFixture) lease(t *testing.T) execution.TaskLease {
	t.Helper()
	lease, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
func (f *roundFixture) progress(t *testing.T, lease execution.TaskLease) error {
	t.Helper()
	return f.service.ReportProgress(t.Context(), execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.ProgressEventKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Sequence: 1, Phase: "working", Message: "synthetic", ObservedAt: f.now,
	})
}
func roundResult(lease execution.TaskLease, kind string) execution.ResultReport {
	report := execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: resultDigestA,
		Summary: "synthetic result", Output: map[string]any{"verdict": "approved"},
	}
	if kind == "failed" {
		report.Status = execution.ResultFailed
		report.Output = nil
		report.Error = &execution.TaskError{Code: "SYNTHETIC_FAILURE", Message: "fake", Retryable: true}
	}
	return report
}
func (f *roundFixture) complete(t *testing.T, lease execution.TaskLease) {
	t.Helper()
	acceptance, err := f.service.ReportResult(t.Context(), roundResult(lease, "completed"))
	if err != nil || !acceptance.Applied || acceptance.Duplicate {
		t.Fatalf("legal report = %+v, %v", acceptance, err)
	}
	view, err := f.service.RunView(t.Context(), f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 {
		t.Fatalf("current node projection = %+v", view)
	}
	current := view.Nodes[0]
	if current.ID != lease.Task.NodeRunID || current.State != execution.NodeDecided {
		t.Fatalf("legal report did not decide its current round: %+v", view)
	}
	if view.State != execution.RunActive {
		t.Fatalf("legal report changed business run state: %+v", view)
	}
}
func (f *roundFixture) unchanged(t *testing.T, before any) {
	t.Helper()
	if after := f.env.Facts(t, f.runID); !reflect.DeepEqual(before, after) {
		t.Errorf("rejection changed persisted facts/versions:\nbefore=%+v\nafter=%+v", before, after)
	}
}
func (f *roundFixture) rejectLate(t *testing.T, old execution.TaskLease, kind string) {
	t.Helper()
	before := f.env.Facts(t, f.runID)
	var err error
	if kind == "progress" {
		err = f.progress(t, old)
	} else {
		var acceptance execution.ResultAcceptance
		acceptance, err = f.service.ReportResult(t.Context(), roundResult(old, kind))
		if acceptance.Applied || acceptance.Duplicate {
			t.Errorf("old report accepted: %+v", acceptance)
		}
	}
	if !errors.Is(err, execution.ErrStaleLease) {
		t.Errorf("old %s = %v, want stale lease", kind, err)
	}
	f.unchanged(t, before)
}

func (f *roundFixture) recoverOld(t *testing.T, kind string) (execution.Task, error) {
	t.Helper()
	if kind == "retry" {
		return f.service.RetryTask(t.Context(), f.oldTask, f.runID+"/obsolete-retry")
	}
	return f.service.ResumeTask(
		t.Context(),
		f.oldTask,
		f.runID+"/obsolete-resume",
		"synthetic-answer",
		resultDigestB,
		nil,
	)
}
