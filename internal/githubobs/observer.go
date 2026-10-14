// Package githubobs owns the control plane's read-only GitHub observation
// seam. It never writes and never assumes: a failed read or an active-identity
// drift stays unknown instead of rendering green. The machine's personal gh
// identity is a separate fact from any Web actor.
package githubobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/SnWalker/kowa/internal/platform/proc"
)

var (
	// ErrUnknown marks an observation the control plane could not establish;
	// callers must not treat it as a positive fact.
	ErrUnknown  = errors.New("githubobs: observation is unknown")
	oidPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
)

// Unknown reasons refine why an observation is not a fact.
const (
	UnknownUnavailable = "unavailable"
	UnknownIdentity    = "identity-drift"
	UnknownMalformed   = "malformed-response"
)

// RepositoryObservation is the observed identity of one repository.
type RepositoryObservation struct {
	RepositoryID  string    `json:"repositoryId"`
	FullName      string    `json:"fullName,omitempty"`
	DefaultBranch string    `json:"defaultBranch,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
	ActorID       string    `json:"actorId,omitempty"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

// RefObservation is the observed head of one branch.
type RefObservation struct {
	RepositoryID  string    `json:"repositoryId"`
	Ref           string    `json:"ref"`
	OID           string    `json:"oid,omitempty"`
	ObservedAt    time.Time `json:"observedAt"`
	ActorID       string    `json:"actorId,omitempty"`
	Unknown       bool      `json:"unknown"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

// Observer is the stable port: repositories, refs and observation watermarks.
// Pull-request, review and merge facts arrive with the delivery stage (S05).
type Observer interface {
	Repository(ctx context.Context, repositoryID string) (RepositoryObservation, error)
	BranchHead(ctx context.Context, repositoryID, ref string) (RefObservation, error)
}

type commandRunner func(ctx context.Context, spec proc.Spec) (proc.Outcome, error)

// GHObserver reads GitHub facts through the machine's authenticated gh CLI.
// Every read first re-verifies the active account's numeric id against the
// expected identity, so account drift cannot silently change what is observed.
type GHObserver struct {
	expectedUserID string
	environment    []string
	run            commandRunner
	now            func() time.Time
}

func NewGHObserver(expectedUserID string, environment []string) *GHObserver {
	return &GHObserver{
		expectedUserID: expectedUserID,
		environment:    environment,
		run:            proc.Run,
		now:            time.Now,
	}
}

type commandResult struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func (o *GHObserver) execute(ctx context.Context, arguments ...string) commandResult {
	directory, err := os.MkdirTemp("", "kowa-ghobs-")
	if err != nil {
		return commandResult{err: err, exitCode: -1}
	}
	defer func() { _ = os.RemoveAll(directory) }()
	processContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	outcome, runErr := o.run(processContext, proc.Spec{
		Command: append([]string{"gh"}, arguments...), Dir: directory,
		Env:        o.environment,
		StdoutPath: filepath.Join(directory, "stdout.log"),
		StderrPath: filepath.Join(directory, "stderr.log"),
		Marker:     fmt.Sprintf("kowa-ghobs-%d", time.Now().UnixNano()),
		Grace:      5 * time.Second,
	})
	stdout, _ := os.ReadFile(filepath.Join(directory, "stdout.log"))
	stderr, _ := os.ReadFile(filepath.Join(directory, "stderr.log"))
	result := commandResult{stdout: string(stdout), stderr: string(stderr), exitCode: outcome.ExitCode, err: runErr}
	if outcome.Canceled || outcome.TimedOut {
		result.err = fmt.Errorf("gh command did not finish: %v", processContext.Err())
	}
	return result
}

// activeIdentity verifies the currently authenticated numeric account id.
func (o *GHObserver) activeIdentity(ctx context.Context) (string, error) {
	result := o.execute(ctx, "api", "user", "--jq", ".id")
	if result.err != nil {
		return "", fmt.Errorf("gh identity probe failed: %w", result.err)
	}
	if result.exitCode != 0 {
		return "", fmt.Errorf("gh identity probe exited %d", result.exitCode)
	}
	identity := strings.TrimSpace(result.stdout)
	if !repoPattern.MatchString(identity) {
		return "", errors.New("gh identity probe returned a non-numeric identity")
	}
	if o.expectedUserID != "" && identity != o.expectedUserID {
		return "", fmt.Errorf("github account drifted: active %s, expected %s", identity, o.expectedUserID)
	}
	return identity, nil
}

// Repository reads one repository identity by its frozen numeric id.
func (o *GHObserver) Repository(ctx context.Context, repositoryID string) (RepositoryObservation, error) {
	observation := RepositoryObservation{RepositoryID: repositoryID, ObservedAt: o.now().UTC()}
	if !repoPattern.MatchString(repositoryID) {
		observation.Unknown, observation.UnknownReason = true, UnknownMalformed
		return observation, fmt.Errorf("%w: invalid repository id", ErrUnknown)
	}
	actor, err := o.activeIdentity(ctx)
	if err != nil {
		observation.Unknown, observation.UnknownReason = true, UnknownIdentity
		if strings.Contains(err.Error(), "drifted") {
			observation.UnknownReason = UnknownIdentity
		} else {
			observation.UnknownReason = UnknownUnavailable
		}
		return observation, fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	observation.ActorID = actor
	result := o.execute(ctx, "api", fmt.Sprintf("repositories/%s", repositoryID),
		"--jq", "{id: (.id|tostring), full_name: .full_name, default_branch: .default_branch}")
	if result.err != nil || result.exitCode != 0 {
		observation.Unknown, observation.UnknownReason = true, UnknownUnavailable
		return observation, fmt.Errorf("%w: repository query failed", ErrUnknown)
	}
	var payload struct {
		ID            string `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.stdout)), &payload); err != nil ||
		payload.ID != repositoryID || payload.FullName == "" {
		observation.Unknown, observation.UnknownReason = true, UnknownMalformed
		return observation, fmt.Errorf("%w: repository response is malformed or mismatched", ErrUnknown)
	}
	observation.FullName = payload.FullName
	observation.DefaultBranch = payload.DefaultBranch
	return observation, nil
}

// BranchHead reads one branch head through the verified repository identity.
func (o *GHObserver) BranchHead(ctx context.Context, repositoryID, ref string) (RefObservation, error) {
	observation := RefObservation{RepositoryID: repositoryID, Ref: ref, ObservedAt: o.now().UTC()}
	repository, err := o.Repository(ctx, repositoryID)
	if err != nil {
		observation.Unknown = true
		observation.UnknownReason = repository.UnknownReason
		return observation, err
	}
	observation.ActorID = repository.ActorID
	result := o.execute(ctx, "api",
		fmt.Sprintf("repos/%s/git/ref/heads/%s", repository.FullName, ref), "--jq", ".object.sha")
	if result.err != nil || result.exitCode != 0 {
		observation.Unknown, observation.UnknownReason = true, UnknownUnavailable
		return observation, fmt.Errorf("%w: ref query failed", ErrUnknown)
	}
	oid := strings.TrimSpace(result.stdout)
	if !oidPattern.MatchString(oid) {
		observation.Unknown, observation.UnknownReason = true, UnknownMalformed
		return observation, fmt.Errorf("%w: ref response is not an oid", ErrUnknown)
	}
	observation.OID = oid
	return observation, nil
}
