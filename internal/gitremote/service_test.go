package gitremote_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SnWalker/kowa/internal/gitremote"
)

type fakeFacts struct {
	fullName        string
	actorID         string
	identityErr     error
	push            bool
	permissionErr   error
	permissionActor string
}

func (f fakeFacts) RepositoryFullName(context.Context, string) (string, string, error) {
	return f.fullName, f.actorID, f.identityErr
}

func (f fakeFacts) PushPermission(context.Context, string) (bool, string, error) {
	return f.push, f.permissionActor, f.permissionErr
}

type fakeIdentities struct {
	runtimeIDs []string
	userIDs    []string
}

func (f fakeIdentities) MachineIdentities(context.Context, string) ([]string, []string, error) {
	return f.runtimeIDs, f.userIDs, nil
}

// scriptedRunner answers git ls-remote attempts per transport.
type scriptedRunner struct {
	httpsFails bool
	sshFails   bool
	calls      []string
}

func (s *scriptedRunner) run(_ context.Context, spec gitremote.CommandSpec) (gitremote.CommandOutcome, error) {
	target := spec.Command[len(spec.Command)-1]
	s.calls = append(s.calls, target)
	switch {
	case strings.HasPrefix(target, "https://"):
		if s.httpsFails {
			return gitremote.CommandOutcome{ExitCode: 128}, nil
		}
	case strings.HasPrefix(target, "git@github.com:"):
		if s.sshFails {
			return gitremote.CommandOutcome{ExitCode: 128}, nil
		}
	}
	return gitremote.CommandOutcome{ExitCode: 0}, nil
}

func newService(facts gitremote.RepositoryFacts, identities gitremote.MachineIdentitySource, runner gitremote.CommandRunner) *gitremote.Service {
	return gitremote.NewService(facts, identities, runner,
		[]string{"PATH=/usr/bin", "HOME=/tmp"},
		func() time.Time { return time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC) })
}

func statusCommand() gitremote.StatusCommand {
	return gitremote.StatusCommand{
		WorkspaceID: "ws_1", RequestedByGitHubUserID: "101",
		Repositories: []gitremote.RepositoryRef{
			{RepositoryID: "1401511579", Role: gitremote.RoleProject},
			{RepositoryID: "1413481670", Role: gitremote.RoleKnowledge},
		},
	}
}

func TestStatusReportsOnlyRealObservations(t *testing.T) {
	runner := &scriptedRunner{}
	service := newService(
		fakeFacts{fullName: "SnWalker/kowa-sandbox", actorID: "135722937", push: true, permissionActor: "135722937"},
		fakeIdentities{runtimeIDs: []string{"rt_1"}, userIDs: []string{"135722937"}},
		runner.run,
	)
	status, err := service.Status(context.Background(), statusCommand())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.ProbeOutcome != "completed" {
		t.Fatalf("Status() outcome = %q", status.ProbeOutcome)
	}
	if status.MachineIdentity.State != gitremote.IdentityMatched ||
		status.MachineIdentity.ObservedGitHubUserID != "135722937" ||
		len(status.MachineIdentity.RegisteredGitHubUserIDs) != 1 {
		t.Fatalf("machine identity = %#v", status.MachineIdentity)
	}
	if len(status.Repositories) != 2 {
		t.Fatalf("repositories = %#v", status.Repositories)
	}
	for _, repository := range status.Repositories {
		if repository.RemoteRead.State != gitremote.ReadReadable || repository.RemoteRead.Transport != "https" {
			t.Fatalf("remote read = %#v", repository.RemoteRead)
		}
		if repository.PushPermission.State != gitremote.PermissionGranted {
			t.Fatalf("push permission = %#v", repository.PushPermission)
		}
		if repository.WriteObservation != gitremote.WriteObservationNone {
			t.Fatalf("write observation = %q, want none", repository.WriteObservation)
		}
	}
	if status.RequestedByGitHubUserID != "101" {
		t.Fatalf("web actor = %q", status.RequestedByGitHubUserID)
	}
}

func TestPermissionAndReadAreNeverInferredFromEachOther(t *testing.T) {
	// GitHub reports push permission but the real remote read fails: the two
	// facts stay separate.
	service := newService(
		fakeFacts{fullName: "SnWalker/kowa-sandbox", actorID: "135722937", push: true, permissionActor: "135722937"},
		fakeIdentities{userIDs: []string{"135722937"}},
		(&scriptedRunner{httpsFails: true, sshFails: true}).run,
	)
	status, err := service.Status(context.Background(), statusCommand())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	repository := status.Repositories[0]
	if repository.RemoteRead.State != gitremote.ReadUnreadable || repository.RemoteRead.Reason == "" {
		t.Fatalf("remote read = %#v", repository.RemoteRead)
	}
	if repository.PushPermission.State != gitremote.PermissionGranted {
		t.Fatalf("push permission = %#v", repository.PushPermission)
	}
	// A denied permission with a readable remote is also reported as-is.
	denied := newService(
		fakeFacts{fullName: "SnWalker/kowa-sandbox", actorID: "135722937", permissionActor: "135722937"},
		fakeIdentities{userIDs: []string{"135722937"}},
		(&scriptedRunner{}).run,
	)
	status, err = denied.Status(context.Background(), statusCommand())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Repositories[0].PushPermission.State != gitremote.PermissionDenied {
		t.Fatalf("push permission = %#v", status.Repositories[0].PushPermission)
	}
	if status.Repositories[0].RemoteRead.State != gitremote.ReadReadable {
		t.Fatalf("remote read = %#v", status.Repositories[0].RemoteRead)
	}
}

func TestUnknownIdentityNeverReadsAsAvailable(t *testing.T) {
	service := newService(
		fakeFacts{fullName: "SnWalker/kowa-sandbox", actorID: "999", push: true, permissionActor: "999"},
		fakeIdentities{runtimeIDs: []string{"rt_1"}, userIDs: []string{"135722937"}},
		(&scriptedRunner{}).run,
	)
	status, err := service.Status(context.Background(), statusCommand())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.MachineIdentity.State != gitremote.IdentityDrifted {
		t.Fatalf("machine identity = %#v, want drifted", status.MachineIdentity)
	}
}

func TestUnreachableIdentityLeavesFactsUnknown(t *testing.T) {
	service := newService(
		fakeFacts{identityErr: errors.New("gh identity probe failed")},
		fakeIdentities{runtimeIDs: []string{"rt_1"}, userIDs: []string{"135722937"}},
		(&scriptedRunner{}).run,
	)
	status, err := service.Status(context.Background(), statusCommand())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.ProbeOutcome != "unknown" || status.ProbeReason == "" {
		t.Fatalf("Status() outcome = %q, %q", status.ProbeOutcome, status.ProbeReason)
	}
	for _, repository := range status.Repositories {
		if repository.RemoteRead.State != gitremote.ReadUnknown ||
			repository.PushPermission.State != gitremote.PermissionUnknown {
			t.Fatalf("repository = %#v", repository)
		}
	}
}

func TestSSHFallbackIsReportedAsTheTransportThatAnswered(t *testing.T) {
	runner := &scriptedRunner{httpsFails: true}
	service := newService(
		fakeFacts{fullName: "SnWalker/kowa-sandbox", actorID: "135722937", push: true, permissionActor: "135722937"},
		fakeIdentities{userIDs: []string{"135722937"}},
		runner.run,
	)
	status, err := service.Status(context.Background(), statusCommand())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Repositories[0].RemoteRead.Transport != "ssh" {
		t.Fatalf("transport = %q", status.Repositories[0].RemoteRead.Transport)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("probe calls = %#v, want two per repository", runner.calls)
	}
}

func TestStatusRejectsUnknownRolesAndActors(t *testing.T) {
	service := newService(fakeFacts{}, fakeIdentities{}, (&scriptedRunner{}).run)
	command := statusCommand()
	command.Repositories[0].Role = "admin"
	if _, err := service.Status(context.Background(), command); !errors.Is(err, gitremote.ErrValidation) {
		t.Fatalf("Status() error = %v, want ErrValidation", err)
	}
	command = statusCommand()
	command.RequestedByGitHubUserID = ""
	if _, err := service.Status(context.Background(), command); !errors.Is(err, gitremote.ErrValidation) {
		t.Fatalf("Status() without a web actor = %v, want ErrValidation", err)
	}
}
