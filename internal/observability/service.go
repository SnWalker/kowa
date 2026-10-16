package observability

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// SchemaVersion is the frozen identity of the two published observability
// projections.
const SchemaVersion = "kowa.observability.v1"

// View identities.
const (
	HealthViewKind     = "observabilityHealth"
	WatermarksViewKind = "observabilityWatermarks"
)

// Projection error categories. They mirror the frozen HTTP categories so a
// transport can map them without reinterpreting anything.
var (
	ErrValidation = errors.New("observability: invalid input")
	ErrNotFound   = errors.New("observability: not found")
	ErrForbidden  = errors.New("observability: forbidden")
)

// Deployment is the frozen identity of the running build. A fact the process
// cannot read is left empty and reads as unknown; none of these fields is
// guessed.
type Deployment struct {
	Commit           string `json:"commit,omitempty"`
	MigrationVersion string `json:"migrationVersion,omitempty"`
	GoVersion        string `json:"goVersion,omitempty"`
}

// Dependency is one required dependency's reachability at one instant.
type Dependency struct {
	Name      string    `json:"name"`
	Available bool      `json:"available"`
	Reason    string    `json:"reason,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// EvaluationStatus is what the evaluation backend port reports to the health
// projection. It is a value, not the port itself: this package must not depend
// on the package that depends on it.
type EvaluationStatus struct {
	Backend   string    `json:"backend,omitempty"`
	Version   string    `json:"version,omitempty"`
	Available bool      `json:"available"`
	Reason    string    `json:"reason,omitempty"`
	Pending   int       `json:"pending"`
	Spooled   int       `json:"spooled"`
	CheckedAt time.Time `json:"checkedAt"`
}

// HealthView is the operator-facing health statement. It reports facts and names
// the reason for every negative one; nothing here is a business verdict and
// nothing here is an SLA.
type HealthView struct {
	SchemaVersion string           `json:"schemaVersion"`
	Kind          string           `json:"kind"`
	At            time.Time        `json:"observedAt"`
	Deployment    Deployment       `json:"deployment"`
	Database      Dependency       `json:"database"`
	Evaluation    EvaluationStatus `json:"evaluation"`
	// Watermarks are the process' own watermarks (what this process observed
	// itself, such as the last successful contact with the evaluation backend).
	Watermarks []Snapshot `json:"watermarks"`
}

// DefinitionInfo identifies the frozen workflow definition a run executes.
type DefinitionInfo struct {
	Name    string `json:"name,omitempty"`
	Version int    `json:"version,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// RunFacts are the progress facts of one delivery run. They come from the
// execution authority; this package reports them and derives their freshness,
// it never computes run state.
type RunFacts struct {
	RunID          string    `json:"workflowRunId"`
	State          string    `json:"state"`
	Version        int64     `json:"version"`
	UpdatedAt      time.Time `json:"updatedAt"`
	NodeRounds     int       `json:"nodeRounds"`
	Attempts       int       `json:"attempts"`
	OpenHumanTasks int       `json:"openHumanTasks"`
}

// ObservationFacts is one subject's GitHub observation, as the delivery
// authority recorded it. A failed read is recorded here with its reason and
// carries no fact.
type ObservationFacts struct {
	SubjectKind   string    `json:"subjectKind"`
	SubjectKey    string    `json:"subjectKey"`
	ObservedAt    time.Time `json:"observedAt"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

// RuntimeFacts is one authorized runtime's last reported observation time. The
// time is the server watermark (when the control plane received it), never the
// Runner's own clock.
type RuntimeFacts struct {
	RuntimeID     string    `json:"runtimeId"`
	ProviderID    string    `json:"providerId,omitempty"`
	ProviderVer   string    `json:"providerVersion,omitempty"`
	Capabilities  []string  `json:"capabilities"`
	ObservedAt    time.Time `json:"observedAt"`
	Authenticated bool      `json:"authenticated"`
}

// ArtifactFacts is the artifact namespace of one workspace.
type ArtifactFacts struct {
	Namespace string `json:"namespace,omitempty"`
	Published int    `json:"published"`
}

// ExternalObservation is the derived watermark of one observed subject. The
// success time only moves on a successful read, so a failed read degrades the
// freshness immediately instead of letting an old fact look new.
type ExternalObservation struct {
	SubjectKind         string    `json:"subjectKind"`
	SubjectKey          string    `json:"subjectKey"`
	LastAttemptAt       time.Time `json:"lastAttemptAt"`
	LastAttemptKnown    bool      `json:"lastAttemptKnown"`
	UnknownReason       string    `json:"unknownReason,omitempty"`
	Freshness           Freshness `json:"freshness"`
	FreshnessReason     string    `json:"freshnessReason,omitempty"`
	SupersededByFailure bool      `json:"supersededByFailure"`
}

// WatermarkView is the machine-readable "freeze the watermarks before you start"
// statement one work item's delivery exposes.
type WatermarkView struct {
	SchemaVersion          string                `json:"schemaVersion"`
	Kind                   string                `json:"kind"`
	At                     time.Time             `json:"observedAt"`
	WorkspaceID            string                `json:"workspaceId"`
	WorkItemID             string                `json:"workItemId"`
	Deployment             Deployment            `json:"deployment"`
	Definition             DefinitionInfo        `json:"definition"`
	ExecutionConfigVersion int64                 `json:"executionConfigVersion"`
	Run                    *RunFacts             `json:"run,omitempty"`
	Observations           []ExternalObservation `json:"observations"`
	// Runtimes reports contactability only. It never reports progress.
	Runtimes []RuntimeContactability `json:"runtimes"`
	// Progress reports the business-progress watermark of the run. It is derived
	// from run facts and never from a heartbeat, so a contactable but stalled
	// run cannot read as progressing.
	Progress               *Snapshot     `json:"progress,omitempty"`
	Artifacts              ArtifactFacts `json:"artifacts"`
	FreshnessWindowSeconds int           `json:"freshnessWindowSeconds"`
}

// RuntimeContactability is one authorized runtime's contactability watermark.
type RuntimeContactability struct {
	RuntimeID     string    `json:"runtimeId"`
	ProviderID    string    `json:"providerId,omitempty"`
	ProviderVer   string    `json:"providerVersion,omitempty"`
	Capabilities  []string  `json:"capabilities"`
	Authenticated bool      `json:"authenticated"`
	ObservedAt    time.Time `json:"observedAt"`
	Freshness     Freshness `json:"freshness"`
	StaleReason   string    `json:"staleReason,omitempty"`
	Contactable   bool      `json:"contactable"`
}

// Source reads the facts the projections are built from. The composition root
// implements it over the owning services; every method is read-only.
type Source interface {
	// Deployment reports the running build's frozen identity.
	Deployment(ctx context.Context) (Deployment, error)
	// WorkItemWatermarks reports the workspace's and one work item's facts.
	WorkItemWatermarks(ctx context.Context, workspaceID, workItemID string) (WatermarkInputs, error)
}

// WatermarkInputs is everything one work item's watermark view is derived from.
type WatermarkInputs struct {
	Definition             DefinitionInfo
	ExecutionConfigVersion int64
	Run                    *RunFacts
	Observations           []ObservationFacts
	Runtimes               []RuntimeFacts
	Artifacts              ArtifactFacts
}

// DependencyProbe reports one dependency's reachability.
type DependencyProbe interface {
	Name() string
	Probe(ctx context.Context) error
}

// EvaluationSource reports the evaluation backend's state.
type EvaluationSource interface {
	EvaluationStatus(ctx context.Context) (EvaluationStatus, error)
}

// Projector derives the two published projections from the observed facts.
type Projector struct {
	source     Source
	database   DependencyProbe
	evaluation EvaluationSource
	registry   *Registry
	now        func() time.Time
}

// NewProjector constructs the projection service. The registry supplies the
// freshness window and holds the watermarks this process observes itself; the
// watermarks derived from persisted facts are computed on the spot.
func NewProjector(
	source Source,
	database DependencyProbe,
	evaluation EvaluationSource,
	registry *Registry,
	now func() time.Time,
) *Projector {
	if now == nil {
		now = time.Now
	}
	if registry == nil {
		registry = NewRegistry(0, now)
	}
	return &Projector{source: source, database: database, evaluation: evaluation, registry: registry, now: now}
}

// Health assembles the process health statement. A probe that is missing is
// reported as unavailable with the reason, never silently omitted.
func (p *Projector) Health(ctx context.Context) (HealthView, error) {
	at := p.now().UTC()
	view := HealthView{
		SchemaVersion: SchemaVersion, Kind: HealthViewKind, At: at,
		Watermarks: []Snapshot{},
	}
	if p.source != nil {
		deployment, err := p.source.Deployment(ctx)
		if err != nil {
			return HealthView{}, err
		}
		view.Deployment = deployment
	}
	view.Database = Dependency{Name: "postgres", CheckedAt: at}
	if p.database == nil {
		view.Database.Reason = "no database probe is configured"
	} else {
		view.Database.Name = p.database.Name()
		if err := p.database.Probe(ctx); err != nil {
			view.Database.Reason = Redact(err.Error())
		} else {
			view.Database.Available = true
		}
	}
	view.Evaluation = EvaluationStatus{Reason: "no evaluation backend is configured", CheckedAt: at}
	backendName := "evaluation"
	if p.evaluation != nil {
		status, err := p.evaluation.EvaluationStatus(ctx)
		switch {
		case err != nil:
			// The health statement reports rather than fails: an unavailable
			// evaluation backend is exactly what this endpoint exists to show.
			view.Evaluation.Reason = Redact(err.Error())
			p.registry.Failure(WatermarkContactability, backendName, at, view.Evaluation.Reason)
		default:
			view.Evaluation = status
			if status.CheckedAt.IsZero() {
				view.Evaluation.CheckedAt = at
			}
			if status.Backend != "" {
				backendName = status.Backend
			}
			// The probe is a real observation point: it is what establishes
			// whether the observation backend can be contacted right now.
			if status.Available {
				p.registry.Success(WatermarkContactability, backendName, "health endpoint answered", at)
			} else {
				p.registry.Failure(WatermarkContactability, backendName, at, status.Reason)
			}
		}
	}
	view.Watermarks = p.registry.Snapshots()
	return view, nil
}

// Watermarks assembles the per-work-item watermark statement.
func (p *Projector) Watermarks(ctx context.Context, workspaceID, workItemID string) (WatermarkView, error) {
	if workspaceID == "" || workItemID == "" {
		return WatermarkView{}, fmt.Errorf("%w: workspace and work item are required", ErrValidation)
	}
	if p.source == nil {
		return WatermarkView{}, fmt.Errorf("%w: no facts source is configured", ErrValidation)
	}
	inputs, err := p.source.WorkItemWatermarks(ctx, workspaceID, workItemID)
	if err != nil {
		return WatermarkView{}, err
	}
	deployment, err := p.source.Deployment(ctx)
	if err != nil {
		return WatermarkView{}, err
	}
	at := p.now().UTC()
	view := WatermarkView{
		SchemaVersion: SchemaVersion, Kind: WatermarksViewKind, At: at,
		WorkspaceID: workspaceID, WorkItemID: workItemID,
		Deployment: deployment, Definition: inputs.Definition,
		ExecutionConfigVersion: inputs.ExecutionConfigVersion,
		Run:                    inputs.Run,
		Observations:           []ExternalObservation{},
		Runtimes:               []RuntimeContactability{},
		Artifacts:              inputs.Artifacts,
		FreshnessWindowSeconds: int(p.registry.TTL() / time.Second),
	}
	for _, observation := range inputs.Observations {
		view.Observations = append(view.Observations, p.observation(at, observation))
	}
	sort.Slice(view.Observations, func(left, right int) bool {
		if view.Observations[left].SubjectKind != view.Observations[right].SubjectKind {
			return view.Observations[left].SubjectKind < view.Observations[right].SubjectKind
		}
		return view.Observations[left].SubjectKey < view.Observations[right].SubjectKey
	})
	for _, runtime := range inputs.Runtimes {
		contact := RuntimeContactability{
			RuntimeID: runtime.RuntimeID, ProviderID: runtime.ProviderID, ProviderVer: runtime.ProviderVer,
			Capabilities: runtime.Capabilities, Authenticated: runtime.Authenticated,
			ObservedAt: runtime.ObservedAt,
		}
		if contact.Capabilities == nil {
			contact.Capabilities = []string{}
		}
		// Contactability is its own watermark, written only by Runner contact.
		watermark := Watermark{Kind: WatermarkContactability, Subject: runtime.RuntimeID}
		if !runtime.ObservedAt.IsZero() {
			watermark.RecordSuccess(runtime.ObservedAt, "runtime observation received")
		}
		contact.Freshness, contact.StaleReason = watermark.FreshnessOf(at, p.registry.TTL())
		contact.Contactable = contact.Freshness == FreshnessFresh
		view.Runtimes = append(view.Runtimes, contact)
	}
	sort.Slice(view.Runtimes, func(left, right int) bool { return view.Runtimes[left].RuntimeID < view.Runtimes[right].RuntimeID })
	if inputs.Run != nil {
		// Business progress is derived from the run's own facts. A heartbeat is
		// never an input here, so a contactable, stalled run reads as stale.
		progress := Watermark{Kind: WatermarkBusinessProgress, Subject: inputs.Run.RunID}
		if !inputs.Run.UpdatedAt.IsZero() {
			progress.RecordSuccess(inputs.Run.UpdatedAt, "run advanced")
		}
		snapshot := Snapshot{Watermark: progress}
		snapshot.Freshness, snapshot.StaleReason = progress.FreshnessOf(at, p.registry.TTL())
		snapshot.Progressing = snapshot.Freshness == FreshnessFresh
		view.Progress = &snapshot
	}
	return view, nil
}

// observation derives one external-observation watermark.
func (p *Projector) observation(at time.Time, facts ObservationFacts) ExternalObservation {
	watermark := Watermark{Kind: WatermarkExternalObservation, Subject: facts.SubjectKind + "/" + facts.SubjectKey}
	if !facts.Unknown {
		watermark.RecordSuccess(facts.ObservedAt, "observed")
	} else {
		watermark.RecordFailure(facts.ObservedAt, facts.UnknownReason)
	}
	freshness, reason := watermark.FreshnessOf(at, p.registry.TTL())
	return ExternalObservation{
		SubjectKind: facts.SubjectKind, SubjectKey: facts.SubjectKey,
		LastAttemptAt: facts.ObservedAt, LastAttemptKnown: !facts.Unknown,
		UnknownReason: facts.UnknownReason,
		Freshness:     freshness, FreshnessReason: reason,
		SupersededByFailure: facts.Unknown,
	}
}
