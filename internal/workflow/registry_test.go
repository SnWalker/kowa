package workflow

import (
	"errors"
	"testing"
)

func TestRegistryPublish(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	plan, err := Compile(validDefinition(), Catalog{
		{ID: "plan.create", Version: 1}: {Input: Schema{Fields: map[string]ValueType{}}, Output: Schema{Fields: map[string]ValueType{"result_ref": ValueArtifactRef}}},
		{ID: "plan.review", Version: 1}: {Input: Schema{Fields: map[string]ValueType{"plan": ValueArtifactRef}}, Output: Schema{Fields: map[string]ValueType{"verdict": ValueString}}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if err := registry.Publish(plan); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := registry.Publish(plan); err != nil {
		t.Fatalf("Publish() idempotent error = %v", err)
	}

	drifted := plan
	drifted.Digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := registry.Publish(drifted); !errors.Is(err, ErrDefinitionConflict) {
		t.Fatalf("Publish() drift error = %v, want ErrDefinitionConflict", err)
	}
	if _, err := registry.Get("missing", 1); !errors.Is(err, ErrDefinitionNotFound) {
		t.Fatalf("Get() error = %v, want ErrDefinitionNotFound", err)
	}
}
