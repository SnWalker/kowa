package observability

import (
	"sort"
	"sync"
	"time"
)

// WatermarkKind separates what a watermark actually establishes. The kinds are
// never merged: an environment that answers heartbeats is not an environment
// that is producing business progress.
type WatermarkKind string

const (
	// WatermarkContactability is written by Runner heartbeats. It establishes
	// that the environment can still be contacted, nothing more.
	WatermarkContactability WatermarkKind = "contactability"
	// WatermarkBusinessProgress is written by node round and Task progress. It
	// is the only kind that establishes that work is still moving.
	WatermarkBusinessProgress WatermarkKind = "business_progress"
	// WatermarkExternalObservation is written by read-only GitHub reads. Only a
	// successful read advances the success time; a failed read advances the
	// attempt time and degrades the freshness immediately.
	WatermarkExternalObservation WatermarkKind = "external_observation"
)

// Freshness is the three-valued state of one watermark. `unknown` (nothing was
// ever established) is distinct from `stale` (something was established and is
// no longer current) and from `fresh`.
type Freshness string

const (
	FreshnessFresh   Freshness = "fresh"
	FreshnessStale   Freshness = "stale"
	FreshnessUnknown Freshness = "unknown"
)

// Stale reasons refine a non-fresh watermark.
const (
	StaleExpired          = "expired"
	StaleSuperseded       = "superseded-by-failure"
	StaleNeverEstablished = "never-established"
)

// Watermark records what was actually established for one subject. A success and
// an attempt are recorded apart: a failure never refreshes a success, so an old
// observation can never read as a new fact.
type Watermark struct {
	Kind              WatermarkKind `json:"kind"`
	Subject           string        `json:"subject"`
	LastSuccessAt     *time.Time    `json:"lastSuccessAt,omitempty"`
	LastSuccessDetail string        `json:"lastSuccessDetail,omitempty"`
	LastAttemptAt     *time.Time    `json:"lastAttemptAt,omitempty"`
	LastFailureAt     *time.Time    `json:"lastFailureAt,omitempty"`
	LastFailureReason string        `json:"lastFailureReason,omitempty"`
	Successes         int64         `json:"successes"`
	Failures          int64         `json:"failures"`
}

// RecordSuccess records one established fact at the given time.
func (w *Watermark) RecordSuccess(at time.Time, detail string) {
	utc := at.UTC()
	w.LastSuccessAt = &utc
	w.LastSuccessDetail = Redact(detail)
	w.LastAttemptAt = &utc
	w.Successes++
}

// RecordFailure records one failed attempt. The success time is deliberately
// left where it was: freshness is derived from the pair, not from this call.
func (w *Watermark) RecordFailure(at time.Time, reason string) {
	utc := at.UTC()
	w.LastAttemptAt = &utc
	w.LastFailureAt = &utc
	w.LastFailureReason = Redact(reason)
	w.Failures++
}

// Freshness derives the state at `now` with the given time to live. A failure
// recorded at or after the last success makes the watermark stale immediately,
// even when the success itself is still inside the window: an observation that
// cannot be ordered against a failed read is reported as stale rather than
// fresh, so an old fact never masquerades as a new one.
func (w Watermark) Freshness(now time.Time, ttl time.Duration) Freshness {
	if w.LastSuccessAt == nil {
		return FreshnessUnknown
	}
	if w.LastFailureAt != nil && !w.LastFailureAt.Before(*w.LastSuccessAt) {
		return FreshnessStale
	}
	if ttl > 0 && now.UTC().Sub(*w.LastSuccessAt) > ttl {
		return FreshnessStale
	}
	return FreshnessFresh
}

// FreshnessOf explains a non-fresh watermark, so a reader is told why.
func (w Watermark) FreshnessOf(now time.Time, ttl time.Duration) (Freshness, string) {
	freshness := w.Freshness(now, ttl)
	if freshness == FreshnessFresh {
		return freshness, ""
	}
	switch {
	case w.LastSuccessAt == nil:
		return freshness, StaleNeverEstablished
	case w.LastFailureAt != nil && !w.LastFailureAt.Before(*w.LastSuccessAt):
		return freshness, StaleSuperseded
	default:
		return freshness, StaleExpired
	}
}

// Snapshot is one watermark together with the freshness it had when the snapshot
// was taken. The freshness is computed once, so a reader cannot disagree with
// itself while walking a list.
type Snapshot struct {
	Watermark
	Freshness   Freshness `json:"freshness"`
	StaleReason string    `json:"staleReason,omitempty"`
	Contactable bool      `json:"contactable"`
	Progressing bool      `json:"progressing"`
}

// Registry holds the process' watermarks. It is a projection of observed facts,
// not a state authority: losing it costs observability, never a business fact.
type Registry struct {
	mutex      sync.Mutex
	watermarks map[string]Watermark
	ttl        time.Duration
	now        func() time.Time
}

// NewRegistry constructs a registry. `ttl` bounds how long a success stays
// fresh; a non-positive ttl makes every established watermark fresh forever,
// which is only useful in tests.
func NewRegistry(ttl time.Duration, now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{watermarks: map[string]Watermark{}, ttl: ttl, now: now}
}

func watermarkKey(kind WatermarkKind, subject string) string {
	return string(kind) + "\x00" + subject
}

// Observe records one attempt at the registry's current time. A nil err is a
// success; a non-nil err records the failure without touching the success time.
func (r *Registry) Observe(kind WatermarkKind, subject, detail string, err error) {
	at := r.now().UTC()
	if err != nil {
		r.Failure(kind, subject, at, err.Error())
		return
	}
	r.Success(kind, subject, detail, at)
}

// Success records one established fact for a subject.
func (r *Registry) Success(kind WatermarkKind, subject, detail string, at time.Time) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	key := watermarkKey(kind, subject)
	watermark := r.watermarks[key]
	watermark.Kind, watermark.Subject = kind, subject
	watermark.RecordSuccess(at, detail)
	r.watermarks[key] = watermark
}

// Failure records one failed attempt for a subject.
func (r *Registry) Failure(kind WatermarkKind, subject string, at time.Time, reason string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	key := watermarkKey(kind, subject)
	watermark := r.watermarks[key]
	watermark.Kind, watermark.Subject = kind, subject
	watermark.RecordFailure(at, reason)
	r.watermarks[key] = watermark
}

// Get returns one subject's snapshot.
func (r *Registry) Get(kind WatermarkKind, subject string) (Snapshot, bool) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	watermark, found := r.watermarks[watermarkKey(kind, subject)]
	if !found {
		return Snapshot{}, false
	}
	return r.snapshot(watermark), true
}

// Snapshots lists every watermark, ordered by kind then subject so a reader gets
// a stable order.
func (r *Registry) Snapshots() []Snapshot {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	watermarks := make([]Watermark, 0, len(r.watermarks))
	for _, watermark := range r.watermarks {
		watermarks = append(watermarks, watermark)
	}
	sort.Slice(watermarks, func(left, right int) bool {
		if watermarks[left].Kind != watermarks[right].Kind {
			return watermarks[left].Kind < watermarks[right].Kind
		}
		return watermarks[left].Subject < watermarks[right].Subject
	})
	snapshots := make([]Snapshot, 0, len(watermarks))
	for _, watermark := range watermarks {
		snapshots = append(snapshots, r.snapshot(watermark))
	}
	return snapshots
}

// SnapshotsOf lists the watermarks of one kind.
func (r *Registry) SnapshotsOf(kind WatermarkKind) []Snapshot {
	snapshots := r.Snapshots()
	filtered := make([]Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.Kind == kind {
			filtered = append(filtered, snapshot)
		}
	}
	return filtered
}

// snapshot stamps the derived fields. Contactable and Progressing are derived
// from the snapshot's own kind only, so a fresh heartbeat can never make a
// stalled subject read as progressing.
func (r *Registry) snapshot(watermark Watermark) Snapshot {
	freshness, reason := watermark.FreshnessOf(r.now().UTC(), r.ttl)
	snapshot := Snapshot{Watermark: watermark, Freshness: freshness, StaleReason: reason}
	switch watermark.Kind {
	case WatermarkContactability:
		snapshot.Contactable = freshness == FreshnessFresh
	case WatermarkBusinessProgress:
		snapshot.Progressing = freshness == FreshnessFresh
	}
	return snapshot
}

// TTL is the freshness window this registry applies.
func (r *Registry) TTL() time.Duration { return r.ttl }
