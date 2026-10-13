package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/provider"
	"github.com/SnWalker/kowa/internal/runner"
	"github.com/SnWalker/kowa/internal/runnertransport"
)

type staticAuthorizer map[string]string

func (a staticAuthorizer) AuthenticateRuntime(token string) (string, bool) {
	runtimeID, ok := a[token]
	return runtimeID, ok
}

// flakyTransport forwards every request and then discards the first response
// for a chosen path, simulating a delivery whose response was lost.
type flakyTransport struct {
	inner      http.RoundTripper
	pathSuffix string
	failOnce   atomic.Bool
}

func (f *flakyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, f.pathSuffix) && f.failOnce.CompareAndSwap(true, false) {
		response, err := f.inner.RoundTrip(request)
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		return nil, errors.New("simulated response loss")
	}
	return f.inner.RoundTrip(request)
}

type loopFixture struct {
	t        *testing.T
	store    *execution.MemoryStore
	service  *execution.Service
	server   *httptest.Server
	client   *runnertransport.Client
	flaky    *flakyTransport
	journal  *runner.Journal
	workRoot string
	logger   *slog.Logger
	now      time.Time
	epoch    string
	loop     *runner.Loop
	git      *runner.GitChannel
	config   runner.LoopConfig
	// taskDeadline overrides the seeded task deadline for bounded-window tests.
	taskDeadline time.Duration
}

func newLoopFixture(t *testing.T, script string, configure func(*runner.LoopConfig)) *loopFixture {
	t.Helper()
	f := &loopFixture{t: t, now: time.Now().UTC().Truncate(time.Second)}
	f.store = execution.NewMemoryStore()
	f.service = execution.NewService(f.store, func() time.Time { return f.now })
	handler := httpapi.NewRuntimeTransportHandler(
		staticAuthorizer{"token-1": "runtime-1"}, f.service, scriptedInput{bundle: loopBundle()},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler.PollInterval = 20 * time.Millisecond
	f.server = httptest.NewServer(handler)
	t.Cleanup(f.server.Close)
	f.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	f.workRoot = t.TempDir()
	journal, err := runner.NewJournal(f.workRoot)
	if err != nil {
		t.Fatal(err)
	}
	f.journal = journal
	// The epoch lives in the journal state, so direct leases in tests use the
	// same generation the loop will register with.
	if f.epoch, err = journal.Epoch(); err != nil {
		t.Fatal(err)
	}
	f.flaky = &flakyTransport{inner: http.DefaultTransport, pathSuffix: "/result"}
	f.flaky.failOnce.Store(false)
	f.client = runnertransport.NewClient(f.server.URL, "runtime-1", "token-1")
	f.client.Epoch = f.epoch
	f.client.HTTP = &http.Client{Transport: f.flaky, Timeout: 30 * time.Second}
	config := runner.LoopConfig{
		RuntimeID: "runtime-1", GitHubUserID: "9001",
		Capabilities:    []execution.Capability{{ID: "plan.create", Version: 1}},
		ProviderVersion: "scripted-1",
		LeaseSeconds:    60, WaitSeconds: 1,
		HeartbeatEvery: 150 * time.Millisecond,
		ReportWindow:   2 * time.Second,
		BackoffMin:     50 * time.Millisecond, BackoffMax: 500 * time.Millisecond,
		WorkRoot: f.workRoot,
	}
	if configure != nil {
		configure(&config)
	}
	f.config = config
	f.loop = f.newLoop(script, config)
	return f
}

func (f *loopFixture) newLoop(script string, config runner.LoopConfig) *runner.Loop {
	f.t.Helper()
	adapter := provider.NewScripted(script)
	host := runner.NewHost(adapter, runner.HostConfig{
		Environment:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")},
		EnvAllowlist: []string{"PATH", "HOME"},
		Grace:        time.Second, ReportMargin: 5 * time.Second, Now: time.Now,
	}, f.logger)
	return runner.NewLoop(f.client, f.journal, host, adapter, f.git, config, f.logger)
}

func (f *loopFixture) seedRun(suffix string) {
	f.t.Helper()
	command := execution.CreateRunCommand{
		Run: execution.Run{
			ID: "run-" + suffix, WorkspaceID: "workspace-1", WorkItemID: "work-" + suffix,
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: loopDigest, State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: "node-" + suffix, RunID: "run-" + suffix, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: loopDigest, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: "task-" + suffix, NodeRunID: "node-" + suffix, Attempt: 1,
			AttemptKind: execution.AttemptInitial,
			Capability:  execution.Capability{ID: "plan.create", Version: 1},
			Provider:    execution.ProviderSelection{ID: "scripted", Version: "scripted-1"},
			GitScopes:   []execution.GitScope{}, State: execution.TaskQueued,
			InputDigest: loopDigest, DeadlineAt: f.now.Add(f.deadline()),
		}},
	}
	if err := f.service.CreateRun(f.t.Context(), command); err != nil {
		f.t.Fatal(err)
	}
}

// loopDigest is the digest of the frozen input every loop fixture serves, so
// the task's frozen inputDigest and the delivery chain verify end to end.
// seedRunWithScopes seeds the standard run but with explicit git scopes.
func (f *loopFixture) seedRunWithScopes(suffix string, scopes []execution.GitScope) {
	f.t.Helper()
	command := execution.CreateRunCommand{
		Run: execution.Run{
			ID: "run-" + suffix, WorkspaceID: "workspace-1", WorkItemID: "work-" + suffix,
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: loopDigest, State: execution.RunActive, Version: 1,
			GitExecutionUserID: "9001",
		},
		Nodes: []execution.NodeRun{{
			ID: "node-" + suffix, RunID: "run-" + suffix, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: loopDigest, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: "task-" + suffix, NodeRunID: "node-" + suffix, Attempt: 1,
			AttemptKind: execution.AttemptInitial,
			Capability:  execution.Capability{ID: "plan.create", Version: 1},
			Provider:    execution.ProviderSelection{ID: "scripted", Version: "scripted-1"},
			GitScopes:   scopes, GitExecutionUserID: "9001",
			State:       execution.TaskQueued,
			InputDigest: loopDigest, DeadlineAt: f.now.Add(f.deadline()),
		}},
	}
	if err := f.service.CreateRun(f.t.Context(), command); err != nil {
		f.t.Fatal(err)
	}
}

var loopDigest = artifact.Digest(loopBundle())

// loopBundle is the frozen input every loop fixture serves.
func loopBundle() []byte { return []byte(`{"fixture":"frozen input for the loop tests"}`) }

// scriptedInput is a deterministic TaskInputProvider for tests.
type scriptedInput struct {
	bundle    []byte
	artifacts []artifact.Ref
	contents  map[string][]byte
	unbound   bool
	tamper    bool
}

func (s scriptedInput) TaskInput(context.Context, string) (runnertransport.InputManifestResponse, error) {
	if s.unbound {
		return runnertransport.InputManifestResponse{}, httpapi.ErrTaskInputUnbound
	}
	bundle := s.bundle
	if s.tamper {
		bundle = append(append([]byte{}, bundle...), '!')
	}
	return runnertransport.InputManifestResponse{
		Bundle: bundle, BundleDigest: artifact.Digest(bundle), Artifacts: s.artifacts,
	}, nil
}

func (s scriptedInput) TaskInputArtifact(_ context.Context, _, artifactID string) ([]byte, error) {
	content, ok := s.contents[artifactID]
	if !ok {
		return nil, httpapi.ErrTaskInputUnbound
	}
	if s.tamper {
		return append(content, '!'), nil
	}
	return content, nil
}

func (f *loopFixture) deadline() time.Duration {
	if f.taskDeadline > 0 {
		return f.taskDeadline
	}
	return time.Hour
}

func (f *loopFixture) start(ctx context.Context) chan error {
	f.t.Helper()
	outcome := make(chan error, 1)
	go func() { outcome <- f.loop.Run(ctx) }()
	return outcome
}

func (f *loopFixture) waitFor(description string, condition func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.t.Fatalf("timed out waiting for %s", description)
}

func (f *loopFixture) taskState(taskID string) execution.TaskState {
	f.t.Helper()
	status, err := f.service.TaskStatus(f.t.Context(), taskID)
	if err != nil {
		return ""
	}
	return status.State
}

func counterScript(counterPath string) string {
	return fmt.Sprintf(
		`echo executed >> %s; printf '%%s' '{"status":"completed","summary":"scripted done","output":{"verdict":"approved"}}' > "$KOWA_SCRIPTED_OUTPUT"`,
		counterPath)
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(content)))
}

func TestLoopExecutesAndSettlesOneAttempt(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	f := newLoopFixture(t, counterScript(counter), nil)
	f.seedRun("a")
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("task completed", func() bool { return f.taskState("task-a") == execution.TaskCompleted })
	view, err := f.service.RunView(t.Context(), "run-a")
	if err != nil || view.Nodes[0].State != execution.NodeDecided || view.Nodes[0].Verdict != "approved" {
		t.Fatalf("run view = %+v, %v", view, err)
	}
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("executions = %d, want exactly 1", got)
	}
	// The journal must hold no residue once the result is accepted.
	attempts, err := f.journal.Attempts()
	if err != nil || len(attempts) != 0 {
		t.Fatalf("journal attempts after settle = %+v, %v", attempts, err)
	}
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

func TestLoopDoesNotExecuteTwiceWhenTheResponseIsLost(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	f := newLoopFixture(t, counterScript(counter), nil)
	f.flaky.failOnce.Store(true)
	f.seedRun("a")
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("task completed", func() bool { return f.taskState("task-a") == execution.TaskCompleted })
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("executions = %d, want exactly 1 despite the lost response", got)
	}
	// Give the loop a moment to clear the journal after the duplicate ack.
	f.waitFor("journal cleared", func() bool {
		attempts, _ := f.journal.Attempts()
		return len(attempts) == 0
	})
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

func TestLoopRestartConvergesInFlightAttemptWithoutReExecution(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	f := newLoopFixture(t, counterScript(counter), nil)
	f.seedRun("a")
	// A previous process registered, leased task-a and died mid-execution.
	if _, err := f.client.Register(t.Context(), execution.RuntimeRegistration{
		ID: "runtime-1", Epoch: f.epoch, GitHubUserID: "9001",
		Provider:              execution.ProviderSelection{ID: "scripted", Version: "scripted-1"},
		ProviderAuthenticated: true,
		Capabilities:          []execution.Capability{{ID: "plan.create", Version: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	lease, found, err := f.client.Lease(t.Context(), 0, 300)
	if err != nil || !found {
		t.Fatalf("lease = %+v, %v", lease, err)
	}
	if err := f.journal.SaveAttempt(runner.AttemptRecord{
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		RuntimeID: "runtime-1", RuntimeEpoch: f.epoch,
		WorkflowRunID: lease.Task.WorkflowRunID, NodeRunID: lease.Task.NodeRunID,
		AttemptKind: lease.Task.AttemptKind, Capability: lease.Task.Capability,
		Phase: runner.PhaseExecuting, Workspace: filepath.Join(f.workRoot, "tasks", "stale"),
		Marker: "stale-marker", StartedAt: f.now, UpdatedAt: f.now,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("restarted attempt converged", func() bool { return f.taskState("task-a") == execution.TaskFailed })
	if got := countLines(t, counter); got != 0 {
		t.Fatalf("executions = %d, want 0: a restart must never re-execute", got)
	}
	status, err := f.service.TaskStatus(t.Context(), "task-a")
	if err != nil || !status.ResultAccepted {
		t.Fatalf("status = %+v, %v", status, err)
	}
	f.waitFor("journal cleared", func() bool {
		attempts, _ := f.journal.Attempts()
		return len(attempts) == 0
	})
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

func TestLoopReplaysJournalledResultWithoutReExecution(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	config := func(c *runner.LoopConfig) { c.ReportWindow = 300 * time.Millisecond }
	f := newLoopFixture(t, counterScript(counter), config)
	// Make every result delivery fail so the loop must journal and defer.
	f.flaky.pathSuffix = "/result"
	f.flaky.failOnce.Store(false)
	f.client.HTTP = &http.Client{Transport: alwaysFailPath{inner: http.DefaultTransport, pathSuffix: "/result"}}
	f.seedRun("a")
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("result deferred to journal", func() bool {
		_, pending, _ := f.journal.Result("task-a")
		return pending
	})
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("executions = %d, want 1", got)
	}
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}

	// A new process starts with a healthy transport: the journalled payload is
	// re-delivered byte for byte and the attempt settles as a duplicate.
	f.client.HTTP = &http.Client{Transport: http.DefaultTransport}
	f.loop = f.newLoop(counterScript(counter), runner.LoopConfig{
		RuntimeID: "runtime-1", GitHubUserID: "9001",
		Capabilities:    []execution.Capability{{ID: "plan.create", Version: 1}},
		ProviderVersion: "scripted-1",
		LeaseSeconds:    60, WaitSeconds: 1,
		HeartbeatEvery: 150 * time.Millisecond, ReportWindow: 2 * time.Second,
		BackoffMin: 50 * time.Millisecond, BackoffMax: 500 * time.Millisecond, WorkRoot: f.workRoot,
	})
	ctx2, cancel2 := context.WithCancel(t.Context())
	outcome2 := f.start(ctx2)
	f.waitFor("replayed result accepted", func() bool { return f.taskState("task-a") == execution.TaskCompleted })
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("executions = %d, want 1: replay must not re-execute", got)
	}
	f.waitFor("journal cleared", func() bool {
		attempts, _ := f.journal.Attempts()
		return len(attempts) == 0
	})
	cancel2()
	if err := <-outcome2; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

// alwaysFailPath fails every request to a path, simulating a persistent outage.
type alwaysFailPath struct {
	inner      http.RoundTripper
	pathSuffix string
}

func (f alwaysFailPath) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.HasSuffix(request.URL.Path, f.pathSuffix) {
		return nil, errors.New("simulated persistent outage")
	}
	return f.inner.RoundTrip(request)
}

func TestLoopCancellationConverges(t *testing.T) {
	script := `sleep 30; printf '%s' '{"status":"completed","summary":"late","output":{"verdict":"approved"}}' > "$KOWA_SCRIPTED_OUTPUT"`
	f := newLoopFixture(t, script, nil)
	f.seedRun("a")
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("task leased", func() bool {
		return f.taskState("task-a") == execution.TaskLeased || f.taskState("task-a") == execution.TaskRunning
	})
	view, err := f.service.RunView(t.Context(), "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.CancelRun(t.Context(), "run-a", view.Version); err != nil {
		t.Fatal(err)
	}
	// The heartbeat delivers the directive, the worker is terminated and the
	// attempt settles as a cancellation, never as success.
	f.waitFor("canceled attempt settled", func() bool { return f.taskState("task-a") == execution.TaskFailed })
	if state := f.view(t, "run-a"); state != execution.RunCanceling {
		t.Fatalf("run after canceled attempt = %s, want CANCELING", state)
	}
	if _, err := f.service.SweepExpiredLeases(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := f.view(t, "run-a"); state != execution.RunCanceled {
		t.Fatalf("run after sweep = %s, want CANCELED", state)
	}
	// Cancellation evidence exists and no completed outcome was ever written.
	workspaces, err := filepath.Glob(filepath.Join(f.workRoot, "tasks", "*"))
	if err != nil || len(workspaces) == 0 {
		t.Fatalf("attempt workspaces = %v, %v", workspaces, err)
	}
	evidence, err := os.ReadFile(filepath.Join(workspaces[0], "evidence.json"))
	if err != nil || !strings.Contains(string(evidence), `"failureCode": "canceled"`) {
		t.Fatalf("cancellation evidence = %s, %v", evidence, err)
	}
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

func TestLoopStopsOnSupersededEpoch(t *testing.T) {
	f := newLoopFixture(t, counterScript(filepath.Join(t.TempDir(), "counter")), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	outcome := f.start(ctx)
	// A new generation registers while the first process is still polling.
	newcomer := runnertransport.NewClient(f.server.URL, "runtime-1", "token-1")
	newcomer.Epoch = "epoch-2"
	f.waitFor("initial registration", func() bool {
		status, err := f.service.RuntimeStatus(t.Context(), "runtime-1")
		return err == nil && status.Epoch != ""
	})
	if _, err := newcomer.Register(t.Context(), execution.RuntimeRegistration{
		ID: "runtime-1", Epoch: "epoch-2", GitHubUserID: "9001",
		Provider:              execution.ProviderSelection{ID: "scripted", Version: "scripted-1"},
		ProviderAuthenticated: true,
		Capabilities:          []execution.Capability{{ID: "plan.create", Version: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-outcome:
		if !errors.Is(err, runnertransport.ErrSuperseded) {
			t.Fatalf("superseded loop exit = %v, want supersession", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after its epoch was superseded")
	}
}

func (f *loopFixture) view(t *testing.T, runID string) execution.RunState {
	t.Helper()
	view, err := f.service.RunView(t.Context(), runID)
	if err != nil {
		return ""
	}
	return view.State
}

// rewritingHandler replaces a successful frozen-input manifest with content the
// server itself never verified, simulating a lying/broken control plane.
type rewritingHandler struct {
	next   http.Handler
	bundle []byte
}

func (w rewritingHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/input") {
		recorder := httptest.NewRecorder()
		w.next.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusOK {
			body, err := json.Marshal(runnertransport.InputManifestResponse{
				Bundle: w.bundle, BundleDigest: artifact.Digest(w.bundle),
			})
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(body)
			return
		}
		for key, values := range recorder.Header() {
			writer.Header()[key] = values
		}
		writer.WriteHeader(recorder.Code)
		_, _ = writer.Write(recorder.Body.Bytes())
		return
	}
	w.next.ServeHTTP(writer, request)
}

func TestLoopRefusesUnverifiedInputWithoutExecution(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	f := newLoopFixture(t, counterScript(counter), nil)
	// The control plane serves a bundle whose digest is not the task's frozen
	// inputDigest: the Runner must refuse to execute and settle the attempt.
	lying := httptest.NewServer(rewritingHandler{next: f.server.Config.Handler, bundle: []byte(`{"lying":"bundle"}`)})
	t.Cleanup(lying.Close)
	f.client.BaseURL = lying.URL
	f.seedRun("a")
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("unverified attempt settled", func() bool { return f.taskState("task-a") == execution.TaskFailed })
	if got := countLines(t, counter); got != 0 {
		t.Fatalf("executions = %d, want 0: unverified input must not execute", got)
	}
	status, err := f.service.TaskStatus(t.Context(), "task-a")
	if err != nil || !status.ResultAccepted {
		t.Fatalf("status = %+v, %v", status, err)
	}
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

func TestLoopSettlesUnboundInputAsRetryable(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	f := newLoopFixture(t, counterScript(counter), nil)
	// Replace the input provider with an unbound one and shorten the task
	// deadline so the bounded fetch window closes quickly.
	handler := httpapi.NewRuntimeTransportHandler(
		staticAuthorizer{"token-1": "runtime-1"}, f.service, scriptedInput{unbound: true},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler.PollInterval = 20 * time.Millisecond
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f.client.BaseURL = server.URL
	f.taskDeadline = 300 * time.Millisecond
	f.seedRun("a")
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	f.waitFor("unavailable attempt settled", func() bool { return f.taskState("task-a") == execution.TaskFailed })
	if got := countLines(t, counter); got != 0 {
		t.Fatalf("executions = %d, want 0", got)
	}
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
}

// TestLoopMaterializesAndDeliversThroughTheGitChannel is the M3 loop journey:
// a task with read and push scopes materializes the frozen project checkout,
// the worker edits it, and the controlled channel commits/pushes a work branch
// whose delivery evidence is attached to the accepted result.
func TestLoopMaterializesAndDeliversThroughTheGitChannel(t *testing.T) {
	bare, baseOid := seedBareRepository(t)
	counter := filepath.Join(t.TempDir(), "counter")
	// The worker's cwd is the project checkout; it edits a file and returns the
	// frozen outcome envelope.
	script := `echo change >> delivered.txt; ` + counterScript(counter)
	f := newLoopFixture(t, script, nil)
	f.git = runner.NewTestGitChannel(map[string]string{"1001": bare}, []string{"main", "master"},
		func(context.Context) (string, error) { return "9001", nil },
		os.Environ(), f.logger)
	if os.Getenv("KOWA_TEST_DEBUG_LOGS") == "1" {
		f.logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	f.loop = f.newLoop(script, f.config)
	f.seedRunWithScopes("m3", []execution.GitScope{
		{RepositoryID: "1001", Role: "project", Operation: "clone", Ref: baseOid},
		{RepositoryID: "1001", Role: "project", Operation: "push", Ref: "task/m3-loop"},
	})
	ctx, cancel := context.WithCancel(t.Context())
	outcome := f.start(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && f.taskState("task-m3") != execution.TaskCompleted {
		time.Sleep(25 * time.Millisecond)
	}
	if state := f.taskState("task-m3"); state != execution.TaskCompleted {
		status, _ := f.service.TaskStatus(t.Context(), "task-m3")
		workspaces, _ := filepath.Glob(filepath.Join(f.workRoot, "tasks", "*"))
		for _, workspace := range workspaces {
			_ = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
				if err == nil && !info.IsDir() {
					content, _ := os.ReadFile(path)
					t.Logf("workspace %s: %s", path, string(content)[:min(600, len(content))])
				}
				return nil
			})
		}
		t.Fatalf("state=%s status=%+v counter=%d", state, status, countLines(t, counter))
	}
	cancel()
	if err := <-outcome; err != nil {
		t.Fatalf("loop exit = %v", err)
	}
	if got := countLines(t, counter); got != 1 {
		t.Fatalf("executions = %d, want 1", got)
	}
	delivered := strings.TrimSpace(runGitInTest(t, bare, "rev-parse", "task/m3-loop"))
	if delivered == "" || delivered == baseOid {
		t.Fatalf("remote work branch = %q, want a delivered commit", delivered)
	}
	// The accepted result carries compact delivery evidence, not a local path.
	view, err := f.service.RunView(t.Context(), "run-m3")
	if err != nil || len(view.Nodes) != 1 {
		t.Fatalf("run view = %+v, %v", view, err)
	}
	delivery, ok := view.Nodes[0].Output["delivery"]
	if !ok {
		t.Fatalf("accepted output lost delivery evidence: %+v", view.Nodes[0].Output)
	}
	entries, ok := delivery.([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("delivery evidence = %T %+v", delivery, delivery)
	}
	entry, ok := entries[0].(map[string]any)
	if !ok || entry["commitOid"] != delivered || entry["branch"] != "task/m3-loop" ||
		entry["baseOid"] != baseOid || entry["reconciled"] != false {
		t.Fatalf("delivery entry = %+v", entry)
	}
	// The attempt workspace records the materialization and delivery evidence.
	workspaces, err := filepath.Glob(filepath.Join(f.workRoot, "tasks", "*"))
	if err != nil || len(workspaces) == 0 {
		t.Fatalf("attempt workspaces = %v, %v", workspaces, err)
	}
	deliveryFiles, err := filepath.Glob(filepath.Join(workspaces[0], "delivery-*.json"))
	if err != nil || len(deliveryFiles) != 1 {
		t.Fatalf("delivery evidence files = %v, %v", deliveryFiles, err)
	}
	materialization, err := os.ReadFile(filepath.Join(workspaces[0], "materialization.json"))
	if err != nil || !strings.Contains(string(materialization), baseOid) {
		t.Fatalf("materialization evidence = %s, %v", materialization, err)
	}
}
