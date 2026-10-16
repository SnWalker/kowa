//go:build integration

package server_test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/provider"
	"github.com/SnWalker/kowa/internal/runner"
)

// TestReopenRealClaudePlanAttempt is the S06 rework's real acceptance: the whole
// delivery chain runs on the real stack (real composition root, real HTTPS, real
// PostgreSQL, real local Git remotes) and the plan attempt is executed by the
// real Claude CLI through the production adapter, exactly like a Runner attempt:
// it reads the frozen input delivered into its task directory and its result must
// carry a nonce that only exists inside the requirement text frozen into that
// bundle. The control plane must then publish the plan deliverable and dispatch
// the review on that published ref.
//
// Opt-in: it consumes real provider quota, so neither CI nor the integration
// script passes the gates. Run with:
//
//	KOWA_REAL_PROVIDER_PROBE=true KOWA_REOPEN_WORKFLOW_PROBE=true \
//	  go test -tags integration -count=1 -timeout 900s \
//	  -run TestReopenRealClaudePlanAttempt -v ./internal/server/
func TestReopenRealClaudePlanAttempt(t *testing.T) {
	if os.Getenv("KOWA_REAL_PROVIDER_PROBE") != "true" || os.Getenv("KOWA_REOPEN_WORKFLOW_PROBE") != "true" {
		t.Skip("set KOWA_REAL_PROVIDER_PROBE=true and KOWA_REOPEN_WORKFLOW_PROBE=true to run the real workflow probe")
	}
	h := startJourney(t)
	// This run is really bound to the real provider: the frozen selection, the
	// registered runtime and the adapter that executes the attempt all agree.
	h.registeredProviderID, h.registeredProviderVersion = "claude", realClaudeVersion(t)

	workspaceID := h.createWorkspace(t, "reopen-probe")
	h.configureRepositories(t, workspaceID)
	h.publishExecutionConfig(t, workspaceID)
	h.registerRuntime(t)

	nonce, err := probeNonce()
	if err != nil {
		t.Fatal(err)
	}
	// The scenario stays a plan: the capability prompt itself is generic (the
	// per-capability prompt templates of the fixed workflow stage are not
	// implemented), so the requirement states that nothing may be written and the
	// plan must quote the requirement's own number. Quoting it is only possible
	// by reading the requirement text the control plane delivered.
	title := "Plan Max for the calc package"
	requirement := "只做方案，不要修改或创建任何文件：一句话说明如何在 calc 包新增 Max(a, b int) int。" +
		"需求编号 " + nonce + " 必须逐字出现在方案里。"
	acceptance := "验收标准：go test ./... 全部通过（验收编号 " + nonce + "-ACC）。"
	workItemID := h.createWorkItemWithText(t, workspaceID, title, requirement, acceptance)
	admission := h.startRun(t, workspaceID, workItemID)
	if admission.Recommendation == nil || admission.Recommendation.HumanTaskID == "" {
		t.Fatalf("admission produced no knowledge confirmation: %+v", admission.Recommendation)
	}
	h.answerHumanTask(t, workspaceID, admission.Recommendation.HumanTaskID,
		"approved", map[string]string{"selection": h.indexSelection(t)})

	plan := h.leaseForRealProvider(t, "plan.create")
	manifest := h.verifyFrozenInput(t, plan)
	// Hard assertion: the bytes the real Provider receives carry the frozen
	// requirement and acceptance text, the knowledge binding and the declared
	// output contract.
	assertPlanInput(t, manifest.Bundle, &planInputExpectation{
		RequirementTitle: title,
		Requirement:      requirement,
		Acceptance:       acceptance,
		KnowledgeRepo:    journeyKnowledgeRepository,
		KnowledgeCommit:  admission.Admission.KnowledgeCommitOID,
		DeclaredOutputs:  `{"plan_ref":"artifact_ref"}`,
	})

	outcome := h.executeRealClaudeAttempt(t, plan, manifest)
	if outcome.Status != provider.StatusCompleted {
		t.Fatalf("the real plan attempt did not complete: %+v", outcome)
	}
	encoded, err := json.Marshal(outcome.Output)
	if err != nil {
		t.Fatal(err)
	}
	// Soft assertion (model behaviour): the attempt could only quote the nonce by
	// reading the requirement text the control plane delivered to it.
	read := strings.Contains(outcome.Summary, nonce) || strings.Contains(string(encoded), nonce)
	t.Logf("the real plan attempt quoted the requirement nonce: %t (summary=%q output=%s)",
		read, outcome.Summary, encoded)
	if !read {
		t.Errorf("the real plan attempt did not quote the requirement nonce %s: summary=%q output=%s",
			nonce, outcome.Summary, encoded)
	}

	h.reportResult(t, plan, "completed", digestOf(string(encoded)), outcome.Output)

	// The control plane accepts the real result, publishes the plan deliverable
	// and dispatches the review with that published artifact as its plan_ref.
	review := h.leaseCapability(t, "plan.review")
	reviewManifest := h.verifyFrozenInput(t, review)
	deliverable := h.publishedDeliverableID(t, workspaceID, workItemID, "node_output_plan")
	bound := false
	for _, reference := range reviewManifest.Artifacts {
		if reference.ArtifactID == deliverable {
			bound = true
		}
	}
	if !bound {
		t.Fatalf("the review input does not bind the published plan deliverable %s: %+v",
			deliverable, reviewManifest.Artifacts)
	}
	t.Logf("plan_ref published as artifact %s and bound into the review input %s",
		deliverable, review.Task.InputDigest)
}

// leaseForRealProvider leases one attempt with a lease long enough for a real
// provider run: the real CLI needs minutes, and an expired lease would make the
// accepted result stale.
func (h *journeyHarness) leaseForRealProvider(t *testing.T, capability string) leasedTask {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var lease leasedTask
		status := h.runtimeRequest(t, http.MethodPost, "/api/v1/runtime/tasks/lease", map[string]any{
			"runtimeId": journeyRuntimeID, "runtimeEpoch": journeyRuntimeEpoch,
			"waitSeconds": 1, "leaseSeconds": 1800,
		}, &lease)
		switch status {
		case http.StatusNoContent:
			continue
		case http.StatusOK:
		default:
			t.Fatalf("lease a task: status %d", status)
		}
		if lease.Task.Capability.ID != capability {
			t.Fatalf("leased %s while waiting for %s", lease.Task.Capability.ID, capability)
		}
		return lease
	}
	t.Fatalf("%s never became leasable", capability)
	return leasedTask{}
}

// executeRealClaudeAttempt lays out the attempt task directory exactly like a
// Runner attempt (repos/<id> checkouts at the frozen commits, the delivered
// input/ bundle, logs and tmp) and executes the production Claude adapter
// against it.
func (h *journeyHarness) executeRealClaudeAttempt(
	t *testing.T,
	lease leasedTask,
	manifest inputManifest,
) provider.Outcome {
	t.Helper()
	taskDirectory := t.TempDir()
	for _, directory := range []string{
		filepath.Join(taskDirectory, "repos"), filepath.Join(taskDirectory, "input"),
		filepath.Join(taskDirectory, "logs"), filepath.Join(taskDirectory, "tmp"),
	} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	projectDirectory := ""
	for _, scope := range lease.Task.GitScopes {
		if scope.Operation != "clone" {
			t.Fatalf("the plan attempt scopes an unread operation %q", scope.Operation)
		}
		remote := h.projectRemote
		if scope.Role == "knowledge" {
			remote = h.knowledgeRemote
		}
		directory := filepath.Join(taskDirectory, "repos", scope.RepositoryID)
		runGit(t, "", "clone", "--quiet", remote, directory)
		runGit(t, directory, "checkout", "--quiet", scope.Ref)
		if scope.Role == "project" {
			projectDirectory = directory
		}
	}
	if projectDirectory == "" {
		t.Fatal("the plan attempt carries no project checkout")
	}
	// The bundle is written verbatim: it is the very input the control plane
	// froze and the Runner verified over the transport.
	bundlePath := filepath.Join(taskDirectory, "input", "bundle.json")
	if err := os.WriteFile(bundlePath, manifest.Bundle, 0o600); err != nil {
		t.Fatal(err)
	}

	allowlist := strings.TrimSpace(os.Getenv("KOWA_RUNNER_ENV_ALLOWLIST"))
	if allowlist == "" {
		allowlist = "PATH,HOME,LANG,LC_ALL,TERM"
	}
	environment := runner.BuildWorkerEnvironment(strings.Split(allowlist, ","), os.Environ(),
		map[string]string{"TMPDIR": filepath.Join(taskDirectory, "tmp")})
	adapter := provider.NewClaudeWithCLI("", os.Getenv("KOWA_REAL_CLAUDE_MODEL"))
	adapter.SetProbeEnvironment(environment)

	started := time.Now().UTC()
	outcome, runErr := adapter.Execute(t.Context(), provider.Request{
		TaskID: lease.Task.TaskID,
		Capability: execution.Capability{
			ID: lease.Task.Capability.ID, Version: lease.Task.Capability.Version,
		},
		Workspace: taskDirectory, WorkDir: projectDirectory,
		LogDirectory: filepath.Join(taskDirectory, "logs"),
		BundlePath:   bundlePath, OutputPath: filepath.Join(taskDirectory, "logs", "claude-result.json"),
		Environment: environment, Marker: "reopen-real-probe", Deadline: time.Now().UTC().Add(20 * time.Minute),
	})
	if runErr != nil {
		t.Fatalf("the real attempt failed to run: %v", runErr)
	}
	evidence, _ := json.MarshalIndent(map[string]any{
		"startedAt": started.Format(time.RFC3339), "finishedAt": time.Now().UTC().Format(time.RFC3339),
		"elapsedSeconds": time.Since(started).Seconds(),
		"taskId":         lease.Task.TaskID, "nodeRunId": lease.Task.NodeRunID,
		"inputDigest": lease.Task.InputDigest, "taskDirectory": taskDirectory,
		"cliVersion": outcome.Telemetry.CLIVersion, "model": outcome.Telemetry.Model,
		"exitCode": outcome.Telemetry.ExitCode, "residual": outcome.Telemetry.Residual,
		"status": outcome.Status, "summary": outcome.Summary, "output": outcome.Output,
		"failure": outcome.Failure,
	}, "", "  ")
	t.Logf("real plan attempt evidence:\n%s", evidence)
	t.Logf("the CLI's own result follows:\n%s", h.claudeResultExcerpt(t, taskDirectory))
	return outcome
}

// claudeResultExcerpt reports the CLI's own result document: the evidence must
// show what the attempt actually ran, even when the assertion below fails.
func (h *journeyHarness) claudeResultExcerpt(t *testing.T, taskDirectory string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(taskDirectory, "logs", "claude-result.json"))
	if err != nil {
		return fmt.Sprintf("(no claude-result.json: %v)", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return fmt.Sprintf("(claude-result.json is not JSON: %v)", err)
	}
	excerpt, _ := json.MarshalIndent(map[string]any{
		"subtype": document["subtype"], "is_error": document["is_error"],
		"num_turns": document["num_turns"], "duration_ms": document["duration_ms"],
		"permission_denials": document["permission_denials"],
		"result":             truncate(document["result"], 1200),
	}, "", "  ")
	return string(excerpt)
}

func truncate(value any, limit int) string {
	text, _ := value.(string)
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…(truncated)"
}

// publishedDeliverableID reads the artifact id of one published deliverable kind.
func (h *journeyHarness) publishedDeliverableID(
	t *testing.T,
	workspaceID, workItemID, kind string,
) string {
	t.Helper()
	var artifacts struct {
		Artifacts []struct {
			ArtifactID string `json:"artifactId"`
			Kind       string `json:"kind"`
			State      string `json:"state"`
		} `json:"artifacts"`
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if status := h.do(t, "GET", "/api/v1/workspaces/"+workspaceID+"/work-items/"+workItemID+
			"/artifacts", nil, &artifacts); status != 200 {
			t.Fatalf("read the artifact list: status %d", status)
		}
		for _, record := range artifacts.Artifacts {
			if record.Kind == kind && record.State == "PUBLISHED" {
				return record.ArtifactID
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no published %s deliverable exists: %+v", kind, artifacts.Artifacts)
	return ""
}

// realClaudeVersion reports the CLI version the registered runtime advertises.
func realClaudeVersion(t *testing.T) string {
	t.Helper()
	adapter := provider.NewClaudeWithCLI("", "")
	probe, err := adapter.Probe(t.Context())
	if err != nil {
		t.Fatalf("probe the real Claude CLI: %v", err)
	}
	if !probe.Executable || probe.Version == "" {
		t.Fatalf("the real Claude CLI is not usable: %+v", probe)
	}
	return probe.Version
}

// probeNonce gives every probe run a fresh identity: the requirement it is
// embedded into can never be confused with an earlier run's text.
func probeNonce() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "KOWA-REQ-" + hex.EncodeToString(buffer), nil
}
