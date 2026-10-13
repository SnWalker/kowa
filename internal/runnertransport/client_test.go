package runnertransport_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/runnertransport"
)

func clientFor(t *testing.T, handler http.HandlerFunc) *runnertransport.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := runnertransport.NewClient(server.URL, "runtime-1", "token-r1")
	client.Epoch = "epoch-1"
	return client
}

func apiErrorBody(category string) string {
	return fmt.Sprintf(
		`{"schemaVersion":"kowa.workflow-execution.v1","kind":"apiError","error":%q,"message":"synthetic"}`,
		category,
	)
}

func TestClientClassifiesErrorResponses(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		status   int
		body     string
		expected error
	}{
		{"unauthorized", http.StatusUnauthorized, apiErrorBody("UNAUTHORIZED"), runnertransport.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, apiErrorBody("FORBIDDEN"), runnertransport.ErrForbidden},
		{"stale", http.StatusConflict, apiErrorBody("STALE_RESULT"), runnertransport.ErrStale},
		{"conflict", http.StatusConflict, apiErrorBody("VERSION_CONFLICT"), runnertransport.ErrConflict},
		{"superseded", http.StatusConflict, apiErrorBody("RESOURCE_UNAVAILABLE"), runnertransport.ErrSuperseded},
		{"missing", http.StatusNotFound, apiErrorBody("RESOURCE_UNAVAILABLE"), runnertransport.ErrUnavailable},
		{"server", http.StatusInternalServerError, apiErrorBody("INTERNAL_ERROR"), runnertransport.ErrServer},
		{"validation", http.StatusBadRequest, apiErrorBody("VALIDATION_ERROR"), runnertransport.ErrProtocol},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := clientFor(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			})
			if _, err := client.TaskStatus(ctx, "task-1"); !errors.Is(err, testCase.expected) {
				t.Fatalf("status %d = %v, want %v", testCase.status, err, testCase.expected)
			}
		})
	}
}

func TestClientRejectsProtocolViolations(t *testing.T) {
	ctx := context.Background()
	bodies := map[string]string{
		"unknown field":      `{"taskId":"task-1","state":"LEASED","unexpected":true}`,
		"trailing data":      `{"taskId":"task-1","state":"LEASED"}{"taskId":"task-2"}`,
		"missing fields":     `{"state":"LEASED"}`,
		"not an apierror":    `{"status":"maybe"}`,
		"error without kind": `{"schemaVersion":"kowa.workflow-execution.v1","error":"UNAUTHORIZED","message":"x"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			status := http.StatusOK
			if name == "not an apierror" || name == "error without kind" {
				status = http.StatusConflict
			}
			client := clientFor(t, func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(body))
			})
			if _, err := client.TaskStatus(ctx, "task-1"); !errors.Is(err, runnertransport.ErrProtocol) {
				t.Fatalf("body %q = %v, want protocol violation", body, err)
			}
		})
	}
}

func TestClientValidatesLeaseShape(t *testing.T) {
	ctx := context.Background()
	// A lease whose runtime epoch does not match the request is a protocol
	// violation even when it decodes cleanly.
	client := clientFor(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"schemaVersion":"kowa.workflow-execution.v1","kind":"taskLease","leaseId":"lease-1","fencingToken":1,"runtimeEpoch":"epoch-other","expiresAt":"2026-10-14T01:00:00Z","task":{"taskId":"task-1","workflowRunId":"run-1","nodeRunId":"node-1","attempt":1,"attemptKind":"initial","capability":{"id":"plan.create","version":1},"providerSelection":{"id":"codex","version":"1.0"},"inputDigest":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","gitExecutionUserId":"9001","gitScopes":[],"deadlineAt":"2026-10-14T01:00:00Z"}}`))
	})
	if _, _, err := client.Lease(ctx, 0, 1800); !errors.Is(err, runnertransport.ErrProtocol) {
		t.Fatalf("foreign-epoch lease = %v, want protocol violation", err)
	}
}

func TestClientEmptyPollAndNetworkFailure(t *testing.T) {
	ctx := context.Background()
	client := clientFor(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	if _, found, err := client.Lease(ctx, 0, 1800); err != nil || found {
		t.Fatalf("empty poll = found=%v, err=%v", found, err)
	}

	// A refused connection is a transport failure, not a classified one.
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	down := runnertransport.NewClient(server.URL, "runtime-1", "token-r1")
	down.Epoch = "epoch-1"
	err := down.ReportProgress(ctx, execution.ProgressReport{
		TaskID: "task-1", LeaseID: "lease-1", FencingToken: 1, Sequence: 1,
		Phase: "working", Message: "synthetic",
	})
	if err == nil || errors.Is(err, runnertransport.ErrProtocol) {
		t.Fatalf("network failure = %v", err)
	}
}
