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
