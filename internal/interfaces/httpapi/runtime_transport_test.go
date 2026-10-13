package httpapi_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/runnertransport"
)

const (
	transportDigest    = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	transportDigestAlt = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

type staticAuthorizer map[string]string

func (a staticAuthorizer) AuthenticateRuntime(token string) (string, bool) {
	runtimeID, ok := a[token]
	return runtimeID, ok
}

type transportFixture struct {
	t       *testing.T
	service *execution.Service
	handler *httpapi.RuntimeTransportHandler
	server  *httptest.Server
	now     time.Time
	client  *runnertransport.Client
	client2 *runnertransport.Client
}

func newTransportFixture(t *testing.T) *transportFixture {
	t.Helper()
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	store := execution.NewMemoryStore()
	service := execution.NewService(store, func() time.Time { return now })
	handler := httpapi.NewRuntimeTransportHandler(
		staticAuthorizer{"token-r1": "runtime-1", "token-r2": "runtime-2"},
		service,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	handler.PollInterval = 5 * time.Millisecond
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	f := &transportFixture{t: t, service: service, handler: handler, server: server, now: now}
	f.client = runnertransport.NewClient(server.URL, "runtime-1", "token-r1")
	f.client.Epoch = "epoch-1"
	f.client2 = runnertransport.NewClient(server.URL, "runtime-2", "token-r2")
	f.client2.Epoch = "epoch-2"
	return f
}

func (f *transportFixture) registration(runtimeID, epoch string) execution.RuntimeRegistration {
	return execution.RuntimeRegistration{
		ID: runtimeID, Epoch: epoch, GitHubUserID: "9001",
		Provider:              execution.ProviderSelection{ID: "codex", Version: "1.0"},
		ProviderAuthenticated: true,
		Capabilities:          []execution.Capability{{ID: "plan.create", Version: 1}},
	}
}

// seedRun creates one active run whose single node has one queued task for the
// runtime-1 provider/capability declaration.
func (f *transportFixture) seedRun(suffix string) {
	f.t.Helper()
	command := execution.CreateRunCommand{
		Run: execution.Run{
			ID: "run-" + suffix, WorkspaceID: "workspace-1", WorkItemID: "work-" + suffix,
			StartedByGitHubUserID: "101", DefinitionName: "delivery", DefinitionVersion: 1,
			DefinitionDigest: transportDigest, State: execution.RunActive, Version: 1,
		},
		Nodes: []execution.NodeRun{{
			ID: "node-" + suffix, RunID: "run-" + suffix, NodeID: "plan", Iteration: 1,
			State: execution.NodeReady, InputDigest: transportDigest, Version: 1,
		}},
		Tasks: []execution.Task{{
			ID: "task-" + suffix, NodeRunID: "node-" + suffix, Attempt: 1,
			AttemptKind: execution.AttemptInitial,
			Capability:  execution.Capability{ID: "plan.create", Version: 1},
			Provider:    execution.ProviderSelection{ID: "codex", Version: "1.0"},
			GitScopes:   []execution.GitScope{}, State: execution.TaskQueued,
			InputDigest: transportDigest, DeadlineAt: f.now.Add(time.Hour),
		}},
	}
	if err := f.service.CreateRun(f.t.Context(), command); err != nil {
		f.t.Fatal(err)
	}
}

func (f *transportFixture) lease(client *runnertransport.Client) execution.TaskLease {
	f.t.Helper()
	lease, found, err := client.Lease(f.t.Context(), 0, 1800)
	if err != nil || !found {
		f.t.Fatalf("lease = %+v, found=%v, err=%v", lease, found, err)
	}
	return lease
}

func (f *transportFixture) completedReport(lease execution.TaskLease) execution.ResultReport {
	return execution.ResultReport{
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: transportDigest,
		Summary: "synthetic result", Output: map[string]any{"verdict": "approved"},
	}
}

func TestRuntimeTransportRegister(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()

	// Unknown credentials must not register anything.
	response, err := http.Post(f.server.URL+"/api/v1/runtime/register", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated register = %d, want 401", response.StatusCode)
	}
	anonymous := runnertransport.NewClient(f.server.URL, "runtime-1", "wrong-token")
	anonymous.Epoch = "epoch-1"
	if _, err := anonymous.Register(ctx, f.registration("runtime-1", "epoch-1")); err == nil {
		t.Fatal("unknown token registered")
	}

	// A credential may only register its own runtimeId (raw request, because the
	// client refuses the mismatch locally before sending).
	crossRuntime := `{"schemaVersion":"kowa.workflow-execution.v1","kind":"runtimeRegistration","runtimeId":"runtime-2","runtimeEpoch":"epoch-1","gitHubUserId":"9001","provider":{"id":"codex","version":"1.0"},"providerAuthenticated":true,"capabilities":[{"id":"plan.create","version":1}],"registeredAt":"2026-10-14T00:00:00Z"}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.server.URL+"/api/v1/runtime/register", strings.NewReader(crossRuntime))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer token-r1")
	request.Header.Set("Content-Type", "application/json")
	crossResponse, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = crossResponse.Body.Close()
	if crossResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-runtime registration = %d, want 403", crossResponse.StatusCode)
	}
	if _, err := f.service.RuntimeStatus(ctx, "runtime-2"); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("cross-runtime registration persisted: %v", err)
	}

	// Strict wire decoding: unknown fields and trailing data are rejected.
	request, err = http.NewRequestWithContext(ctx, http.MethodPost,
		f.server.URL+"/api/v1/runtime/register", strings.NewReader(
			`{"schemaVersion":"kowa.workflow-execution.v1","kind":"runtimeRegistration","runtimeId":"runtime-1","runtimeEpoch":"epoch-1","gitHubUserId":"9001","provider":{"id":"codex","version":"1.0"},"providerAuthenticated":true,"capabilities":[{"id":"plan.create","version":1}],"registeredAt":"2026-10-14T00:00:00Z","extra":true}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer token-r1")
	request.Header.Set("Content-Type", "application/json")
	raw, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Body.Close()
	if raw.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field register = %d, want 400", raw.StatusCode)
	}

	// Idempotent re-registration and conflicting declaration for the same epoch.
	ack, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1"))
	if err != nil || ack.RuntimeID != "runtime-1" || ack.RuntimeEpoch != "epoch-1" {
		t.Fatalf("register = %+v, %v", ack, err)
	}
	if _, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1")); err != nil {
		t.Fatalf("idempotent register = %v", err)
	}
	conflict := f.registration("runtime-1", "epoch-1")
	conflict.ProviderAuthenticated = false
	if _, err := f.client.Register(ctx, conflict); err == nil {
		t.Fatal("conflicting declaration accepted")
	} else if !errors.Is(err, runnertransport.ErrConflict) {
		t.Fatalf("conflicting declaration = %v, want conflict", err)
	}
	// The rejected declaration must not have changed the persisted generation.
	status, err := f.service.RuntimeStatus(ctx, "runtime-1")
	if err != nil || !status.ProviderAuthenticated || status.Epoch != "epoch-1" {
		t.Fatalf("persisted registration after conflict = %+v, %v", status, err)
	}
}

func TestRuntimeTransportLeaseAndEpoch(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	if _, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1")); err != nil {
		t.Fatal(err)
	}

	// Empty poll with waitSeconds=0 returns 204 and no lease.
	lease, found, err := f.client.Lease(ctx, 0, 1800)
	if err != nil || found {
		t.Fatalf("empty lease = %+v, found=%v, err=%v", lease, found, err)
	}

	f.seedRun("a")
	lease = f.lease(f.client)
	if lease.Task.ID != "task-a" || lease.Task.ProviderSelection.ID != "codex" {
		t.Fatalf("lease = %+v", lease)
	}
	// The same task is not re-leased while its attempt is in flight.
	if _, found, err := f.client.Lease(ctx, 0, 1800); err != nil || found {
		t.Fatalf("second lease = found=%v, err=%v", found, err)
	}

	// Bounds are enforced before any dispatch.
	if _, _, err := f.client.Lease(ctx, 0, 5); err == nil {
		t.Fatal("out-of-bounds leaseSeconds accepted")
	}

	// A lease under a superseded epoch is rejected without state change; the
	// Runner client classifies it as fatal supersession.
	runtime2 := f.registration("runtime-1", "epoch-2")
	if _, err := f.client.Register(ctx, runtime2); err != nil {
		t.Fatal(err)
	}
	old := runnertransport.NewClient(f.server.URL, "runtime-1", "token-r1")
	old.Epoch = "epoch-1"
	status, err := f.service.TaskStatus(ctx, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := old.Lease(ctx, 0, 1800); err == nil || !errors.Is(err, runnertransport.ErrSuperseded) {
		t.Fatalf("superseded lease = %v, want supersession", err)
	}
	after, err := f.service.TaskStatus(ctx, "task-a")
	if err != nil || after != status {
		t.Fatalf("superseded lease changed facts: %+v -> %+v", status, after)
	}

	// A progress report for the superseded generation is stale, not accepted.
	report := execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.ProgressEventKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Sequence: 1, Phase: "working", Message: "synthetic", ObservedAt: f.now,
	}
	if err := old.ReportProgress(ctx, report); err == nil {
		t.Fatal("superseded progress accepted")
	}
}

func TestRuntimeTransportHeartbeatCancellation(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	if _, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1")); err != nil {
		t.Fatal(err)
	}
	f.seedRun("a")
	lease := f.lease(f.client)

	// A heartbeat must not extend the lease: the expiry is byte-identical.
	before, err := f.service.TaskStatus(ctx, lease.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := f.client.Heartbeat(ctx, []string{lease.Task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.CancelTaskIDs) != 0 {
		t.Fatalf("active run cancel directives = %+v", ack)
	}
	after, err := f.service.TaskStatus(ctx, lease.Task.ID)
	if err != nil || !after.LeaseExpiresAt.Equal(before.LeaseExpiresAt) {
		t.Fatalf("heartbeat changed lease expiry: %+v -> %+v", before, after)
	}

	// Cancelling the run produces a directive while work remains in flight.
	view, err := f.service.RunView(ctx, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.CancelRun(ctx, "run-a", view.Version); err != nil {
		t.Fatal(err)
	}
	ack, err = f.client.Heartbeat(ctx, []string{lease.Task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(ack.CancelTaskIDs) != 1 || ack.CancelTaskIDs[0] != lease.Task.ID {
		t.Fatalf("cancel directives = %+v", ack)
	}

	// The runner settles the canceled attempt: the intent survives and the
	// sweep finalizes the run.
	canceled := f.completedReport(lease)
	canceled.Status = execution.ResultFailed
	canceled.Output = nil
	canceled.Error = &execution.TaskError{Code: "canceled", Message: "runner canceled the attempt", Retryable: false}
	if _, err := f.client.ReportResult(ctx, canceled); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.RunView(ctx, "run-a")
	if err != nil || view.State != execution.RunCanceling {
		t.Fatalf("run after canceled report = %+v, %v", view, err)
	}
	if _, err := f.service.SweepExpiredLeases(ctx); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.RunView(ctx, "run-a")
	if err != nil || view.State != execution.RunCanceled {
		t.Fatalf("run after sweep = %+v, %v", view, err)
	}
}

func TestRuntimeTransportOwnershipAndStrictness(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	if _, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client2.Register(ctx, f.registration("runtime-2", "epoch-2")); err != nil {
		t.Fatal(err)
	}
	f.seedRun("a")
	lease := f.lease(f.client)

	// Another Runtime cannot observe or settle this attempt.
	if _, err := f.client2.TaskStatus(ctx, lease.Task.ID); err == nil {
		t.Fatal("foreign runtime read task status")
	} else if !errors.Is(err, runnertransport.ErrForbidden) {
		t.Fatalf("foreign runtime status = %v, want forbidden", err)
	}
	report := f.completedReport(lease)
	if _, err := f.client2.ReportResult(ctx, report); err == nil {
		t.Fatal("foreign runtime reported result")
	} else if !errors.Is(err, runnertransport.ErrForbidden) {
		t.Fatalf("foreign runtime result = %v, want forbidden", err)
	}

	// Unknown tasks are not found.
	if _, err := f.client.TaskStatus(ctx, "missing"); err == nil || !errors.Is(err, runnertransport.ErrUnavailable) {
		t.Fatalf("unknown task status = %v", err)
	}

	// Happy path: accepted once, duplicate on the identical report, conflict on
	// a different digest, stale after expiry.
	ack, err := f.client.ReportResult(ctx, report)
	if err != nil || !ack.Accepted || ack.Duplicate {
		t.Fatalf("first result = %+v, %v", ack, err)
	}
	ack, err = f.client.ReportResult(ctx, report)
	if err != nil || !ack.Accepted || !ack.Duplicate {
		t.Fatalf("duplicate result = %+v, %v", ack, err)
	}
	conflict := f.completedReport(lease)
	conflict.ResultDigest = transportDigestAlt
	if _, err := f.client.ReportResult(ctx, conflict); err == nil || !errors.Is(err, runnertransport.ErrConflict) {
		t.Fatalf("conflicting digest = %v", err)
	}
	status, err := f.client.TaskStatus(ctx, lease.Task.ID)
	if err != nil || status.State != string(execution.TaskCompleted) || !status.ResultAccepted ||
		status.LeaseID != lease.LeaseID || status.FencingToken != lease.FencingToken {
		t.Fatalf("final task status = %+v, %v", status, err)
	}

	// Progress after settlement is stale.
	stale := execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.ProgressEventKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Sequence: 2, Phase: "late", Message: "late", ObservedAt: f.now,
	}
	if err := f.client.ReportProgress(ctx, stale); err == nil || !errors.Is(err, runnertransport.ErrStale) {
		t.Fatalf("late progress = %v", err)
	}
}

func TestRuntimeTransportProgressPathMatch(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	if _, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1")); err != nil {
		t.Fatal(err)
	}
	f.seedRun("a")
	lease := f.lease(f.client)
	// Force a disagreement between the path task id and the body task id; the
	// transport must reject it before any domain call.
	body := `{"schemaVersion":"kowa.workflow-execution.v1","kind":"progressEvent","taskId":"task-other","leaseId":"` +
		lease.LeaseID + `","fencingToken":` + strconv.FormatInt(lease.FencingToken, 10) +
		`,"sequence":1,"phase":"working","message":"synthetic","observedAt":"2026-10-14T00:00:00Z"}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.server.URL+"/api/v1/runtime/tasks/"+lease.Task.ID+"/progress", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer token-r1")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("path/body mismatch = %d, want 400", response.StatusCode)
	}
}

func TestRuntimeTransportLongPollDeliversAfterWait(t *testing.T) {
	f := newTransportFixture(t)
	ctx := t.Context()
	if _, err := f.client.Register(ctx, f.registration("runtime-1", "epoch-1")); err != nil {
		t.Fatal(err)
	}
	// A task arrives while the poll is blocked; the long-poll must deliver it.
	delivered := make(chan execution.TaskLease, 1)
	failures := make(chan error, 1)
	go func() {
		lease, found, err := f.client.Lease(ctx, 2, 1800)
		if err != nil {
			failures <- err
			return
		}
		if !found {
			failures <- io.EOF
			return
		}
		delivered <- lease
	}()
	time.Sleep(50 * time.Millisecond)
	f.seedRun("late")
	select {
	case lease := <-delivered:
		if lease.Task.ID != "task-late" {
			t.Fatalf("long-poll lease = %+v", lease)
		}
	case err := <-failures:
		t.Fatalf("long-poll failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("long-poll did not deliver the queued task")
	}
}
