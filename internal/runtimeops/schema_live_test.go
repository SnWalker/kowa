package runtimeops_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/execution"
	"github.com/SnWalker/kowa/internal/runtimeops"
)

// TestLiveProjectionsMatchTheFrozenSchema validates what the real
// runtime-operations producer emits — list items, a view with historical
// observation, the zero configuration and a written configuration — against
// api/runtime-web/v1/schema.json with the pinned repository validator.
func TestLiveProjectionsMatchTheFrozenSchema(t *testing.T) {
	ctx := t.Context()
	source := fakeSource{
		"runtime-1": statusFor("runtime-1", "epoch-1", execution.Capability{ID: "plan.create", Version: 1}),
	}
	f := newFixture(t, source)
	if err := f.svc.ReportObservation(ctx, observationFor("runtime-1", "epoch-1", f.now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateExecutionConfig(ctx, runtimeops.UpdateExecutionConfigCommand{
		WorkspaceID: "workspace-dev-01", ActorID: "101", RequestKey: "schema-1", ExpectedVersion: 0,
		Capabilities: []runtimeops.Capability{{ID: "plan.create", Version: 1}},
		Runtimes:     []string{"runtime-1"},
	}); err != nil {
		t.Fatal(err)
	}

	views, err := f.svc.WorkspaceRuntimes(ctx, "workspace-dev-01")
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %+v, %v", views, err)
	}
	view, err := f.svc.WorkspaceRuntime(ctx, "workspace-dev-01", "runtime-1")
	if err != nil {
		t.Fatal(err)
	}
	config, err := f.svc.ExecutionConfig(ctx, "workspace-dev-01")
	if err != nil {
		t.Fatal(err)
	}
	// A real backend:S01 workspace id is base64url and may begin with `-` or
	// `_`; the frozen schema pattern must accept it verbatim.
	const emptyWorkspaceID = "-_Yg3kQw9pLm"
	emptyViews, err := f.svc.WorkspaceRuntimes(ctx, emptyWorkspaceID)
	if err != nil || len(emptyViews) != 0 {
		t.Fatalf("empty views = %+v, %v", emptyViews, err)
	}
	zero, err := f.svc.ExecutionConfig(ctx, emptyWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}

	instances := map[string]any{
		"runtimeList": map[string]any{
			"schemaVersion": runtimeops.SchemaVersion, "kind": "runtimeList", "runtimes": views,
		},
		"runtimeView": map[string]any{
			"schemaVersion": runtimeops.SchemaVersion, "kind": "runtimeView", "runtime": view,
		},
		"emptyRuntimeList": map[string]any{
			"schemaVersion": runtimeops.SchemaVersion, "kind": "runtimeList", "runtimes": emptyViews,
		},
		"executionConfig": map[string]any{
			"schemaVersion": runtimeops.SchemaVersion, "kind": "executionConfig",
			"config": map[string]any{
				"workspaceId": config.WorkspaceID, "version": config.Version,
				"capabilities": config.Capabilities, "runtimes": config.Runtimes,
				"updatedByGitHubUserId": config.UpdatedByGitHubUserID,
				"updatedAt":             config.UpdatedAt,
			},
		},
		"zeroExecutionConfig": map[string]any{
			"schemaVersion": runtimeops.SchemaVersion, "kind": "executionConfig",
			"config": map[string]any{
				"workspaceId": zero.WorkspaceID, "version": zero.Version,
				"capabilities": zero.Capabilities, "runtimes": zero.Runtimes,
			},
		},
	}

	root := runtimeopsRepoRoot(t)
	schema := filepath.Join(root, "api", "runtime-web", "v1", "schema.json")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go binary unavailable for the pinned schema validator: %v", err)
	}
	for name, instance := range instances {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(instance)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), name+".json")
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(goBinary, "run",
				"github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0", "-f", "-c", schema, path)
			command.Dir = root
			command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("live projection violates the frozen schema: %v\n%s\n%s", err, output, encoded)
			}
		})
	}
}

func runtimeopsRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "api", "runtime-web", "v1", "schema.json")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root with the frozen runtime-web schema was not found")
		}
		directory = parent
	}
}
