package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestService_CompleteLoginRejectsInvalidAndReplayedState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	store := newMemoryStore()
	oauth := &fakeOAuthClient{user: GitHubUser{ID: "101", Login: "member-a"}}
	service := NewService(store, oauth, newSequenceTokens(), &clock, DefaultConfig())

	login, err := service.BeginLogin(ctx, "/workspaces")
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}

	if _, err := service.CompleteLogin(ctx, CompleteLoginCommand{
		State:       "wrong-state",
		StateCookie: login.State,
		Code:        "one-time-code",
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("CompleteLogin() invalid state error = %v, want ErrUnauthorized", err)
	}

	result, err := service.CompleteLogin(ctx, CompleteLoginCommand{
		State:       login.State,
		StateCookie: login.State,
		Code:        "one-time-code",
	})
	if err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	if result.Identity.GitHubUserID != "101" {
		t.Fatalf("CompleteLogin() GitHubUserID = %q, want 101", result.Identity.GitHubUserID)
	}
	if result.SessionToken == "" || result.CSRFToken == "" {
		t.Fatal("CompleteLogin() returned empty browser credentials")
	}
	if oauth.lastVerifier == "" {
		t.Fatal("CompleteLogin() did not use the server-side PKCE verifier")
	}

	if _, err := service.CompleteLogin(ctx, CompleteLoginCommand{
		State:       login.State,
		StateCookie: login.State,
		Code:        "replayed-code",
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("CompleteLogin() replay error = %v, want ErrUnauthorized", err)
	}
}

func TestService_AuthenticateHonorsCSRFExpiryAndRevocation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clock := fixedClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	store := newMemoryStore()
	service := NewService(
		store,
		&fakeOAuthClient{user: GitHubUser{ID: "202", Login: "member-b"}},
		newSequenceTokens(),
		&clock,
		DefaultConfig(),
	)

	login, err := service.BeginLogin(ctx, "")
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	completed, err := service.CompleteLogin(ctx, CompleteLoginCommand{
		State:       login.State,
		StateCookie: login.State,
		Code:        "code",
	})
	if err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}

	identity, err := service.Authenticate(ctx, completed.SessionToken, completed.CSRFToken, true)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.GitHubUserID != "202" {
		t.Fatalf("Authenticate() GitHubUserID = %q, want 202", identity.GitHubUserID)
	}

	if _, err := service.Authenticate(ctx, completed.SessionToken, "wrong-csrf", true); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() CSRF error = %v, want ErrUnauthorized", err)
	}
	if err := service.Logout(ctx, completed.SessionToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, completed.SessionToken, "", false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() revoked error = %v, want ErrUnauthorized", err)
	}

	secondLogin, err := service.BeginLogin(ctx, "")
	if err != nil {
		t.Fatalf("BeginLogin() second error = %v", err)
	}
	secondSession, err := service.CompleteLogin(ctx, CompleteLoginCommand{
		State:       secondLogin.State,
		StateCookie: secondLogin.State,
		Code:        "code-2",
	})
	if err != nil {
		t.Fatalf("CompleteLogin() second error = %v", err)
	}
	clock.now = clock.now.Add(25 * time.Hour)
	if _, err := service.Authenticate(ctx, secondSession.SessionToken, "", false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() expired error = %v, want ErrUnauthorized", err)
	}
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

type sequenceTokens struct {
	next int
}

func newSequenceTokens() *sequenceTokens { return &sequenceTokens{} }

func (g *sequenceTokens) New() (string, error) {
	g.next++
	return "token-" + string(rune('a'+g.next)), nil
}

type fakeOAuthClient struct {
	user         GitHubUser
	lastVerifier string
}

func (c *fakeOAuthClient) AuthorizationURL(state, challenge string) string {
	return "https://github.example/login?state=" + state + "&challenge=" + challenge
}

func (c *fakeOAuthClient) Exchange(_ context.Context, _, verifier string) (GitHubUser, error) {
	c.lastVerifier = verifier
	return c.user, nil
}
