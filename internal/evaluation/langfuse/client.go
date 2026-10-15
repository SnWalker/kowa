// Package langfuse adapts a self-hosted Langfuse instance to the evaluation
// backend port.
//
// The transport is OpenTelemetry OTLP over HTTP with JSON encoding, the
// documented Langfuse ingestion endpoint. It is implemented with the standard
// library only: the record shapes Kowa sends are small and fixed, so the OTLP
// encoder is a few dozen lines and no OpenTelemetry SDK has to enter the module.
//
// Langfuse is an observation backend. This adapter can write records and report
// its own health; it has no read path back into Kowa, and nothing it returns can
// change a WorkflowRun, a NodeRun or a delivery state.
package langfuse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation/backend"
	"github.com/SnWalker/kowa/internal/observability"
)

// Frozen endpoint contract. These three values are what a self-hosted instance
// must expose for the adapter to work, and they are pinned by
// TestRequestShapeMatchesTheFrozenLangfuseContract.
const (
	TracesPath       = "/api/public/otel/v1/traces"
	HealthPath       = "/api/public/health"
	IngestionVersion = "4"
	defaultTimeout   = 10 * time.Second
	scopeName        = "kowa.evaluation"
	serviceName      = "kowa"
)

// Config is one deployment's Langfuse connection. The secret key is a
// credential: it is never logged, never returned and never placed in an error.
type Config struct {
	// BaseURL is the instance root, for example http://127.0.0.1:3000.
	BaseURL string
	// PublicKey and SecretKey are the project keys; together they form the Basic
	// authorization header.
	PublicKey string
	SecretKey string
	// Environment and Release are the deployment labels Langfuse records.
	Environment string
	Release     string
	// Timeout bounds one delivery attempt.
	Timeout time.Duration
}

// Validate reports whether the adapter can be constructed.
func (c Config) Validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return fmt.Errorf("%w: a base url is required", backend.ErrValidation)
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: the base url must be an absolute http(s) url", backend.ErrValidation)
	}
	if c.PublicKey == "" || c.SecretKey == "" {
		return fmt.Errorf("%w: the public and secret keys are both required", backend.ErrValidation)
	}
	return nil
}

// Client is the OTLP/HTTP adapter.
type Client struct {
	config Config
	http   *http.Client
	now    func() time.Time
}

// New constructs the adapter.
func New(config Config, httpClient *http.Client) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	}
	return &Client{config: config, http: httpClient, now: time.Now}, nil
}

// Name identifies the adapter in records and in the health projection.
func (c *Client) Name() string { return "langfuse" }

// Emit delivers one record. A transport failure or a 5xx answer is
// backend.ErrUnavailable (retryable); any other non-2xx answer is
// backend.ErrRejected, because repeating it cannot help.
func (c *Client) Emit(ctx context.Context, envelope backend.Envelope) error {
	payload, err := c.encode(envelope)
	if err != nil {
		return fmt.Errorf("%w: %v", backend.ErrRejected, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(TracesPath), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", backend.ErrUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-langfuse-ingestion-version", IngestionVersion)
	request.Header.Set("Authorization", "Basic "+c.authorization())
	response, err := c.http.Do(request)
	if err != nil {
		// The transport error text can carry the URL but never the credentials:
		// they live in a header, which net/http does not echo into the error.
		return fmt.Errorf("%w: %v", backend.ErrUnavailable, observability.Redact(err.Error()))
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode >= 500:
		return fmt.Errorf("%w: langfuse answered %d: %s",
			backend.ErrUnavailable, response.StatusCode, observability.Redact(shorten(string(body))))
	default:
		return fmt.Errorf("%w: langfuse answered %d: %s",
			backend.ErrRejected, response.StatusCode, observability.Redact(shorten(string(body))))
	}
}

// Health reports whether the instance answers its own health endpoint. An
// unreachable instance is unavailable; it is never reported as healthy.
func (c *Client) Health(ctx context.Context) backend.Health {
	health := backend.Health{Backend: c.Name(), CheckedAt: c.now().UTC()}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(HealthPath), nil)
	if err != nil {
		health.Reason = observability.Redact(err.Error())
		return health
	}
	response, err := c.http.Do(request)
	if err != nil {
		health.Reason = observability.Redact(err.Error())
		return health
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		health.Reason = fmt.Sprintf("langfuse health answered %d", response.StatusCode)
		return health
	}
	health.Available = true
	health.Version = response.Header.Get("x-langfuse-version")
	return health
}

// encode renders one record as an OTLP/JSON trace payload.
//
// The trace and span ids are derived from the record id, so re-delivering a
// record updates one object instead of creating a second one.
func (c *Client) encode(envelope backend.Envelope) ([]byte, error) {
	if envelope.RecordID == "" || !backend.KnownKind(envelope.Kind) || !backend.KnownMode(envelope.Mode) {
		return nil, fmt.Errorf("the record is incomplete")
	}
	traceID, spanID := deriveIDs(envelope.RecordID)
	start := envelope.At.UTC()
	if start.IsZero() {
		start = c.now().UTC()
	}
	attributes := []otlpAttribute{
		stringAttribute("langfuse.trace.name", "kowa."+string(envelope.Kind)),
		stringAttribute("langfuse.trace.metadata.kowaKind", string(envelope.Kind)),
		stringAttribute("langfuse.trace.metadata.kowaMode", string(envelope.Mode)),
		stringAttribute("langfuse.trace.metadata.kowaRecordId", envelope.RecordID),
		stringAttribute("kowa.workspace.id", envelope.WorkspaceID),
		stringAttribute("kowa.workitem.id", envelope.WorkItemID),
		stringAttribute("kowa.run.id", envelope.WorkflowRunID),
		stringAttribute("kowa.node.id", envelope.NodeRunID),
		stringAttribute("kowa.task.id", envelope.TaskID),
		stringAttribute("kowa.subject.digest", envelope.SubjectDigest),
	}
	if envelope.WorkflowRunID != "" {
		attributes = append(attributes, stringAttribute("langfuse.session.id", envelope.WorkflowRunID))
	}
	if c.config.Environment != "" {
		attributes = append(attributes, stringAttribute("langfuse.environment", c.config.Environment))
	}
	if c.config.Release != "" {
		attributes = append(attributes, stringAttribute("langfuse.release", c.config.Release))
	}
	// Only identities, digests and short classifications travel. Everything is
	// scrubbed again here: the adapter is the last gate before the network.
	names := make([]string, 0, len(envelope.Attributes))
	for name := range envelope.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	observationInput := make([]string, 0, len(names))
	for _, name := range names {
		value := observability.RedactField(name, envelope.Attributes[name])
		attributes = append(attributes, stringAttribute("kowa.attribute."+name, value))
		observationInput = append(observationInput, name+"="+value)
	}
	attributes = append(attributes, stringAttribute("langfuse.observation.input", strings.Join(observationInput, ";")))

	payload := otlpPayload{
		ResourceSpans: []otlpResourceSpans{{
			Resource: otlpResource{Attributes: []otlpAttribute{
				stringAttribute("service.name", serviceName),
			}},
			ScopeSpans: []otlpScopeSpans{{
				Scope: otlpScope{Name: scopeName, Version: backend.SchemaVersion},
				Spans: []otlpSpan{{
					TraceID:           traceID,
					SpanID:            spanID,
					Name:              "kowa.evaluation." + string(envelope.Kind),
					Kind:              1,
					StartTimeUnixNano: fmt.Sprint(start.UnixNano()),
					EndTimeUnixNano:   fmt.Sprint(start.UnixNano()),
					Attributes:        attributes,
				}},
			}},
		}},
	}
	return json.Marshal(payload)
}

func (c *Client) endpoint(path string) string {
	return strings.TrimSuffix(c.config.BaseURL, "/") + path
}

// authorization builds the Basic credential. It exists as a named method so the
// credential is built in exactly one place and never stored on the struct.
func (c *Client) authorization() string {
	return base64.StdEncoding.EncodeToString([]byte(c.config.PublicKey + ":" + c.config.SecretKey))
}

// deriveIDs derives the OTLP ids of one record. Both are never all zero, which
// OTLP forbids.
func deriveIDs(recordID string) (string, string) {
	trace := sha256.Sum256([]byte("kowa.langfuse.trace\x00" + recordID))
	span := sha256.Sum256([]byte("kowa.langfuse.span\x00" + recordID))
	return nonZeroHex(trace[:16]), nonZeroHex(span[:8])
}

func nonZeroHex(raw []byte) string {
	zero := true
	for _, b := range raw {
		if b != 0 {
			zero = false
			break
		}
	}
	if zero {
		raw[len(raw)-1] = 1
	}
	return hex.EncodeToString(raw)
}

func shorten(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 300 {
		return text[:300]
	}
	return text
}

// The OTLP/JSON shapes. Only the fields Kowa sends are modelled; uint64 fields
// are encoded as strings, as the OTLP JSON mapping requires.
type otlpPayload struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes"`
}

type otlpAttribute struct {
	Key   string       `json:"key"`
	Value otlpAnyValue `json:"value"`
}

type otlpAnyValue struct {
	StringValue string `json:"stringValue,omitempty"`
}

func stringAttribute(key, value string) otlpAttribute {
	return otlpAttribute{Key: key, Value: otlpAnyValue{StringValue: value}}
}
