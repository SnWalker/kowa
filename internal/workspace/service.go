// Package workspace owns Kowa membership authorization and repository bindings.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	ErrValidation          = errors.New("workspace validation failed")
	ErrUnauthorized        = errors.New("workspace authentication required")
	ErrForbidden           = errors.New("workspace access forbidden")
	ErrVersionConflict     = errors.New("workspace version conflict")
	ErrResourceUnavailable = errors.New("repository unavailable")
	ErrPolicyBlocked       = errors.New("workspace policy blocked")

	externalIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// Role is a Kowa Workspace role, independent of GitHub installation access.
type Role string

const (
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleViewer    Role = "viewer"
)

// RepositoryRole describes repository purpose within a Workspace.
type RepositoryRole string

const (
	RepositoryRoleProject   RepositoryRole = "project"
	RepositoryRoleKnowledge RepositoryRole = "knowledge"
)

// RepositoryAccess is the Kowa usage contract, not a claim about GitHub hard isolation.
type RepositoryAccess string

const (
	AccessReadOnly  RepositoryAccess = "read"
	AccessReadWrite RepositoryAccess = "read_write"
)

// Actor comes from the authenticated server-side session.
type Actor struct {
	GitHubUserID string
}

// Member is a Workspace membership and revocation record.
type Member struct {
	GitHubUserID string     `json:"githubUserId"`
	Role         Role       `json:"role"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
}

// RepositoryBinding binds a stable GitHub repository ID to one purpose.
type RepositoryBinding struct {
	GitHubRepositoryID string           `json:"githubRepositoryId"`
	InstallationID     string           `json:"installationId"`
	DisplayName        string           `json:"displayName"`
	Role               RepositoryRole   `json:"role"`
	Access             RepositoryAccess `json:"access"`
}

// Workspace is the current versioned aggregate returned to authorized consumers.
type Workspace struct {
	ID        string             `json:"workspaceId"`
	Name      string             `json:"name"`
	Version   int64              `json:"version"`
	Members   []Member           `json:"members"`
	Project   *RepositoryBinding `json:"project,omitempty"`
	Knowledge *RepositoryBinding `json:"knowledge,omitempty"`
}

// Allowlist identifies GitHub users permitted to bootstrap a first admin role.
type Allowlist map[string]struct{}

// IDGenerator creates opaque Workspace IDs.
type IDGenerator interface {
	New() (string, error)
}

// RepositoryVisibility verifies installation visibility without accepting clone URLs.
type RepositoryVisibility interface {
	RepositoryVisible(ctx context.Context, installationID, repositoryID string) error
}

// Store owns atomic version, idempotency, authorization, mutation, and audit writes.
type Store interface {
	Bootstrap(context.Context, BootstrapMutation) (Workspace, error)
	Get(context.Context, string) (Workspace, error)
	ConfigureRepositories(context.Context, ConfigureRepositoriesMutation) (Workspace, error)
	AddRepositoryBinding(context.Context, AddRepositoryBindingMutation) (Workspace, error)
	UpsertMember(context.Context, UpsertMemberMutation) (Workspace, error)
	RevokeMember(context.Context, RevokeMemberMutation) (Workspace, error)
}

// Service implements Workspace authorization and configuration rules.
type Service struct {
	store      Store
	visibility RepositoryVisibility
	allowlist  Allowlist
	ids        IDGenerator
}

func NewService(store Store, visibility RepositoryVisibility, allowlist Allowlist, ids IDGenerator) *Service {
	return &Service{store: store, visibility: visibility, allowlist: allowlist, ids: ids}
}

// BootstrapCommand creates a Workspace with the authenticated caller as admin.
type BootstrapCommand struct {
	Name       string
	RequestKey string
}

// BootstrapMutation is the validated atomic store command.
type BootstrapMutation struct {
	Workspace     Workspace
	Actor         Actor
	RequestKey    string
	RequestDigest string
}

// Bootstrap creates the first membership only for a deployment allowlisted identity.
func (s *Service) Bootstrap(ctx context.Context, actor Actor, command BootstrapCommand) (Workspace, error) {
	if !validActor(actor) {
		return Workspace{}, ErrUnauthorized
	}
	if _, allowed := s.allowlist[actor.GitHubUserID]; !allowed {
		return Workspace{}, ErrForbidden
	}
	name := strings.TrimSpace(command.Name)
	if name == "" || command.RequestKey == "" {
		return Workspace{}, ErrValidation
	}
	id, err := s.ids.New()
	if err != nil {
		return Workspace{}, fmt.Errorf("generate workspace id: %w", err)
	}
	workspace := Workspace{
		ID:      id,
		Name:    name,
		Version: 1,
		Members: []Member{{GitHubUserID: actor.GitHubUserID, Role: RoleAdmin}},
	}
	return s.store.Bootstrap(ctx, BootstrapMutation{
		Workspace:     workspace,
		Actor:         actor,
		RequestKey:    command.RequestKey,
		RequestDigest: commandDigest("bootstrap", name),
	})
}

// Get returns a Workspace only to a current member.
func (s *Service) Get(ctx context.Context, actor Actor, workspaceID string) (Workspace, error) {
	if !validActor(actor) {
		return Workspace{}, ErrUnauthorized
	}
	workspace, err := s.store.Get(ctx, workspaceID)
	if err != nil {
		return Workspace{}, ErrForbidden
	}
	if _, ok := activeMember(workspace, actor.GitHubUserID); !ok {
		return Workspace{}, ErrForbidden
	}
	return workspace, nil
}

// RepositoryInput is stable GitHub identity and display metadata from a trusted adapter.
type RepositoryInput struct {
	GitHubRepositoryID string
	InstallationID     string
	DisplayName        string
}

// ConfigureRepositoriesCommand atomically sets the MVP project and knowledge bindings.
type ConfigureRepositoriesCommand struct {
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	Project         RepositoryInput
	Knowledge       RepositoryInput
}

// ConfigureRepositoriesMutation is the atomic store command.
type ConfigureRepositoriesMutation struct {
	Actor           Actor
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	RequestDigest   string
	Project         RepositoryBinding
	Knowledge       RepositoryBinding
}

// ConfigureRepositories binds exactly one writable project and one read-only knowledge repository.
func (s *Service) ConfigureRepositories(
	ctx context.Context,
	actor Actor,
	command ConfigureRepositoriesCommand,
) (Workspace, error) {
	workspace, err := s.requireAdmin(ctx, actor, command.WorkspaceID)
	if err != nil {
		return Workspace{}, err
	}
	if workspace.Version != command.ExpectedVersion {
		return Workspace{}, ErrVersionConflict
	}
	if command.RequestKey == "" || command.Project.GitHubRepositoryID == command.Knowledge.GitHubRepositoryID {
		return Workspace{}, ErrValidation
	}
	project, err := s.binding(ctx, command.Project, RepositoryRoleProject)
	if err != nil {
		return Workspace{}, err
	}
	knowledge, err := s.binding(ctx, command.Knowledge, RepositoryRoleKnowledge)
	if err != nil {
		return Workspace{}, err
	}
	digest := commandDigest(
		"configure_repositories",
		command.WorkspaceID,
		fmt.Sprint(command.ExpectedVersion),
		project.GitHubRepositoryID,
		project.InstallationID,
		knowledge.GitHubRepositoryID,
		knowledge.InstallationID,
	)
	return s.store.ConfigureRepositories(ctx, ConfigureRepositoriesMutation{
		Actor:           actor,
		WorkspaceID:     command.WorkspaceID,
		ExpectedVersion: command.ExpectedVersion,
		RequestKey:      command.RequestKey,
		RequestDigest:   digest,
		Project:         project,
		Knowledge:       knowledge,
	})
}

// AddRepositoryBindingCommand exposes the rejection behavior needed by future callers.
type AddRepositoryBindingCommand struct {
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	Role            RepositoryRole
	Repository      RepositoryInput
}

// AddRepositoryBindingMutation is the atomic store command.
type AddRepositoryBindingMutation struct {
	Actor           Actor
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	RequestDigest   string
	Binding         RepositoryBinding
}

// AddRepositoryBinding refuses a second binding for either frozen MVP role.
func (s *Service) AddRepositoryBinding(ctx context.Context, actor Actor, command AddRepositoryBindingCommand) error {
	workspace, err := s.requireAdmin(ctx, actor, command.WorkspaceID)
	if err != nil {
		return err
	}
	if workspace.Version != command.ExpectedVersion {
		return ErrVersionConflict
	}
	if command.RequestKey == "" || (command.Role != RepositoryRoleProject && command.Role != RepositoryRoleKnowledge) {
		return ErrValidation
	}
	if command.Role == RepositoryRoleProject && workspace.Project != nil {
		return ErrPolicyBlocked
	}
	if command.Role == RepositoryRoleKnowledge && workspace.Knowledge != nil {
		return ErrPolicyBlocked
	}
	binding, err := s.binding(ctx, command.Repository, command.Role)
	if err != nil {
		return err
	}
	_, err = s.store.AddRepositoryBinding(ctx, AddRepositoryBindingMutation{
		Actor:           actor,
		WorkspaceID:     command.WorkspaceID,
		ExpectedVersion: command.ExpectedVersion,
		RequestKey:      command.RequestKey,
		RequestDigest:   commandDigest("add_repository", string(command.Role), binding.GitHubRepositoryID),
		Binding:         binding,
	})
	return err
}

// UpsertMemberCommand grants or changes a Workspace role for a stable GitHub user ID.
type UpsertMemberCommand struct {
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	GitHubUserID    string
	Role            Role
}

// UpsertMemberMutation is the atomic store command.
type UpsertMemberMutation struct {
	Actor           Actor
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	RequestDigest   string
	GitHubUserID    string
	Role            Role
}

// UpsertMember makes the role change and its audit record one transaction.
func (s *Service) UpsertMember(ctx context.Context, actor Actor, command UpsertMemberCommand) error {
	workspaceValue, err := s.requireAdmin(ctx, actor, command.WorkspaceID)
	if err != nil {
		return err
	}
	if workspaceValue.Version != command.ExpectedVersion {
		return ErrVersionConflict
	}
	if command.RequestKey == "" || !externalIDPattern.MatchString(command.GitHubUserID) || !validRole(command.Role) {
		return ErrValidation
	}
	_, err = s.store.UpsertMember(ctx, UpsertMemberMutation{
		Actor:           actor,
		WorkspaceID:     command.WorkspaceID,
		ExpectedVersion: command.ExpectedVersion,
		RequestKey:      command.RequestKey,
		RequestDigest:   commandDigest("upsert_member", command.GitHubUserID, string(command.Role)),
		GitHubUserID:    command.GitHubUserID,
		Role:            command.Role,
	})
	return err
}

// RevokeMemberCommand revokes future Kowa authorization without rewriting history.
type RevokeMemberCommand struct {
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	GitHubUserID    string
}

// RevokeMemberMutation is the atomic store command.
type RevokeMemberMutation struct {
	Actor           Actor
	WorkspaceID     string
	ExpectedVersion int64
	RequestKey      string
	RequestDigest   string
	GitHubUserID    string
}

func (s *Service) RevokeMember(ctx context.Context, actor Actor, command RevokeMemberCommand) error {
	workspace, err := s.requireAdmin(ctx, actor, command.WorkspaceID)
	if err != nil {
		return err
	}
	if workspace.Version != command.ExpectedVersion {
		return ErrVersionConflict
	}
	if command.RequestKey == "" || !externalIDPattern.MatchString(command.GitHubUserID) {
		return ErrValidation
	}
	_, err = s.store.RevokeMember(ctx, RevokeMemberMutation{
		Actor:           actor,
		WorkspaceID:     command.WorkspaceID,
		ExpectedVersion: command.ExpectedVersion,
		RequestKey:      command.RequestKey,
		RequestDigest:   commandDigest("revoke_member", command.GitHubUserID),
		GitHubUserID:    command.GitHubUserID,
	})
	return err
}

func (s *Service) requireAdmin(ctx context.Context, actor Actor, workspaceID string) (Workspace, error) {
	workspace, err := s.Get(ctx, actor, workspaceID)
	if err != nil {
		return Workspace{}, err
	}
	member, ok := activeMember(workspace, actor.GitHubUserID)
	if !ok || member.Role != RoleAdmin {
		return Workspace{}, ErrForbidden
	}
	return workspace, nil
}

func (s *Service) binding(
	ctx context.Context,
	input RepositoryInput,
	role RepositoryRole,
) (RepositoryBinding, error) {
	if !externalIDPattern.MatchString(input.GitHubRepositoryID) || !externalIDPattern.MatchString(input.InstallationID) {
		return RepositoryBinding{}, ErrValidation
	}
	if err := s.visibility.RepositoryVisible(ctx, input.InstallationID, input.GitHubRepositoryID); err != nil {
		return RepositoryBinding{}, fmt.Errorf("verify repository visibility: %w", ErrResourceUnavailable)
	}
	access := AccessReadOnly
	if role == RepositoryRoleProject {
		access = AccessReadWrite
	}
	return RepositoryBinding{
		GitHubRepositoryID: input.GitHubRepositoryID,
		InstallationID:     input.InstallationID,
		DisplayName:        strings.TrimSpace(input.DisplayName),
		Role:               role,
		Access:             access,
	}, nil
}

func validActor(actor Actor) bool {
	return externalIDPattern.MatchString(actor.GitHubUserID)
}

func validRole(role Role) bool {
	return role == RoleAdmin || role == RoleDeveloper || role == RoleViewer
}

func activeMember(workspace Workspace, githubUserID string) (Member, bool) {
	for _, member := range workspace.Members {
		if member.GitHubUserID == githubUserID && member.RevokedAt == nil {
			return member, true
		}
	}
	return Member{}, false
}

func commandDigest(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}
