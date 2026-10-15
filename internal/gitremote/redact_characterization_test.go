package gitremote_test

import (
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/gitremote"
)

// TestCredentialScrubberOutputIsFrozen characterizes the S05 scrubber before
// backend:S07 promotes it to the shared collection-time filter. The table below
// records the current observable output byte for byte, including the cases where
// the scrubber masks a URL that carries no userinfo password. S07 must keep every
// one of these results identical; the shared filter may only mask strictly more.
func TestCredentialScrubberOutputIsFrozen(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "userinfo password is masked and the host survives",
			input: "fatal: could not read from https://user:ghp_secret@github.com/org/repo.git",
			want:  "fatal: could not read from https://***@github.com/org/repo.git",
		},
		{
			name:  "userinfo without a password is masked too",
			input: "https://tok@github.com/x",
			want:  "https://***@github.com/x",
		},
		{
			name:  "every authority in the text is masked",
			input: "see http://u:p@h/a then https://u2:p2@h2/b",
			want:  "see http://***@h/a then https://***@h2/b",
		},
		{
			name:  "a URL without an authority marker is left alone",
			input: "https://github.com/org/repo",
			want:  "https://github.com/org/repo",
		},
		{
			name:  "text without a URL is returned unchanged",
			input: "no url here at all",
			want:  "no url here at all",
		},
		{
			name:  "an at-sign outside the authority still masks the authority",
			input: "https://host/path@x",
			want:  "https://***@x",
		},
		{
			name:  "a masked URL is never matched a second time",
			input: "https://https://a:b@c@d",
			want:  "https://***@c@d",
		},
		{
			name:  "the text before and after the authority is preserved",
			input: "clone https://me:secret@example.com/x.git done",
			want:  "clone https://***@example.com/x.git done",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := gitremote.Redact(testCase.input); got != testCase.want {
				t.Fatalf("Redact(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// TestCredentialScrubberLeavesOrdinaryEvidenceAlone pins what the scrubber must
// *not* touch: the projection keeps digests and commit identities readable,
// because consumers bind on them.
func TestCredentialScrubberLeavesOrdinaryEvidenceAlone(t *testing.T) {
	untouched := []string{
		"sha256:8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b",
		"refs/heads/task/kowa-run_0123456789abcdef",
		"8f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b5f2f0f6b",
	}
	for _, value := range untouched {
		if got := gitremote.Redact(value); got != value {
			t.Fatalf("Redact(%q) = %q, want it unchanged", value, got)
		}
	}
}

// TestCredentialScrubberNeverHangs guards the single-pass property that keeps a
// replaced URL from being matched again. One whitespace-delimited token is one
// URL: the scan rewrites its authority and continues past it.
func TestCredentialScrubberNeverHangs(t *testing.T) {
	input := strings.Repeat("https://a:b@c ", 64)
	done := make(chan string, 1)
	go func() { done <- gitremote.Redact(input) }()
	select {
	case redacted := <-done:
		if strings.Contains(redacted, "a:b@") {
			t.Fatalf("credential survived redaction: %q", redacted)
		}
		if got := strings.Count(redacted, "https://***@c"); got != 64 {
			t.Fatalf("masked %d authorities, want 64: %q", got, redacted)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Redact() did not terminate")
	}
}
