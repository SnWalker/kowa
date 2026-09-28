package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:  "empty directory",
			files: map[string]string{},
		},
		{
			name: "paired migration",
			files: map[string]string{
				"000001_bootstrap.up.sql":   "SELECT 1;\n",
				"000001_bootstrap.down.sql": "SELECT 1;\n",
			},
		},
		{
			name: "invalid filename",
			files: map[string]string{
				"bootstrap.sql": "SELECT 1;\n",
			},
			wantErr: "invalid migration filename",
		},
		{
			name: "missing direction pair",
			files: map[string]string{
				"000001_bootstrap.up.sql": "SELECT 1;\n",
			},
			wantErr: "missing down migration",
		},
		{
			name: "empty migration",
			files: map[string]string{
				"000001_bootstrap.up.sql":   "\n",
				"000001_bootstrap.down.sql": "SELECT 1;\n",
			},
			wantErr: "is empty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for name, contents := range test.files {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
					t.Fatalf("write migration fixture: %v", err)
				}
			}

			err := Check(dir)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Check() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Check() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestCheck_MissingDirectory(t *testing.T) {
	t.Parallel()

	err := Check(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "read migrations") {
		t.Fatalf("Check() error = %v, want read migrations error", err)
	}
}
