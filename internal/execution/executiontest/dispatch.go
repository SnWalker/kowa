package executiontest

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/workflow"
)

// DispatchEnv supplies one adapter for the shared dispatch/decision contract.
// Every adapter publishes the same synthetic compiled plan through its own
// freeze path, so the matrix drives real store transitions rather than mocks.
type DispatchEnv struct {
	Store       execution.Store
	WorkspaceID string
	PublishPlan func(*testing.T, workflow.CompiledPlan)
}

const (
	dispatchDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dispatchDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dispatchOID     = "0123456789abcdef0123456789abcdef01234567"
	dispatchBranch  = "refs/heads/task/kowa-run-1"
)

// DispatchPort applies the same dispatch and decision invariants to each
// adapter. It is the additive orchestration contract of backend:S06: the module
// stays the only producer of NodeRun and Task state, and a caller can only
// materialise a round from already frozen facts.
func DispatchPort(t *testing.T, newEnv func(*testing.T) DispatchEnv) {
	t.Run("attach_first_attempt_to_the_admitted_round", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		outcome, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady)
		if err != nil {
			t.Fatalf("dispatch first attempt: %v", err)
		}
		if outcome.Duplicate {
			t.Fatalf("first dispatch reported a duplicate")
		}
		if outcome.NodeRun.Iteration != 1 || outcome.NodeRun.ID != f.nodeRunID("prepare") {
			t.Fatalf("dispatch created round %+v, want the admitted round 1", outcome.NodeRun)
		}
		if outcome.NodeRun.InputDigest != dispatchDigestA {
			t.Fatalf("round input digest = %s, want the frozen %s", outcome.NodeRun.InputDigest, dispatchDigestA)
		}
		task := outcome.Task
		if task.Attempt != 1 || task.AttemptKind != execution.AttemptInitial || task.State != execution.TaskQueued {
			t.Fatalf("first attempt = %+v", task)
		}
		if task.InputDigest != dispatchDigestA || task.Capability != (execution.Capability{ID: "fixture.prepare", Version: 1}) {
			t.Fatalf("attempt does not carry the frozen input or capability: %+v", task)
		}
		if task.Provider != f.provider || len(task.GitScopes) != 2 || task.DeadlineAt.IsZero() {
			t.Fatalf("attempt does not carry the frozen provider, scopes or deadline: %+v", task)
		}
		lease := f.lease(t)
		if lease.Task.ID != task.ID {
			t.Fatalf("leased %s, want the dispatched attempt %s", lease.Task.ID, task.ID)
		}
	})

	t.Run("create_the_first_round_when_the_node_has_none", func(t *testing.T) {
		// Admission may create only the round of the first node; a later node
		// materialises its first round through the same port.
		f := newDispatchFixtureWithNodes(t, newEnv(t), "finalize")
		outcome, err := f.dispatchInitial(t, "prepare", "prepare-task-1", execution.NodeReady)
		if err != nil {
			t.Fatalf("dispatch first round: %v", err)
		}
		if outcome.NodeRun.Iteration != 1 || outcome.NodeRun.State != execution.NodeReady {
			t.Fatalf("first round = %+v", outcome.NodeRun)
		}
		if outcome.NodeRun.ID != f.nodeRunID("prepare") {
			t.Fatalf("first round id = %s", outcome.NodeRun.ID)
		}
	})

	t.Run("duplicate_identity_is_idempotent", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		first, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady)
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		before := f.snapshot(t)
		// A legal replay keeps working after the run version moved and after the
		// frozen deadline passed: it must not be refused and must not create a
		// second execution.
		f.now = f.now.Add(48 * time.Hour)
		command := f.lastCommand
		command.ExpectedRunVersion = first.NodeRun.Version + 5
		replay, err := f.service.DispatchNode(t.Context(), command)
		if err != nil || !replay.Duplicate {
			t.Fatalf("replay = %+v, %v, want an idempotent duplicate", replay, err)
		}
		if replay.Task.ID != first.Task.ID || replay.NodeRun.ID != first.NodeRun.ID {
			t.Fatalf("replay returned another identity: %+v", replay)
		}
		if after := f.snapshot(t); after != before {
			t.Fatalf("replay changed persisted facts:\nbefore=%s\nafter=%s", before, after)
		}
	})

	t.Run("duplicate_identity_with_other_content_conflicts", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		before := f.snapshot(t)
		command := f.dispatchCommand("prepare", "prepare-task", execution.NodeReady)
		command.InputDigest = dispatchDigestB
		if _, err := f.service.DispatchNode(t.Context(), command); !errors.Is(err, execution.ErrResultConflict) {
			t.Fatalf("same identity with other content = %v, want version conflict", err)
		}
		if after := f.snapshot(t); after != before {
			t.Fatalf("conflicting replay changed persisted facts")
		}
	})

	t.Run("second_attempt_identity_for_the_same_round_is_refused", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		before := f.snapshot(t)
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task-2", execution.NodeReady); !errors.Is(err, execution.ErrRetryNotAllowed) {
			t.Fatalf("second attempt identity = %v, want retry not allowed", err)
		}
		if after := f.snapshot(t); after != before {
			t.Fatalf("refused dispatch changed persisted facts")
		}
	})

	t.Run("revision_requires_a_settled_round", func(t *testing.T) {
		// An unsettled attempt on the current round blocks a revision, whatever
		// its granularity: queued and leased rounds both refuse.
		for _, mode := range []string{"queued", "leased"} {
			t.Run(mode, func(t *testing.T) {
				f := newDispatchFixture(t, newEnv(t), true)
				if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
					t.Fatalf("dispatch: %v", err)
				}
				if mode == "leased" {
					f.lease(t)
				}
				before := f.snapshot(t)
				command := f.dispatchCommand("prepare", "prepare-task-2", execution.NodeReady)
				command.Round = execution.DispatchRound{
					Kind: execution.RoundRevision, SupersedesNodeRunID: f.nodeRunID("prepare"), MaxRounds: 4,
				}
				if _, err := f.service.DispatchNode(t.Context(), command); !errors.Is(err, execution.ErrRetryNotAllowed) {
					t.Fatalf("revision over an unsettled round = %v, want retry not allowed", err)
				}
				if after := f.snapshot(t); after != before {
					t.Fatalf("refused revision changed persisted facts")
				}
			})
		}
	})

	t.Run("revision_supersedes_only_the_settled_current_round", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		f.complete(t, "prepare-task")
		// A supersession identifier that is not the current round is refused.
		foreign := f.dispatchCommand("prepare", "prepare-task-2", execution.NodeReady)
		foreign.Round = execution.DispatchRound{
			Kind: execution.RoundRevision, SupersedesNodeRunID: f.nodeRunID("other"), MaxRounds: 4,
		}
		if _, err := f.service.DispatchNode(t.Context(), foreign); !errors.Is(err, execution.ErrVersionConflict) {
			t.Fatalf("foreign supersession = %v, want version conflict", err)
		}
		// The legal revision opens the next round for the same node.
		revised, err := f.dispatchRevision(t, "prepare", "prepare-task-2", 4)
		if err != nil {
			t.Fatalf("revision: %v", err)
		}
		if revised.NodeRun.Iteration != 2 || revised.NodeRun.State != execution.NodeReady {
			t.Fatalf("revision round = %+v", revised.NodeRun)
		}
		if revised.NodeRun.InputDigest != dispatchDigestA || revised.Task.InputDigest != dispatchDigestA {
			t.Fatalf("revision round does not carry the new frozen input: %+v", revised)
		}
		lease := f.lease(t)
		if lease.Task.ID != revised.Task.ID {
			t.Fatalf("leased %s, want the revision attempt %s", lease.Task.ID, revised.Task.ID)
		}
	})

	t.Run("round_budget_comes_from_the_caller", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		f.complete(t, "prepare-task")
		// MaxRounds=1 is the caller's policy: round 2 exceeds it.
		bounded := f.dispatchCommand("prepare", "prepare-task-2", execution.NodeReady)
		bounded.Round = execution.DispatchRound{
			Kind: execution.RoundRevision, SupersedesNodeRunID: f.nodeRunID("prepare"), MaxRounds: 1,
		}
		if _, err := f.service.DispatchNode(t.Context(), bounded); !errors.Is(err, execution.ErrPolicyBlocked) {
			t.Fatalf("exhausted budget = %v, want policy blocked", err)
		}
		if _, err := f.dispatchRevision(t, "prepare", "prepare-task-2", 2); err != nil {
			t.Fatalf("revision within budget: %v", err)
		}
	})

	t.Run("terminated_and_canceling_runs_refuse_dispatch", func(t *testing.T) {
		t.Run("canceled", func(t *testing.T) {
			f := newDispatchFixture(t, newEnv(t), true)
			run, err := f.service.Run(t.Context(), f.runID)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.CancelRun(t.Context(), f.runID, run.Version); err != nil {
				t.Fatalf("cancel run: %v", err)
			}
			before := f.snapshot(t)
			if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); !errors.Is(err, execution.ErrPolicyBlocked) {
				t.Fatalf("dispatch after cancel = %v, want policy blocked", err)
			}
			if after := f.snapshot(t); after != before {
				t.Fatalf("refused dispatch changed persisted facts")
			}
			// No leasable work exists behind a canceled run.
			if _, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
				RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
			}); !errors.Is(err, execution.ErrCapabilityUnavailable) {
				t.Fatalf("lease after cancel = %v, want no leasable task", err)
			}
		})
		t.Run("paused_by_a_failed_attempt", func(t *testing.T) {
			f := newDispatchFixture(t, newEnv(t), true)
			if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			lease := f.lease(t)
			if _, err := f.service.ReportResult(t.Context(), execution.ResultReport{
				SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
				TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
				Status: execution.ResultFailed, ResultDigest: dispatchDigestB, Summary: "synthetic failure",
				Error: &execution.TaskError{Code: "SYNTHETIC", Message: "synthetic", Retryable: true},
			}); err != nil {
				t.Fatalf("failed report: %v", err)
			}
			if _, err := f.dispatchInitial(t, "execute", "execute-task", execution.NodeReady); !errors.Is(err, execution.ErrPolicyBlocked) {
				t.Fatalf("dispatch into a paused run = %v, want policy blocked", err)
			}
		})
	})

	t.Run("stale_run_version_is_refused", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		command := f.dispatchCommand("prepare", "prepare-task", execution.NodeReady)
		command.ExpectedRunVersion = 7
		if _, err := f.service.DispatchNode(t.Context(), command); !errors.Is(err, execution.ErrVersionConflict) {
			t.Fatalf("stale run version = %v, want version conflict", err)
		}
	})

	t.Run("unknown_system_and_foreign_capability_nodes_are_refused", func(t *testing.T) {
		f := newDispatchFixture(t, newEnv(t), true)
		cases := map[string]func(*execution.DispatchNodeCommand){
			"unknown node": func(c *execution.DispatchNodeCommand) { c.NodeID = "unknown_node" },
			"system node":  func(c *execution.DispatchNodeCommand) { c.NodeID = "finalize" },
			"foreign capability": func(c *execution.DispatchNodeCommand) {
				c.Capability = execution.Capability{ID: "fixture.other", Version: 1}
			},
			"foreign capability version": func(c *execution.DispatchNodeCommand) {
				c.Capability = execution.Capability{ID: "fixture.prepare", Version: 9}
			},
			"past deadline": func(c *execution.DispatchNodeCommand) { c.DeadlineAt = f.now.Add(-time.Second) },
			"knowledge write scope": func(c *execution.DispatchNodeCommand) {
				c.GitScopes = append(c.GitScopes, execution.GitScope{
					RepositoryID: "3002", Role: "knowledge", Operation: "push", Ref: dispatchBranch,
				})
			},
			"unfrozen ref grammar": func(c *execution.DispatchNodeCommand) {
				c.GitScopes = []execution.GitScope{{
					RepositoryID: "3001", Role: "project", Operation: "clone", Ref: "main",
				}}
			},
			// A read scope is a frozen input: a moving branch reference must
			// never stand in for the commit the attempt was admitted against.
			"branch read scope": func(c *execution.DispatchNodeCommand) {
				c.GitScopes = []execution.GitScope{{
					RepositoryID: "3001", Role: "project", Operation: "clone", Ref: dispatchBranch,
				}}
			},
		}
		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				command := f.dispatchCommand("prepare", "prepare-task", execution.NodeReady)
				mutate(&command)
				before := f.snapshot(t)
				if _, err := f.service.DispatchNode(t.Context(), command); err == nil {
					t.Fatalf("%s was accepted", name)
				}
				if after := f.snapshot(t); after != before {
					t.Fatalf("refused dispatch changed persisted facts")
				}
			})
		}
		// A refused dispatch does not consume the node: the legal request after
		// it still succeeds.
		if _, err := f.dispatchInitial(t, "prepare", "prepare-task", execution.NodeReady); err != nil {
			t.Fatalf("legal dispatch after refusals: %v", err)
		}
	})

	t.Run("decide_node_materializes_and_records_control_plane_rounds", func(t *testing.T) {
		f := newDispatchFixtureWithNodes(t, newEnv(t), "prepare")
		controlDigest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		waiting, err := f.service.DecideNode(t.Context(), execution.DecideNodeCommand{
			RunID: f.runID, NodeID: "finalize", NodeRunID: f.nodeRunID("finalize"),
			ExpectedRunVersion: f.runVersion(t), InputDigest: controlDigest,
			Round: execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
			State: execution.NodeWaitingHuman,
		})
		if err != nil || waiting.NodeRun.State != execution.NodeWaitingHuman ||
			waiting.NodeRun.Iteration != 1 || waiting.NodeRun.InputDigest != controlDigest {
			t.Fatalf("waiting transition = %+v, %v", waiting, err)
		}
		decided, err := f.service.DecideNode(t.Context(), execution.DecideNodeCommand{
			RunID: f.runID, NodeID: "finalize", NodeRunID: f.nodeRunID("finalize"),
			ExpectedRunVersion: f.runVersion(t), InputDigest: controlDigest,
			Round: execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
			State: execution.NodeDecided, Verdict: "approved", Output: map[string]any{"detail": "synthetic"},
		})
		if err != nil || decided.NodeRun.State != execution.NodeDecided || decided.NodeRun.Verdict != "approved" {
			t.Fatalf("decision = %+v, %v", decided, err)
		}
		before := f.snapshot(t)
		replay, err := f.service.DecideNode(t.Context(), execution.DecideNodeCommand{
			RunID: f.runID, NodeID: "finalize", NodeRunID: f.nodeRunID("finalize"),
			ExpectedRunVersion: f.runVersion(t), InputDigest: controlDigest,
			Round: execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
			State: execution.NodeDecided, Verdict: "approved", Output: map[string]any{"detail": "synthetic"},
		})
		if err != nil || !replay.Duplicate {
			t.Fatalf("decision replay = %+v, %v", replay, err)
		}
		if after := f.snapshot(t); after != before {
			t.Fatalf("decision replay changed persisted facts")
		}
		if _, err := f.service.DecideNode(t.Context(), execution.DecideNodeCommand{
			RunID: f.runID, NodeID: "finalize", NodeRunID: f.nodeRunID("finalize"),
			ExpectedRunVersion: f.runVersion(t), InputDigest: controlDigest,
			Round: execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
			State: execution.NodeDecided, Verdict: "rejected", Output: map[string]any{"detail": "synthetic"},
		}); !errors.Is(err, execution.ErrResultConflict) {
			t.Fatalf("conflicting decision = %v, want result conflict", err)
		}
		if after := f.snapshot(t); after != before {
			t.Fatalf("conflicting decision changed persisted facts")
		}
		// A capability round is decided only by result acceptance, and a
		// decision without a frozen input digest is not a decision.
		if _, err := f.service.DecideNode(t.Context(), execution.DecideNodeCommand{
			RunID: f.runID, NodeID: "prepare", NodeRunID: f.nodeRunID("prepare"),
			ExpectedRunVersion: f.runVersion(t), InputDigest: controlDigest,
			Round: execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
			State: execution.NodeDecided, Verdict: "approved",
		}); !errors.Is(err, execution.ErrInvalidInput) {
			t.Fatalf("deciding a capability round = %v, want invalid input", err)
		}
		if _, err := f.service.DecideNode(t.Context(), execution.DecideNodeCommand{
			RunID: f.runID, NodeID: "finalize", NodeRunID: f.nodeRunID("finalize") + "-other",
			ExpectedRunVersion: f.runVersion(t), InputDigest: "not-a-digest",
			Round: execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
			State: execution.NodeDecided, Verdict: "approved",
		}); !errors.Is(err, execution.ErrInvalidInput) {
			t.Fatalf("decision without a frozen input digest = %v, want invalid input", err)
		}
	})
}

type dispatchFixture struct {
	t         *testing.T
	env       DispatchEnv
	service   *execution.Service
	now       time.Time
	runID     string
	runtimeID string
	provider  execution.ProviderSelection
	// lastCommand is the command the fixture dispatched most recently, so a
	// replay can reuse the exact frozen intent.
	lastCommand execution.DispatchNodeCommand
}

// newDispatchFixture admits the prepared node's round without a task, exactly
// like run admission does before the first attempt exists.
func newDispatchFixture(t *testing.T, env DispatchEnv, admittedRound bool) *dispatchFixture {
	t.Helper()
	nodeIDs := []string{"finalize"}
	if admittedRound {
		nodeIDs = append([]string{"prepare"}, nodeIDs...)
	}
	return newDispatchFixtureWithNodes(t, env, nodeIDs...)
}

// newDispatchFixtureWithNodes admits exactly the given node rounds, so a case
// can prove how a node without an admitted round materialises its first one.
func newDispatchFixtureWithNodes(t *testing.T, env DispatchEnv, nodeIDs ...string) *dispatchFixture {
	t.Helper()
	name := t.Name()
	f := &dispatchFixture{
		t: t, env: env, now: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC),
		runID: name + "/run", runtimeID: name + "/runtime",
		provider: execution.ProviderSelection{ID: "codex", Version: name + "/provider"},
	}
	f.service = execution.NewService(env.Store, func() time.Time { return f.now })
	env.PublishPlan(t, DispatchPlan(t))
	if err := f.service.RegisterRuntime(t.Context(), execution.RuntimeRegistration{
		ID: f.runtimeID, Epoch: "epoch-1", GitHubUserID: "9001",
		Provider: f.provider, ProviderAuthenticated: true,
		Capabilities: []execution.Capability{{ID: "fixture.prepare", Version: 1}, {ID: "fixture.execute", Version: 1}},
	}); err != nil {
		t.Fatalf("register runtime: %v", err)
	}
	nodes := make([]execution.NodeRun, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodes = append(nodes, f.nodeRun(t, nodeID))
	}
	if err := f.service.CreateRun(t.Context(), execution.CreateRunCommand{Run: f.run(), Nodes: nodes}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return f
}

func (f *dispatchFixture) run() execution.Run {
	return execution.Run{
		ID: f.runID, WorkspaceID: f.env.WorkspaceID, WorkItemID: f.t.Name() + "/work",
		StartedByGitHubUserID: "101", DefinitionName: "dispatch_matrix", DefinitionVersion: 1,
		DefinitionDigest: DispatchPlan(f.t).Digest, State: execution.RunActive, Version: 1,
	}
}

func (f *dispatchFixture) nodeRun(t *testing.T, nodeID string) execution.NodeRun {
	t.Helper()
	return execution.NodeRun{
		ID: f.nodeRunID(nodeID), RunID: f.runID, NodeID: nodeID, Iteration: 1,
		State: execution.NodeReady, InputDigest: dispatchDigestA, Version: 1,
	}
}

func (f *dispatchFixture) nodeRunID(nodeID string) string {
	return fmt.Sprintf("%s/%s-round-1", f.runID, nodeID)
}

// taskID namespaces an attempt under its own run, so two subtests of the same
// adapter never collide on an identity.
func (f *dispatchFixture) taskID(name string) string { return f.runID + "/" + name }

func (f *dispatchFixture) dispatchCommand(nodeID, taskID string, _ execution.NodeState) execution.DispatchNodeCommand {
	capability := execution.Capability{ID: "fixture.prepare", Version: 1}
	if nodeID == "execute" {
		capability = execution.Capability{ID: "fixture.execute", Version: 1}
	}
	return execution.DispatchNodeCommand{
		RunID: f.runID, NodeID: nodeID, NodeRunID: f.nodeRunID(nodeID), TaskID: f.taskID(taskID),
		ExpectedRunVersion: f.runVersion(f.t), InputDigest: dispatchDigestA,
		Capability: capability, Provider: f.provider,
		GitScopes: []execution.GitScope{
			{RepositoryID: "3001", Role: "project", Operation: "clone", Ref: dispatchOID},
			{RepositoryID: "3002", Role: "knowledge", Operation: "clone", Ref: dispatchOID},
		},
		DeadlineAt: f.now.Add(time.Hour),
		Round:      execution.DispatchRound{Kind: execution.RoundInitial, MaxRounds: 4},
	}
}

func (f *dispatchFixture) dispatchInitial(
	t *testing.T,
	nodeID, taskID string,
	state execution.NodeState,
) (execution.DispatchOutcome, error) {
	t.Helper()
	f.lastCommand = f.dispatchCommand(nodeID, taskID, state)
	return f.service.DispatchNode(t.Context(), f.lastCommand)
}

func (f *dispatchFixture) dispatchRevision(
	t *testing.T,
	nodeID, taskID string,
	maxRounds int,
) (execution.DispatchOutcome, error) {
	t.Helper()
	command := f.dispatchCommand(nodeID, taskID, execution.NodeReady)
	command.NodeRunID = fmt.Sprintf("%s/%s-round-2", f.runID, nodeID)
	command.Round = execution.DispatchRound{
		Kind: execution.RoundRevision, SupersedesNodeRunID: f.nodeRunID(nodeID), MaxRounds: maxRounds,
	}
	return f.service.DispatchNode(t.Context(), command)
}

func (f *dispatchFixture) runVersion(t *testing.T) int64 {
	t.Helper()
	run, err := f.service.Run(t.Context(), f.runID)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	return run.Version
}

func (f *dispatchFixture) lease(t *testing.T) execution.TaskLease {
	t.Helper()
	lease, err := f.service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: f.runtimeID, RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	return lease
}

func (f *dispatchFixture) complete(t *testing.T, taskID string) {
	t.Helper()
	lease := f.lease(t)
	if lease.Task.ID != f.taskID(taskID) {
		t.Fatalf("leased %s, want %s", lease.Task.ID, f.taskID(taskID))
	}
	if _, err := f.service.ReportResult(t.Context(), execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: dispatchDigestB, Summary: "synthetic result",
		Output: map[string]any{"verdict": "approved"},
	}); err != nil {
		t.Fatalf("complete %s: %v", taskID, err)
	}
}

// snapshot is a store-agnostic view of the observable run facts, so a refusal
// can be proven to change nothing.
func (f *dispatchFixture) snapshot(t *testing.T) string {
	t.Helper()
	view, err := f.service.RunView(t.Context(), f.runID)
	if err != nil {
		t.Fatalf("run view: %v", err)
	}
	run, err := f.service.Run(t.Context(), f.runID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	facts := map[string]any{"run": run, "view": view}
	tasks := map[string]execution.TaskStatus{}
	for _, candidate := range f.taskIDs(t) {
		status, err := f.service.TaskStatus(t.Context(), candidate)
		if err != nil || status.RunID != f.runID {
			continue
		}
		tasks[candidate] = status
	}
	facts["tasks"] = tasks
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return string(encoded)
}

// taskIDs lists every identity the matrix may have created, so a snapshot does
// not depend on store-specific iteration.
func (f *dispatchFixture) taskIDs(t *testing.T) []string {
	t.Helper()
	names := []string{"prepare-task", "prepare-task-2", "execute-task", "finalize-task"}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		ids = append(ids, f.taskID(name))
	}
	return ids
}

// dispatchPlan is the synthetic plan the matrix runs against. It exercises one
// node with an admitted round, one node materialised later and one
// control-plane node.
func DispatchPlan(t *testing.T) workflow.CompiledPlan {
	t.Helper()
	definition := workflow.Definition{
		SchemaVersion: workflow.SchemaVersion, Kind: workflow.DefinitionKind,
		Name: "dispatch_matrix", Version: 1,
		Nodes: []workflow.Node{
			{
				ID: "prepare", Type: workflow.NodeCapability, DependsOn: []string{}, Inputs: []workflow.InputBinding{},
				Capability: workflow.CapabilityRef{ID: "fixture.prepare", Version: 1}, Mandatory: true,
			},
			{
				ID: "execute", Type: workflow.NodeCapability, DependsOn: []string{"prepare"},
				Inputs: []workflow.InputBinding{{
					Key: "prepare_ref", ProducerNodeID: "prepare", Output: "prepare_ref",
					Type: workflow.ValueArtifactRef, Required: true,
				}},
				Capability: workflow.CapabilityRef{ID: "fixture.execute", Version: 1}, Mandatory: true,
			},
			{
				ID: "finalize", Type: workflow.NodeSystem, DependsOn: []string{"execute"},
				Inputs: []workflow.InputBinding{}, Operation: "fixture.finalize", Mandatory: true,
			},
		},
	}
	catalog := workflow.Catalog{
		{ID: "fixture.prepare", Version: 1}: {
			Input:  workflow.Schema{Fields: map[string]workflow.ValueType{}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{"prepare_ref": workflow.ValueArtifactRef}},
		},
		{ID: "fixture.execute", Version: 1}: {
			Input:  workflow.Schema{Fields: map[string]workflow.ValueType{"prepare_ref": workflow.ValueArtifactRef}},
			Output: workflow.Schema{Fields: map[string]workflow.ValueType{"execute_ref": workflow.ValueArtifactRef}},
		},
	}
	plan, err := workflow.Compile(definition, catalog)
	if err != nil {
		t.Fatalf("compile dispatch matrix plan: %v", err)
	}
	return plan
}
