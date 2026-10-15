package observability_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/observability"
)

// contractDirectory is where the frozen examples live.
const contractDirectory = "../../api/observability/v1/examples"

func readExample(t *testing.T, name string) map[string]any {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(contractDirectory, name))
	if err != nil {
		t.Fatalf("reading %s failed: %v", name, err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decoding %s failed: %v", name, err)
	}
	return document
}

// TestContractExamples walks the published examples and checks the semantics the
// JSON Schema cannot express. The schema gate (scripts/check-observability-
// contract.sh) checks the shape; these are the rules that make the shape mean
// something.
func TestContractExamples(t *testing.T) {
	entries, err := os.ReadDir(contractDirectory)
	if err != nil {
		t.Fatalf("reading the example directory failed: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := entry.Name()
		document := readExample(t, name)
		checked++
		switch {
		case strings.HasPrefix(name, "negative-semantic-"):
			if err := semanticViolation(document); err == nil {
				t.Fatalf("%s must be refused by the semantic rules", name)
			}
		case strings.HasPrefix(name, "negative-schema-"):
			// Shape problems are the schema gate's business; this test only
			// asserts that a leaked credential is never silently acceptable.
			if name == "negative-schema-secret-field.json" {
				if !documentCarriesACredential(document) {
					t.Fatalf("%s no longer demonstrates a leaked credential", name)
				}
			}
		default:
			if err := semanticViolation(document); err != nil {
				t.Fatalf("%s must satisfy the semantic rules: %v", name, err)
			}
			if documentCarriesACredential(document) {
				t.Fatalf("%s carries a credential", name)
			}
		}
	}
	if checked < 5 {
		t.Fatalf("only %d examples were checked, so the gate proves little", checked)
	}
}

// semanticViolation encodes the rules the projection guarantees: a failed read
// never reads as a fresh fact, progress is never claimed for a stalled run, and
// a contactability statement never says anything about progress.
func semanticViolation(document map[string]any) error {
	for _, observation := range list(document["observations"]) {
		freshness, _ := observation["freshness"].(string)
		known, _ := observation["lastAttemptKnown"].(bool)
		superseded, _ := observation["supersededByFailure"].(bool)
		if !known && freshness == string(observability.FreshnessFresh) {
			return errorStub("a failed observation attempt is reported as fresh")
		}
		if known && superseded {
			return errorStub("a known observation cannot be superseded by a failure")
		}
	}
	if progress, ok := document["progress"].(map[string]any); ok {
		freshness, _ := progress["freshness"].(string)
		progressing, _ := progress["progressing"].(bool)
		if (freshness == string(observability.FreshnessFresh)) != progressing {
			return errorStub("the progress flag disagrees with its own freshness")
		}
	}
	for _, runtime := range list(document["runtimes"]) {
		progressing, _ := runtime["progressing"].(bool)
		if progressing {
			return errorStub("a contactability statement must never claim progress")
		}
	}
	return nil
}

// documentCarriesACredential reports whether any string in the document still
// matches a credential shape. The published surface must never satisfy it.
func documentCarriesACredential(value any) bool {
	switch typed := value.(type) {
	case string:
		return observability.ContainsCredential(typed)
	case map[string]any:
		for _, nested := range typed {
			if documentCarriesACredential(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if documentCarriesACredential(nested) {
				return true
			}
		}
	}
	return false
}

func list(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	documents := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if document, ok := item.(map[string]any); ok {
			documents = append(documents, document)
		}
	}
	return documents
}

type errorStub string

func (e errorStub) Error() string { return string(e) }
