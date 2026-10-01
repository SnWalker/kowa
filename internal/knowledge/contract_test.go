package knowledge_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/artifact"
	"github.com/SnWalker/kowa/internal/knowledge"
)

func TestContractExamples(t *testing.T) {
	const dir = "../../api/knowledge-artifact/v1/examples"
	files, err := filepath.Glob(dir + "/positive-*.json")
	if err != nil || len(files) != 5 {
		t.Fatalf("fixtures %v %v", files, err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case strings.Contains(filepath.Base(file), "snapshot"):
				var s knowledge.Snapshot
				if err = json.Unmarshal(data, &s); err != nil {
					t.Fatal(err)
				}
				s.Canonical, err = json.Marshal(s.Content)
				if err != nil {
					t.Fatal(err)
				}
				if err = knowledge.ValidateSnapshot(s); err != nil {
					t.Fatal(err)
				}
			case strings.Contains(filepath.Base(file), "effective-input"):
				var input knowledge.EffectiveInput
				if err = json.Unmarshal(data, &input); err != nil {
					t.Fatal(err)
				}
				canonical, e := json.Marshal(input.Content)
				if e != nil {
					t.Fatal(e)
				}
				if artifact.Digest(canonical) != input.Digest {
					t.Fatal("input canonical digest mismatch")
				}
			case strings.Contains(filepath.Base(file), "artifact"):
				var ref artifact.Ref
				if err = json.Unmarshal(data, &ref); err != nil {
					t.Fatal(err)
				}
				if err = ref.Validate(); err != nil {
					t.Fatal(err)
				}
			case strings.Contains(filepath.Base(file), "binding-event"):
				var event knowledge.BindingEvent
				if err = json.Unmarshal(data, &event); err != nil {
					t.Fatal(err)
				}
				if event.ID == "" || event.HumanResponseID == "" || event.ConfirmedAt.IsZero() {
					t.Fatal("invalid confirmation")
				}
			}
		})
	}
	t.Run("semantic_snapshot_digest", func(t *testing.T) {
		data, err := os.ReadFile(dir + "/negative-semantic-snapshot-digest.json")
		if err != nil {
			t.Fatal(err)
		}
		var s knowledge.Snapshot
		if err = json.Unmarshal(data, &s); err != nil {
			t.Fatal(err)
		}
		s.Canonical, err = json.Marshal(s.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err = knowledge.ValidateSnapshot(s); err == nil {
			t.Fatal("accepted wrong digest")
		}
	})
	t.Run("semantic_storage_identity", func(t *testing.T) {
		data, err := os.ReadFile(dir + "/negative-semantic-storage-identity.json")
		if err != nil {
			t.Fatal(err)
		}
		var ref artifact.Ref
		if err = json.Unmarshal(data, &ref); err != nil {
			t.Fatal(err)
		}
		if err = ref.Validate(); err == nil {
			t.Fatal("accepted wrong storage identity")
		}
	})
}
