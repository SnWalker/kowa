package gitremote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// probeTimeout bounds one real remote probe so a status request stays bounded.
const probeTimeout = 15 * time.Second

// Service reads the git remote projection. Every request performs a bounded,
// read-only probe: `git ls-remote` over the machine's own credentials followed
// by GitHub's permission fact for the identity that answered.
type Service struct {
	facts       RepositoryFacts
	identities  MachineIdentitySource
	runner      CommandRunner
	now         func() time.Time
	environment []string
}

// CommandRunner runs one bounded external command; the platform process
// supervisor owns the implementation.
type CommandRunner func(ctx context.Context, spec CommandSpec) (CommandOutcome, error)

// CommandSpec is the minimal process request this package needs.
type CommandSpec struct {
	Command    []string
	Dir        string
	Env        []string
	StdoutPath string
	StderrPath string
	Marker     string
	Grace      time.Duration
}

// CommandOutcome is the minimal process result this package needs.
type CommandOutcome struct {
	ExitCode int
	Canceled bool
	TimedOut bool
}

func NewService(
	facts RepositoryFacts,
	identities MachineIdentitySource,
	runner CommandRunner,
	environment []string,
	now func() time.Time,
) *Service {
	return &Service{facts: facts, identities: identities, runner: runner, environment: environment, now: now}
}

// Status probes every repository binding of the workspace and reports the
// observed facts. The GitHub query identity is verified before any repository
// fact is reported; drifted or unreadable facts stay unknown.
func (s *Service) Status(ctx context.Context, command StatusCommand) (Status, error) {
	if command.WorkspaceID == "" || !externalIDPattern.MatchString(command.RequestedByGitHubUserID) {
		return Status{}, ErrValidation
	}
	status := Status{
		SchemaVersion: SchemaVersion, Kind: GitRemoteStatusKind,
		WorkspaceID: command.WorkspaceID, RequestedByGitHubUserID: command.RequestedByGitHubUserID,
		ObservedAt: s.now().UTC(), ProbeOutcome: "completed",
		Repositories: []RepositoryStatus{},
		MachineIdentity: MachineIdentity{
			RuntimeIDs: []string{}, RegisteredGitHubUserIDs: []string{}, State: IdentityUnknown,
			Reason: "no registered machine identity is available for this workspace",
		},
	}
	if s.identities != nil {
		runtimeIDs, registered, err := s.identities.MachineIdentities(ctx, command.WorkspaceID)
		if err != nil {
			return Status{}, err
		}
		if runtimeIDs == nil {
			runtimeIDs = []string{}
		}
		if registered == nil {
			registered = []string{}
		}
		status.MachineIdentity = MachineIdentity{
			RuntimeIDs: runtimeIDs, RegisteredGitHubUserIDs: registered, State: IdentityUnknown,
			Reason: "the machine identity has not been observed yet",
		}
	}
	for _, repository := range command.Repositories {
		if !externalIDPattern.MatchString(repository.RepositoryID) ||
			(repository.Role != RoleProject && repository.Role != RoleKnowledge) {
			return Status{}, ErrValidation
		}
		status.Repositories = append(status.Repositories, s.probeRepository(ctx, repository, &status))
	}
	if status.MachineIdentity.State == IdentityUnknown && status.MachineIdentity.ObservedGitHubUserID == "" {
		status.ProbeOutcome = "unknown"
		status.ProbeReason = "the GitHub query identity could not be verified"
		status.MachineIdentity.Reason = status.ProbeReason
	}
	if len(status.MachineIdentity.RegisteredGitHubUserIDs) == 0 && status.MachineIdentity.ObservedGitHubUserID != "" {
		status.MachineIdentity.State = IdentityUnknown
		status.MachineIdentity.Reason = "no registered machine identity is available to compare with the query identity"
	}
	return status, nil
}

// probeRepository performs the two real observations of one repository: the
// remote read over Git itself, and GitHub's permission fact.
func (s *Service) probeRepository(
	ctx context.Context,
	repository RepositoryRef,
	status *Status,
) RepositoryStatus {
	observedAt := s.now().UTC()
	result := RepositoryStatus{
		RepositoryID: repository.RepositoryID, Role: repository.Role,
		RemoteRead: RemoteRead{State: ReadUnknown}, PushPermission: PushPermission{State: PermissionUnknown},
		WriteObservation: WriteObservationNone, ObservedAt: observedAt,
	}
	fullName, actorID, err := s.facts.RepositoryFullName(ctx, repository.RepositoryID)
	if err != nil || fullName == "" {
		reason := "the repository identity could not be established"
		if err != nil {
			reason = err.Error()
		}
		result.RemoteRead.Reason = reason
		result.PushPermission.Reason = reason
		return result
	}
	result.ObservedByGitHubUserID = actorID
	status.MachineIdentity.ObservedGitHubUserID = actorID
	status.MachineIdentity.State = matchIdentity(actorID, status.MachineIdentity.RegisteredGitHubUserIDs)
	if status.MachineIdentity.State == IdentityDrifted {
		status.MachineIdentity.Reason = "the GitHub query identity is not a registered machine identity"
	}

	transport, readErr := s.probeRead(ctx, fullName)
	if readErr != nil {
		result.RemoteRead.State = ReadUnreadable
		result.RemoteRead.Reason = readErr.Error()
	} else {
		result.RemoteRead.State = ReadReadable
		result.RemoteRead.Transport = transport
	}

	granted, permissionActor, err := s.facts.PushPermission(ctx, repository.RepositoryID)
	switch {
	case err != nil:
		result.PushPermission.State = PermissionUnknown
		result.PushPermission.Reason = err.Error()
	case permissionActor != actorID:
		result.PushPermission.State = PermissionUnknown
		result.PushPermission.Reason = "the permission query was answered by another identity"
	case granted:
		result.PushPermission.State = PermissionGranted
	default:
		result.PushPermission.State = PermissionDenied
	}
	return result
}

// probeRead tries the canonical HTTPS remote first and the SSH remote second,
// and reports which transport actually answered. `gh` being logged in never
// substitutes for this observation.
func (s *Service) probeRead(ctx context.Context, fullName string) (string, error) {
	failures := make([]string, 0, 2)
	for _, candidate := range []struct {
		name string
		url  string
		env  []string
	}{
		{name: "https", url: "https://github.com/" + fullName + ".git"},
		{name: "ssh", url: "git@github.com:" + fullName + ".git",
			env: []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=5"}},
	} {
		if err := s.lsRemote(ctx, candidate.url, candidate.env); err == nil {
			return candidate.name, nil
		} else {
			failures = append(failures, candidate.name+": "+err.Error())
		}
	}
	return "", fmt.Errorf("git ls-remote failed for both transports (%s)", strings.Join(failures, "; "))
}

func (s *Service) lsRemote(ctx context.Context, url string, extraEnv []string) error {
	if s.runner == nil {
		return fmt.Errorf("no command runner is configured")
	}
	directory, err := os.MkdirTemp("", "kowa-gitremote-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	processContext, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	environment := append([]string{}, s.environment...)
	environment = append(environment, extraEnv...)
	environment = append(environment, "GIT_TERMINAL_PROMPT=0")
	outcome, runErr := s.runner(processContext, CommandSpec{
		Command: []string{"git", "ls-remote", "--heads", "--tags", url},
		Dir:     directory, Env: environment,
		StdoutPath: filepath.Join(directory, "stdout.log"),
		StderrPath: filepath.Join(directory, "stderr.log"),
		Marker:     fmt.Sprintf("kowa-gitremote-%d", time.Now().UnixNano()),
		Grace:      5 * time.Second,
	})
	if runErr != nil {
		return runErr
	}
	if outcome.Canceled || outcome.TimedOut {
		return fmt.Errorf("git ls-remote did not finish within %s", probeTimeout)
	}
	if outcome.ExitCode != 0 {
		stderr, _ := os.ReadFile(filepath.Join(directory, "stderr.log"))
		detail := strings.TrimSpace(string(stderr))
		if len(detail) > 300 {
			detail = detail[:300]
		}
		detail = redact(detail)
		if detail == "" {
			detail = "git exited non-zero"
		}
		return fmt.Errorf("%s", detail)
	}
	return nil
}

// redact removes anything that looks like an embedded credential from a
// reported command error: the projection is shown to Web consumers.
func redact(message string) string {
	for _, marker := range []string{"https://", "http://"} {
		for {
			index := strings.Index(message, marker)
			if index < 0 {
				break
			}
			end := strings.IndexAny(message[index:], " \n\t")
			if end < 0 {
				end = len(message) - index
			}
			url := message[index : index+end]
			if strings.Contains(url, "@") {
				scheme := marker
				at := strings.Index(url, "@")
				message = message[:index] + scheme + "***@" + url[at+1:] + message[index+end:]
				continue
			}
			break
		}
	}
	return message
}

// matchIdentity compares the query identity with the registered machine
// identities without ever upgrading an absent fact to a match.
func matchIdentity(observed string, registered []string) string {
	if observed == "" {
		return IdentityUnknown
	}
	if len(registered) == 0 {
		return IdentityUnknown
	}
	for _, candidate := range registered {
		if candidate == observed {
			return IdentityMatched
		}
	}
	return IdentityDrifted
}
