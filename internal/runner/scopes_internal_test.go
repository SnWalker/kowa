package runner

import (
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/execution"
)

// TestClassifyScopesFailsClosed proves an operation outside the frozen enum is
// refused rather than silently ignored.
func TestClassifyScopesFailsClosed(t *testing.T) {
	scopes := []execution.GitScope{
		{RepositoryID: "1", Role: "project", Operation: "clone", Ref: "refs/heads/main"},
		{RepositoryID: "1", Role: "project", Operation: "delete", Ref: "refs/heads/main"},
	}
	read, write, err := classifyScopes(scopes)
	if err == nil || !strings.Contains(err.Error(), "git_scope_refused") {
		t.Fatalf("unknown operation = %v, want refusal", err)
	}
	if read != nil || write != nil {
		t.Fatalf("classify returned scopes on refusal: %+v / %+v", read, write)
	}

	valid := []execution.GitScope{
		{RepositoryID: "1", Role: "knowledge", Operation: "fetch", Ref: "refs/heads/main"},
		{RepositoryID: "1", Role: "project", Operation: "push", Ref: "refs/heads/task/x"},
	}
	read, write, err = classifyScopes(valid)
	if err != nil || len(read) != 1 || len(write) != 1 ||
		read[0].Operation != "fetch" || write[0].Operation != "push" {
		t.Fatalf("classify = %+v / %+v, %v", read, write, err)
	}
}
