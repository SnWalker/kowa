package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestService_BootstrapRequiresAllowlistedIdentity(t *testing.T) {
	t.Parallel()

	store := newMemoryStore()
	ids := fixedIDs{"workspace-1", "workspace-2", "workspace-3"}
	service := NewService(store, staticVisibility(true), Allowlist{"101": {}}, &ids)

	if _, err := service.Bootstrap(context.Background(), Actor{GitHubUserID: "999"}, BootstrapCommand{
		Name:       "Kowa",
		RequestKey: "request-1",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Bootstrap() error = %v, want ErrForbidden", err)
	}

	result, err := service.Bootstrap(context.Background(), Actor{GitHubUserID: "101"}, BootstrapCommand{
		Name:       "Kowa",
		RequestKey: "request-1",
	})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if result.Version != 1 || result.Members[0].Role != RoleAdmin {
		t.Fatalf("Bootstrap() = %#v, want version 1 with admin", result)
	}
	replayed, err := service.Bootstrap(context.Background(), Actor{GitHubUserID: "101"}, BootstrapCommand{
		Name:       "Kowa",
		RequestKey: "request-1",
	})
	if err != nil {
		t.Fatalf("Bootstrap() replay error = %v", err)
	}
	if replayed.ID != result.ID {
		t.Fatalf("Bootstrap() replay ID = %q, want %q", replayed.ID, result.ID)
	}
	if _, err := service.Bootstrap(context.Background(), Actor{GitHubUserID: "101"}, BootstrapCommand{
		Name:       "Changed",
		RequestKey: "request-1",
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Bootstrap() changed replay error = %v, want ErrVersionConflict", err)
	}
}

func TestService_ConfigureRepositoriesRejectsConflictsAndSecondWritableRepository(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStore()
	ids := fixedIDs{"workspace-1"}
	service := NewService(store, staticVisibility(true), Allowlist{"101": {}}, &ids)
	created, err := service.Bootstrap(ctx, Actor{GitHubUserID: "101"}, BootstrapCommand{
		Name:       "Kowa",
		RequestKey: "bootstrap",
	})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	configured, err := service.ConfigureRepositories(ctx, Actor{GitHubUserID: "101"}, ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: 1,
		RequestKey:      "configure-1",
		Project: RepositoryInput{
			GitHubRepositoryID: "3001",
			InstallationID:     "4001",
			DisplayName:        "team/project",
		},
		Knowledge: RepositoryInput{
			GitHubRepositoryID: "3002",
			InstallationID:     "4001",
			DisplayName:        "team/knowledge",
		},
	})
	if err != nil {
		t.Fatalf("ConfigureRepositories() error = %v", err)
	}
	if configured.Version != 2 {
		t.Fatalf("ConfigureRepositories() version = %d, want 2", configured.Version)
	}
	if configured.Project.Access != AccessReadWrite || configured.Knowledge.Access != AccessReadOnly {
		t.Fatalf("ConfigureRepositories() access = %q/%q", configured.Project.Access, configured.Knowledge.Access)
	}

	if _, err := service.ConfigureRepositories(ctx, Actor{GitHubUserID: "101"}, ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: 1,
		RequestKey:      "stale-version",
		Project:         RepositoryInput{GitHubRepositoryID: "3001", InstallationID: "4001"},
		Knowledge:       RepositoryInput{GitHubRepositoryID: "3002", InstallationID: "4001"},
	}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ConfigureRepositories() stale version error = %v, want ErrVersionConflict", err)
	}

	if err := service.AddRepositoryBinding(ctx, Actor{GitHubUserID: "101"}, AddRepositoryBindingCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: 2,
		RequestKey:      "second-project",
		Role:            RepositoryRoleProject,
		Repository:      RepositoryInput{GitHubRepositoryID: "3003", InstallationID: "4001"},
	}); !errors.Is(err, ErrPolicyBlocked) {
		t.Fatalf("AddRepositoryBinding() error = %v, want ErrPolicyBlocked", err)
	}
}

func TestService_RejectsCrossWorkspaceAndRevokedMember(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStore()
	ids := fixedIDs{"workspace-1", "workspace-2"}
	service := NewService(store, staticVisibility(true), Allowlist{"101": {}, "202": {}}, &ids)
	first, err := service.Bootstrap(ctx, Actor{GitHubUserID: "101"}, BootstrapCommand{Name: "one", RequestKey: "one"})
	if err != nil {
		t.Fatalf("Bootstrap() first error = %v", err)
	}
	second, err := service.Bootstrap(ctx, Actor{GitHubUserID: "202"}, BootstrapCommand{Name: "two", RequestKey: "two"})
	if err != nil {
		t.Fatalf("Bootstrap() second error = %v", err)
	}

	if _, err := service.Get(ctx, Actor{GitHubUserID: "101"}, second.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() cross-workspace error = %v, want ErrForbidden", err)
	}

	if err := service.RevokeMember(ctx, Actor{GitHubUserID: "101"}, RevokeMemberCommand{
		WorkspaceID:     first.ID,
		ExpectedVersion: 1,
		RequestKey:      "revoke-self",
		GitHubUserID:    "101",
	}); err != nil {
		t.Fatalf("RevokeMember() error = %v", err)
	}
	if _, err := service.Get(ctx, Actor{GitHubUserID: "101"}, first.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() revoked member error = %v, want ErrForbidden", err)
	}
}

func TestService_AdminManagesMemberRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStore()
	ids := fixedIDs{"workspace-1"}
	service := NewService(store, staticVisibility(true), Allowlist{"101": {}}, &ids)
	created, err := service.Bootstrap(ctx, Actor{GitHubUserID: "101"}, BootstrapCommand{Name: "Kowa", RequestKey: "bootstrap"})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if err := service.UpsertMember(ctx, Actor{GitHubUserID: "101"}, UpsertMemberCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: created.Version,
		RequestKey:      "add-developer",
		GitHubUserID:    "202",
		Role:            RoleDeveloper,
	}); err != nil {
		t.Fatalf("UpsertMember() error = %v", err)
	}
	memberView, err := service.Get(ctx, Actor{GitHubUserID: "202"}, created.ID)
	if err != nil {
		t.Fatalf("Get() member error = %v", err)
	}
	if memberView.Version != 2 {
		t.Fatalf("Get() version = %d, want 2", memberView.Version)
	}
	_, err = service.ConfigureRepositories(ctx, Actor{GitHubUserID: "202"}, ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: 2,
		RequestKey:      "developer-configure",
		Project:         RepositoryInput{GitHubRepositoryID: "3001", InstallationID: "4001"},
		Knowledge:       RepositoryInput{GitHubRepositoryID: "3002", InstallationID: "4001"},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ConfigureRepositories() developer error = %v, want ErrForbidden", err)
	}
}

func TestService_AuditFailureRollsBackMutation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStore()
	ids := fixedIDs{"workspace-1"}
	service := NewService(store, staticVisibility(true), Allowlist{"101": {}}, &ids)
	created, err := service.Bootstrap(ctx, Actor{GitHubUserID: "101"}, BootstrapCommand{Name: "Kowa", RequestKey: "bootstrap"})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	store.auditErr = errors.New("audit unavailable")

	_, err = service.ConfigureRepositories(ctx, Actor{GitHubUserID: "101"}, ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: 1,
		RequestKey:      "configure",
		Project:         RepositoryInput{GitHubRepositoryID: "3001", InstallationID: "4001"},
		Knowledge:       RepositoryInput{GitHubRepositoryID: "3002", InstallationID: "4001"},
	})
	if err == nil {
		t.Fatal("ConfigureRepositories() error = nil, want audit failure")
	}
	store.auditErr = nil
	got, err := service.Get(ctx, Actor{GitHubUserID: "101"}, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Version != 1 || got.Project != nil || got.Knowledge != nil {
		t.Fatalf("Get() after audit failure = %#v, want unchanged version 1", got)
	}
}

func TestService_ConfigureRepositoriesRejectsInvisibleRepository(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStore()
	ids := fixedIDs{"workspace-1"}
	service := NewService(store, staticVisibility(false), Allowlist{"101": {}}, &ids)
	created, err := service.Bootstrap(ctx, Actor{GitHubUserID: "101"}, BootstrapCommand{Name: "Kowa", RequestKey: "bootstrap"})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	_, err = service.ConfigureRepositories(ctx, Actor{GitHubUserID: "101"}, ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: 1,
		RequestKey:      "configure",
		Project:         RepositoryInput{GitHubRepositoryID: "3001", InstallationID: "4001"},
		Knowledge:       RepositoryInput{GitHubRepositoryID: "3002", InstallationID: "4001"},
	})
	if !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("ConfigureRepositories() error = %v, want ErrResourceUnavailable", err)
	}
}

type staticVisibility bool

func (v staticVisibility) RepositoryVisible(context.Context, string, string) error {
	if !v {
		return ErrResourceUnavailable
	}
	return nil
}

type fixedIDs []string

func (ids *fixedIDs) New() (string, error) {
	value := (*ids)[0]
	*ids = (*ids)[1:]
	return value, nil
}
