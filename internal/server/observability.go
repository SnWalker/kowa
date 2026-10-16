package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/evaluation/backend"
	"github.com/SnWalker/kowa/internal/evaluation/langfuse"
	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/githubdelivery"
	"github.com/SnWalker/kowa/internal/humantask"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/interfaces/httpapi"
	"github.com/SnWalker/kowa/internal/observability"
	"github.com/SnWalker/kowa/internal/orchestration"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/platform/random"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/workitem"
	"github.com/SnWalker/kowa/internal/workspace"
)

// healthProbeTimeout bounds the evaluation backend probe of one readiness
// request. A readiness answer that waits for a slow external service is not a
// readiness answer.
const healthProbeTimeout = 5 * time.Second

var errNoDatabase = errors.New("no database is configured")

// newObservabilityRegistry builds the process watermark registry. The freshness
// window is the runtime-observation window, so the runtime projection and the
// health statement cannot disagree about when an observation stopped being
// current.
func newObservabilityRegistry(observabilityConfig config.Observability) *observability.Registry {
	ttl := observabilityConfig.ObservationTTL
	if ttl <= 0 {
		ttl = runtimeops.DefaultObservationTTL
	}
	return observability.NewRegistry(ttl, time.Now)
}

// newEvaluationRecorder assembles the evaluation recorder over the file spool.
// The spool lives with the rest of the rebuildable local state: losing it costs
// trajectories, never a business fact. The backend is absent unless the whole
// configuration is present, and an absent backend is reported as such.
func newEvaluationRecorder(
	runtimeConfig config.Runtime,
	observabilityConfig config.Observability,
) (*backend.Recorder, error) {
	if err := observabilityConfig.Validate(); err != nil {
		return nil, err
	}
	spool, err := backend.NewFileSpool(filepath.Join(runtimeConfig.MaterializeRoot, "evaluation-spool"))
	if err != nil {
		return nil, err
	}
	var backendPort backend.Backend
	if observabilityConfig.Enabled {
		client, err := langfuse.New(langfuse.Config{
			BaseURL:     observabilityConfig.BaseURL,
			PublicKey:   observabilityConfig.PublicKey,
			SecretKey:   observabilityConfig.SecretKey,
			Environment: observabilityConfig.Environment,
			Release:     observabilityConfig.Release,
			Timeout:     observabilityConfig.Timeout,
		}, nil)
		if err != nil {
			// A configuration error is logged without the credential and leaves
			// the backend absent: the process still starts.
			return backend.NewRecorder(nil, spool, recordIDs{}, time.Now), nil
		}
		backendPort = client
	}
	return backend.NewRecorder(backendPort, spool, recordIDs{}, time.Now), nil
}

// newObservabilityProjector assembles the two published projections over the
// services that own the facts.
func newObservabilityProjector(
	runtimeConfig config.Runtime,
	orchestrationService *orchestration.Service,
	workItems *workitem.Service,
	runs *execution.Service,
	humans *humantask.Service,
	runtimes *runtimeops.Service,
	deliveries *githubdelivery.Service,
	db *sql.DB,
	recorder *backend.Recorder,
	registry *observability.Registry,
) *observability.Projector {
	if orchestrationService == nil || workItems == nil || runs == nil || runtimes == nil {
		return nil
	}
	source := observabilitySource{
		orchestration: orchestrationService, workItems: workItems, runs: runs, humans: humans,
		runtimes: runtimes, deliveries: deliveries, materializeRoot: runtimeConfig.MaterializeRoot,
	}
	return observability.NewProjector(source, databaseProbe{db}, evaluationHealth{recorder}, registry, time.Now)
}

// observabilitySource reads the watermarks' facts from their owning services.
// Every method is read-only and every identity is re-checked by the owner.
type observabilitySource struct {
	orchestration   *orchestration.Service
	workItems       *workitem.Service
	runs            *execution.Service
	humans          *humantask.Service
	runtimes        *runtimeops.Service
	deliveries      *githubdelivery.Service
	materializeRoot string
}

func (s observabilitySource) Deployment(context.Context) (observability.Deployment, error) {
	return deploymentFacts(), nil
}

func (s observabilitySource) WorkItemWatermarks(
	ctx context.Context,
	workspaceID, workItemID string,
) (observability.WatermarkInputs, error) {
	item, err := s.workItems.Get(ctx, workspaceID, workItemID)
	if err != nil {
		return observability.WatermarkInputs{}, err
	}
	plan := s.orchestration.Plan()
	inputs := observability.WatermarkInputs{
		Definition: observability.DefinitionInfo{
			Name: plan.Name, Version: plan.Version, Digest: plan.Digest,
		},
		Observations: []observability.ObservationFacts{},
		Runtimes:     []observability.RuntimeFacts{},
		Artifacts: observability.ArtifactFacts{
			// The namespace identifies the store without publishing a host path.
			Namespace: artifactNamespace(s.materializeRoot),
		},
	}
	if executionConfig, err := s.runtimes.ExecutionConfig(ctx, workspaceID); err == nil {
		inputs.ExecutionConfigVersion = executionConfig.Version
	}
	if items, err := s.orchestration.Artifacts(ctx, workspaceID, workItemID); err == nil {
		inputs.Artifacts.Published = len(items.Artifacts)
	}
	if views, err := s.runtimes.WorkspaceRuntimes(ctx, workspaceID); err == nil {
		for _, view := range views {
			facts := observability.RuntimeFacts{
				RuntimeID: view.RuntimeID, ProviderID: view.ProviderID, ProviderVer: view.ProviderVersion,
				Capabilities: []string{}, Authenticated: view.ProviderAuthenticated,
			}
			for _, capability := range view.Capabilities {
				facts.Capabilities = append(facts.Capabilities, capability.ID+"@"+strconv.Itoa(capability.Version))
			}
			if view.Observation != nil {
				// The server watermark, never the Runner's clock.
				facts.ObservedAt = view.Observation.ReceivedAt
			} else {
				facts.ObservedAt = view.RegisteredAt
			}
			inputs.Runtimes = append(inputs.Runtimes, facts)
		}
	}
	if s.deliveries != nil {
		if view, err := s.deliveries.DeliveryView(ctx, workspaceID, workItemID); err == nil {
			for _, fact := range view.Facts {
				inputs.Observations = append(inputs.Observations, observability.ObservationFacts{
					SubjectKind: fact.SubjectKind, SubjectKey: fact.SubjectKey,
					ObservedAt: fact.ObservedAt, Unknown: fact.Unknown, UnknownReason: fact.UnknownReason,
				})
			}
		}
	}
	if item.CurrentRunID != "" {
		run, err := s.runs.Run(ctx, item.CurrentRunID)
		if err == nil && run.WorkspaceID == workspaceID && run.WorkItemID == workItemID {
			facts := observability.RunFacts{
				RunID: run.ID, State: string(run.State), Version: run.Version, UpdatedAt: run.UpdatedAt,
			}
			if rounds, err := s.runs.RunRounds(ctx, run.ID); err == nil {
				facts.NodeRounds = len(rounds)
			}
			if attempts, err := s.runs.RunAttempts(ctx, run.ID); err == nil {
				facts.Attempts = len(attempts)
			}
			inputs.Run = &facts
			if s.humans != nil {
				if pending, err := s.humans.List(ctx, workspaceID, true); err == nil {
					for _, task := range pending {
						if task.WorkItemID == workItemID {
							inputs.Run.OpenHumanTasks++
						}
					}
				}
			}
		}
	}
	return inputs, nil
}

// deploymentFacts reports what the running binary can prove about itself. A fact
// it cannot read is left empty rather than guessed.
func deploymentFacts() observability.Deployment {
	facts := observability.Deployment{GoVersion: runtime.Version(), MigrationVersion: migrationsVersion()}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				facts.Commit = setting.Value
			}
		}
	}
	return facts
}

var migrationNamePattern = regexp.MustCompile(`^([0-9]{6})_.*\.up\.sql$`)

// migrationsVersion reads the highest migration version of the deployed tree.
// An unreadable directory yields an empty version, which the projection reports
// as unknown instead of inventing a number.
func migrationsVersion() string {
	entries, err := os.ReadDir(filepath.Join("db", "migrations"))
	if err != nil {
		return ""
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if match := migrationNamePattern.FindStringSubmatch(entry.Name()); match != nil {
			versions = append(versions, match[1])
		}
	}
	if len(versions) == 0 {
		return ""
	}
	sort.Strings(versions)
	return versions[len(versions)-1]
}

// artifactNamespace identifies the artifact store by a digest of its root, so
// the statement is comparable between runs without publishing a host path.
func artifactNamespace(root string) string {
	if root == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(root))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// recordIDs generates opaque evaluation record ids. The hex alphabet keeps the
// id inside the frozen identifier grammar, so it can travel into a spool file
// name and an evidence reference unchanged.
type recordIDs struct{}

func (recordIDs) New() (string, error) {
	suffix, err := random.HexGenerator{}.New()
	if err != nil {
		return "", err
	}
	return "eval_" + suffix, nil
}

// databaseProbe reports PostgreSQL reachability for the readiness statement.
type databaseProbe struct{ db *sql.DB }

func (p databaseProbe) Name() string { return "postgres" }

func (p databaseProbe) Probe(ctx context.Context) error {
	if p.db == nil {
		return errNoDatabase
	}
	return p.db.PingContext(ctx)
}

// evaluationHealth adapts the evaluation recorder to the health projection.
type evaluationHealth struct{ recorder *backend.Recorder }

func (h evaluationHealth) EvaluationStatus(ctx context.Context) (observability.EvaluationStatus, error) {
	if h.recorder == nil {
		return observability.EvaluationStatus{Reason: "no evaluation backend is configured"}, nil
	}
	probeContext, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	health := h.recorder.Health(probeContext)
	records, err := h.recorder.Records(ctx)
	status := observability.EvaluationStatus{
		Backend: health.Backend, Version: health.Version, Available: health.Available,
		Reason: health.Reason, Spooled: len(records), CheckedAt: health.CheckedAt,
	}
	if err != nil {
		status.Reason = observability.Redact(err.Error())
		status.Available = false
		return status, nil
	}
	for _, record := range records {
		if record.Status.Retryable() {
			status.Pending++
		}
	}
	return status, nil
}

// newObservabilitySurface assembles the kowa.observability.v1 handler over the
// S01 session and membership authorities. It is nil when identity is disabled,
// exactly like the other authenticated surfaces.
func newObservabilitySurface(
	identityConfig config.Identity,
	auth *identity.Service,
	workspaces *workspace.Service,
	projector *observability.Projector,
	logger *slog.Logger,
) *httpapi.ObservabilityHandler {
	if !identityConfig.Enabled || auth == nil || workspaces == nil || projector == nil {
		return nil
	}
	return httpapi.NewObservabilityHandler(auth, workspaces, projector, identityConfig.TrustedOrigin, logger)
}

// mountObservabilitySurface adds the readiness endpoint and the
// kowa.observability.v1 routes. It is mounted after the frozen surfaces, so the
// workspace dispatcher never has to know about the observability path family.
func mountObservabilitySurface(
	srv *http.Server,
	handler *httpapi.ObservabilityHandler,
	projector *observability.Projector,
	logger *slog.Logger,
) {
	if handler == nil || projector == nil {
		return
	}
	existing := srv.Handler
	observabilityAPI := http.Handler(handler)
	readiness := httpapi.NewReadinessHandler(projector)
	srv.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == httpapi.ReadyPath:
			readiness.ServeHTTP(writer, request)
		case strings.HasPrefix(request.URL.Path, "/api/v1/observability/"),
			isObservabilityWatermarksPath(request):
			observabilityAPI.ServeHTTP(writer, request)
		default:
			existing.ServeHTTP(writer, request)
		}
	})
	if logger != nil {
		logger.Info("observability surface mounted", "health", "/api/v1/observability/health", "readyz", httpapi.ReadyPath)
	}
}

// isObservabilityWatermarksPath matches exactly the frozen watermark route:
// /api/v1/workspaces/{workspaceID}/work-items/{workItemID}/observability/watermarks
func isObservabilityWatermarksPath(request *http.Request) bool {
	path := request.URL.Path
	const prefix = "/api/v1/workspaces/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/observability/watermarks") {
		return false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(path, prefix), "/observability/watermarks")
	parts := strings.Split(middle, "/")
	return len(parts) == 4 && parts[0] != "" && parts[1] == "work-items" && parts[2] != "" && parts[3] != ""
}
