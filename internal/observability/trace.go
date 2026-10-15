package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrInvalidTrace marks a correlation context that is not a usable W3C trace
// context.
var ErrInvalidTrace = errors.New("observability: invalid trace context")

// TraceContext is the correlation context of one record. Its ids are *derived*
// from identities Kowa already froze — the run, the node round and the Task —
// so the Server and the Runner produce the same trace for one attempt without
// adding a single field to the frozen task wire or to any published schema.
//
// The shape follows W3C Trace Context: a 16-byte trace id and an 8-byte span id
// as lowercase hex, never all zero, so the same value can be handed to an OTLP
// backend unchanged.
type TraceContext struct {
	TraceID      string `json:"traceId"`
	SpanID       string `json:"spanId"`
	ParentSpanID string `json:"parentSpanId,omitempty"`
	Sampled      bool   `json:"sampled"`
}

// traceNamespace and spanNamespace keep a derived trace id from ever colliding
// with a span id derived from the same identity.
const (
	traceNamespace = "kowa.trace"
	spanNamespace  = "kowa.span"
)

// TraceForRun derives the trace of a whole WorkflowRun. Every node round and
// attempt of that run shares this trace id.
func TraceForRun(runID string) TraceContext {
	return TraceContext{
		TraceID: deriveID(traceNamespace, 16, "run", runID),
		SpanID:  deriveID(spanNamespace, 8, "run", runID),
		Sampled: true,
	}
}

// TraceForNode derives the correlation of one materialised node round: the same
// trace as its run, a span of its own, parented on the run span.
func TraceForNode(runID, nodeRunID string) TraceContext {
	run := TraceForRun(runID)
	return TraceContext{
		TraceID:      run.TraceID,
		SpanID:       deriveID(spanNamespace, 8, "node", runID, nodeRunID),
		ParentSpanID: run.SpanID,
		Sampled:      true,
	}
}

// TraceForAttempt derives the correlation of one execution attempt: the same
// trace as its run, a span of its own, parented on the node round span. `taskID`
// identifies the attempt, so a retry is a new span under the same node round.
func TraceForAttempt(runID, nodeRunID, taskID string) TraceContext {
	node := TraceForNode(runID, nodeRunID)
	return TraceContext{
		TraceID:      node.TraceID,
		SpanID:       deriveID(spanNamespace, 8, "attempt", runID, nodeRunID, taskID),
		ParentSpanID: node.SpanID,
		Sampled:      true,
	}
}

// Validate reports whether the context is a usable W3C trace context.
func (t TraceContext) Validate() error {
	if !validHex(t.TraceID, 32) || !validHex(t.SpanID, 16) {
		return ErrInvalidTrace
	}
	if t.ParentSpanID != "" && !validHex(t.ParentSpanID, 16) {
		return ErrInvalidTrace
	}
	return nil
}

// String renders the W3C traceparent header value of this context.
func (t TraceContext) String() string {
	flags := "00"
	if t.Sampled {
		flags = "01"
	}
	return "00-" + t.TraceID + "-" + t.SpanID + "-" + flags
}

// deriveID hashes a namespaced identity into a lowercase hex id of the requested
// byte length. An all-zero result is impossible for SHA-256 in practice, and is
// corrected anyway because W3C forbids it.
func deriveID(namespace string, size int, parts ...string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + strings.Join(parts, "\x00")))
	raw := sum[:size]
	zero := true
	for _, b := range raw {
		if b != 0 {
			zero = false
			break
		}
	}
	if zero {
		raw[size-1] = 1
	}
	return hex.EncodeToString(raw)
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9', character >= 'a' && character <= 'f':
		default:
			return false
		}
	}
	return true
}
