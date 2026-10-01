// Package executiontest holds adapter-neutral contract suites for execution.Store implementations.
package executiontest

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
)

const (
	resultDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	resultDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	originalUser  = "9001"
	otherUser     = "9002"
)

// Env is one isolated store behind a Service plus the white-box probes the suite needs.
type Env struct {
	Service *execution.Service
	// NewRun persists a WorkflowRun with one queued initial plan.create task for provider.
	// frozenGitUser is the run's already frozen machine identity, or empty when unfrozen.
	NewRun func(t *testing.T, name string, provider execution.ProviderSelection, frozenGitUser string) (taskID string)
	// Registration returns the persisted registration for runtimeID.
	Registration func(t *testing.T, runtimeID string) (execution.RuntimeRegistration, bool)
	// Task returns a comparable snapshot of every persisted field of one task.
	Task func(t *testing.T, taskID string) any
}

// RuntimeRegistration runs the frozen kowa.workflow-execution.v1 Runtime registration contract:
// same epoch and same content is idempotent, same epoch with other content conflicts and leaves
// the persisted facts untouched, and a new epoch replaces the previous dispatch generation.
func RuntimeRegistration(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	t.Run("idempotent_same_epoch", func(t *testing.T) { idempotentSameEpoch(t, newEnv(t)) })
	t.Run("same_epoch_conflicts", func(t *testing.T) { sameEpochConflicts(t, newEnv) })
	t.Run("new_epoch_replaces", func(t *testing.T) { newEpochReplaces(t, newEnv) })
	t.Run("invalid_input", func(t *testing.T) { invalidInput(t, newEnv(t)) })
	t.Run("unknown_runtime_or_epoch", func(t *testing.T) { unknownRuntimeOrEpoch(t, newEnv(t)) })
	t.Run("concurrent_registration", func(t *testing.T) { concurrentRegistration(t, newEnv(t)) })
	t.Run("lease_currency", func(t *testing.T) { leaseCurrency(t, newEnv) })
}

type fixture struct {
	env      Env
	provider execution.ProviderSelection
	base     execution.RuntimeRegistration
	taskID   string
}

// newFixture registers the base runtime and persists one queued task only that registration can serve.
func newFixture(t *testing.T, env Env, mutateBase func(*execution.RuntimeRegistration), frozenGitUser string) fixture {
	t.Helper()
	provider := execution.ProviderSelection{ID: "codex", Version: "v-" + t.Name()}
	base := baseRegistration(t, provider)
	if mutateBase != nil {
		mutateBase(&base)
	}
	fx := fixture{env: env, provider: provider, base: base}
	if err := env.Service.RegisterRuntime(t.Context(), base); err != nil {
		t.Fatalf("RegisterRuntime(base) error = %v", err)
	}
	fx.taskID = env.NewRun(t, t.Name(), provider, frozenGitUser)
	return fx
}

func baseRegistration(t *testing.T, provider execution.ProviderSelection) execution.RuntimeRegistration {
	t.Helper()
	return execution.RuntimeRegistration{
		ID: t.Name() + "/runtime", Epoch: "epoch-1", GitHubUserID: originalUser,
		Provider: provider, ProviderAuthenticated: true,
		Capabilities: []execution.Capability{{ID: "plan.create", Version: 1}, {ID: "code.write", Version: 1}},
	}
}

func (f fixture) register(t *testing.T, registration execution.RuntimeRegistration) error {
	t.Helper()
	return f.env.Service.RegisterRuntime(t.Context(), registration)
}

func (f fixture) lease(t *testing.T, epoch string) (execution.TaskLease, error) {
	t.Helper()
	return f.env.Service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: f.base.ID, RuntimeEpoch: epoch, LeaseDuration: time.Minute,
	})
}

func (f fixture) mustLease(t *testing.T, epoch string) execution.TaskLease {
	t.Helper()
	lease, err := f.lease(t, epoch)
	if err != nil {
		t.Fatalf("LeaseTask(%s) error = %v, want a lease", epoch, err)
	}
	if lease.Task.ID != f.taskID || lease.RuntimeEpoch != epoch {
		t.Fatalf("LeaseTask(%s) = task %q epoch %q, want task %q", epoch, lease.Task.ID, lease.RuntimeEpoch, f.taskID)
	}
	return lease
}

func (f fixture) assertStored(t *testing.T, want execution.RuntimeRegistration) {
	t.Helper()
	got, ok := f.env.Registration(t, want.ID)
	if !ok {
		t.Fatalf("runtime %q is not persisted", want.ID)
	}
	if !reflect.DeepEqual(declaration(got), declaration(want)) {
		t.Fatalf("persisted registration = %#v, want %#v", declaration(got), declaration(want))
	}
}

// declared is the content Runtime registration fixes; capability order carries no meaning.
type declared struct {
	ID, Epoch, GitHubUserID string
	Provider                execution.ProviderSelection
	Authenticated           bool
	Capabilities            []execution.Capability
}

func declaration(r execution.RuntimeRegistration) declared {
	capabilities := slices.Clone(r.Capabilities)
	slices.SortFunc(capabilities, func(a, b execution.Capability) int {
		if a.ID != b.ID {
			if a.ID < b.ID {
				return -1
			}
			return 1
		}
		return a.Version - b.Version
	})
	return declared{
		ID: r.ID, Epoch: r.Epoch, GitHubUserID: r.GitHubUserID, Provider: r.Provider,
		Authenticated: r.ProviderAuthenticated, Capabilities: capabilities,
	}
}

func (f fixture) progress(t *testing.T, lease execution.TaskLease, sequence int64) error {
	t.Helper()
	return f.env.Service.ReportProgress(t.Context(), execution.ProgressReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.ProgressEventKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Sequence: sequence, Phase: "working", Message: "working",
		ObservedAt: time.Date(2026, 9, 29, 4, 0, 0, 0, time.UTC),
	})
}

func (f fixture) result(t *testing.T, lease execution.TaskLease, digest string) (execution.ResultAcceptance, error) {
	t.Helper()
	return f.env.Service.ReportResult(t.Context(), execution.ResultReport{
		SchemaVersion: execution.SchemaVersion, Kind: execution.TaskResultKind,
		TaskID: lease.Task.ID, LeaseID: lease.LeaseID, FencingToken: lease.FencingToken,
		Status: execution.ResultCompleted, ResultDigest: digest,
		Output: map[string]any{"verdict": "approved"},
	})
}

func idempotentSameEpoch(t *testing.T, env Env) {
	fx := newFixture(t, env, nil, "")
	reordered := fx.base
	reordered.Capabilities = []execution.Capability{fx.base.Capabilities[1], fx.base.Capabilities[0]}
	for name, registration := range map[string]execution.RuntimeRegistration{"identical": fx.base, "reordered_capabilities": reordered} {
		if err := fx.register(t, registration); err != nil {
			t.Fatalf("%s same-epoch registration error = %v, want idempotent nil", name, err)
		}
		fx.assertStored(t, fx.base)
	}
	lease := fx.mustLease(t, "epoch-1")
	if lease.Task.GitExecutionUserID != originalUser || lease.Task.ProviderSelection != fx.provider {
		t.Fatalf("lease authority = user %q provider %#v", lease.Task.GitExecutionUserID, lease.Task.ProviderSelection)
	}
}

func sameEpochConflicts(t *testing.T, newEnv func(t *testing.T) Env) {
	cases := []struct {
		name string
		// base mutates the registered runtime before the conflicting claim.
		base func(*execution.RuntimeRegistration)
		// claim mutates a copy of base into the different same-epoch content.
		claim func(*execution.RuntimeRegistration)
		// leasable reports whether the persisted registration can still serve the task.
		leasable bool
	}{
		{"github_user_changed", nil, func(r *execution.RuntimeRegistration) { r.GitHubUserID = otherUser }, true},
		{"provider_id_changed", nil, func(r *execution.RuntimeRegistration) { r.Provider.ID = "claude-code" }, true},
		{"provider_version_changed", nil, func(r *execution.RuntimeRegistration) { r.Provider.Version += "-other" }, true},
		{"authenticated_true_to_false", nil, func(r *execution.RuntimeRegistration) { r.ProviderAuthenticated = false }, true},
		{
			"authenticated_false_to_true",
			func(r *execution.RuntimeRegistration) { r.ProviderAuthenticated = false },
			func(r *execution.RuntimeRegistration) { r.ProviderAuthenticated = true },
			false,
		},
		{"capability_removed", nil, func(r *execution.RuntimeRegistration) { r.Capabilities = r.Capabilities[:1] }, true},
		{"capability_added", nil, func(r *execution.RuntimeRegistration) {
			r.Capabilities = append(slices.Clone(r.Capabilities), execution.Capability{ID: "review.run", Version: 1})
		}, true},
		{"capability_version_changed", nil, func(r *execution.RuntimeRegistration) {
			r.Capabilities = []execution.Capability{{ID: "plan.create", Version: 1}, {ID: "code.write", Version: 2}}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t, newEnv(t), c.base, "")
			claim := fx.base
			claim.Capabilities = slices.Clone(fx.base.Capabilities)
			c.claim(&claim)
			if err := fx.register(t, claim); !errors.Is(err, execution.ErrRuntimeConflict) {
				t.Fatalf("different same-epoch registration error = %v, want ErrRuntimeConflict", err)
			}
			fx.assertStored(t, fx.base)
			if !c.leasable {
				if _, err := fx.lease(t, "epoch-1"); !errors.Is(err, execution.ErrCapabilityUnavailable) {
					t.Fatalf("LeaseTask after rejected claim error = %v, want ErrCapabilityUnavailable", err)
				}
				if err := fx.register(t, fx.base); err != nil {
					t.Fatalf("repeating the original registration error = %v, want nil", err)
				}
				promoted := fx.base
				promoted.Epoch = "epoch-2"
				promoted.ProviderAuthenticated = true
				if err := fx.register(t, promoted); err != nil {
					t.Fatalf("new epoch registration error = %v, want nil", err)
				}
				fx.mustLease(t, "epoch-2")
				return
			}
			lease := fx.mustLease(t, "epoch-1")
			if lease.Task.GitExecutionUserID != originalUser || lease.Task.ProviderSelection != fx.provider {
				t.Fatalf("lease authority = user %q provider %#v, want the persisted registration", lease.Task.GitExecutionUserID, lease.Task.ProviderSelection)
			}
			if err := fx.progress(t, lease, 1); err != nil {
				t.Fatalf("ReportProgress() after rejected claim error = %v", err)
			}
			if accepted, err := fx.result(t, lease, resultDigestA); err != nil || !accepted.Applied {
				t.Fatalf("ReportResult() after rejected claim = %#v, %v", accepted, err)
			}
		})
	}
}

func newEpochReplaces(t *testing.T, newEnv func(t *testing.T) Env) {
	cases := []struct {
		name   string
		frozen string
		change func(*execution.RuntimeRegistration)
		// wantErr is nil when the replaced registration serves the task.
		wantErr  error
		wantUser string
	}{
		{"same_content", "", nil, nil, originalUser},
		{"authenticated_false", "", func(r *execution.RuntimeRegistration) { r.ProviderAuthenticated = false }, execution.ErrCapabilityUnavailable, ""},
		{"provider_changed", "", func(r *execution.RuntimeRegistration) { r.Provider.ID = "claude-code" }, execution.ErrCapabilityUnavailable, ""},
		{"capability_missing", "", func(r *execution.RuntimeRegistration) { r.Capabilities = r.Capabilities[1:] }, execution.ErrCapabilityUnavailable, ""},
		{"github_user_changed_unfrozen_run", "", func(r *execution.RuntimeRegistration) { r.GitHubUserID = otherUser }, nil, otherUser},
		{"github_user_changed_frozen_run", originalUser, func(r *execution.RuntimeRegistration) { r.GitHubUserID = otherUser }, execution.ErrCapabilityUnavailable, ""},
		{"github_user_kept_frozen_run", originalUser, nil, nil, originalUser},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fx := newFixture(t, newEnv(t), nil, c.frozen)
			next := fx.base
			next.Epoch = "epoch-2"
			next.Capabilities = slices.Clone(fx.base.Capabilities)
			if c.change != nil {
				c.change(&next)
			}
			if err := fx.register(t, next); err != nil {
				t.Fatalf("new epoch registration error = %v, want nil", err)
			}
			fx.assertStored(t, next)
			if _, err := fx.lease(t, "epoch-1"); !errors.Is(err, execution.ErrRuntimeConflict) {
				t.Fatalf("LeaseTask(old epoch) error = %v, want ErrRuntimeConflict", err)
			}
			if c.wantErr != nil {
				if _, err := fx.lease(t, "epoch-2"); !errors.Is(err, c.wantErr) {
					t.Fatalf("LeaseTask(new epoch) error = %v, want %v", err, c.wantErr)
				}
				return
			}
			lease := fx.mustLease(t, "epoch-2")
			if lease.Task.GitExecutionUserID != c.wantUser {
				t.Fatalf("lease machine identity = %q, want %q", lease.Task.GitExecutionUserID, c.wantUser)
			}
		})
	}
}

func invalidInput(t *testing.T, env Env) {
	fx := newFixture(t, env, nil, "")
	cases := map[string]func(*execution.RuntimeRegistration){
		"missing_epoch":             func(r *execution.RuntimeRegistration) { r.Epoch = "" },
		"missing_provider_id":       func(r *execution.RuntimeRegistration) { r.Provider.ID = "" },
		"missing_provider_version":  func(r *execution.RuntimeRegistration) { r.Provider.Version = "" },
		"missing_capabilities":      func(r *execution.RuntimeRegistration) { r.Capabilities = nil },
		"duplicate_capability":      func(r *execution.RuntimeRegistration) { r.Capabilities = append(r.Capabilities, r.Capabilities[0]) },
		"capability_version_zero":   func(r *execution.RuntimeRegistration) { r.Capabilities = []execution.Capability{{ID: "plan.create"}} },
		"github_user_not_decimal":   func(r *execution.RuntimeRegistration) { r.GitHubUserID = "octocat" },
		"github_user_leading_zero":  func(r *execution.RuntimeRegistration) { r.GitHubUserID = "0900" },
		"github_user_missing":       func(r *execution.RuntimeRegistration) { r.GitHubUserID = "" },
		"missing_capability_id":     func(r *execution.RuntimeRegistration) { r.Capabilities = []execution.Capability{{Version: 1}} },
		"new_epoch_missing_version": func(r *execution.RuntimeRegistration) { r.Epoch = "epoch-2"; r.Provider.Version = "" },
	}
	for name, mutate := range cases {
		claim := fx.base
		claim.Capabilities = slices.Clone(fx.base.Capabilities)
		mutate(&claim)
		if err := fx.register(t, claim); !errors.Is(err, execution.ErrInvalidInput) {
			t.Fatalf("%s error = %v, want ErrInvalidInput", name, err)
		}
		fx.assertStored(t, fx.base)
	}
	fx.mustLease(t, "epoch-1")
}

func unknownRuntimeOrEpoch(t *testing.T, env Env) {
	fx := newFixture(t, env, nil, "")
	if _, err := fx.env.Service.LeaseTask(t.Context(), execution.LeaseRequest{
		RuntimeID: fx.base.ID + "-unknown", RuntimeEpoch: "epoch-1", LeaseDuration: time.Minute,
	}); !errors.Is(err, execution.ErrRuntimeConflict) {
		t.Fatalf("LeaseTask(unknown runtime) error = %v, want ErrRuntimeConflict", err)
	}
	if _, err := fx.lease(t, "epoch-0"); !errors.Is(err, execution.ErrRuntimeConflict) {
		t.Fatalf("LeaseTask(unregistered epoch) error = %v, want ErrRuntimeConflict", err)
	}
	fx.mustLease(t, "epoch-1")
}

func concurrentRegistration(t *testing.T, env Env) {
	const rounds, callers = 25, 8
	provider := execution.ProviderSelection{ID: "codex", Version: "v-" + t.Name()}
	for round := range rounds {
		base := baseRegistration(t, provider)
		base.ID += "-" + string(rune('a'+round))
		rival := base
		rival.Capabilities = slices.Clone(base.Capabilities)
		rival.Provider.Version += "-rival"
		variants := []execution.RuntimeRegistration{base, rival}

		start := make(chan struct{})
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for caller := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs[caller] = env.Service.RegisterRuntime(t.Context(), variants[caller%len(variants)])
			}()
		}
		close(start)
		wg.Wait()

		stored, ok := env.Registration(t, base.ID)
		if !ok {
			t.Fatalf("round %d: no registration persisted: %v", round, errs)
		}
		winner := -1
		for index, variant := range variants {
			if reflect.DeepEqual(declaration(stored), declaration(variant)) {
				winner = index
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: persisted registration %#v matches no request", round, declaration(stored))
		}
		for caller, err := range errs {
			if caller%len(variants) == winner && err != nil {
				t.Fatalf("round %d caller %d: registration identical to the persisted one error = %v, want nil", round, caller, err)
			}
			if caller%len(variants) != winner && !errors.Is(err, execution.ErrRuntimeConflict) {
				t.Fatalf("round %d caller %d: different same-epoch registration error = %v, want ErrRuntimeConflict", round, caller, err)
			}
		}
	}
}

func leaseCurrency(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Run("superseded_epoch_lease_is_stale", func(t *testing.T) {
		for _, running := range []bool{false, true} {
			name := "leased"
			if running {
				name = "running"
			}
			t.Run(name, func(t *testing.T) {
				fx := newFixture(t, newEnv(t), nil, "")
				lease := fx.mustLease(t, "epoch-1")
				if running {
					if err := fx.progress(t, lease, 1); err != nil {
						t.Fatal(err)
					}
				}
				next := fx.base
				next.Epoch = "epoch-2"
				if err := fx.register(t, next); err != nil {
					t.Fatalf("new epoch registration error = %v", err)
				}
				before := fx.env.Task(t, fx.taskID)
				if err := fx.progress(t, lease, 2); !errors.Is(err, execution.ErrStaleLease) {
					t.Fatalf("ReportProgress(superseded epoch) error = %v, want ErrStaleLease", err)
				}
				if accepted, err := fx.result(t, lease, resultDigestA); !errors.Is(err, execution.ErrStaleLease) || accepted.Applied {
					t.Fatalf("ReportResult(superseded epoch) = %#v, %v, want ErrStaleLease", accepted, err)
				}
				if after := fx.env.Task(t, fx.taskID); !reflect.DeepEqual(before, after) {
					t.Fatalf("rejected superseded-epoch reports changed the task: %#v -> %#v", before, after)
				}
			})
		}
	})
	t.Run("accepted_result_stays_idempotent_after_new_epoch", func(t *testing.T) {
		fx := newFixture(t, newEnv(t), nil, "")
		lease := fx.mustLease(t, "epoch-1")
		if accepted, err := fx.result(t, lease, resultDigestA); err != nil || !accepted.Applied {
			t.Fatalf("ReportResult() = %#v, %v", accepted, err)
		}
		next := fx.base
		next.Epoch = "epoch-2"
		if err := fx.register(t, next); err != nil {
			t.Fatal(err)
		}
		if accepted, err := fx.result(t, lease, resultDigestA); err != nil || !accepted.Duplicate || accepted.Applied {
			t.Fatalf("duplicate ReportResult() = %#v, %v, want accepted duplicate", accepted, err)
		}
		if _, err := fx.result(t, lease, resultDigestB); !errors.Is(err, execution.ErrResultConflict) {
			t.Fatalf("different digest error = %v, want ErrResultConflict", err)
		}
	})
	t.Run("idempotent_registration_keeps_lease_current", func(t *testing.T) {
		fx := newFixture(t, newEnv(t), nil, "")
		lease := fx.mustLease(t, "epoch-1")
		if err := fx.register(t, fx.base); err != nil {
			t.Fatal(err)
		}
		if err := fx.progress(t, lease, 1); err != nil {
			t.Fatalf("ReportProgress() after idempotent registration error = %v", err)
		}
		if accepted, err := fx.result(t, lease, resultDigestA); err != nil || !accepted.Applied {
			t.Fatalf("ReportResult() after idempotent registration = %#v, %v", accepted, err)
		}
	})
	t.Run("rejected_registration_keeps_lease_current", func(t *testing.T) {
		fx := newFixture(t, newEnv(t), nil, "")
		lease := fx.mustLease(t, "epoch-1")
		claim := fx.base
		claim.ProviderAuthenticated = false
		if err := fx.register(t, claim); !errors.Is(err, execution.ErrRuntimeConflict) {
			t.Fatalf("different same-epoch registration error = %v, want ErrRuntimeConflict", err)
		}
		before := fx.env.Task(t, fx.taskID)
		if err := fx.progress(t, lease, 1); err != nil {
			t.Fatalf("ReportProgress() after rejected registration error = %v", err)
		}
		if reflect.DeepEqual(before, fx.env.Task(t, fx.taskID)) {
			t.Fatal("accepted progress did not advance the persisted task")
		}
		if accepted, err := fx.result(t, lease, resultDigestA); err != nil || !accepted.Applied {
			t.Fatalf("ReportResult() after rejected registration = %#v, %v", accepted, err)
		}
	})
}
