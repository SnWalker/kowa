package githubobs_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/githubobs"
	"github.com/SnWalker/kowa/internal/platform/proc"
)

// fakeGH answers gh invocations from a scripted table and writes the outputs
// the observer reads from per-command log files.
func fakeGH(t *testing.T, responses map[string]string) func(context.Context, proc.Spec) (proc.Outcome, error) {
	t.Helper()
	return func(_ context.Context, spec proc.Spec) (proc.Outcome, error) {
		key := strings.Join(spec.Command[1:], " ")
		for prefix, output := range responses {
			if strings.HasPrefix(key, prefix) {
				if err := os.WriteFile(spec.StdoutPath, []byte(output), 0o600); err != nil {
					t.Error(err)
				}
				return proc.Outcome{ExitCode: 0}, nil
			}
		}
		if err := os.WriteFile(spec.StderrPath, []byte("unexpected gh invocation: "+key), 0o600); err != nil {
			t.Error(err)
		}
		return proc.Outcome{ExitCode: 1}, &os.PathError{Op: "exec", Path: "gh", Err: errors.New("exit 1")}
	}
}

func observerForTest(t *testing.T, expectedUserID string, responses map[string]string) *githubobs.GHObserver {
	t.Helper()
	observer := githubobs.NewGHObserver(expectedUserID, os.Environ())
	githubobs.SetRunnerForTest(observer, fakeGH(t, responses))
	return observer
}

func TestRepositoryObservationSuccess(t *testing.T) {
	observer := observerForTest(t, "135722937", map[string]string{
		"api user --jq .id":           "135722937",
		"api repositories/1401511579": `{"id":"1401511579","full_name":"SnWalker/kowa-sandbox","default_branch":"main"}`,
	})
	observation, err := observer.Repository(t.Context(), "1401511579")
	if err != nil || observation.Unknown {
		t.Fatalf("observation = %+v, %v", observation, err)
	}
	if observation.FullName != "SnWalker/kowa-sandbox" || observation.DefaultBranch != "main" ||
		observation.ActorID != "135722937" {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestRepositoryObservationFailsClosed(t *testing.T) {
	t.Run("identity drift stays unknown", func(t *testing.T) {
		observer := observerForTest(t, "135722937", map[string]string{
			"api user --jq .id": "999999999",
		})
		observation, err := observer.Repository(t.Context(), "1401511579")
		if !errors.Is(err, githubobs.ErrUnknown) || !observation.Unknown ||
			observation.UnknownReason != githubobs.UnknownIdentity {
			t.Fatalf("observation = %+v, %v", observation, err)
		}
		if observation.FullName != "" {
			t.Fatalf("unknown observation carries facts: %+v", observation)
		}
	})
	t.Run("query failure stays unknown", func(t *testing.T) {
		observer := observerForTest(t, "135722937", map[string]string{
			"api user --jq .id": "135722937",
		})
		observation, err := observer.Repository(t.Context(), "1401511579")
		if !errors.Is(err, githubobs.ErrUnknown) || observation.UnknownReason != githubobs.UnknownUnavailable {
			t.Fatalf("observation = %+v, %v", observation, err)
		}
	})
	t.Run("mismatched response stays unknown", func(t *testing.T) {
		observer := observerForTest(t, "135722937", map[string]string{
			"api user --jq .id":           "135722937",
			"api repositories/1401511579": `{"id":"1401511579","full_name":"","default_branch":"main"}`,
		})
		observation, err := observer.Repository(t.Context(), "1401511579")
		if !errors.Is(err, githubobs.ErrUnknown) || observation.UnknownReason != githubobs.UnknownMalformed {
			t.Fatalf("observation = %+v, %v", observation, err)
		}
	})
	t.Run("invalid id is rejected before any read", func(t *testing.T) {
		observer := observerForTest(t, "135722937", nil)
		if _, err := observer.Repository(t.Context(), "not-an-id"); !errors.Is(err, githubobs.ErrUnknown) {
			t.Fatalf("invalid id = %v", err)
		}
	})
}

func TestBranchHeadObservation(t *testing.T) {
	observer := observerForTest(t, "135722937", map[string]string{
		"api user --jq .id":           "135722937",
		"api repositories/1401511579": `{"id":"1401511579","full_name":"SnWalker/kowa-sandbox","default_branch":"main"}`,
		"api repos/SnWalker/kowa-sandbox/git/ref/heads/main --jq .object.sha": "b99754c271e7c8b759e9affa7d9fc875625f6d8c",
	})
	observation, err := observer.BranchHead(t.Context(), "1401511579", "main")
	if err != nil || observation.Unknown || observation.OID != "b99754c271e7c8b759e9affa7d9fc875625f6d8c" {
		t.Fatalf("observation = %+v, %v", observation, err)
	}
	// A malformed ref response is never coerced into an OID.
	broken := observerForTest(t, "135722937", map[string]string{
		"api user --jq .id":           "135722937",
		"api repositories/1401511579": `{"id":"1401511579","full_name":"SnWalker/kowa-sandbox","default_branch":"main"}`,
		"api repos/SnWalker/kowa-sandbox/git/ref/heads/main --jq .object.sha": "not-an-oid",
	})
	observation, err = broken.BranchHead(t.Context(), "1401511579", "main")
	if !errors.Is(err, githubobs.ErrUnknown) || observation.OID != "" || observation.UnknownReason != githubobs.UnknownMalformed {
		t.Fatalf("malformed = %+v, %v", observation, err)
	}
}
