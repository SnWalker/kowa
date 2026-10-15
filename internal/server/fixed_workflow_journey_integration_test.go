//go:build integration

package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/githubobs"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/server"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

const (
	journeyAdminID             = "7001"
	journeyAdminLogin          = "journey-admin"
	journeyHumanID             = "7002"
	journeyHumanLogin          = "journey-reviewer"
	journeyMachineID           = "7003"
	journeyProjectRepository   = "9001"
	journeyKnowledgeRepository = "9002"
	journeyRuntimeID           = "runtime-journey"
	journeyRuntimeEpoch        = "epoch-journey"
	journeyRuntimeToken        = "journey-runtime-token-0123456789"
)

// TestFixedWorkflowJourney drives the whole fixed workflow through the real
// control plane: a real HTTPS surface and the real PostgreSQL authority, with
// only the external adapters scripted (an OAuth stub, a scripted GitHub
// observation and a scripted Runner that leases over the frozen transport).
func TestFixedWorkflowJourney(t *testing.T) {
	h := startJourney(t)

	workspaceID := h.createWorkspace(t, "journey")
	h.configureRepositories(t, workspaceID)
	h.publishExecutionConfig(t, workspaceID)
	h.registerRuntime(t)

	t.Run("knowledge_confirmation_gates_the_fixed_flow", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Add a deterministic greeting helper")
		admission := h.startRun(t, workspaceID, workItemID)
		if admission.Admission.ProjectCommitOID != h.projectHead(t) {
			t.Fatalf("the admitted baseline is %s, the frozen project head is %s",
				admission.Admission.ProjectCommitOID, h.projectHead(t))
		}
		if !strings.HasPrefix(admission.Admission.WorkingBranchRef, "refs/heads/task/") {
			t.Fatalf("the admitted working branch is %q", admission.Admission.WorkingBranchRef)
		}
		if admission.Recommendation == nil || len(admission.Recommendation.Candidates) == 0 {
			t.Fatalf("admission did not recommend knowledge candidates: %+v", admission.Recommendation)
		}
		if admission.Recommendation.HumanTaskID == "" {
			t.Fatalf("the admission carries no confirmation request: %+v", admission.Recommendation)
		}
		// No attempt may exist before the human confirmation.
		h.expectNoLease(t, "no attempt may be dispatched before the knowledge confirmation")

		// A branch that advances after admission never changes a frozen input.
		h.advanceProjectBranch(t)
		h.answerHumanTask(t, workspaceID, admission.Recommendation.HumanTaskID,
			"approved", map[string]string{"selection": h.indexSelection(t)})
		h.waitFor(t, func() bool {
			journey := h.journey(t, workspaceID, workItemID)
			return journey.Run != nil && journey.Run.DecidedNodes >= 3
		}, "the knowledge step must confirm and bind the frozen selection")

		plan := h.leaseCapability(t, "plan.create")
		if plan.Task.InputDigest == "" {
			t.Fatal("the dispatched attempt carries no frozen input digest")
		}
		h.expectFrozenScopes(t, plan, admission.Admission, false)
		h.reportResult(t, plan, "completed", digestOf("plan"), map[string]any{
			"verdict": "approved", "summary": "plan",
		})
	})

	t.Run("fixed_flow_reaches_closure_through_the_frozen_gates", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Close the greeting delivery")
		h.startRun(t, workspaceID, workItemID)
		view := h.drive(t, workspaceID, workItemID, driveOptions{})
		if view.WorkItem.State != "CLOSED" || view.Closure == nil {
			t.Fatalf("the journey did not close: %+v", view)
		}
		if view.Run != nil && view.Run.State != "SUCCEEDED" {
			t.Fatalf("the delivery run ended %s", view.Run.State)
		}
		// The delivered commit is a real commit of the local remote.
		if commit := h.machineCommit(); !isOID(commit) {
			t.Fatalf("the delivered commit is not an oid: %q", commit)
		}
	})

	t.Run("post_merge_failure_keeps_the_item_open_and_repair_takes_over", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Fail the post-merge verification")
		admission := h.startRun(t, workspaceID, workItemID)
		view := h.drive(t, workspaceID, workItemID, driveOptions{
			failIntegration: true, stopWhenDecided: "integration",
		})
		if view.WorkItem.State != "OPEN" {
			t.Fatalf("a failed post-merge verification must keep the item open: %+v", view)
		}
		if view.Closure != nil {
			t.Fatalf("a failed post-merge verification must not close the item")
		}
		// A repair run can only take over once the failed run has ended: the
		// human cancels it, then the repair continues from the merged baseline.
		blocked := h.journey(t, workspaceID, workItemID)
		if blocked.Run == nil {
			t.Fatal("the failed run is not readable in the journey")
		}
		var stopped struct {
			Applied  bool   `json:"applied"`
			RunState string `json:"state"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "cancel", "requestKey": h.nextKey("journey-stop"),
				"expectedVersion": blocked.Run.Version,
			}, &stopped); status != http.StatusOK || !stopped.Applied {
			t.Fatalf("the failed run could not be stopped: status %d, %+v", status, stopped)
		}
		h.waitFor(t, func() bool {
			current := h.journey(t, workspaceID, workItemID)
			return current.Run != nil && current.Run.State == "CANCELED"
		}, "the failed run must settle before the repair takes over")

		// The repair run takes advancement authority while the original item
		// stays incomplete, and it never inherits the old approval.
		var recovery struct {
			RunID   string `json:"workflowRunId"`
			Applied bool   `json:"applied"`
			Detail  string `json:"detail"`
		}
		status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "repair", "reason": "the merged verification failed",
				"requestKey":      h.nextKey("journey-repair"),
				"expectedVersion": h.journey(t, workspaceID, workItemID).WorkItem.Version,
			}, &recovery)
		if status != http.StatusOK || !recovery.Applied || recovery.RunID == admission.Admission.RunID {
			t.Fatalf("the repair run was not admitted: status %d, %+v", status, recovery)
		}
		after := h.journey(t, workspaceID, workItemID)
		if after.WorkItem.State != "OPEN" {
			t.Fatalf("the original work item changed state: %+v", after.WorkItem)
		}
		if after.Run == nil || after.Run.WorkflowRunID != recovery.RunID {
			t.Fatalf("the repair run does not hold advancement authority: %+v", after.Run)
		}
	})

	t.Run("cancellation_stops_new_dispatch", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Cancel the delivery")
		admission := h.startRun(t, workspaceID, workItemID)
		var recovery struct {
			Applied  bool   `json:"applied"`
			RunState string `json:"state"`
		}
		status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "cancel", "requestKey": h.nextKey("journey-cancel"), "expectedVersion": 1,
			}, &recovery)
		if status != http.StatusOK || !recovery.Applied {
			t.Fatalf("cancellation was not applied: status %d, %+v", status, recovery)
		}
		h.waitFor(t, func() bool {
			journey := h.journey(t, workspaceID, workItemID)
			return journey.Run != nil && journey.Run.State == "CANCELED"
		}, "the canceled run must settle")
		// A canceled run materialises no further node round, whatever the
		// workspace's runtimes do with other runs.
		before := h.runNodeCount(t, workspaceID, workItemID, admission.Admission.RunID)
		time.Sleep(2 * time.Second)
		after := h.runNodeCount(t, workspaceID, workItemID, admission.Admission.RunID)
		if after != before {
			t.Fatalf("a canceled run dispatched %d more rounds", after-before)
		}
	})

	t.Run("a_failed_required_verification_keeps_the_delivery_open", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Fail the required verification once")
		admission := h.startRun(t, workspaceID, workItemID)
		view := h.drive(t, workspaceID, workItemID, driveOptions{failRequiredTest: true, stopWhenDecided: "test"})
		if view.WorkItem.State != "OPEN" || view.Closure != nil {
			t.Fatalf("a failed required verification closed the item: %+v", view.WorkItem)
		}
		// The failed verdict releases nothing: the downstream review and publish
		// gates stay closed.
		var detail struct {
			Nodes []struct {
				NodeID string `json:"nodeId"`
				State  string `json:"state"`
			} `json:"nodes"`
		}
		if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
			"/runs/"+admission.Admission.RunID, nil, &detail); status != http.StatusOK {
			t.Fatalf("read the run detail: status %d", status)
		}
		for _, node := range detail.Nodes {
			if (node.NodeID == "code_review" || node.NodeID == "publish_pr") && node.State == "DECIDED" {
				t.Fatalf("the failed verification released %s", node.NodeID)
			}
		}
		// A legal controlled recovery still succeeds: rerun the failed
		// verification and let it pass.
		var rerun struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "rerun", "nodeId": "test", "reason": "rerun the failed verification",
				"requestKey": h.nextKey("journey-rerun-test"), "expectedVersion": 1,
			}, &rerun); status != http.StatusOK || !rerun.Applied {
			t.Fatalf("the controlled rerun of the failed verification: status %d, %+v", status, rerun)
		}
		final := h.drive(t, workspaceID, workItemID, driveOptions{})
		if final.WorkItem.State != "CLOSED" || final.Run == nil || final.Run.State != "SUCCEEDED" {
			t.Fatalf("the recovered flow did not close: %+v", final)
		}
	})

	t.Run("a_plan_rerun_rebuilds_a_locally_rerun_downstream", func(t *testing.T) {
		// The frozen causal reference decides staleness, not per-node counters:
		// a code round locally rerun to the same count as the new plan round
		// still references the superseded plan and must be rebuilt.
		workItemID := h.createWorkItem(t, workspaceID, "Rerun the code first, then the plan")
		admission := h.startRun(t, workspaceID, workItemID)
		h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "publish_pr"})
		// code2 is rerun while the plan is still round 1; its frozen input
		// references the plan round 1 deliverable.
		h.rerunNode(t, workspaceID, workItemID, admission.Admission.RunID, "code", "the review requested changes")
		h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "code", stopIteration: 2})
		before := h.nodeRound(t, workspaceID, workItemID, admission.Admission.RunID, "code")
		if before.Iteration != 2 {
			t.Fatalf("the locally rerun code round = %+v, want iteration 2", before)
		}
		// The plan rerun rebuilds its review, re-asks its approval, and must
		// rebuild code into a round that references the new plan.
		h.rerunNode(t, workspaceID, workItemID, admission.Admission.RunID, "plan", "the plan needs another round")
		final := h.drive(t, workspaceID, workItemID, driveOptions{})
		if final.WorkItem.State != "CLOSED" || final.Run == nil || final.Run.State != "SUCCEEDED" {
			t.Fatalf("the rebuilt flow did not close: %+v", final)
		}
		after := h.nodeRound(t, workspaceID, workItemID, admission.Admission.RunID, "code")
		if after.Iteration <= before.Iteration || after.InputDigest == before.InputDigest {
			t.Fatalf("the code round still references the superseded plan: %+v (was %+v)", after, before)
		}
	})

	t.Run("a_plan_rerun_rebuilds_a_downstream_ahead_of_its_count", func(t *testing.T) {
		// A downstream rerun past the new upstream count must still rebuild:
		// the counter inequality is false, the frozen reference is what counts.
		workItemID := h.createWorkItem(t, workspaceID, "Rerun the code past the new plan round")
		admission := h.startRun(t, workspaceID, workItemID)
		h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "publish_pr"})
		h.rerunNode(t, workspaceID, workItemID, admission.Admission.RunID, "code", "the review requested changes")
		h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "code", stopIteration: 2})
		h.rerunNode(t, workspaceID, workItemID, admission.Admission.RunID, "code", "another requested change")
		h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "code", stopIteration: 3})
		before := h.nodeRound(t, workspaceID, workItemID, admission.Admission.RunID, "code")
		if before.Iteration != 3 {
			t.Fatalf("the twice-rerun code round = %+v, want iteration 3", before)
		}
		h.rerunNode(t, workspaceID, workItemID, admission.Admission.RunID, "plan", "the plan needs another round")
		final := h.drive(t, workspaceID, workItemID, driveOptions{})
		if final.WorkItem.State != "CLOSED" || final.Run == nil || final.Run.State != "SUCCEEDED" {
			t.Fatalf("the rebuilt flow did not close: %+v", final)
		}
		after := h.nodeRound(t, workspaceID, workItemID, admission.Admission.RunID, "code")
		if after.Iteration <= before.Iteration || after.InputDigest == before.InputDigest {
			t.Fatalf("the code round ahead of the count still references the superseded plan: %+v (was %+v)", after, before)
		}
	})

	t.Run("a_region_less_revision_verdict_gets_an_explicit_round", func(t *testing.T) {
		// A verifier without a declared automatic region reports needs_revision:
		// the decision goes to a human, and only an approved answer grants the
		// node one explicit extra round.
		rejectedItemID := h.createWorkItem(t, workspaceID, "Refuse the verifier revision request")
		rejectedAdmission := h.startRun(t, workspaceID, rejectedItemID)
		h.drive(t, workspaceID, rejectedItemID, driveOptions{needsRevisionTest: true, stopWhenDecided: "test"})
		rejectedTask := h.waitForHumanTask(t, workspaceID, rejectedItemID, "revision_escalation")
		h.answerHumanTask(t, workspaceID, rejectedTask, "rejected", map[string]string{"feedback": "do not continue"})
		time.Sleep(2 * time.Second)
		for round := 0; round < 3; round++ {
			var detail struct {
				Nodes []struct {
					NodeID    string `json:"nodeId"`
					Iteration int    `json:"iteration"`
					State     string `json:"state"`
				} `json:"nodes"`
			}
			if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+rejectedItemID+
				"/runs/"+rejectedAdmission.Admission.RunID, nil, &detail); status != http.StatusOK {
				t.Fatalf("read the run detail: status %d", status)
			}
			for _, node := range detail.Nodes {
				if node.NodeID == "test" && node.Iteration != 1 {
					t.Fatalf("the refused revision re-ran the verifier at iteration %d", node.Iteration)
				}
				if node.NodeID == "code_review" && node.State == "DECIDED" {
					t.Fatal("the refused revision released the downstream review")
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		// The explicit cancellation remains the legal exit of a refused region.
		current := h.journey(t, workspaceID, rejectedItemID)
		var stopped struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+rejectedItemID+"/runs/"+
				rejectedAdmission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "cancel", "requestKey": h.nextKey("journey-refused-region-cancel"),
				"expectedVersion": current.Run.Version,
			}, &stopped); status != http.StatusOK || !stopped.Applied {
			t.Fatalf("the cancellation after the refusal: status %d, %+v", status, stopped)
		}

		// Approved: the verifier really re-runs and the flow closes.
		approvedItemID := h.createWorkItem(t, workspaceID, "Grant the verifier another round")
		h.startRun(t, workspaceID, approvedItemID)
		h.drive(t, workspaceID, approvedItemID, driveOptions{needsRevisionTest: true, stopWhenDecided: "test"})
		approvedTask := h.waitForHumanTask(t, workspaceID, approvedItemID, "revision_escalation")
		h.answerHumanTask(t, workspaceID, approvedTask, "approved", map[string]string{"feedback": "continue"})
		final := h.drive(t, workspaceID, approvedItemID, driveOptions{})
		if final.WorkItem.State != "CLOSED" || final.Run == nil || final.Run.State != "SUCCEEDED" {
			t.Fatalf("the granted region did not close: %+v", final)
		}
	})

	t.Run("a_plan_rerun_never_releases_the_downstream_before_the_new_approval", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Rerun the plan after a first pull request")
		admission := h.startRun(t, workspaceID, workItemID)
		// The first delivery runs through its pull request, so the reviewed plan
		// really has an approved, executed downstream.
		first := h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "publish_pr"})
		if first.Run == nil || first.WorkItem.State != "OPEN" {
			t.Fatalf("the first delivery stopped early: %+v", first.WorkItem)
		}
		// Rerun the plan: its review is rebuilt and its approval re-asked.
		var rerun struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "rerun", "nodeId": "plan", "reason": "the plan needs another round",
				"requestKey": h.nextKey("journey-plan-rerun-gate"), "expectedVersion": 1,
			}, &rerun); status != http.StatusOK || !rerun.Applied {
			t.Fatalf("the plan rerun: status %d, %+v", status, rerun)
		}
		// Execute the rebuilt plan and review only; the drive stops the moment
		// the rebuilt review decides, before it would settle the new approval.
		h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "plan_review", stopIteration: 2})
		h.waitFor(t, func() bool {
			for _, task := range h.journey(t, workspaceID, workItemID).OpenHumanTasks {
				if task.Kind == "plan_approval" {
					return true
				}
			}
			return false
		}, "the rebuilt plan must ask for a new approval")
		// Negative: while the new approval is pending, the rebuilt downstream
		// stays closed — the code round must not advance past its first version.
		for round := 0; round < 5; round++ {
			var detail struct {
				Nodes []struct {
					NodeID    string `json:"nodeId"`
					Iteration int    `json:"iteration"`
				} `json:"nodes"`
			}
			if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
				"/runs/"+admission.Admission.RunID, nil, &detail); status != http.StatusOK {
				t.Fatalf("read the run detail: status %d", status)
			}
			for _, node := range detail.Nodes {
				if (node.NodeID == "code" || node.NodeID == "design_tests") && node.Iteration > 1 {
					t.Fatalf("%s advanced to iteration %d before the new approval", node.NodeID, node.Iteration)
				}
			}
			time.Sleep(400 * time.Millisecond)
		}
		// Positive: answering the new approval releases the rebuilt flow to its
		// closure.
		for _, task := range h.journey(t, workspaceID, workItemID).OpenHumanTasks {
			if task.Kind == "plan_approval" {
				h.answerHumanTask(t, workspaceID, task.HumanTaskID, "approved", map[string]string{"feedback": "looks good"})
			}
		}
		final := h.drive(t, workspaceID, workItemID, driveOptions{})
		if final.WorkItem.State != "CLOSED" || final.Run == nil || final.Run.State != "SUCCEEDED" {
			t.Fatalf("the rebuilt flow did not close: %+v", final)
		}
	})

	t.Run("code_rerun_delivers_a_second_pull_request_intent", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Revise the delivery after the first pull request")
		admission := h.startRun(t, workspaceID, workItemID)
		first := h.drive(t, workspaceID, workItemID, driveOptions{stopWhenDecided: "publish_pr"})
		if first.Run == nil || first.WorkItem.State != "OPEN" {
			t.Fatalf("the first delivery stopped early: %+v", first.WorkItem)
		}
		firstCommit := h.machineCommit()
		// The human-controlled changes-requested path: rerun the code node.
		var rerun struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "rerun", "nodeId": "code", "reason": "the review requested changes",
				"requestKey": h.nextKey("journey-rerun-code"), "expectedVersion": 1,
			}, &rerun); status != http.StatusOK || !rerun.Applied {
			t.Fatalf("the code rerun: status %d, %+v", status, rerun)
		}
		// The revised delivery goes through a second pull-request intent of its
		// own commit and closes.
		second := h.drive(t, workspaceID, workItemID, driveOptions{})
		if second.WorkItem.State != "CLOSED" || second.Run == nil || second.Run.State != "SUCCEEDED" {
			t.Fatalf("the revised delivery did not close: %+v", second)
		}
		if h.machineCommit() == firstCommit {
			t.Fatal("the code rerun delivered no new commit")
		}
		publishes := 0
		commits := map[string]bool{}
		for _, operation := range h.deliveryOperations(t, workspaceID, workItemID) {
			if operation.Kind == "publish_pr" {
				publishes++
				commits[operation.Request.CommitOID] = true
			}
		}
		if publishes != 2 || len(commits) != 2 {
			t.Fatalf("the delivery reports %d publish intents over %d commits", publishes, len(commits))
		}
	})

	t.Run("real-producer-responses_match_the_frozen_schemas", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Read every composite view")
		// The admission response of the real producer passes the frozen schema.
		status, admissionBody := h.doRaw(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs",
			map[string]any{"requestKey": h.nextKey("journey-schema-admit"), "expectedVersion": 1})
		if status != http.StatusCreated {
			t.Fatalf("admit through the raw surface: status %d", status)
		}
		assertJourneySchema(t, "web-application", "the admission response", admissionBody)
		var admission admissionResponse
		if err := json.Unmarshal(admissionBody, &admission); err != nil || admission.Admission.RunID == "" {
			t.Fatalf("decode the admission response: %v", err)
		}
		// The workspace list resolves through the S01 membership table.
		var list map[string]any
		if status := h.do(t, http.MethodGet, "/api/v1/workspaces", nil, &list); status != http.StatusOK {
			t.Fatalf("workspace list: status %d", status)
		}
		h.expectSchema(t, "web-application", "the workspace list", "/api/v1/workspaces", nil)
		h.expectSchema(t, "web-application", "the journey",
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/journey", nil)
		h.expectSchema(t, "web-application", "the run detail",
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+admission.Admission.RunID, nil)
		h.expectSchema(t, "web-application", "the artifact list",
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/artifacts", nil)
		h.expectSchema(t, "fixed-workflow", "the fixed workflow",
			"/api/v1/workspaces/"+workspaceID+"/fixed-workflow", nil)
		// The recovery response of the real producer passes the frozen schema too.
		status, recoveryBody := h.doRaw(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{"kind": "cancel", "requestKey": h.nextKey("journey-schema-cancel"),
				"expectedVersion": 1})
		if status != http.StatusOK {
			t.Fatalf("cancel through the raw surface: status %d", status)
		}
		assertJourneySchema(t, "web-application", "the recovery response", recoveryBody)
	})

	t.Run("retry_keeps_the_frozen_input_and_rerun_rebuilds_the_flow", func(t *testing.T) {
		workItemID := h.createWorkItem(t, workspaceID, "Retry and rerun a delivery")
		admission := h.startRun(t, workspaceID, workItemID)
		h.answerHumanTask(t, workspaceID, admission.Recommendation.HumanTaskID,
			"approved", map[string]string{"selection": h.indexSelection(t)})
		h.waitFor(t, func() bool {
			return h.nodeDecided(t, workspaceID, workItemID, h.journey(t, workspaceID, workItemID), "knowledge_bind")
		}, "the knowledge binding must be frozen")
		// The first plan attempt fails, and a retry keeps its frozen input.
		failed := h.leaseCapability(t, "plan.create")
		h.verifyFrozenInput(t, failed)
		h.reportFailure(t, failed)
		h.waitFor(t, func() bool {
			return h.runAttemptCount(t, workspaceID, workItemID, admission.Admission.RunID) >= 1
		}, "the failed attempt must be persisted")
		var recovery struct {
			Applied bool   `json:"applied"`
			Detail  string `json:"detail"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "retry", "nodeId": "plan", "requestKey": h.nextKey("journey-retry"),
				"expectedVersion": 1,
			}, &recovery); status != http.StatusOK || !recovery.Applied {
			t.Fatalf("retry: status %d, %+v", status, recovery)
		}
		// The retried attempt is leasable *and* its frozen input is readable.
		retried := h.leaseCapability(t, "plan.create")
		if retried.Task.TaskID == failed.Task.TaskID {
			t.Fatal("the retry reused the failed attempt identity")
		}
		h.verifyFrozenInput(t, retried)
		// A second consecutive retry is a new attempt with the same frozen
		// input, never a conflict.
		h.reportFailure(t, retried)
		var secondRetry struct {
			Applied bool   `json:"applied"`
			Detail  string `json:"detail"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "retry", "nodeId": "plan", "requestKey": h.nextKey("journey-retry-again"),
				"expectedVersion": 1,
			}, &secondRetry); status != http.StatusOK || !secondRetry.Applied {
			t.Fatalf("the second retry: status %d, %+v", status, secondRetry)
		}
		if !strings.Contains(secondRetry.Detail, "attempt 3") {
			t.Fatalf("the second retry detail = %q", secondRetry.Detail)
		}
		second := h.leaseCapability(t, "plan.create")
		if second.Task.TaskID == retried.Task.TaskID || second.Task.TaskID == failed.Task.TaskID {
			t.Fatal("the second retry reused an old attempt identity")
		}
		h.verifyFrozenInput(t, second)
		h.reportResult(t, second, "completed", digestOf("plan-retry"),
			map[string]any{"verdict": "approved", "summary": "plan"})

		// A rerun of the plan rebuilds the review and asks the human again.
		var rerun struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "rerun", "nodeId": "plan", "reason": "the plan needs another round",
				"requestKey": h.nextKey("journey-rerun"), "expectedVersion": 1,
			}, &rerun); status != http.StatusOK || !rerun.Applied {
			t.Fatalf("rerun: status %d, %+v", status, rerun)
		}
		revised := h.leaseCapability(t, "plan.create")
		h.reportResult(t, revised, "completed", digestOf("plan-second"),
			map[string]any{"verdict": "approved", "summary": "plan"})
		review := h.leaseCapability(t, "plan.review")
		h.reportResult(t, review, "completed", digestOf("review-second"),
			map[string]any{"verdict": "approved", "summary": "review"})
		// The superseded approval cannot keep gating the rebuilt flow: the
		// review of the replaced plan round is re-run against the new round and
		// only then is a fresh, versioned approval requested.
		h.waitFor(t, func() bool {
			if len(h.openHumanTaskTitles(t, workspaceID, workItemID)) > 0 {
				return true
			}
			var lease leasedTask
			switch status := h.leaseOnce(t, &lease); status {
			case http.StatusNoContent:
				return false
			case http.StatusOK:
			default:
				t.Fatalf("lease the rebuilt review: status %d", status)
			}
			if lease.Task.Capability.ID != "plan.review" {
				t.Fatalf("leased %s while waiting for the rebuilt review", lease.Task.Capability.ID)
			}
			h.reportResult(t, lease, "completed", digestOf("review-rebuilt"),
				map[string]any{"verdict": "approved", "summary": "review"})
			return false
		}, "the rebuilt flow must ask for a new approval")
	})

	t.Run("recovery_and_reads_refuse_foreign_or_stale_commands", func(t *testing.T) {
		otherWorkspaceID := h.createWorkspace(t, "journey-other")
		workItemID := h.createWorkItem(t, workspaceID, "Refuse foreign commands")
		admission := h.startRun(t, workspaceID, workItemID)
		// A run of another workspace is not addressable here.
		var refused struct {
			Error string `json:"error"`
		}
		if status := h.do(t, http.MethodGet,
			"/api/v1/workspaces/"+otherWorkspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID, nil, &refused); status != http.StatusForbidden {
			t.Fatalf("cross-workspace read answered %d", status)
		}
		// A stale expected version is a version conflict, not a silent success.
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "cancel", "requestKey": h.nextKey("journey-stale"),
				"expectedVersion": 99,
			}, &refused); status != http.StatusConflict {
			t.Fatalf("stale recovery answered %d", status)
		}
		// The same request key with other content is a conflict too.
		requestKey := h.nextKey("journey-replay")
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{"kind": "cancel", "requestKey": requestKey, "expectedVersion": 1},
			&refused); status != http.StatusOK {
			t.Fatalf("legal recovery answered %d", status)
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{"kind": "rerun", "nodeId": "plan", "reason": "other content",
				"requestKey": requestKey, "expectedVersion": 1}, &refused); status != http.StatusConflict {
			t.Fatalf("reused request key answered %d", status)
		}
	})

	t.Run("knowledge_suggestion_outage_never_blocks_the_closure", func(t *testing.T) {
		h.blockSuggestionArtifacts(t, "")
		t.Cleanup(func() { h.blockSuggestionArtifacts(t, "drop") })
		workItemID := h.createWorkItem(t, workspaceID, "Close despite a suggestion outage")
		admission := h.startRun(t, workspaceID, workItemID)
		view := h.drive(t, workspaceID, workItemID, driveOptions{})
		if view.WorkItem.State != "CLOSED" || view.Run == nil || view.Run.State != "SUCCEEDED" {
			t.Fatalf("the closure was blocked by the suggestion outage: %+v", view)
		}
		closedItem, closedRun := view.WorkItem.Version, view.Run.Version
		// The auxiliary attempt fails honestly: the pending fact was frozen
		// before the closure, and the failure is recorded without inventing a
		// report artifact.
		failed := h.waitForSuggestion(t, workspaceID, workItemID, admission.Admission.RunID, "FAILED")
		if failed.Attempts != 1 || failed.FailureReason == "" || len(failed.History) != 1 {
			t.Fatalf("the failed suggestion is not readable: %+v", failed)
		}
		if evidence := h.suggestionEvidence(t, workspaceID, workItemID); len(evidence) != 1 ||
			evidence[0].Result != "failed" || evidence[0].ReportArtifactID != "" {
			t.Fatalf("the auxiliary failure evidence = %+v", evidence)
		}
		// The failure never touched the business facts.
		after := h.journey(t, workspaceID, workItemID)
		if after.WorkItem.Version != closedItem || after.Run.Version != closedRun {
			t.Fatalf("the suggestion failure changed the closure: item %d->%d run %d->%d",
				closedItem, after.WorkItem.Version, closedRun, after.Run.Version)
		}

		// The outage ends and the dedicated recovery kind retries the
		// auxiliary branch; the previous failure stays readable.
		h.blockSuggestionArtifacts(t, "drop")
		retryKey := h.nextKey("journey-suggestion-retry")
		retryBody := map[string]any{
			"kind": "retry-knowledge-suggestion", "requestKey": retryKey, "expectedVersion": closedRun,
		}
		var retried struct {
			Applied bool   `json:"applied"`
			Detail  string `json:"detail"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery", retryBody, &retried); status != http.StatusOK || !retried.Applied {
			t.Fatalf("the suggestion retry was refused: status %d, %+v", status, retried)
		}
		published := h.waitForSuggestion(t, workspaceID, workItemID, admission.Admission.RunID, "PUBLISHED")
		if published.Attempts != 2 || published.ArtifactID == "" || len(published.History) != 2 ||
			published.History[0].State != "FAILED" || published.History[1].State != "PUBLISHED" {
			t.Fatalf("the retried suggestion is not readable: %+v", published)
		}
		evidence := h.suggestionEvidence(t, workspaceID, workItemID)
		if len(evidence) != 2 || evidence[0].Result != "failed" || evidence[1].Result != "passed" ||
			evidence[1].ReportArtifactID == "" {
			t.Fatalf("the retried evidence = %+v", evidence)
		}
		// The published suggestion is a real artifact of the work item, and it
		// is never a knowledge-repository write.
		artifacts := h.artifactList(t, workspaceID, workItemID)
		suggestions := 0
		for _, record := range artifacts {
			if record.Kind == "knowledge_suggestion" {
				suggestions++
				if record.ArtifactID != published.ArtifactID || record.State != "PUBLISHED" {
					t.Fatalf("the suggestion artifact = %+v", record)
				}
			}
		}
		if suggestions != 1 {
			t.Fatalf("the artifact list reports %d suggestion artifacts", suggestions)
		}
		// The real producer response of the retried run still passes the
		// frozen schema, and the run detail carries the whole attempt history.
		h.expectSchema(t, "web-application", "the run detail with a suggestion",
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+admission.Admission.RunID, nil)

		// A legal repeat of the very same command replays its recorded
		// outcome, even though the state has advanced to PUBLISHED.
		var replayed struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery", retryBody, &replayed); status != http.StatusOK || !replayed.Applied {
			t.Fatalf("the identical retry replay answered %d, %+v", status, replayed)
		}
		// The same key with other content conflicts, and a retry of a
		// published suggestion is a policy refusal.
		var refused struct {
			Error string `json:"error"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "retry-knowledge-suggestion", "requestKey": retryKey, "expectedVersion": closedRun + 1,
			}, &refused); status != http.StatusConflict {
			t.Fatalf("the reused key with other content answered %d", status)
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "retry-knowledge-suggestion", "requestKey": h.nextKey("journey-suggestion-repeat"),
				"expectedVersion": closedRun,
			}, &refused); status != http.StatusUnprocessableEntity {
			t.Fatalf("a retry of a published suggestion answered %d", status)
		}
		// Ordinary recovery commands keep their closed-item restriction.
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+
				admission.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "retry", "nodeId": "plan", "requestKey": h.nextKey("journey-closed-retry"),
				"expectedVersion": closedRun,
			}, &refused); status != http.StatusUnprocessableEntity {
			t.Fatalf("a business retry on a closed item answered %d", status)
		}
		// The closure facts are field-by-field unchanged by the auxiliary work.
		final := h.journey(t, workspaceID, workItemID)
		if final.WorkItem.Version != closedItem || final.WorkItem.State != "CLOSED" ||
			final.Run.Version != closedRun || final.Run.State != "SUCCEEDED" || final.Closure == nil {
			t.Fatalf("the auxiliary retry changed the closure: %+v", final)
		}
	})

	t.Run("a_hung_suggestion_upload_is_bounded_and_fails_terminal", func(t *testing.T) {
		// The upload hangs until the gate is released; the attempt's own bound
		// must end it long before that.
		h.blockSuggestionArtifacts(t, "gate")
		t.Cleanup(func() {
			h.blockSuggestionArtifacts(t, "release")
			h.blockSuggestionArtifacts(t, "drop")
		})
		hungItemID := h.createWorkItem(t, workspaceID, "Hang the suggestion upload")
		hung := h.startRun(t, workspaceID, hungItemID)
		hungView := h.drive(t, workspaceID, hungItemID, driveOptions{})
		if hungView.WorkItem.State != "CLOSED" {
			t.Fatalf("the closure waited for the suggestion: %+v", hungView.WorkItem)
		}
		h.waitForSuggestion(t, workspaceID, hungItemID, hung.Admission.RunID, "PROCESSING")
		claimed := time.Now()
		failed := h.waitForSuggestion(t, workspaceID, hungItemID, hung.Admission.RunID, "FAILED")
		if elapsed := time.Since(claimed); elapsed > 20*time.Second {
			t.Fatalf("the hung upload was not bounded: it held the workspace lock for %v", elapsed)
		}
		if failed.Attempts != 1 || failed.FailureReason == "" {
			t.Fatalf("the bounded failure is not readable: %+v", failed)
		}
		// A bounded failure is terminal: the worker never re-claims it. Only
		// the explicit command opens the next attempt.
		time.Sleep(3 * time.Second)
		if again := h.knowledgeSuggestion(t, workspaceID, hungItemID, hung.Admission.RunID); again.Attempts != 1 {
			t.Fatalf("the failed attempt was re-claimed without a command: %+v", again)
		}
		// The workspace keeps working once the bound released the lock.
		otherItemID := h.createWorkItem(t, workspaceID, "Keep working after the bounded failure")
		h.startRun(t, workspaceID, otherItemID)
		otherView := h.drive(t, workspaceID, otherItemID, driveOptions{})
		if otherView.WorkItem.State != "CLOSED" {
			t.Fatalf("the next delivery in the same workspace did not close: %+v", otherView.WorkItem)
		}
		// The explicit retry publishes, and the bounded failure stays readable.
		h.blockSuggestionArtifacts(t, "drop")
		var retried struct {
			Applied bool `json:"applied"`
		}
		if status := h.do(t, http.MethodPost,
			"/api/v1/workspaces/"+workspaceID+"/work-items/"+hungItemID+"/runs/"+
				hung.Admission.RunID+"/recovery",
			map[string]any{
				"kind": "retry-knowledge-suggestion", "requestKey": h.nextKey("journey-suggestion-retry-bounded"),
				"expectedVersion": 1,
			}, &retried); status != http.StatusOK || !retried.Applied {
			t.Fatalf("the explicit suggestion retry: status %d, %+v", status, retried)
		}
		published := h.waitForSuggestion(t, workspaceID, hungItemID, hung.Admission.RunID, "PUBLISHED")
		if published.Attempts != 2 || len(published.History) != 2 ||
			published.History[0].State != "FAILED" || published.History[1].State != "PUBLISHED" {
			t.Fatalf("the retried suggestion is not readable: %+v", published)
		}
	})

	t.Run("unknown_attempt_input_fails_closed", func(t *testing.T) {
		status := h.runtimeRequest(t, http.MethodGet, "/api/v1/runtime/tasks/journey-unknown-task/input", nil, nil)
		if status != http.StatusNotFound && status != http.StatusConflict {
			t.Fatalf("an unknown attempt input answered %d, want a closed refusal", status)
		}
		// The legal request after the refusal still succeeds.
		if status := h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/tasks/lease", map[string]any{
			"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch,
			"waitSeconds": 1, "leaseSeconds": 300,
		}, nil); status != http.StatusNoContent && status != http.StatusOK {
			t.Fatalf("the transport refused a legal request after the refusal: %d", status)
		}
	})
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

type journeyHarness struct {
	t               *testing.T
	baseURL         string
	origin          string
	client          *http.Client
	csrfToken       string
	observer        *scriptedObserver
	projectRemote   string
	knowledgeRemote string
	projectHeadOID  string
	materializeRoot string
	// workingBranch is the frozen write authorization of the admitted run.
	workingBranch string
	counter       int
}

// nextKey returns a unique, grammar-safe request key for one command.
func (h *journeyHarness) nextKey(prefix string) string {
	h.counter++
	return fmt.Sprintf("%s-%d", prefix, h.counter)
}

type admissionResponse struct {
	Admission struct {
		RunID                 string `json:"workflowRunId"`
		ProjectRepositoryID   string `json:"projectRepositoryId"`
		KnowledgeRepositoryID string `json:"knowledgeRepositoryId"`
		ProjectCommitOID      string `json:"projectCommitOid"`
		KnowledgeCommitOID    string `json:"knowledgeCommitOid"`
		WorkingBranchRef      string `json:"workingBranchRef"`
		ProviderID            string `json:"providerId"`
	} `json:"admission"`
	Recommendation *struct {
		HumanTaskID string `json:"humanTaskId"`
		IndexDigest string `json:"indexDigest"`
		Candidates  []struct {
			Path     string `json:"path"`
			Digest   string `json:"digest"`
			Scope    string `json:"scope"`
			Required bool   `json:"required"`
		} `json:"candidates"`
	} `json:"knowledgeRecommendation"`
}

func startJourney(t *testing.T) *journeyHarness {
	t.Helper()
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	h := &journeyHarness{t: t}
	h.projectRemote, h.projectHeadOID = buildProjectRepository(t)
	h.knowledgeRemote, _ = buildKnowledgeRepository(t)
	h.materializeRoot = t.TempDir()
	h.observer = newScriptedObserver(map[string]string{
		journeyProjectRepository:   h.projectRemote,
		journeyKnowledgeRepository: h.knowledgeRemote,
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	h.origin = "https://" + listener.Addr().String()
	h.baseURL = h.origin

	stub := newJourneyOAuthStub(t)
	defer stub.Close()

	var controlPlane *http.Server
	app := server.New(
		fx.Replace(
			config.Server{Address: "127.0.0.1:0"},
			config.Identity{
				Enabled: true, DatabaseURL: databaseURL, TrustedOrigin: h.origin,
				ClientID: "journey-client", ClientSecret: "journey-secret",
				AdminIDs: []string{journeyAdminID, journeyHumanID},
			},
			config.Runtime{
				Enabled: true, DatabaseURL: databaseURL,
				Credentials:         map[string]string{journeyRuntimeID: journeyRuntimeToken},
				GitHubMachineUserID: journeyMachineID,
				MaterializeRoot:     h.materializeRoot,
				RepositoryRegistry: map[string]string{
					journeyProjectRepository:   h.projectRemote,
					journeyKnowledgeRepository: h.knowledgeRemote,
				},
				ReconcileInterval: 200 * time.Millisecond,
			},
		),
		fx.Decorate(func(identity.OAuthClient) (identity.OAuthClient, error) {
			return stub.client(h.origin), nil
		}),
		fx.Decorate(func(workspace.RepositoryVisibility) workspace.RepositoryVisibility {
			return journeyVisibility{}
		}),
		fx.Decorate(func(githubobs.Observer) githubobs.Observer { return h.observer }),
		fx.Populate(&controlPlane),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("build the control plane: %v", err)
	}
	startContext, cancel := context.WithCancel(context.Background())
	if err := app.Start(startContext); err != nil {
		t.Fatalf("start the control plane: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Stop(context.Background())
		cancel()
		_ = listener.Close()
	})

	if controlPlane == nil {
		t.Fatal("the control plane did not expose its HTTP server")
	}
	certificate := selfSigned(t, listener.Addr().String())
	webServer := &http.Server{Handler: controlPlane.Handler, ReadHeaderTimeout: 10 * time.Second}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	go func() { _ = webServer.Serve(tlsListener) }()
	t.Cleanup(func() { _ = webServer.Close() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	h.client = &http.Client{
		Jar:       jar,
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	h.login(t)
	return h
}

type journeyVisibility struct{}

// RepositoryVisible accepts every repository except one marker id, so a real
// denial stays expressible.
func (journeyVisibility) RepositoryVisible(_ context.Context, _, repositoryID string) error {
	if repositoryID == "999999" {
		return workspace.ErrResourceUnavailable
	}
	return nil
}

func (h *journeyHarness) login(t *testing.T) {
	t.Helper()
	var login struct {
		AuthorizationURL string `json:"authorizationUrl"`
	}
	if status := h.do(t, http.MethodPost, "/api/v2/auth/github/login",
		map[string]string{"returnTo": "/"}, &login); status != http.StatusOK {
		t.Fatalf("begin login: status %d", status)
	}
	if login.AuthorizationURL == "" {
		t.Fatal("begin login returned no authorization url")
	}
	response, err := h.client.Get(login.AuthorizationURL)
	if err != nil {
		t.Fatalf("follow the authorization redirect: %v", err)
	}
	_ = response.Body.Close()
	var snapshot struct {
		CSRFToken string `json:"csrfToken"`
		Identity  struct {
			GitHubUserID string `json:"githubUserId"`
		} `json:"identity"`
	}
	if status := h.do(t, http.MethodPost, "/api/v2/auth/session",
		map[string]string{"requestId": "journey-session"}, &snapshot); status != http.StatusOK {
		t.Fatalf("recover the session: status %d", status)
	}
	if snapshot.CSRFToken == "" || snapshot.Identity.GitHubUserID != journeyAdminID {
		t.Fatalf("session snapshot = %+v", snapshot)
	}
	h.csrfToken = snapshot.CSRFToken
}

// doWithKey sends one command whose owning operation reads its idempotency key
// from the Idempotency-Key header.
func (h *journeyHarness) doWithKey(
	t *testing.T,
	method, path, requestKey string,
	body any, out any,
) int {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode the request body: %v", err)
	}
	request, err := http.NewRequest(method, h.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Origin", h.origin)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", requestKey)
	if h.csrfToken != "" {
		request.Header.Set("X-CSRF-Token", h.csrfToken)
	}
	return h.roundTrip(t, request, out)
}

func (h *journeyHarness) do(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Origin", h.origin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if h.csrfToken != "" && method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", h.csrfToken)
	}
	return h.roundTrip(t, request, out)
}

// doRaw sends one command and returns the raw response body, so a real
// producer response can be validated against the frozen schema byte for byte.
func (h *journeyHarness) doRaw(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set("Origin", h.origin)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if h.csrfToken != "" && method != http.MethodGet {
		request.Header.Set("X-CSRF-Token", h.csrfToken)
	}
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	return response.StatusCode, payload
}

func (h *journeyHarness) roundTrip(t *testing.T, request *http.Request, out any) int {
	t.Helper()
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.URL.Path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		t.Logf("%s %s -> %d %s", request.Method, request.URL.Path, response.StatusCode, payload)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("%s %s: decode the response %s: %v", request.Method, request.URL.Path, payload, err)
		}
	}
	return response.StatusCode
}

func (h *journeyHarness) createWorkspace(t *testing.T, name string) string {
	t.Helper()
	var created struct {
		WorkspaceID string `json:"workspaceId"`
	}
	if status := h.doWithKey(t, http.MethodPost, "/api/v1/workspaces", "journey-workspace-"+name,
		map[string]string{"name": name}, &created); status != http.StatusCreated {
		t.Fatalf("bootstrap the workspace: status %d", status)
	}
	return created.WorkspaceID
}

func (h *journeyHarness) configureRepositories(t *testing.T, workspaceID string) {
	t.Helper()
	body := map[string]any{
		"expectedVersion": 1,
		"project": map[string]string{
			"githubRepositoryId": journeyProjectRepository, "displayName": "journey/project", "installationId": "1",
		},
		"knowledge": map[string]string{
			"githubRepositoryId": journeyKnowledgeRepository, "displayName": "journey/knowledge", "installationId": "1",
		},
	}
	if status := h.doWithKey(t, http.MethodPut, "/api/v1/workspaces/"+workspaceID+"/repositories",
		"journey-repositories", body, nil); status != http.StatusOK {
		t.Fatalf("configure the repositories: status %d", status)
	}
}

func (h *journeyHarness) publishExecutionConfig(t *testing.T, workspaceID string) {
	t.Helper()
	capabilities := []map[string]any{}
	for _, capability := range journeyCapabilities {
		capabilities = append(capabilities, map[string]any{"id": capability, "version": 1})
	}
	body := map[string]any{
		"requestKey": "journey-execution-config", "expectedVersion": 0,
		"capabilities": capabilities, "runtimes": []string{journeyRuntimeID},
	}
	if status := h.do(t, http.MethodPut, "/api/v1/workspaces/"+workspaceID+"/execution-config", body, nil); status != http.StatusOK {
		t.Fatalf("publish the execution configuration: status %d", status)
	}
}

var journeyCapabilities = []string{
	"plan.create", "plan.review", "testcases.design", "code.implement", "test.execute",
	"code.review", "security.review", "delivery.publish_pr", "delivery.integration_verify",
}

func (h *journeyHarness) registerRuntime(t *testing.T) {
	t.Helper()
	capabilities := []map[string]any{}
	for _, capability := range journeyCapabilities {
		capabilities = append(capabilities, map[string]any{"id": capability, "version": 1})
	}
	body := map[string]any{
		"schemaVersion": "kowa.workflow-execution.v1", "kind": "runtimeRegistration",
		"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch, "gitHubUserId": journeyMachineID,
		"provider":              map[string]string{"id": "scripted", "version": "scripted-1"},
		"providerAuthenticated": true, "capabilities": capabilities,
	}
	status := h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/register", body, nil)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("register the runtime: status %d", status)
	}
}

// runtimeRequest drives the frozen Runner transport as a runtime identity.
func (h *journeyHarness) runtimeRequest(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the runtime request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build the runtime request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+journeyRuntimeToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the runtime response: %v", err)
	}
	if response.StatusCode >= http.StatusInternalServerError {
		t.Logf("%s %s -> %d %s", request.Method, request.URL.Path, response.StatusCode, payload)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("decode the runtime response %s: %v", payload, err)
		}
	}
	return response.StatusCode
}

func (h *journeyHarness) createWorkItem(t *testing.T, workspaceID, title string) string {
	t.Helper()
	var created struct {
		WorkItem struct {
			ID string `json:"workItemId"`
		} `json:"workItem"`
	}
	body := map[string]any{
		"requestKey":  h.nextKey("journey-item"),
		"title":       title,
		"requirement": map[string]any{"version": 1, "content": "Add a helper that greets deterministically."},
		"acceptance":  map[string]any{"version": 1, "content": "The helper has a passing unit test."},
	}
	if status := h.do(t, http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/work-items", body, &created); status != http.StatusCreated {
		t.Fatalf("create the work item: status %d", status)
	}
	if created.WorkItem.ID == "" {
		t.Fatal("the work item response carries no identity")
	}
	return created.WorkItem.ID
}

func (h *journeyHarness) startRun(t *testing.T, workspaceID, workItemID string) admissionResponse {
	t.Helper()
	var admission admissionResponse
	body := map[string]any{"requestKey": h.nextKey("journey-run"), "expectedVersion": 1}
	status := h.do(t, http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs", body, &admission)
	if status != http.StatusCreated {
		t.Fatalf("admit the run: status %d", status)
	}
	h.workingBranch = admission.Admission.WorkingBranchRef
	return admission
}

func (h *journeyHarness) journey(t *testing.T, workspaceID, workItemID string) journeyView {
	t.Helper()
	var view journeyView
	if status := h.do(t, http.MethodGet,
		"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/journey", nil, &view); status != http.StatusOK {
		t.Fatalf("read the journey: status %d", status)
	}
	return view
}

type journeyView struct {
	WorkItem struct {
		State   string `json:"state"`
		Version int64  `json:"version"`
	} `json:"workItem"`
	Run *struct {
		WorkflowRunID string `json:"workflowRunId"`
		State         string `json:"state"`
		Version       int64  `json:"version"`
		DecidedNodes  int    `json:"decidedNodes"`
		TotalNodes    int    `json:"totalNodes"`
	} `json:"run"`
	OpenHumanTasks []struct {
		HumanTaskID string `json:"humanTaskId"`
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		State       string `json:"state"`
	} `json:"openHumanTasks"`
	Closure *struct {
		ClosureID string `json:"closureId"`
	} `json:"closure"`
}

type journeyHumanTask struct {
	Request struct {
		Version int `json:"version"`
		Object  struct {
			Kind    string `json:"kind"`
			ID      string `json:"id"`
			Version string `json:"version"`
			Digest  string `json:"digest"`
		} `json:"object"`
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	} `json:"request"`
	State   string `json:"state"`
	Version int64  `json:"version"`
}

type journeyHumanTaskResponse struct {
	HumanTask journeyHumanTask `json:"humanTask"`
}

func (h *journeyHarness) answerHumanTask(
	t *testing.T,
	workspaceID, taskID, decision string,
	values map[string]string,
) {
	t.Helper()
	var response journeyHumanTaskResponse
	if status := h.do(t, http.MethodGet,
		"/api/v1/workspaces/"+workspaceID+"/human-tasks/"+taskID, nil, &response); status != http.StatusOK {
		t.Fatalf("read the human task: status %d", status)
	}
	task := response.HumanTask
	submitted := map[string]string{}
	for name, value := range values {
		submitted[name] = value
	}
	// The frozen request declares its own fields: a required decision field
	// carries the decision value, so the answer never disagrees with it.
	for _, field := range task.Request.Fields {
		if field.Name == "decision" {
			submitted["decision"] = decision
		}
	}
	body := map[string]any{
		"requestKey": h.nextKey("journey-answer"), "requestVersion": task.Request.Version,
		"object": map[string]string{
			"kind": task.Request.Object.Kind, "id": task.Request.Object.ID,
			"version": task.Request.Object.Version, "digest": task.Request.Object.Digest,
		},
		"decision": decision, "values": submitted,
	}
	if status := h.do(t, http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/human-tasks/"+taskID+"/responses", body, nil); status != http.StatusOK {
		t.Fatalf("answer the human task: status %d", status)
	}
}

func allCandidatePaths(admission *struct {
	HumanTaskID string `json:"humanTaskId"`
	IndexDigest string `json:"indexDigest"`
	Candidates  []struct {
		Path     string `json:"path"`
		Digest   string `json:"digest"`
		Scope    string `json:"scope"`
		Required bool   `json:"required"`
	} `json:"candidates"`
}) string {
	paths := make([]string, 0, len(admission.Candidates))
	for _, candidate := range admission.Candidates {
		paths = append(paths, candidate.Path)
	}
	return strings.Join(paths, "\n")
}

func (h *journeyHarness) expectNoLease(t *testing.T, message string) {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	var lease leasedTask
	status := h.leaseOnce(t, &lease)
	if status != http.StatusNoContent {
		t.Fatalf("%s (lease status %d, body %+v)", message, status, lease)
	}
}

type leasedTask struct {
	LeaseID      string `json:"leaseId"`
	FencingToken int64  `json:"fencingToken"`
	Task         struct {
		TaskID     string `json:"taskId"`
		NodeRunID  string `json:"nodeRunId"`
		Capability struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"capability"`
		InputDigest string `json:"inputDigest"`
		GitScopes   []struct {
			RepositoryID string `json:"repositoryId"`
			Role         string `json:"role"`
			Operation    string `json:"operation"`
			Ref          string `json:"ref"`
		} `json:"gitScopes"`
	} `json:"task"`
}

func (h *journeyHarness) leaseCapability(t *testing.T, capability string) leasedTask {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var lease leasedTask
		status := h.leaseOnce(t, &lease)
		if status == http.StatusNoContent {
			continue
		}
		if status != http.StatusOK {
			t.Fatalf("lease a task: status %d", status)
		}
		if lease.Task.Capability.ID != capability {
			t.Fatalf("leased %s while waiting for %s", lease.Task.Capability.ID, capability)
		}
		return lease
	}
	t.Fatalf("no %s attempt was dispatched in time", capability)
	return leasedTask{}
}

func (h *journeyHarness) expectFrozenScopes(t *testing.T, lease leasedTask, admission struct {
	RunID                 string `json:"workflowRunId"`
	ProjectRepositoryID   string `json:"projectRepositoryId"`
	KnowledgeRepositoryID string `json:"knowledgeRepositoryId"`
	ProjectCommitOID      string `json:"projectCommitOid"`
	KnowledgeCommitOID    string `json:"knowledgeCommitOid"`
	WorkingBranchRef      string `json:"workingBranchRef"`
	ProviderID            string `json:"providerId"`
}, withWrite bool) {
	t.Helper()
	var projectRead, knowledgeRead, write bool
	for _, scope := range lease.Task.GitScopes {
		switch scope.Operation {
		case "clone":
			if scope.Role == "project" && scope.Ref == admission.ProjectCommitOID && isOID(scope.Ref) {
				projectRead = true
			}
			if scope.Role == "knowledge" && scope.Ref == admission.KnowledgeCommitOID && isOID(scope.Ref) {
				knowledgeRead = true
			}
		case "push":
			if scope.Ref == admission.WorkingBranchRef {
				write = true
			}
		}
	}
	if !projectRead || !knowledgeRead {
		t.Fatalf("read scopes are not frozen commit oids: %+v", lease.Task.GitScopes)
	}
	if write != withWrite {
		t.Fatalf("write authorization = %v, want %v: %+v", write, withWrite, lease.Task.GitScopes)
	}
}

func isOID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func (h *journeyHarness) projectHead(t *testing.T) string {
	t.Helper()
	return h.projectHeadOID
}

// advanceProjectBranch adds one commit to the delivery branch. It must never
// change a frozen input of an already admitted run.
func (h *journeyHarness) advanceProjectBranch(t *testing.T) {
	t.Helper()
	clone := t.TempDir()
	runGit(t, "", "clone", "--quiet", h.projectRemote, clone)
	writeFile(t, filepath.Join(clone, "NOTES.md"), "a later commit\n")
	runGit(t, clone, "add", "NOTES.md")
	runGit(t, clone, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "later change")
	runGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/main")
}

// runNodeCount reads how many node rounds one run has materialised.
func (h *journeyHarness) runNodeCount(t *testing.T, workspaceID, workItemID, runID string) int {
	t.Helper()
	var detail struct {
		Nodes []struct {
			NodeID string `json:"nodeId"`
		} `json:"nodes"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/runs/"+runID, nil, &detail); status != http.StatusOK {
		t.Fatalf("read the run detail: status %d", status)
	}
	return len(detail.Nodes)
}

func (h *journeyHarness) waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", message)
}

// ---------------------------------------------------------------------------
// scripted external adapters
// ---------------------------------------------------------------------------

// scriptedObserver plays GitHub's read facts for the journey. Branch heads are
// read from the real local remotes over the same read-only Git protocol the
// production adapter uses; only the GitHub API facts are scripted. Nothing here
// is Kowa: it is the external platform the acceptance boundary isolates.
type scriptedObserver struct {
	mu               sync.Mutex
	pullRequest      *githubobs.PullRequestObservation
	review           *githubobs.ReviewObservation
	registry         map[string]string
	observedBranches map[string]bool
	machineCommit    string
}

func newScriptedObserver(registry map[string]string) *scriptedObserver {
	return &scriptedObserver{registry: registry, observedBranches: map[string]bool{}}
}

func (o *scriptedObserver) Identity(context.Context) (string, error) { return journeyMachineID, nil }

func (o *scriptedObserver) Repository(_ context.Context, repositoryID string) (githubobs.RepositoryObservation, error) {
	return githubobs.RepositoryObservation{
		RepositoryID: repositoryID, FullName: "journey/repo-" + repositoryID,
		DefaultBranch: "main", ActorID: journeyMachineID, ObservedAt: time.Now().UTC(),
	}, nil
}

// BranchHead reads the branch head from the real local remote over the same
// read-only Git protocol the production adapter uses; only the GitHub API facts
// are scripted.
func (o *scriptedObserver) BranchHead(_ context.Context, repositoryID, ref string) (githubobs.RefObservation, error) {
	observation := githubobs.RefObservation{
		RepositoryID: repositoryID, Ref: ref, ActorID: journeyMachineID, ObservedAt: time.Now().UTC(),
	}
	url, known := o.registry[repositoryID]
	if !known {
		observation.Unknown, observation.UnknownReason = true, githubobs.UnknownUnavailable
		return observation, githubobs.ErrUnknown
	}
	// The delivery contract addresses branches by their bare name; Git needs the
	// full reference.
	spec := ref
	if !strings.HasPrefix(spec, "refs/") {
		spec = "refs/heads/" + spec
	}
	output, err := exec.Command("git", "ls-remote", url, spec).Output()
	if err != nil {
		observation.Unknown, observation.UnknownReason = true, githubobs.UnknownUnavailable
		return observation, githubobs.ErrUnknown
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == spec && isOID(fields[0]) {
			observation.OID = fields[0]
			return observation, nil
		}
	}
	observation.Unknown, observation.UnknownReason = true, githubobs.UnknownUnavailable
	return observation, fmt.Errorf("%w: branch %s does not resolve", githubobs.ErrUnknown, ref)
}

func (o *scriptedObserver) PullRequest(_ context.Context, repositoryID, number string) (githubobs.PullRequestObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pullRequest == nil || o.pullRequest.Number != number {
		return githubobs.PullRequestObservation{
			RepositoryID: repositoryID, Number: number, ActorID: journeyMachineID,
			ObservedAt: time.Now().UTC(), Unknown: true, UnknownReason: githubobs.UnknownUnavailable,
		}, fmt.Errorf("%w: no pull request %s", githubobs.ErrUnknown, number)
	}
	observation := *o.pullRequest
	observation.ObservedAt = time.Now().UTC()
	return observation, nil
}

func (o *scriptedObserver) PullRequestsByHead(_ context.Context, repositoryID, headBranch string) ([]githubobs.PullRequestObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.pullRequest == nil {
		return nil, nil
	}
	observation := *o.pullRequest
	observation.ObservedAt = time.Now().UTC()
	return []githubobs.PullRequestObservation{observation}, nil
}

func (o *scriptedObserver) Reviews(_ context.Context, repositoryID, number string) ([]githubobs.ReviewObservation, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.review == nil || o.review.Number != number {
		return nil, nil
	}
	observation := *o.review
	observation.ObservedAt = time.Now().UTC()
	return []githubobs.ReviewObservation{observation}, nil
}

func (o *scriptedObserver) ReviewerPermission(_ context.Context, _, _, expectedUserID string) (string, error) {
	if expectedUserID == journeyHumanID {
		return "write", nil
	}
	return "", nil
}

func (o *scriptedObserver) Checks(_ context.Context, repositoryID, commitOID string) (githubobs.ChecksObservation, error) {
	return githubobs.ChecksObservation{
		RepositoryID: repositoryID, CommitOID: commitOID, State: "passed",
		Total: 1, Failed: 0, Pending: 0, ObservedAt: time.Now().UTC(),
	}, nil
}

func (o *scriptedObserver) Permission(_ context.Context, repositoryID string) (githubobs.PermissionObservation, error) {
	return githubobs.PermissionObservation{
		RepositoryID: repositoryID, Push: true, ActorID: journeyMachineID, ObservedAt: time.Now().UTC(),
	}, nil
}

// ---------------------------------------------------------------------------
// local repositories
// ---------------------------------------------------------------------------

func buildProjectRepository(t *testing.T) (string, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "project.git")
	runGit(t, "", "init", "--quiet", "--bare", "--initial-branch=main", remote)
	work := t.TempDir()
	runGit(t, "", "clone", "--quiet", remote, work)
	writeFile(t, filepath.Join(work, "README.md"), "# journey project\n")
	runGit(t, work, "add", "README.md")
	runGit(t, work, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "initial project")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	return remote, head
}

func buildKnowledgeRepository(t *testing.T) (string, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "knowledge.git")
	runGit(t, "", "init", "--quiet", "--bare", "--initial-branch=main", remote)
	work := t.TempDir()
	runGit(t, "", "clone", "--quiet", remote, work)
	design := "The project uses small deterministic helpers.\n"
	writeFile(t, filepath.Join(work, "docs", "design.md"), design)
	index := fmt.Sprintf(`{"schemaVersion":"kowa.web-application.v1","entries":[{"path":"docs/design.md","digest":"%s","scope":"design","required":true}]}`,
		digestOf(design))
	writeFile(t, filepath.Join(work, "kowa-index.json"), index)
	runGit(t, work, "add", "docs/design.md", "kowa-index.json")
	runGit(t, work, "-c", "user.email=journey@example.com", "-c", "user.name=journey",
		"commit", "--quiet", "-m", "knowledge index")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	return remote, head
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create the directory of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

// expectSchema reads one path and validates the real response against the
// frozen contract family.
func (h *journeyHarness) expectSchema(t *testing.T, family, definition, path string, _ any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s: %v", definition, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d: %s", definition, response.StatusCode, body)
	}
	assertJourneySchema(t, family, definition, body)
}

// reportFailure reports one retryable attempt failure over the frozen transport.
func (h *journeyHarness) reportFailure(t *testing.T, lease leasedTask) {
	t.Helper()
	body := map[string]any{
		"schemaVersion": "kowa.workflow-execution.v1", "kind": "taskResult",
		"taskId": lease.Task.TaskID, "leaseId": lease.LeaseID, "fencingToken": lease.FencingToken,
		"status": "failed", "resultDigest": digestOf("failed-" + lease.Task.TaskID),
		"summary": "scripted failure",
		"error":   map[string]any{"code": "PROVIDER_TIMEOUT", "message": "scripted", "retryable": true},
	}
	if status := h.runtimeRequest(t, http.MethodPost,
		"/api/v1/runtime/tasks/"+lease.Task.TaskID+"/result", body, nil); status != http.StatusOK {
		t.Fatalf("report the failure: status %d", status)
	}
}

// runAttemptCount counts the attempts the run detail reports.
func (h *journeyHarness) runAttemptCount(t *testing.T, workspaceID, workItemID, runID string) int {
	t.Helper()
	var detail struct {
		Nodes []struct {
			Attempts []struct {
				TaskID string `json:"taskId"`
			} `json:"attempts"`
		} `json:"nodes"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/runs/"+runID, nil, &detail); status != http.StatusOK {
		t.Fatalf("read the run detail: status %d", status)
	}
	count := 0
	for _, node := range detail.Nodes {
		count += len(node.Attempts)
	}
	return count
}

// openHumanTaskTitles lists the open human tasks of one work item.
func (h *journeyHarness) openHumanTaskTitles(t *testing.T, workspaceID, workItemID string) []string {
	t.Helper()
	view := h.journey(t, workspaceID, workItemID)
	titles := make([]string, 0, len(view.OpenHumanTasks))
	for _, task := range view.OpenHumanTasks {
		titles = append(titles, task.Title)
	}
	return titles
}

// knowledgeSuggestion reads the auxiliary suggestion summary of one run, or
// nil when the run never materialised the branch.
func (h *journeyHarness) knowledgeSuggestion(
	t *testing.T,
	workspaceID, workItemID, runID string,
) *suggestionView {
	t.Helper()
	var detail struct {
		KnowledgeSuggestion *suggestionView `json:"knowledgeSuggestion"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/runs/"+runID, nil, &detail); status != http.StatusOK {
		t.Fatalf("read the run detail: status %d", status)
	}
	return detail.KnowledgeSuggestion
}

type suggestionView struct {
	RunID         string                    `json:"workflowRunId"`
	State         string                    `json:"state"`
	Attempts      int                       `json:"attempts"`
	ArtifactID    string                    `json:"artifactId"`
	FailureReason string                    `json:"failureReason"`
	History       []suggestionAttemptRecord `json:"history"`
}

type suggestionAttemptRecord struct {
	AttemptID      string `json:"attemptId"`
	Attempt        int    `json:"attempt"`
	State          string `json:"state"`
	InputDigest    string `json:"inputDigest"`
	ArtifactID     string `json:"artifactId"`
	ArtifactDigest string `json:"artifactDigest"`
	FailureReason  string `json:"failureReason"`
}

// waitForSuggestion waits until the run detail reports the wanted state.
func (h *journeyHarness) waitForSuggestion(
	t *testing.T,
	workspaceID, workItemID, runID, state string,
) *suggestionView {
	t.Helper()
	var last *suggestionView
	h.waitFor(t, func() bool {
		last = h.knowledgeSuggestion(t, workspaceID, workItemID, runID)
		return last != nil && last.State == state
	}, "the knowledge suggestion must reach state "+state)
	return last
}

// suggestionEvidence reads the auxiliary verification evidence of one work
// item from the delivery view.
func (h *journeyHarness) suggestionEvidence(
	t *testing.T,
	workspaceID, workItemID string,
) []struct {
	Kind             string `json:"kind"`
	Result           string `json:"result"`
	ReportArtifactID string `json:"reportArtifactId"`
} {
	t.Helper()
	var view struct {
		Verification []struct {
			Kind             string `json:"kind"`
			Result           string `json:"result"`
			ReportArtifactID string `json:"reportArtifactId"`
		} `json:"verification"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/delivery", nil, &view); status != http.StatusOK {
		t.Fatalf("read the delivery view: status %d", status)
	}
	evidence := []struct {
		Kind             string `json:"kind"`
		Result           string `json:"result"`
		ReportArtifactID string `json:"reportArtifactId"`
	}{}
	for _, verification := range view.Verification {
		if verification.Kind == "knowledge_suggestion" {
			evidence = append(evidence, verification)
		}
	}
	return evidence
}

// rerunNode posts one human-controlled node rerun and expects it to apply.
func (h *journeyHarness) rerunNode(t *testing.T, workspaceID, workItemID, runID, nodeID, reason string) {
	t.Helper()
	var rerun struct {
		Applied bool `json:"applied"`
	}
	if status := h.do(t, http.MethodPost,
		"/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+"/runs/"+runID+"/recovery",
		map[string]any{
			"kind": "rerun", "nodeId": nodeID, "reason": reason,
			"requestKey": h.nextKey("journey-rerun-" + nodeID), "expectedVersion": 1,
		}, &rerun); status != http.StatusOK || !rerun.Applied {
		t.Fatalf("the rerun of node %s: status %d, %+v", nodeID, status, rerun)
	}
}

// nodeRound reads one node's current round identity from the run detail.
func (h *journeyHarness) nodeRound(
	t *testing.T,
	workspaceID, workItemID, runID, nodeID string,
) struct {
	Iteration   int    `json:"iteration"`
	InputDigest string `json:"inputDigest"`
	State       string `json:"state"`
} {
	t.Helper()
	var detail struct {
		Nodes []struct {
			NodeID      string `json:"nodeId"`
			Iteration   int    `json:"iteration"`
			InputDigest string `json:"inputDigest"`
			State       string `json:"state"`
		} `json:"nodes"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/runs/"+runID, nil, &detail); status != http.StatusOK {
		t.Fatalf("read the run detail: status %d", status)
	}
	// The run detail carries every materialised round; the current one is the
	// highest iteration.
	current := struct {
		Iteration   int    `json:"iteration"`
		InputDigest string `json:"inputDigest"`
		State       string `json:"state"`
	}{}
	for _, node := range detail.Nodes {
		if node.NodeID == nodeID && node.Iteration >= current.Iteration {
			current = struct {
				Iteration   int    `json:"iteration"`
				InputDigest string `json:"inputDigest"`
				State       string `json:"state"`
			}{Iteration: node.Iteration, InputDigest: node.InputDigest, State: node.State}
		}
	}
	if current.Iteration == 0 {
		t.Fatalf("node %s has no round in the run detail", nodeID)
	}
	return current
}

// waitForHumanTask waits for one open human task of a kind and returns it.
func (h *journeyHarness) waitForHumanTask(t *testing.T, workspaceID, workItemID, kind string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, task := range h.journey(t, workspaceID, workItemID).OpenHumanTasks {
			if task.Kind == kind {
				return task.HumanTaskID
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no open %s task appeared for work item %s", kind, workItemID)
	return ""
}

// deliveryOperation is one external operation of the delivery view.
type deliveryOperation struct {
	Kind    string `json:"kind"`
	Request struct {
		CommitOID string `json:"commitOid"`
	} `json:"request"`
}

// deliveryOperations reads the external operations of one work item.
func (h *journeyHarness) deliveryOperations(t *testing.T, workspaceID, workItemID string) []deliveryOperation {
	t.Helper()
	var view struct {
		Operations []deliveryOperation `json:"operations"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/delivery", nil, &view); status != http.StatusOK {
		t.Fatalf("read the delivery view: status %d", status)
	}
	return view.Operations
}

type artifactRecord struct {
	ArtifactID string `json:"artifactId"`
	Kind       string `json:"kind"`
	Digest     string `json:"digest"`
	State      string `json:"state"`
}

// artifactList reads the published deliverables of one work item.
func (h *journeyHarness) artifactList(t *testing.T, workspaceID, workItemID string) []artifactRecord {
	t.Helper()
	var view struct {
		Artifacts []artifactRecord `json:"artifacts"`
	}
	if status := h.do(t, http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
		"/artifacts", nil, &view); status != http.StatusOK {
		t.Fatalf("read the artifact list: status %d", status)
	}
	return view.Artifacts
}

// blockSuggestionArtifacts controls the isolated database trigger that makes
// the auxiliary suggestion publication fail or hang. It only exists in the
// throwaway journey database; no repository migration or production code is
// involved. Modes: "" fails the publication, "gate" blocks it until released,
// "release" opens the gate, "drop" removes the trigger.
func (h *journeyHarness) blockSuggestionArtifacts(t *testing.T, mode string) {
	t.Helper()
	database, err := postgres.Open(context.Background(), os.Getenv("KOWA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("open the journey database: %v", err)
	}
	defer func() { _ = database.Close() }()
	switch mode {
	case "drop":
		if _, err := database.ExecContext(context.Background(), `
			drop trigger if exists journey_block_suggestion on artifact;
			drop function if exists journey_block_suggestion();
			drop table if exists journey_suggestion_gate`); err != nil {
			t.Fatalf("drop the suggestion trigger: %v", err)
		}
	case "release":
		// Best effort: a cleanup may release after the subtest already dropped
		// the gate.
		if _, err := database.ExecContext(context.Background(), `
			do $$ begin
				insert into journey_suggestion_gate (released) values (1) on conflict do nothing;
			exception when undefined_table then null;
			end $$`); err != nil {
			t.Fatalf("release the suggestion gate: %v", err)
		}
	case "gate":
		if _, err := database.ExecContext(context.Background(), `
			create table if not exists journey_suggestion_gate (released integer primary key);
			delete from journey_suggestion_gate;
			create or replace function journey_block_suggestion() returns trigger as $$
			begin
				if new.kind = 'knowledge_suggestion' then
					for attempt in 1..240 loop
						if exists (select 1 from journey_suggestion_gate) then
							return new;
						end if;
						perform pg_sleep(0.5);
					end loop;
					raise exception 'the suggestion gate was never released';
				end if;
				return new;
			end $$ language plpgsql`); err != nil {
			t.Fatalf("define the suggestion gate: %v", err)
		}
		if _, err := database.ExecContext(context.Background(), `
			drop trigger if exists journey_block_suggestion on artifact;
			create trigger journey_block_suggestion before insert on artifact
				for each row execute function journey_block_suggestion()`); err != nil {
			t.Fatalf("install the suggestion gate: %v", err)
		}
	default:
		if _, err := database.ExecContext(context.Background(), `
			create or replace function journey_block_suggestion() returns trigger as $$
			begin
				if new.kind = 'knowledge_suggestion' then
					raise exception 'injected suggestion outage';
				end if;
				return new;
			end $$ language plpgsql`); err != nil {
			t.Fatalf("define the suggestion trigger: %v", err)
		}
		if _, err := database.ExecContext(context.Background(), `
			drop trigger if exists journey_block_suggestion on artifact;
			create trigger journey_block_suggestion before insert on artifact
				for each row execute function journey_block_suggestion()`); err != nil {
			t.Fatalf("install the suggestion trigger: %v", err)
		}
	}
}

// leaseOnce performs one long-poll lease attempt over the frozen transport.
func (h *journeyHarness) leaseOnce(t *testing.T, out *leasedTask) int {
	t.Helper()
	return h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/tasks/lease", map[string]any{
		"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch,
		"waitSeconds": 1, "leaseSeconds": 300,
	}, out)
}
