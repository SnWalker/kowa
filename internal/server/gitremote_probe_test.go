//go:build gitremoteprobe

package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/githubobs"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/platform/proc"
)

// TestGitRemoteRealProbe performs the standalone real, read-only observation of
// the frozen git remote projection. It never writes to GitHub: it verifies the
// active gh identity, reads the repository identity and GitHub's permission
// fact, and runs `git ls-remote` over the machine's own transports. The output
// is the environment evidence of the projection; it is opt-in because it needs
// the machine's GitHub credentials.
func TestGitRemoteRealProbe(t *testing.T) {
	expectedUserID := os.Getenv("KOWA_GITHUB_MACHINE_USER_ID")
	if expectedUserID == "" {
		t.Fatal("KOWA_GITHUB_MACHINE_USER_ID is required")
	}
	projectRepository := os.Getenv("KOWA_PROBE_PROJECT_REPOSITORY_ID")
	knowledgeRepository := os.Getenv("KOWA_PROBE_KNOWLEDGE_REPOSITORY_ID")
	if projectRepository == "" || knowledgeRepository == "" {
		t.Fatal("KOWA_PROBE_PROJECT_REPOSITORY_ID and KOWA_PROBE_KNOWLEDGE_REPOSITORY_ID are required")
	}
	observer := githubobs.NewGHObserver(expectedUserID, os.Environ())
	service := gitremote.NewService(
		gitRemoteFacts{observer}, nil,
		func(ctx context.Context, spec gitremote.CommandSpec) (gitremote.CommandOutcome, error) {
			outcome, err := proc.Run(ctx, proc.Spec{
				Command: spec.Command, Dir: spec.Dir, Env: spec.Env,
				StdoutPath: spec.StdoutPath, StderrPath: spec.StderrPath,
				Marker: spec.Marker, Grace: spec.Grace,
			})
			return gitremote.CommandOutcome{
				ExitCode: outcome.ExitCode, Canceled: outcome.Canceled, TimedOut: outcome.TimedOut,
			}, err
		},
		os.Environ(), time.Now,
	)
	status, err := service.Status(context.Background(), gitremote.StatusCommand{
		WorkspaceID: "remote-probe", RequestedByGitHubUserID: "101",
		Repositories: []gitremote.RepositoryRef{
			{RepositoryID: projectRepository, Role: gitremote.RoleProject},
			{RepositoryID: knowledgeRepository, Role: gitremote.RoleKnowledge},
		},
	})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encode status: %v", err)
	}
	t.Logf("observed status: %s", encoded)
	if status.ProbeOutcome == "completed" && status.MachineIdentity.ObservedGitHubUserID != expectedUserID {
		t.Fatalf("observed identity %q is not the configured machine identity",
			status.MachineIdentity.ObservedGitHubUserID)
	}
	for _, repository := range status.Repositories {
		if repository.WriteObservation != gitremote.WriteObservationNone {
			t.Fatalf("a write observation appeared in a read-only probe: %#v", repository)
		}
	}
}
