package githubobs_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/githubobs"
)

// mergedPullRequestPayload is the shape `gh api repos/{owner}/{repo}/pulls/{n}`
// returns for a merged pull request.
const mergedPullRequestPayload = `{"number":42,"user":{"id":135722937,"login":"machine"},"head":{"sha":"de62a4324cad57908376f8c5f3e14bd3bd3dab95","ref":"task/s05"},"base":{"ref":"main"},"state":"closed","merged_at":"2026-10-14T13:00:00Z","merge_commit_sha":"ca6c6ec8446ec687213d58064a7fd8363050321a","merged_by":{"id":277460407,"login":"reviewer-a","type":"User"}}`

// TestTheProductionFilterDecodesIntoAMergedFact runs the real jq filter the
// observer uses over a realistic gh payload and feeds its output through the
// real decoder. It is the check that would have caught a filter that omits a
// field the decoder requires.
func TestTheProductionFilterDecodesIntoAMergedFact(t *testing.T) {
	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skipf("jq is unavailable for the production filter check: %v", err)
	}
	filter, err := githubobs.PullRequestFilter()
	if err != nil {
		t.Fatalf("PullRequestFilter() error = %v", err)
	}
	command := exec.Command(jq, "-c", filter)
	command.Stdin = strings.NewReader(mergedPullRequestPayload)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("jq filter failed: %v (%s)", err, output)
	}
	observation, err := githubobs.DecodePullRequest("1401511579", "42", "135722937", strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("the production filter output does not decode: %v (%s)", err, output)
	}
	if observation.MergedByID != "277460407" || observation.MergedByType != "User" ||
		observation.MergeCommitOID != "ca6c6ec8446ec687213d58064a7fd8363050321a" {
		t.Fatalf("decoded merged fact = %#v", observation)
	}
	var raw map[string]any
	if err := json.Unmarshal(output, &raw); err != nil {
		t.Fatalf("filter output is not JSON: %v", err)
	}
	// The filter must emit every field the decoder requires for a merged PR.
	for _, field := range []string{"number", "author", "head", "state", "merged", "merge_commit", "merged_by", "merged_by_type"} {
		if _, found := raw[field]; !found {
			t.Fatalf("the production filter omits %q: %s", field, output)
		}
	}
}
