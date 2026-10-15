package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// EventSchemaVersion is the frozen envelope identity of a structured event.
const EventSchemaVersion = "kowa.event.v1"

var (
	// ErrInvalidEvent marks an event that cannot be written.
	ErrInvalidEvent = errors.New("observability: invalid event")
)

// EventKind is the frozen event vocabulary. The kinds keep the categories the
// quality design separates: an environment heartbeat, an execution outcome, a
// business verdict, a human answer, an external GitHub fact and closing evidence
// are never merged into one "status" field.
type EventKind string

const (
	// KindProgress is a liveness/progress report. It says work is moving; it is
	// not a verdict and it never satisfies a gate.
	KindProgress EventKind = "progress"
	// KindExecutionResult is an execution outcome of one attempt.
	KindExecutionResult EventKind = "execution_result"
	// KindBusinessVerdict is a decided node verdict.
	KindBusinessVerdict EventKind = "business_verdict"
	// KindHumanAnswer is an accepted human answer.
	KindHumanAnswer EventKind = "human_answer"
	// KindExternalGitHubFact is a reconciled GitHub fact.
	KindExternalGitHubFact EventKind = "external_github_fact"
	// KindClosureEvidence is evidence bound by a closure.
	KindClosureEvidence EventKind = "closure_evidence"
)

// KnownEventKind reports whether the kind is part of the frozen vocabulary.
func KnownEventKind(kind EventKind) bool {
	switch kind {
	case KindProgress, KindExecutionResult, KindBusinessVerdict,
		KindHumanAnswer, KindExternalGitHubFact, KindClosureEvidence:
		return true
	default:
		return false
	}
}

// Correlation carries the dimensions every event must be traceable along. Every
// field is an identity Kowa already froze; an event never invents one.
type Correlation struct {
	WorkspaceID       string   `json:"workspaceId,omitempty"`
	WorkItemID        string   `json:"workItemId,omitempty"`
	WorkflowRunID     string   `json:"workflowRunId,omitempty"`
	NodeRunID         string   `json:"nodeRunId,omitempty"`
	TaskID            string   `json:"taskId,omitempty"`
	Attempt           int      `json:"attempt,omitempty"`
	AttemptKind       string   `json:"attemptKind,omitempty"`
	Capability        string   `json:"capability,omitempty"`
	RuntimeID         string   `json:"runtimeId,omitempty"`
	RuntimeEpoch      string   `json:"runtimeEpoch,omitempty"`
	ProviderID        string   `json:"providerId,omitempty"`
	Model             string   `json:"model,omitempty"`
	DefinitionName    string   `json:"definitionName,omitempty"`
	DefinitionVersion int      `json:"definitionVersion,omitempty"`
	DefinitionDigest  string   `json:"definitionDigest,omitempty"`
	InputDigest       string   `json:"inputDigest,omitempty"`
	OutputDigest      string   `json:"outputDigest,omitempty"`
	RepositoryID      string   `json:"repositoryId,omitempty"`
	CommitOID         string   `json:"commitOid,omitempty"`
	ArtifactIDs       []string `json:"artifactIds"`
	// ObservableAt is the time the emitting side observed the fact. It is not
	// the time the record was written, and the two are never conflated.
	ObservableAt time.Time `json:"observedAt"`
}

// Event is one structured observation of the system. It carries identities,
// digests and short classifications; it never carries a credential and never
// carries a complete user input or prompt body. A value that looks like a
// credential is refused at construction, not hidden later.
type Event struct {
	EventID     string
	Kind        EventKind
	At          time.Time
	Trace       TraceContext
	Correlation Correlation
	Message     string

	attributes map[string]string
	refused    []string
}

// NewEvent constructs a validated event. The message is scrubbed here, and a
// message that is itself a credential is refused outright.
func NewEvent(kind EventKind, at time.Time, trace TraceContext, correlation Correlation, message string) (*Event, error) {
	if !KnownEventKind(kind) {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalidEvent, kind)
	}
	if at.IsZero() {
		return nil, fmt.Errorf("%w: the event time is required", ErrInvalidEvent)
	}
	if err := trace.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	event := &Event{
		EventID: "evt_" + deriveID("kowa.event", 16, string(kind), trace.SpanID, at.UTC().Format(time.RFC3339Nano)),
		Kind:    kind, At: at.UTC(), Trace: trace, Correlation: correlation,
		attributes: map[string]string{},
	}
	if ContainsCredential(message) {
		event.refused = append(event.refused, "message")
		event.Message = Mask
	} else {
		event.Message = Redact(message)
	}
	if event.Correlation.ObservableAt.IsZero() {
		event.Correlation.ObservableAt = at.UTC()
	}
	if event.Correlation.ArtifactIDs == nil {
		event.Correlation.ArtifactIDs = []string{}
	}
	return event, nil
}

// EventKind is a string type so an event can be logged and compared without a
// conversion at every call site.
func (k EventKind) String() string { return string(k) }

// Set records one attribute. A sensitive field name, or a value that looks like
// a credential, is refused: the attribute is recorded as masked and its name is
// listed, so a reader can tell that something was withheld instead of seeing a
// silently altered value.
func (e *Event) Set(name, value string) {
	if name == "" {
		return
	}
	if SensitiveFieldName(name) || ContainsCredential(value) {
		e.attributes[name] = Mask
		e.refused = append(e.refused, name)
		return
	}
	e.attributes[name] = Redact(value)
}

// Attributes returns a copy of the recorded attributes.
func (e *Event) Attributes() map[string]string {
	attributes := make(map[string]string, len(e.attributes))
	for name, value := range e.attributes {
		attributes[name] = value
	}
	return attributes
}

// RefusedAttributes lists the attribute names that were withheld, sorted.
func (e *Event) RefusedAttributes() []string {
	refused := append([]string{}, e.refused...)
	sort.Strings(refused)
	return refused
}

// Validate is the fail-closed collection check every sink runs before writing:
// an event that still carries a credential shape is refused instead of written.
func (e *Event) Validate() error {
	if e == nil {
		return fmt.Errorf("%w: event is nil", ErrInvalidEvent)
	}
	if !KnownEventKind(e.Kind) {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEvent, e.Kind)
	}
	if e.At.IsZero() {
		return fmt.Errorf("%w: the event time is required", ErrInvalidEvent)
	}
	if err := e.Trace.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if ContainsCredential(e.Message) {
		return fmt.Errorf("%w: the message still carries a credential", ErrInvalidEvent)
	}
	for name, value := range e.attributes {
		if value == Mask {
			continue
		}
		if ContainsCredential(value) {
			return fmt.Errorf("%w: attribute %q still carries a credential", ErrInvalidEvent, name)
		}
	}
	return nil
}

// Sink publishes one event. A sink is an observation path: its failure is
// recorded, and never changes whether a node, a run or a delivery succeeded.
type Sink interface {
	Emit(ctx context.Context, event *Event) error
}

// SlogSink writes an event as one structured log record, carrying the same trace
// ids the evaluation backend receives so a log and its trace can be matched.
type SlogSink struct{ Logger *slog.Logger }

// Emit writes the event. The process logger already scrubs values; this sink
// still validates first, because the check belongs to the producer.
func (s SlogSink) Emit(ctx context.Context, event *Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	attributes := make([]slog.Attr, 0, 8+len(event.attributes))
	attributes = append(attributes,
		slog.String("eventSchema", EventSchemaVersion),
		slog.String("eventId", event.EventID),
		slog.String("kind", string(event.Kind)),
		slog.String("traceId", event.Trace.TraceID),
		slog.String("spanId", event.Trace.SpanID),
	)
	if event.Trace.ParentSpanID != "" {
		attributes = append(attributes, slog.String("parentSpanId", event.Trace.ParentSpanID))
	}
	attributes = append(attributes,
		slog.String("workflowRunId", event.Correlation.WorkflowRunID),
		slog.String("nodeRunId", event.Correlation.NodeRunID),
		slog.String("taskId", event.Correlation.TaskID),
	)
	names := make([]string, 0, len(event.attributes))
	for name := range event.attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		attributes = append(attributes, slog.String(name, event.attributes[name]))
	}
	logger.LogAttrs(ctx, slog.LevelInfo, event.Message, attributes...)
	return nil
}

// FanoutSink emits to several sinks. It reports every failure but keeps going:
// one unavailable observation backend must not silence the others.
type FanoutSink []Sink

func (f FanoutSink) Emit(ctx context.Context, event *Event) error {
	var failures []error
	for _, sink := range f {
		if sink == nil {
			continue
		}
		if err := sink.Emit(ctx, event); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
