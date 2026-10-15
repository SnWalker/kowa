// secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.

// Package security holds backend:S07's negative security tests: the rules that
// keep a credential inside the process and keep one workspace's facts out of
// another's projections. The process-level fake-credential boundary probe lives
// in scripts/test-recovery-drill.sh, which can observe a real task directory.
package security

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation/backend"
	"github.com/SnWalker/kowa/internal/evaluation/langfuse"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/observability"
	"github.com/SnWalker/kowa/internal/platform/logging"
	"github.com/SnWalker/kowa/internal/workspace"
)

// TestTheTwoScrubbersAgree is the characterization check of the S07 move: the
// git remote projection delegates to the shared filter, and for every input the
// S05 rule handled the result is unchanged. If the two ever disagree, a
// consumer of one could be shown what the other refuses to show.
func TestTheTwoScrubbersAgree(t *testing.T) {
	corpus := []string{
		"fatal: could not read from https://user:ghp_secret@github.com/org/repo.git",
		"https://tok@github.com/x",
		"see http://u:p@h/a then https://u2:p2@h2/b",
		"https://github.com/org/repo",
		"no url here at all",
		"https://host/path@x",
		"https://https://a:b@c@d",
		"clone https://me:secret@example.com/x.git done",
		"sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b",
		"refs/heads/task/kowa-run_0123456789abcdef",
	}
	for _, input := range corpus {
		if got, want := observability.Redact(input), gitremote.Redact(input); got != want {
			t.Fatalf("the shared filter and the projection disagree on %q: %q vs %q", input, got, want)
		}
	}
}

// TestASecretNeverReachesAnyObservationSurface drives one fake credential
// through every S07 path: the process log, a structured event, the evaluation
// record spool and the outbound OTLP payload. None of them may carry it.
func TestASecretNeverReachesAnyObservationSurface(t *testing.T) {
	const fakeToken = "ghp_0123456789abcdefghijklmnopqrstuvwx"
	const fakeKey = "sk-lf-0987654321abcdef"

	var logBuffer strings.Builder
	logger := logging.NewWithWriter(&logBuffer)

	// 1. A provider/transport message carrying the credential.
	logger.Error("controlled delivery failed",
		"error", errors.New("git push failed for https://x-access-token:"+fakeToken+"@github.com/o/r.git"),
		"authorization", "Bearer "+fakeKey,
		"detail", "token="+fakeToken)

	// 2. A structured event whose attribute carries it.
	event, err := observability.NewEvent(observability.KindExecutionResult, time.Now().UTC(),
		observability.TraceForAttempt("run_1", "node_2", "task_3"),
		observability.Correlation{WorkspaceID: "ws_1", WorkflowRunID: "run_1", NodeRunID: "node_2", TaskID: "task_3"},
		"attempt reported with "+fakeToken)
	if err != nil {
		t.Fatalf("constructing the event failed: %v", err)
	}
	event.Set("outputDigest", "sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b")
	event.Set("note", "pushed using "+fakeToken)
	if err := (observability.SlogSink{Logger: logger}).Emit(context.Background(), event); err != nil {
		t.Fatalf("emitting the event failed: %v", err)
	}

	// 3. The evaluation backend and its spool.
	var received string
	collector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body := make([]byte, request.ContentLength)
		_, _ = request.Body.Read(body)
		received = string(body)
		writer.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	client, err := langfuse.New(langfuse.Config{
		BaseURL: collector.URL, PublicKey: "pk-lf-1111111111", SecretKey: fakeKey, Timeout: 5 * time.Second,
	}, collector.Client())
	if err != nil {
		t.Fatalf("constructing the adapter failed: %v", err)
	}
	spool := backend.NewMemorySpool()
	recorder := backend.NewRecorder(client, spool, stubIDs{}, time.Now)
	record, err := recorder.SubmitAsync(context.Background(), backend.SubmitCommand{
		Kind: backend.KindOfflineRegression, Mode: backend.ModeAsync,
		WorkspaceID: "ws_1", WorkflowRunID: "run_1", Trace: observability.TraceForAttempt("run_1", "node_2", "task_3"),
		Attributes: map[string]string{"note": "pushed using " + fakeToken, "accessToken": fakeKey},
	})
	if err != nil {
		t.Fatalf("submitting the evaluation record failed: %v", err)
	}
	if record.Status != backend.StatusAccepted {
		t.Fatalf("the fake collector accepted nothing: %+v", record)
	}
	encoded, err := jsonOf(record)
	if err != nil {
		t.Fatalf("encoding the record failed: %v", err)
	}

	for name, surface := range map[string]string{
		"the process log":        logBuffer.String(),
		"the outbound OTLP body": received,
		"the spooled record":     encoded,
	} {
		for _, secret := range []string{fakeToken, fakeKey} {
			if strings.Contains(surface, secret) {
				t.Fatalf("a credential reached %s: %s", name, surface)
			}
		}
	}
	// The surfaces must still be useful: an operator has to be able to tell
	// that something was withheld.
	if !strings.Contains(logBuffer.String(), observability.Mask) {
		t.Fatalf("the log lost every trace of the refusal: %s", logBuffer.String())
	}
}

type stubIDs struct{}

func (stubIDs) New() (string, error) { return "eval_0123456789abcdef", nil }

func jsonOf(value any) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

// TestTheWatermarkSurfaceRefusesAForeignWorkspace pins that the new surface does
// not re-implement authorization: an unauthenticated caller is refused before the
// projection runs, and a caller who is not a member of the named workspace is
// refused by the S01 authority.
func TestTheWatermarkSurfaceRefusesAForeignWorkspace(t *testing.T) {
	cases := []struct {
		name       string
		cookie     string
		wantStatus int
	}{
		{name: "no session at all", cookie: "", wantStatus: http.StatusUnauthorized},
		{name: "a session that is not a member", cookie: "session-of-an-outsider", wantStatus: http.StatusForbidden},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			projector := &recordingProjector{}
			handler := httpapi.NewObservabilityHandler(
				authStub{}, workspaceStub{}, projector, "", nil)
			request := httptest.NewRequest(http.MethodGet,
				"/api/v1/workspaces/ws_other/work-items/wi_1/observability/watermarks", nil)
			if testCase.cookie != "" {
				request.AddCookie(&http.Cookie{Name: "__Host-kowa-session", Value: testCase.cookie})
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", recorder.Code, testCase.wantStatus, recorder.Body)
			}
			if projector.watermarkCalls != 0 {
				t.Fatalf("the projection must not run for a refused caller, it ran %d times", projector.watermarkCalls)
			}
			if body := recorder.Body.String(); strings.Contains(body, "ws_other") {
				t.Fatalf("the refusal echoes the requested identity: %s", body)
			}
		})
	}
}

// recordingProjector counts calls so a test can prove the projection never ran.
type recordingProjector struct {
	watermarkCalls int
	healthCalls    int
}

func (p *recordingProjector) Health(context.Context) (observability.HealthView, error) {
	p.healthCalls++
	return observability.HealthView{}, nil
}

func (p *recordingProjector) Watermarks(context.Context, string, string) (observability.WatermarkView, error) {
	p.watermarkCalls++
	return observability.WatermarkView{}, nil
}

// authStub authenticates exactly one cookie and refuses everything else.
type authStub struct{}

func (authStub) BeginLogin(context.Context, string) (identity.BeginLoginResult, error) {
	return identity.BeginLoginResult{}, identity.ErrUnauthorized
}

func (authStub) CompleteLogin(context.Context, identity.CompleteLoginCommand) (identity.CompleteLoginResult, error) {
	return identity.CompleteLoginResult{}, identity.ErrUnauthorized
}

func (authStub) Authenticate(_ context.Context, sessionID, _ string, _ bool) (identity.WebIdentity, error) {
	if sessionID != "session-of-an-outsider" {
		return identity.WebIdentity{}, identity.ErrUnauthorized
	}
	return identity.WebIdentity{GitHubUserID: "4242"}, nil
}

func (authStub) Logout(context.Context, string) error { return nil }

func (authStub) RecoverSession(context.Context, string) (identity.SessionView, error) {
	return identity.SessionView{}, identity.ErrUnauthorized
}

// workspaceStub refuses every workspace, which is what a non-member sees.
type workspaceStub struct{}

func (workspaceStub) Get(context.Context, workspace.Actor, string) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrForbidden
}
