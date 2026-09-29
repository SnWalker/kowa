//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/infra/postgres"
	"github.com/SnWalker/kowa/internal/platform/clock"
	"github.com/SnWalker/kowa/internal/platform/random"
	"github.com/SnWalker/kowa/internal/workspace"
)

func TestIdentityWorkspacePostgres(t *testing.T) {
	databaseURL := os.Getenv("KOWA_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("KOWA_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	database, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("postgres.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("database.Close() error = %v", err)
		}
	})
	store := postgres.NewStore(database)

	identityService := identity.NewService(
		store,
		integrationOAuth{user: identity.GitHubUser{ID: "101", Login: "member-a"}},
		random.Generator{},
		clock.Clock{},
		identity.DefaultConfig(),
	)
	login, err := identityService.BeginLogin(ctx, "/workspaces")
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	completed, err := identityService.CompleteLogin(ctx, identity.CompleteLoginCommand{
		State:       login.State,
		StateCookie: login.State,
		Code:        "test-code",
	})
	if err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	webIdentity, err := identityService.Authenticate(ctx, completed.SessionToken, completed.CSRFToken, true)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if webIdentity.GitHubUserID != "101" {
		t.Fatalf("Authenticate() identity = %#v", webIdentity)
	}
	assertRawCredentialNotStored(t, database, completed.SessionToken)

	workspaceService := workspace.NewService(
		store,
		integrationVisibility{},
		workspace.Allowlist{"101": {}},
		random.Generator{},
	)
	actor := workspace.Actor{GitHubUserID: webIdentity.GitHubUserID}
	testConcurrentIdempotency(t, workspaceService, actor)
	created, err := workspaceService.Bootstrap(ctx, actor, workspace.BootstrapCommand{
		Name:       "Kowa integration",
		RequestKey: "bootstrap-1",
	})
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if err := workspaceService.UpsertMember(ctx, actor, workspace.UpsertMemberCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: created.Version,
		RequestKey:      "add-member-1",
		GitHubUserID:    "202",
		Role:            workspace.RoleDeveloper,
	}); err != nil {
		t.Fatalf("UpsertMember() error = %v", err)
	}
	created, err = workspaceService.Get(ctx, actor, created.ID)
	if err != nil {
		t.Fatalf("Get() after member grant error = %v", err)
	}
	if _, err := workspaceService.Get(ctx, workspace.Actor{GitHubUserID: "202"}, created.ID); err != nil {
		t.Fatalf("Get() as granted member error = %v", err)
	}
	configured, err := workspaceService.ConfigureRepositories(ctx, actor, workspace.ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: created.Version,
		RequestKey:      "configure-1",
		Project:         workspace.RepositoryInput{GitHubRepositoryID: "3001", InstallationID: "4001", DisplayName: "team/project"},
		Knowledge:       workspace.RepositoryInput{GitHubRepositoryID: "3002", InstallationID: "4001", DisplayName: "team/knowledge"},
	})
	if err != nil {
		t.Fatalf("ConfigureRepositories() error = %v", err)
	}
	if configured.Project == nil || configured.Project.Access != workspace.AccessReadWrite {
		t.Fatalf("configured project = %#v", configured.Project)
	}
	if configured.Knowledge == nil || configured.Knowledge.Access != workspace.AccessReadOnly {
		t.Fatalf("configured knowledge = %#v", configured.Knowledge)
	}

	testAuditFailureRollback(t, database, workspaceService, actor)

	if err := workspaceService.RevokeMember(ctx, actor, workspace.RevokeMemberCommand{
		WorkspaceID:     configured.ID,
		ExpectedVersion: configured.Version,
		RequestKey:      "revoke-1",
		GitHubUserID:    "202",
	}); err != nil {
		t.Fatalf("RevokeMember() error = %v", err)
	}
	if _, err := workspaceService.Get(ctx, workspace.Actor{GitHubUserID: "202"}, configured.ID); !errors.Is(err, workspace.ErrForbidden) {
		t.Fatalf("Get() after member revocation error = %v, want ErrForbidden", err)
	}

	if err := identityService.Logout(ctx, completed.SessionToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := identityService.Authenticate(ctx, completed.SessionToken, "", false); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("Authenticate() after logout error = %v, want ErrUnauthorized", err)
	}

	var auditCount int
	if err := database.QueryRowContext(ctx, `select count(*) from audit_event`).Scan(&auditCount); err != nil {
		t.Fatalf("count audit_event error = %v", err)
	}
	if auditCount < 4 {
		t.Fatalf("audit_event count = %d, want at least 4", auditCount)
	}
}

func testConcurrentIdempotency(t *testing.T, service *workspace.Service, actor workspace.Actor) {
	t.Helper()
	ctx := context.Background()
	type result struct {
		workspace workspace.Workspace
		err       error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			value, err := service.Bootstrap(ctx, actor, workspace.BootstrapCommand{
				Name:       "Concurrent bootstrap",
				RequestKey: "concurrent-bootstrap",
			})
			results <- result{workspace: value, err: err}
		}()
	}
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent Bootstrap() errors = %v, %v", first.err, second.err)
	}
	if first.workspace.ID != second.workspace.ID || first.workspace.Version != second.workspace.Version {
		t.Fatalf("concurrent Bootstrap() results = %#v, %#v", first.workspace, second.workspace)
	}
}

func testAuditFailureRollback(
	t *testing.T,
	database *sql.DB,
	service *workspace.Service,
	actor workspace.Actor,
) {
	t.Helper()
	ctx := context.Background()
	created, err := service.Bootstrap(ctx, actor, workspace.BootstrapCommand{
		Name:       "Audit rollback",
		RequestKey: "bootstrap-audit",
	})
	if err != nil {
		t.Fatalf("Bootstrap() audit workspace error = %v", err)
	}
	_, err = database.ExecContext(ctx, `alter table audit_event rename to audit_event_unavailable`)
	if err != nil {
		t.Fatalf("rename audit table for failure injection error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(
			context.Background(),
			`alter table if exists audit_event_unavailable rename to audit_event`,
		)
	})
	_, err = service.ConfigureRepositories(ctx, actor, workspace.ConfigureRepositoriesCommand{
		WorkspaceID:     created.ID,
		ExpectedVersion: created.Version,
		RequestKey:      "configure-audit-failure",
		Project:         workspace.RepositoryInput{GitHubRepositoryID: "5001", InstallationID: "6001"},
		Knowledge:       workspace.RepositoryInput{GitHubRepositoryID: "5002", InstallationID: "6001"},
	})
	if err == nil {
		t.Fatal("ConfigureRepositories() audit failure error = nil")
	}
	_, err = database.ExecContext(ctx, `alter table audit_event_unavailable rename to audit_event`)
	if err != nil {
		t.Fatalf("restore audit table after failure injection error = %v", err)
	}
	got, err := service.Get(ctx, actor, created.ID)
	if err != nil {
		t.Fatalf("Get() audit workspace error = %v", err)
	}
	if got.Version != 1 || got.Project != nil || got.Knowledge != nil {
		t.Fatalf("workspace after audit failure = %#v", got)
	}
}

func assertRawCredentialNotStored(t *testing.T, database *sql.DB, raw string) {
	t.Helper()
	var count int
	if err := database.QueryRowContext(
		context.Background(),
		`select count(*) from web_session where encode(token_digest, 'escape') = $1`,
		raw,
	).Scan(&count); err != nil {
		t.Fatalf("credential storage query error = %v", err)
	}
	if count != 0 {
		t.Fatal("raw session credential was stored")
	}
}

type integrationOAuth struct {
	user identity.GitHubUser
}

func (integrationOAuth) AuthorizationURL(state, challenge string) string {
	return "https://github.example/login?state=" + state + "&challenge=" + challenge
}

func (o integrationOAuth) Exchange(context.Context, string, string) (identity.GitHubUser, error) {
	return o.user, nil
}

type integrationVisibility struct{}

func (integrationVisibility) RepositoryVisible(context.Context, string, string) error { return nil }
