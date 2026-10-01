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

func TestService_RecoverSessionStableAcrossRefreshAndTabs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := fixedClock{now: time.Now()}
	store := newMemoryStore()
	service := NewService(store,
		&fakeOAuthClient{user: GitHubUser{ID: "101",
			Login: "member-a"}},
		newSequenceTokens(),
		&clock,
		DefaultConfig())
	login, err := service.BeginLogin(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.CompleteLogin(ctx, CompleteLoginCommand{State: login.State, StateCookie: login.State, Code: "code"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		r, err := service.RecoverSession(ctx, a.SessionToken)
		if err != nil {
			t.Fatal(err)
		}
		if r.CSRFToken != a.CSRFToken || r.Identity.GitHubUserID != "101" || r.SessionID == "" {
			t.Fatalf("recovery mismatch: %+v", r)
		}
	}
	bLogin, err := service.BeginLogin(ctx, "/workspaces")
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.CompleteLogin(ctx,
		CompleteLoginCommand{State: bLogin.State,
			StateCookie: bLogin.State,
			Code:        "next"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, b.SessionToken, a.CSRFToken, true); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-session csrf accepted: %v", err)
	}
	if _, err := service.Authenticate(ctx, b.SessionToken, b.CSRFToken, true); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, a.SessionToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverSession(ctx, a.SessionToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked recovery: %v", err)
	}
	clock.now = clock.now.Add(25 * time.Hour)
	if _, err := service.RecoverSession(ctx, b.SessionToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired recovery: %v", err)
	}
}

func TestService_ReturnToRejectsBrowserRedirectAmbiguity(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"//evil.test/x",
		"/\\evil.test",
		"/%2f%2fevil.test",
		"/%5cevil.test",
		"/\r\nInjected: x",
		"https://evil.test",
		"/api/v2/auth/github/callback?code=secret",
		"/workspaces?token=secret"} {
		t.Run(path, func(t *testing.T) {
			s := NewService(newMemoryStore(),
				&fakeOAuthClient{},
				newSequenceTokens(),
				fixedClock{now: time.Now()},
				DefaultConfig())
			if _, err := s.BeginLogin(context.Background(), path); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("unsafe returnTo %q accepted: %v", path, err)
			}
		})
	}
}

func TestService_FailedOrExpiredOAuthDoesNotCreateSession(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		advance  time.Duration
		oauthErr error
	}{{name: "expired state", advance: 11 * time.Minute}, {name: "external error", oauthErr: ErrResourceUnavailable}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clock := fixedClock{now: time.Now()}
			store := newMemoryStore()
			oauth := &failingOAuth{err: tc.oauthErr}
			s := NewService(store, oauth, newSequenceTokens(), &clock, DefaultConfig())
			l, err := s.BeginLogin(ctx, "/workspaces")
			if err != nil {
				t.Fatal(err)
			}
			clock.now = clock.now.Add(tc.advance)
			if _,
				err := s.CompleteLogin(ctx,
				CompleteLoginCommand{State: l.State,
					StateCookie: l.State,
					Code:        "bad"}); err == nil {
				t.Fatal("invalid flow succeeded")
			}
			if len(store.sessions) != 0 {
				t.Fatal("session created on invalid flow")
			}
			if tc.oauthErr != nil {
				oauth.err = nil
				next, err := s.BeginLogin(ctx, "/workspaces")
				if err != nil {
					t.Fatal(err)
				}
				if _,
					err := s.CompleteLogin(ctx,
					CompleteLoginCommand{State: next.State,
						StateCookie: next.State,
						Code:        "good"}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type failingOAuth struct{ err error }

func (*failingOAuth) AuthorizationURL(state, challenge string) string {
	return "https://github.test/?state=" + state + "&challenge=" + challenge
}
func (o *failingOAuth) Exchange(context.Context, string, string) (GitHubUser, error) {
	if o.err != nil {
		return GitHubUser{}, o.err
	}
	return GitHubUser{ID: "101", Login: "member-a"}, nil
}

func TestService_PreRecoverySessionRequiresNewLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := fixedClock{now: time.Now()}
	store := newMemoryStore()
	service := NewService(store,
		&fakeOAuthClient{user: GitHubUser{ID: "101",
			Login: "member-a"}},
		newSequenceTokens(),
		clock,
		DefaultConfig())
	if err := store.CreateSession(ctx,
		GitHubUser{ID: "101",
			Login: "member-a"},
		Session{TokenDigest: digest("pre-recovery-session"),
			CSRFDigest: digest("old-random-csrf"),
			Identity: WebIdentity{GitHubUserID: "101",
				Login: "member-a"},
			ExpiresAt: clock.now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverSession(ctx, "pre-recovery-session"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old session unexpectedly recoverable: %v", err)
	}
	if _, err := service.Authenticate(ctx, "pre-recovery-session", "old-random-csrf", true); err != nil {
		t.Fatal("recovery refusal changed existing v1 credential behavior")
	}
}

func TestService_CompleteLoginRevalidatesStoredReturnTo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := fixedClock{now: time.Now()}
	store := newMemoryStore()
	s := NewService(store,
		&fakeOAuthClient{user: GitHubUser{ID: "101",
			Login: "a"}},
		newSequenceTokens(),
		clock,
		DefaultConfig())
	if err := store.SaveLoginFlow(ctx,
		LoginFlow{StateDigest: digest("old-state"),
			Verifier:  "old-verifier",
			ReturnTo:  "/workspaces?token=old",
			ExpiresAt: clock.now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _,
		err := s.CompleteLogin(ctx,
		CompleteLoginCommand{State: "old-state",
			StateCookie: "old-state",
			Code:        "code"}); !errors.Is(err,
		ErrUnauthorized) {
		t.Fatalf("stored unsafe redirect was accepted: %v", err)
	}
	if len(store.sessions) != 0 {
		t.Fatal("unsafe stored flow created session")
	}
}
