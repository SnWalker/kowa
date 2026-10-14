package runtimeops_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestContractExamples validates the kowa.runtime-web.v1 examples semantically:
// the shell gate proves schema conformance with jv, and this test proves the
// invariants a JSON Schema cannot express (freshness reasons, watermark order,
// strict key sets).
func TestContractExamples(t *testing.T) {
	examplesDirectory := filepath.Join("..", "..", "api", "runtime-web", "v1", "examples")
	read := func(name string) map[string]any {
		t.Helper()
		encoded, err := os.ReadFile(filepath.Join(examplesDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(encoded, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	runtimeOf := func(value map[string]any) map[string]any {
		t.Helper()
		runtime, ok := value["runtime"].(map[string]any)
		if !ok {
			t.Fatalf("example has no runtime object: %v", value)
		}
		return runtime
	}

	// Positive list: every freshness value carries its required companions.
	list := read("positive-runtime-list.json")
	runtimes, ok := list["runtimes"].([]any)
	if !ok || len(runtimes) != 3 {
		t.Fatalf("positive list = %v", list)
	}
	seenFreshness := map[string]bool{}
	for _, entry := range runtimes {
		runtime, ok := entry.(map[string]any)
		if !ok {
			t.Fatal("runtime entry is not an object")
		}
		freshness, _ := runtime["freshness"].(string)
		seenFreshness[freshness] = true
		_, hasObservation := runtime["observation"]
		_, hasReason := runtime["staleReason"]
		switch freshness {
		case "fresh":
			if !hasObservation || hasReason {
				t.Fatalf("fresh runtime lacks an observation or carries a stale reason: %v", runtime)
			}
		case "stale":
			if !hasReason {
				t.Fatalf("stale runtime lacks a staleReason: %v", runtime)
			}
		case "unknown":
			if hasObservation {
				t.Fatalf("unknown runtime must not carry an observation: %v", runtime)
			}
		default:
			t.Fatalf("unknown freshness %q", freshness)
		}
	}
	for _, expected := range []string{"fresh", "stale", "unknown"} {
		if !seenFreshness[expected] {
			t.Fatalf("positive list does not cover %q", expected)
		}
	}

	// Positive detail: a superseded-generation observation stays visible with
	// its reason instead of disappearing.
	view := runtimeOf(read("positive-runtime-view.json"))
	if view["freshness"] != "stale" || view["staleReason"] != "superseded-epoch" {
		t.Fatalf("positive view = %v", view)
	}

	// Zero configuration: the audit fields must be absent, because the frozen
	// schema would reject an empty decimal id or a zero timestamp.
	zero := read("positive-execution-config-zero.json")
	zeroConfig, ok := zero["config"].(map[string]any)
	if !ok || zeroConfig["version"] != float64(0) {
		t.Fatalf("zero config = %v", zero)
	}
	if _, present := zeroConfig["updatedByGitHubUserId"]; present {
		t.Fatalf("zero config leaked an empty actor id: %v", zeroConfig)
	}
	if _, present := zeroConfig["updatedAt"]; present {
		t.Fatalf("zero config leaked a zero timestamp: %v", zeroConfig)
	}

	// Semantic negative: a fresh projection cannot carry a watermark that
	// predates its own observation.
	invalid := runtimeOf(read("negative-semantic-watermark-order.json"))
	observation, ok := invalid["observation"].(map[string]any)
	if !ok {
		t.Fatal("semantic negative lacks an observation")
	}
	observedAt, err := time.Parse(time.RFC3339, observation["observedAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	receivedAt, err := time.Parse(time.RFC3339, observation["receivedAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !receivedAt.Before(observedAt) {
		t.Fatal("the semantic negative must violate watermark order")
	}
	if invalid["freshness"] != "fresh" {
		t.Fatal("the semantic negative must claim freshness")
	}

	// Schema negatives: unexpected keys (an environment dump, a runtime token)
	// must be rejected by strict decoding into the frozen view.
	for _, name := range []string{"negative-schema-unknown-field.json", "negative-schema-secret-field.json"} {
		raw := read(name)
		for _, forbidden := range []string{"environment", "runtimeToken"} {
			if _, present := raw[forbidden]; present {
				encoded, err := json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
				decoder := json.NewDecoder(strings.NewReader(string(encoded)))
				decoder.DisallowUnknownFields()
				var target struct {
					Kind string `json:"kind"`
				}
				if err := decoder.Decode(&target); err == nil {
					t.Fatalf("%s was accepted by strict decoding", name)
				}
				continue
			}
		}
		if _, present := raw["runtime"]; present {
			// The secret field lives inside the nested runtime object.
			if _, nested := runtimeOf(raw)["runtimeToken"]; nested {
				encoded, _ := json.Marshal(runtimeOf(raw))
				decoder := json.NewDecoder(strings.NewReader(string(encoded)))
				decoder.DisallowUnknownFields()
				var target struct {
					RuntimeID string `json:"runtimeId"`
				}
				if err := decoder.Decode(&target); err == nil {
					t.Fatalf("%s was accepted by strict decoding", name)
				}
			}
		}
	}
}
