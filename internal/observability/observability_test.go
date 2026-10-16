// secret-scan: allow-file — this file must contain synthetic credentials to prove the scrubber refuses them.

package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/observability"
	"github.com/SnWalker/kowa/internal/platform/logging"
)

func TestScrubberMasksTheFrozenCredentialVocabulary(t *testing.T) {
	secrets := []string{
		"ghp_0123456789abcdefghijklmnopqrstuvwx",
		"github_pat_11ABCDEFG0abcdefghijklmnopqrstuvwxyz0123456789",
		"sk-lf-0123456789abcdef",
		"sk-ant-api03-0123456789abcdefghijkl",
		"xoxb-EXAMPLE-not-a-real-token-0000",
		"AKIAIOSFODNN7EXAMPLE",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
	}
	for _, secret := range secrets {
		masked := observability.Redact("failed with " + secret + " while probing")
		if strings.Contains(masked, secret) {
			t.Fatalf("credential survived: %q", masked)
		}
		if !strings.Contains(masked, "***") {
			t.Fatalf("nothing was masked in %q", masked)
		}
	}
}

func TestScrubberMasksCredentialShapedAssignments(t *testing.T) {
	cases := map[string]string{
		"token=abcdefgh":                   "token=***",
		"Authorization: Bearer abcdefghij": "Authorization:***",
		"password: hunter2000":             "password:***",
		"client_secret=0123456789abcdef":   "client_secret=***",
		"?access_token=0123456789abcdef":   "?access_token=***",
		"https://u:p@h/x":                  "https://***@h/x",
	}
	for input, want := range cases {
		if got := observability.Redact("prefix " + input + " suffix"); got != "prefix "+want+" suffix" {
			t.Fatalf("Redact(%q) = %q, want %q", input, got, "prefix "+want+" suffix")
		}
	}
}

func TestScrubberKeepsFrozenEvidenceReadable(t *testing.T) {
	readable := []string{
		"sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b",
		"6d88be15052683e0807e7a363f33000565608066",
		"refs/heads/task/kowa-run_0123456789abcdef",
		"run_0123456789abcdef",
		"request key req-intake-0001",
	}
	for _, evidence := range readable {
		if got := observability.Redact(evidence); got != evidence {
			t.Fatalf("Redact(%q) = %q, want it unchanged", evidence, got)
		}
	}
}

func TestSensitiveFieldNameIsExact(t *testing.T) {
	sensitive := []string{"authorization", "Authorization", "accessToken", "runtime_token", "client_secret", "X-Api-Key", "private.key", "langfuseSecret"}
	for _, name := range sensitive {
		if !observability.SensitiveFieldName(name) {
			t.Fatalf("field %q must be sensitive", name)
		}
	}
	ordinary := []string{"requestKey", "request_key", "definitionDigest", "commitOid", "taskId", "kind", "monkey", "keyword"}
	for _, name := range ordinary {
		if observability.SensitiveFieldName(name) {
			t.Fatalf("field %q must not be masked", name)
		}
	}
}

func TestRedactFieldMasksByFieldNameAndByValue(t *testing.T) {
	if got := observability.RedactField("authorization", "Bearer abcdefghij"); got != "***" {
		t.Fatalf("a sensitive field name must mask the whole value, got %q", got)
	}
	if got := observability.RedactField("detail", "ghp_0123456789abcdefghijklmnopqrstuvwx"); got != "***" {
		t.Fatalf("a credential-shaped value must be masked, got %q", got)
	}
	if got := observability.RedactField("detail", "sha256:abc"); got != "sha256:abc" {
		t.Fatalf("ordinary evidence must survive, got %q", got)
	}
}

func TestTraceContextsAreDerivedAndStable(t *testing.T) {
	first := observability.TraceForAttempt("run_1", "node_2", "task_3")
	second := observability.TraceForAttempt("run_1", "node_2", "task_3")
	if first != second {
		t.Fatalf("the same attempt must derive the same trace: %+v vs %+v", first, second)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("derived trace is invalid: %v", err)
	}
	if len(first.TraceID) != 32 || len(first.SpanID) != 16 || len(first.ParentSpanID) != 16 {
		t.Fatalf("unexpected id widths: %+v", first)
	}
	if !strings.HasPrefix(first.String(), "00-"+first.TraceID+"-"+first.SpanID+"-") {
		t.Fatalf("traceparent is malformed: %s", first.String())
	}

	// One run is one trace; a node round and its attempt are distinct spans in
	// it, so a reader can group by trace and still see the attempt.
	run := observability.TraceForRun("run_1")
	node := observability.TraceForNode("run_1", "node_2")
	if run.TraceID != node.TraceID || node.TraceID != first.TraceID {
		t.Fatalf("a run and its attempts must share one trace")
	}
	if run.SpanID == node.SpanID || node.SpanID == first.SpanID {
		t.Fatalf("each level must have its own span")
	}
	if node.ParentSpanID != run.SpanID || first.ParentSpanID != node.SpanID {
		t.Fatalf("the span lineage must follow run -> node -> attempt")
	}
	if other := observability.TraceForRun("run_2"); other.TraceID == run.TraceID {
		t.Fatalf("different runs must not share a trace")
	}
	// A retry is a new attempt under the same node round.
	retry := observability.TraceForAttempt("run_1", "node_2", "task_4")
	if retry.TraceID != node.TraceID || retry.SpanID == first.SpanID || retry.ParentSpanID != node.SpanID {
		t.Fatalf("a retry must be a new span under the same node round: %+v", retry)
	}
}

func TestEventRefusesCredentialBearingValues(t *testing.T) {
	event, err := observability.NewEvent(observability.KindExecutionResult, time.Now().UTC(),
		observability.TraceForAttempt("run_1", "node_2", "task_3"),
		observability.Correlation{WorkflowRunID: "run_1", NodeRunID: "node_2", TaskID: "task_3"},
		"attempt finished")
	if err != nil {
		t.Fatalf("constructing the event failed: %v", err)
	}
	event.Set("capability", "code.generate@1")
	event.Set("accessToken", "gho_16C7e42F292c6912E7710c838347Ae178B4a")
	event.Set("detail", "pushed with ghp_0123456789abcdefghijklmnopqrstuvwx")
	if err := event.Validate(); err != nil {
		t.Fatalf("the event must be writable after refusing the credentials: %v", err)
	}
	attributes := event.Attributes()
	if attributes["capability"] != "code.generate@1" {
		t.Fatalf("ordinary attributes must survive: %#v", attributes)
	}
	if attributes["accessToken"] != "***" {
		t.Fatalf("a sensitive field name must be refused: %#v", attributes)
	}
	if strings.Contains(attributes["detail"], "ghp_") {
		t.Fatalf("a credential-shaped value must be refused: %#v", attributes)
	}
	refused := strings.Join(event.RefusedAttributes(), ",")
	if !strings.Contains(refused, "accessToken") || !strings.Contains(refused, "detail") {
		t.Fatalf("refused attributes must be listed, got %q", refused)
	}
}

func TestEventRejectsAnUnknownKindOrAMissingTrace(t *testing.T) {
	if _, err := observability.NewEvent("invented", time.Now().UTC(),
		observability.TraceForRun("run_1"), observability.Correlation{}, "x"); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
	if _, err := observability.NewEvent(observability.KindProgress, time.Now().UTC(),
		observability.TraceContext{}, observability.Correlation{}, "x"); err == nil {
		t.Fatal("a missing trace context must be refused")
	}
}

func TestSlogSinkCarriesTheTraceIdsAndScrubsOnceMore(t *testing.T) {
	var buffer bytes.Buffer
	logger := logging.NewWithWriter(&buffer)
	event, err := observability.NewEvent(observability.KindBusinessVerdict, time.Now().UTC(),
		observability.TraceForAttempt("run_1", "node_2", "task_3"),
		observability.Correlation{WorkspaceID: "ws_1", WorkflowRunID: "run_1", NodeRunID: "node_2", TaskID: "task_3"},
		"plan_review decided approved")
	if err != nil {
		t.Fatalf("constructing the event failed: %v", err)
	}
	event.Set("verdict", "approved")
	if err := (observability.SlogSink{Logger: logger}).Emit(context.Background(), event); err != nil {
		t.Fatalf("emitting the event failed: %v", err)
	}
	line := buffer.String()
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &record); err != nil {
		t.Fatalf("the emitted record is not one JSON object: %q (%v)", line, err)
	}
	for key, want := range map[string]string{
		"eventSchema":   observability.EventSchemaVersion,
		"kind":          "business_verdict",
		"traceId":       event.Trace.TraceID,
		"spanId":        event.Trace.SpanID,
		"workflowRunId": "run_1",
		"nodeRunId":     "node_2",
		"taskId":        "task_3",
		"verdict":       "approved",
	} {
		if got, _ := record[key].(string); got != want {
			t.Fatalf("record[%q] = %v, want %q (%s)", key, record[key], want, line)
		}
	}
}

func TestWatermarkSeparatesContactabilityFromProgress(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	current := now
	registry := observability.NewRegistry(90*time.Second, func() time.Time { return current })

	// The Runner answers heartbeats while the delivery makes no progress.
	registry.Success(observability.WatermarkContactability, "runtime_1", "heartbeat", now.Add(-5*time.Second))
	registry.Success(observability.WatermarkBusinessProgress, "run_1", "node coder dispatched", now.Add(-30*time.Minute))

	contactability, found := registry.Get(observability.WatermarkContactability, "runtime_1")
	if !found || contactability.Freshness != observability.FreshnessFresh || !contactability.Contactable {
		t.Fatalf("a fresh heartbeat must read as contactable: %+v", contactability)
	}
	progress, found := registry.Get(observability.WatermarkBusinessProgress, "run_1")
	if !found {
		t.Fatal("the progress watermark must exist")
	}
	if progress.Progressing || progress.Freshness != observability.FreshnessStale {
		t.Fatalf("a heartbeat must never make a stalled run read as progressing: %+v", progress)
	}
	if progress.StaleReason != observability.StaleExpired {
		t.Fatalf("the stale reason must say the window expired: %+v", progress)
	}
}

func TestWatermarkFailureSupersedesAnObservedFact(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	watermark := observability.Watermark{Kind: observability.WatermarkExternalObservation, Subject: "repo_1"}
	watermark.RecordSuccess(now.Add(-time.Second), "pull request 12 observed")
	if freshness := watermark.Freshness(now, time.Hour); freshness != observability.FreshnessFresh {
		t.Fatalf("a recent successful observation must be fresh, got %s", freshness)
	}
	// A failed read advances the attempt only: the old fact must not be
	// presented as if the read had just succeeded.
	watermark.RecordFailure(now, "gh api timed out")
	if freshness, reason := watermark.FreshnessOf(now, time.Hour); freshness != observability.FreshnessStale || reason != observability.StaleSuperseded {
		t.Fatalf("a failure after a success must degrade the watermark, got %s/%s", freshness, reason)
	}
	if watermark.LastSuccessAt == nil || !watermark.LastSuccessAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("the success time must not be rewritten by a failure: %+v", watermark)
	}
	if watermark.LastAttemptAt == nil || !watermark.LastAttemptAt.Equal(now) {
		t.Fatalf("the attempt time must advance: %+v", watermark)
	}
}

func TestWatermarkUnknownIsNotStale(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var never observability.Watermark
	if freshness, reason := never.FreshnessOf(now, time.Minute); freshness != observability.FreshnessUnknown || reason != observability.StaleNeverEstablished {
		t.Fatalf("a watermark that never succeeded is unknown, got %s/%s", freshness, reason)
	}
	failed := observability.Watermark{Kind: observability.WatermarkExternalObservation, Subject: "repo_1"}
	failed.RecordFailure(now, "gh unavailable")
	if freshness := failed.Freshness(now, time.Minute); freshness != observability.FreshnessUnknown {
		t.Fatalf("a subject that never succeeded stays unknown even after a failure, got %s", freshness)
	}
}

func TestRegistryObserveRecordsSuccessAndFailure(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	registry := observability.NewRegistry(time.Minute, func() time.Time { return now })
	registry.Observe(observability.WatermarkExternalObservation, "repo_1", "observed", nil)
	registry.Observe(observability.WatermarkExternalObservation, "repo_1", "observed", errStub{})
	snapshot, found := registry.Get(observability.WatermarkExternalObservation, "repo_1")
	if !found {
		t.Fatal("the watermark must exist")
	}
	if snapshot.Successes != 1 || snapshot.Failures != 1 {
		t.Fatalf("attempts must be counted apart: %+v", snapshot)
	}
	if snapshot.Freshness != observability.FreshnessStale {
		t.Fatalf("the failure must degrade the snapshot: %+v", snapshot)
	}
	if len(registry.SnapshotsOf(observability.WatermarkExternalObservation)) != 1 {
		t.Fatalf("SnapshotsOf must filter by kind: %+v", registry.Snapshots())
	}
}

type errStub struct{}

func (errStub) Error() string { return "gh api returned 503" }
