// secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.

package langfuse_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation/backend"
	"github.com/SnWalker/kowa/internal/evaluation/langfuse"
	"github.com/SnWalker/kowa/internal/observability"
)

const (
	publicKey = "pk-lf-1234567890"
	secretKey = "sk-lf-0987654321"
)

type capturedRequest struct {
	path    string
	method  string
	headers http.Header
	body    []byte
}

func newTestClient(t *testing.T, handler http.HandlerFunc) (*langfuse.Client, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		captured.path, captured.method, captured.headers, captured.body =
			request.URL.Path, request.Method, request.Header.Clone(), body
		handler(writer, request)
	}))
	t.Cleanup(server.Close)
	client, err := langfuse.New(langfuse.Config{
		BaseURL: server.URL, PublicKey: publicKey, SecretKey: secretKey,
		Environment: "local", Release: "kowa-test", Timeout: 5 * time.Second,
	}, server.Client())
	if err != nil {
		t.Fatalf("constructing the client failed: %v", err)
	}
	return client, captured
}

func envelope() backend.Envelope {
	return backend.Envelope{
		RecordID: "eval_0001", Kind: backend.KindOfflineRegression, Mode: backend.ModeAsync,
		WorkspaceID: "ws_1", WorkItemID: "wi_1", WorkflowRunID: "run_1", NodeRunID: "node_2", TaskID: "task_3",
		SubjectDigest: "sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b",
		Trace:         observability.TraceForAttempt("run_1", "node_2", "task_3"),
		Attributes:    map[string]string{"verdict": "passed", "attempt": "2"},
		At:            time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC),
	}
}

// TestRequestShapeMatchesTheFrozenLangfuseContract pins the transport: the
// documented OTLP/HTTP path, the ingestion version header, Basic authorization
// built from the project keys, and an OTLP/JSON body whose ids are the ones the
// OTLP mapping requires.
func TestRequestShapeMatchesTheFrozenLangfuseContract(t *testing.T) {
	client, captured := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	if err := client.Emit(context.Background(), envelope()); err != nil {
		t.Fatalf("emitting failed: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/public/otel/v1/traces" {
		t.Fatalf("unexpected endpoint: %s %s", captured.method, captured.path)
	}
	if got := captured.headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := captured.headers.Get("x-langfuse-ingestion-version"); got != langfuse.IngestionVersion {
		t.Fatalf("ingestion version = %q, want %q", got, langfuse.IngestionVersion)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(publicKey+":"+secretKey))
	if got := captured.headers.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %q, want the Basic credential", got)
	}

	var payload struct {
		ResourceSpans []struct {
			Resource struct {
				Attributes []struct {
					Key   string `json:"key"`
					Value struct {
						StringValue string `json:"stringValue"`
					} `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				Scope struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"scope"`
				Spans []struct {
					TraceID           string `json:"traceId"`
					SpanID            string `json:"spanId"`
					Name              string `json:"name"`
					StartTimeUnixNano string `json:"startTimeUnixNano"`
					Attributes        []struct {
						Key   string `json:"key"`
						Value struct {
							StringValue string `json:"stringValue"`
						} `json:"value"`
					} `json:"attributes"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(captured.body, &payload); err != nil {
		t.Fatalf("the body is not an OTLP/JSON payload: %v (%s)", err, captured.body)
	}
	span := payload.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if len(span.TraceID) != 32 || len(span.SpanID) != 16 {
		t.Fatalf("OTLP requires a 16-byte trace id and an 8-byte span id: %+v", span)
	}
	if span.TraceID == strings.Repeat("0", 32) || span.SpanID == strings.Repeat("0", 16) {
		t.Fatalf("an all-zero id is forbidden by OTLP: %+v", span)
	}
	if want := fmt.Sprint(envelope().At.UnixNano()); span.StartTimeUnixNano != want {
		t.Fatalf("the observation time must be the authoritative start time, got %s want %s", span.StartTimeUnixNano, want)
	}
	attributes := map[string]string{}
	for _, attribute := range span.Attributes {
		attributes[attribute.Key] = attribute.Value.StringValue
	}
	for key, want := range map[string]string{
		"langfuse.trace.name":              "kowa.offline_regression",
		"langfuse.session.id":              "run_1",
		"langfuse.environment":             "local",
		"langfuse.release":                 "kowa-test",
		"kowa.run.id":                      "run_1",
		"kowa.node.id":                     "node_2",
		"kowa.task.id":                     "task_3",
		"kowa.record.id":                   "",
		"langfuse.trace.metadata.kowaKind": "offline_regression",
		"langfuse.trace.metadata.kowaMode": "async",
		"kowa.attribute.verdict":           "passed",
	} {
		if want == "" {
			continue
		}
		if attributes[key] != want {
			t.Fatalf("attribute %q = %q, want %q", key, attributes[key], want)
		}
	}
	if attributes["langfuse.trace.metadata.kowaRecordId"] != "eval_0001" {
		t.Fatalf("the record id must travel with the span: %+v", attributes)
	}
	if payload.ResourceSpans[0].ScopeSpans[0].Scope.Name != "kowa.evaluation" ||
		payload.ResourceSpans[0].ScopeSpans[0].Scope.Version != backend.SchemaVersion {
		t.Fatalf("the scope must name this contract: %+v", payload.ResourceSpans[0].ScopeSpans[0].Scope)
	}
}

// TestEmitClassifiesFailures pins the classification the recorder depends on: a
// 5xx or a transport failure is retryable, anything else is permanent.
func TestEmitClassifiesFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"server error is retryable", http.StatusServiceUnavailable, backend.ErrUnavailable},
		{"gateway error is retryable", http.StatusBadGateway, backend.ErrUnavailable},
		{"unauthorized is permanent", http.StatusUnauthorized, backend.ErrRejected},
		{"bad request is permanent", http.StatusBadRequest, backend.ErrRejected},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, _ := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(`{"message":"nope"}`))
			})
			err := client.Emit(context.Background(), envelope())
			if !errors.Is(err, testCase.want) {
				t.Fatalf("want %v, got %v", testCase.want, err)
			}
		})
	}
}

func TestTransportFailureIsRetryableAndScrubbed(t *testing.T) {
	client, err := langfuse.New(langfuse.Config{
		BaseURL: "http://127.0.0.1:1", PublicKey: publicKey, SecretKey: secretKey, Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("constructing the client failed: %v", err)
	}
	err = client.Emit(context.Background(), envelope())
	if !errors.Is(err, backend.ErrUnavailable) {
		t.Fatalf("an unreachable instance must be unavailable, got %v", err)
	}
	if strings.Contains(err.Error(), secretKey) {
		t.Fatalf("the secret key reached the error: %v", err)
	}
}

// TestEmitNeverSendsACredential is the outbound half of the collection-time
// rule: even if a caller hands the adapter a credential, it does not leave the
// process.
func TestEmitNeverSendsACredential(t *testing.T) {
	client, captured := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	envelope := envelope()
	envelope.Attributes = map[string]string{
		"note":          "push used ghp_0123456789abcdefghijklmnopqrstuvwx",
		"accessToken":   "gho_16C7e42F292c6912E7710c838347Ae178B4a",
		"authorization": "Bearer " + secretKey,
	}
	if err := client.Emit(context.Background(), envelope); err != nil {
		t.Fatalf("emitting failed: %v", err)
	}
	body := string(captured.body)
	for _, secret := range []string{"ghp_0123456789abcdefghijklmnopqrstuvwx", "gho_16C7e42F292c6912E7710c838347Ae178B4a"} {
		if strings.Contains(body, secret) {
			t.Fatalf("a credential left the process: %s", body)
		}
	}
	if strings.Contains(body, `"value":"Bearer `) {
		t.Fatalf("an authorization header value left the process: %s", body)
	}
}

// TestTheSameRecordDerivesTheSameSpan pins that a retry re-delivers one object
// instead of creating a second one.
func TestTheSameRecordDerivesTheSameSpan(t *testing.T) {
	ids := make([]string, 0, 2)
	client, captured := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})
	for attempt := 0; attempt < 2; attempt++ {
		if err := client.Emit(context.Background(), envelope()); err != nil {
			t.Fatalf("emitting failed: %v", err)
		}
		var payload struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []struct {
						TraceID string `json:"traceId"`
						SpanID  string `json:"spanId"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.Unmarshal(captured.body, &payload); err != nil {
			t.Fatalf("the body is not an OTLP/JSON payload: %v", err)
		}
		span := payload.ResourceSpans[0].ScopeSpans[0].Spans[0]
		ids = append(ids, span.TraceID+"/"+span.SpanID)
	}
	if ids[0] != ids[1] {
		t.Fatalf("a retry must reuse the same span, got %v", ids)
	}
	other := envelope()
	other.RecordID = "eval_0002"
	if err := client.Emit(context.Background(), other); err != nil {
		t.Fatalf("emitting failed: %v", err)
	}
	var payload struct {
		ResourceSpans []struct {
			ScopeSpans []struct {
				Spans []struct {
					TraceID string `json:"traceId"`
					SpanID  string `json:"spanId"`
				} `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if err := json.Unmarshal(captured.body, &payload); err != nil {
		t.Fatalf("the body is not an OTLP/JSON payload: %v", err)
	}
	span := payload.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if span.TraceID+"/"+span.SpanID == ids[0] {
		t.Fatalf("a different record must be a different object")
	}
}

func TestHealthReportsRealAvailability(t *testing.T) {
	available, captured := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("x-langfuse-version", "3.0.0")
		writer.WriteHeader(http.StatusOK)
	})
	health := available.Health(context.Background())
	if !health.Available || health.Backend != "langfuse" || health.Version != "3.0.0" || health.CheckedAt.IsZero() {
		t.Fatalf("an answering instance must be available: %+v", health)
	}
	if captured.method != http.MethodGet || captured.path != "/api/public/health" {
		t.Fatalf("health must use the frozen path: %s %s", captured.method, captured.path)
	}

	unhealthy, _ := newTestClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	if health := unhealthy.Health(context.Background()); health.Available {
		t.Fatalf("a 503 must not read as available: %+v", health)
	}

	unreachable, err := langfuse.New(langfuse.Config{
		BaseURL: "http://127.0.0.1:1", PublicKey: publicKey, SecretKey: secretKey, Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("constructing the client failed: %v", err)
	}
	health = unreachable.Health(context.Background())
	if health.Available || health.Reason == "" {
		t.Fatalf("an unreachable instance must be unavailable with a reason: %+v", health)
	}
	if strings.Contains(health.Reason, secretKey) {
		t.Fatalf("the reason must not carry the secret: %q", health.Reason)
	}
}

func TestConfigValidationRefusesUnusableSettings(t *testing.T) {
	cases := map[string]langfuse.Config{
		"missing base url":  {PublicKey: publicKey, SecretKey: secretKey},
		"relative base url": {BaseURL: "/otel", PublicKey: publicKey, SecretKey: secretKey},
		"missing keys":      {BaseURL: "http://127.0.0.1:3000"},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := langfuse.New(config, nil); !errors.Is(err, backend.ErrValidation) {
				t.Fatalf("want a validation refusal, got %v", err)
			}
		})
	}
}

// TestTheAdapterHasNoReadPath is the structural half of "the backend is not a
// state source": the client exposes exactly the port, so there is no method that
// could hand evaluation data back to Kowa.
func TestTheAdapterHasNoReadPath(t *testing.T) {
	var port backend.Backend = &langfuse.Client{}
	methods := map[string]bool{}
	for index := 0; index < reflect.TypeOf(port).NumMethod(); index++ {
		methods[reflect.TypeOf(port).Method(index).Name] = true
	}
	for name := range methods {
		if !backendPortMethods[name] {
			t.Fatalf("the adapter exposes %q, which is not part of the port", name)
		}
	}
	if !methods["Name"] || !methods["Emit"] || !methods["Health"] {
		t.Fatalf("the adapter must implement the whole port, got %v", methods)
	}
}

var backendPortMethods = map[string]bool{"Name": true, "Emit": true, "Health": true}
