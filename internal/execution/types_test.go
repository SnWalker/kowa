package execution

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTaskLeaseWireShape(t *testing.T) {
	t.Parallel()

	lease := TaskLease{
		SchemaVersion: SchemaVersion,
		Kind:          TaskLeaseKind,
		LeaseID:       "lease-1",
		FencingToken:  1,
		RuntimeEpoch:  "epoch-1",
		ExpiresAt:     time.Date(2026, 9, 29, 6, 1, 0, 0, time.UTC),
		Task: TaskSpec{
			ID: "task-1", WorkflowRunID: "run-1", NodeRunID: "node-1",
			Attempt: 1, AttemptKind: AttemptInitial,
			Capability:        Capability{ID: "plan.create", Version: 1},
			ProviderSelection: ProviderSelection{ID: "codex", Version: "1"},
			InputDigest:       digestA, GitExecutionUserID: "9001", GitScopes: []GitScope{},
			DeadlineAt: time.Date(2026, 9, 29, 6, 30, 0, 0, time.UTC),
		},
	}
	encoded, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if envelope["schemaVersion"] != SchemaVersion || envelope["kind"] != TaskLeaseKind {
		t.Fatalf("wire envelope = %s", encoded)
	}
	task, ok := envelope["task"].(map[string]any)
	if !ok {
		t.Fatalf("wire task = %#v", envelope["task"])
	}
	if _, exists := task["providerSelection"]; !exists {
		t.Fatalf("wire task missing providerSelection: %s", encoded)
	}
	for _, forbidden := range []string{"state", "runtimeId", "leaseId", "resultDigest"} {
		if _, exists := task[forbidden]; exists {
			t.Fatalf("wire task includes internal field %q: %s", forbidden, encoded)
		}
	}
}

func TestRuntimeRegistrationSameDeclaration(t *testing.T) {
	t.Parallel()

	base := RuntimeRegistration{
		ID: "runtime-1", Epoch: "epoch-1", GitHubUserID: "9001",
		Provider: ProviderSelection{ID: "codex", Version: "1"}, ProviderAuthenticated: true,
		Capabilities: []Capability{{ID: "plan.create", Version: 1}, {ID: "code.write", Version: 1}},
	}
	cases := []struct {
		name   string
		mutate func(*RuntimeRegistration)
		want   bool
	}{
		{"identical", func(*RuntimeRegistration) {}, true},
		{"capability order and envelope are not content", func(r *RuntimeRegistration) {
			r.Capabilities = []Capability{{ID: "code.write", Version: 1}, {ID: "plan.create", Version: 1}}
			r.RegisteredAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			r.SchemaVersion, r.Kind = SchemaVersion, RuntimeRegistrationKind
		}, true},
		{"runtime id", func(r *RuntimeRegistration) { r.ID = "runtime-2" }, false},
		{"epoch", func(r *RuntimeRegistration) { r.Epoch = "epoch-2" }, false},
		{"github user", func(r *RuntimeRegistration) { r.GitHubUserID = "9002" }, false},
		{"provider id", func(r *RuntimeRegistration) { r.Provider.ID = "claude-code" }, false},
		{"provider version", func(r *RuntimeRegistration) { r.Provider.Version = "2" }, false},
		{"authentication", func(r *RuntimeRegistration) { r.ProviderAuthenticated = false }, false},
		{"capability removed", func(r *RuntimeRegistration) { r.Capabilities = r.Capabilities[:1] }, false},
		{"capability version", func(r *RuntimeRegistration) {
			r.Capabilities = []Capability{{ID: "plan.create", Version: 1}, {ID: "code.write", Version: 2}}
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			other := base
			other.Capabilities = append([]Capability(nil), base.Capabilities...)
			c.mutate(&other)
			if got := base.SameDeclaration(other); got != c.want {
				t.Fatalf("SameDeclaration() = %v, want %v", got, c.want)
			}
			if got := other.SameDeclaration(base); got != c.want {
				t.Fatalf("reverse SameDeclaration() = %v, want %v", got, c.want)
			}
			if base.Capabilities[0].ID != "plan.create" || base.Capabilities[1].ID != "code.write" {
				t.Fatal("SameDeclaration() reordered the receiver's capabilities")
			}
		})
	}
}
