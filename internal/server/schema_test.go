package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Use the same pinned complete JSON Schema validator as the repository contract
// gate, on real serialized producer output (including external identity refs).
func responseSchemaError(t *testing.T, family string, data []byte) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "actual-response.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	schema, err := filepath.Abs(filepath.Join("../../api", family, "v1/schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	apiRoot, err := filepath.Abs("../../api")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go",
		"run",
		"github.com/santhosh-tekuri/jsonschema/cmd/jv@v0.7.0",
		"-q",
		"-f",
		"-c",
		"-m",
		"https://kowa.dev/contracts/="+apiRoot,
		schema,
		path)
	command.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	return command.Run()
}
func assertResponseSchema(t *testing.T, family, definition string, data []byte) {
	t.Helper()
	if err := responseSchemaError(t, family, data); err != nil {
		t.Fatalf("%s producer violates frozen schema: %v; %s", definition, err, data)
	}
}

func TestResponseSchema_RejectsOldIdentityAndExtraCredentials(t *testing.T) {
	for _, tc := range []struct {
		name,
		body string
	}{{"old fields",
		`{"identity":{"GitHubUserID":"101","Login":"member-a"},"csrfToken":"csrf","returnTo":"/workspaces"}`},
		{"extra token",
			`{"identity":{"githubUserId":"101","login":"member-a","accessToken":"secret"},"csrfToken":"csrf",
		"returnTo":"/workspaces"}`},
		{"missing id",
			`{"identity":{"login":"member-a"},"csrfToken":"csrf","returnTo":"/workspaces"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := responseSchemaError(t, "identity-workspace", []byte(tc.body)); err == nil {
				t.Fatal("schema accepted invalid producer wire")
			}
		})
	}
	assertResponseSchema(t,
		"identity-workspace",
		"LoginCallbackResponse",
		[]byte(`{"identity":{"githubUserId":"101","login":"member-a"},"csrfToken":"csrf","returnTo":"/workspaces"}`))
}
