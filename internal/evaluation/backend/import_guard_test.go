package backend_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// deliveryAuthorityPackages are the packages that own delivery state. The
// evaluation backend port must not be able to reach any of them: that is what
// makes "the backend is not a state source" a property of the code rather than a
// promise in a document.
var deliveryAuthorityPackages = map[string]string{
	"github.com/SnWalker/kowa/internal/evaluation":         "the delivery closure authority lives there",
	"github.com/SnWalker/kowa/internal/execution":          "run and node state live there",
	"github.com/SnWalker/kowa/internal/workitem":           "work item state lives there",
	"github.com/SnWalker/kowa/internal/githubdelivery":     "external delivery facts live there",
	"github.com/SnWalker/kowa/internal/infra/postgres":     "closing commits through the database there",
	"github.com/SnWalker/kowa/internal/interfaces/httpapi": "the delivery surface lives there",
}

// TestTheBackendPortCannotReachDeliveryState parses this package's own sources
// and refuses any import of a delivery authority. An adapter that could import
// the execution or work item service would be able to change a delivery from the
// observation path; this guard is what keeps that impossible.
func TestTheBackendPortCannotReachDeliveryState(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory failed: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s failed: %v", name, err)
		}
		checked++
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("reading an import of %s failed: %v", name, err)
			}
			if reason, forbidden := deliveryAuthorityPackages[path]; forbidden {
				t.Fatalf("%s imports %s: %s. The evaluation backend stays an observation path.", name, path, reason)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source file was checked, so the guard proves nothing")
	}
}
