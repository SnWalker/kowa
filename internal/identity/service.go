// Package identity owns GitHub Web identity and Kowa browser sessions.
package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

var (
	// ErrUnauthorized means the caller did not prove a current Web identity.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrConflict means a one-time identity operation has already been consumed.
	ErrConflict = errors.New("identity conflict")
	// ErrResourceUnavailable means GitHub could not establish the external identity fact.
	ErrResourceUnavailable = errors.New("identity resource unavailable")

	githubIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// GitHubUser is the stable GitHub identity returned by the OAuth adapter.
type GitHubUser struct {
	ID    string
	Login string
}

// WebIdentity is the authenticated identity exposed to Kowa use cases.
type WebIdentity struct {
	GitHubUserID string
	Login        string
}

// LoginFlow is the short-lived server-side OAuth state and PKCE record.
// StateDigest is persisted instead of the browser-visible state token.
type LoginFlow struct {
	StateDigest [32]byte
	Verifier    string
	ReturnTo    string
	ExpiresAt   time.Time
}

// Session is the server-side browser session record. Raw browser credentials
// are never stored.
type Session struct {
	TokenDigest [32]byte
	CSRFDigest  [32]byte
	Identity    WebIdentity
	ExpiresAt   time.Time
	RevokedAt   *time.Time
}

// Store persists one-time login flows, identities, and browser sessions.
type Store interface {
	SaveLoginFlow(context.Context, LoginFlow) error
	ConsumeLoginFlow(context.Context, [32]byte, time.Time) (LoginFlow, error)
	CreateSession(context.Context, GitHubUser, Session) error
	GetSession(context.Context, [32]byte) (Session, error)
	RevokeSession(context.Context, [32]byte, time.Time) error
}

// OAuthClient hides the GitHub user token inside the adapter boundary.
type OAuthClient interface {
	AuthorizationURL(state, codeChallenge string) string
	Exchange(ctx context.Context, code, codeVerifier string) (GitHubUser, error)
}

// TokenGenerator creates cryptographically random opaque values in production.
type TokenGenerator interface {
	New() (string, error)
}

// Clock makes expiry behavior deterministic in tests.
type Clock interface {
	Now() time.Time
}

// Service implements OAuth state/PKCE and service-side session rules.
type Service struct {
	store        Store
	oauth        OAuthClient
	tokens       TokenGenerator
	clock        Clock
	loginFlowTTL time.Duration
	sessionTTL   time.Duration
}

// ServiceConfig keeps deployment policy separate from the identity contract.
type ServiceConfig struct {
	LoginFlowTTL time.Duration
	SessionTTL   time.Duration
}

// DefaultConfig returns the test and local-development policy. Deployment
// configuration may override it without changing the wire contract.
func DefaultConfig() ServiceConfig {
	return ServiceConfig{
		LoginFlowTTL: 10 * time.Minute,
		SessionTTL:   24 * time.Hour,
	}
}

// NewService constructs an identity service.
func NewService(
	store Store,
	oauth OAuthClient,
	tokens TokenGenerator,
	clock Clock,
	config ServiceConfig,
) *Service {
	defaults := DefaultConfig()
	if config.LoginFlowTTL <= 0 {
		config.LoginFlowTTL = defaults.LoginFlowTTL
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = defaults.SessionTTL
	}
	return &Service{
		store:        store,
		oauth:        oauth,
		tokens:       tokens,
		clock:        clock,
		loginFlowTTL: config.LoginFlowTTL,
		sessionTTL:   config.SessionTTL,
	}
}

// BeginLogin creates a single-use OAuth state bound to a server-side PKCE verifier.
func (s *Service) BeginLogin(ctx context.Context, returnTo string) (BeginLoginResult, error) {
	if !validReturnTo(returnTo) {
		return BeginLoginResult{}, fmt.Errorf("invalid return path: %w", ErrUnauthorized)
	}

	state, err := s.tokens.New()
	if err != nil {
		return BeginLoginResult{}, fmt.Errorf("generate oauth state: %w", err)
	}
	verifier, err := s.tokens.New()
	if err != nil {
		return BeginLoginResult{}, fmt.Errorf("generate pkce verifier: %w", err)
	}

	challengeDigest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeDigest[:])
	flow := LoginFlow{
		StateDigest: digest(state),
		Verifier:    verifier,
		ReturnTo:    returnTo,
		ExpiresAt:   s.clock.Now().Add(s.loginFlowTTL),
	}
	if err := s.store.SaveLoginFlow(ctx, flow); err != nil {
		return BeginLoginResult{}, fmt.Errorf("save oauth flow: %w", err)
	}

	return BeginLoginResult{
		AuthorizationURL: s.oauth.AuthorizationURL(state, challenge),
		State:            state,
	}, nil
}

// BeginLoginResult contains the browser redirect and state-cookie value.
type BeginLoginResult struct {
	AuthorizationURL string
	State            string
}

// CompleteLoginCommand contains only browser callback material. Actor identity
// comes from the OAuth adapter, never from the request body.
type CompleteLoginCommand struct {
	State       string
	StateCookie string
	Code        string
}

// CompleteLoginResult contains raw credentials exactly once for browser delivery.
type CompleteLoginResult struct {
	Identity     WebIdentity
	SessionToken string
	CSRFToken    string
	ReturnTo     string
	ExpiresAt    time.Time
}

// CompleteLogin consumes the one-time flow, obtains a stable GitHub ID, and
// creates an opaque server-side session.
func (s *Service) CompleteLogin(ctx context.Context, command CompleteLoginCommand) (CompleteLoginResult, error) {
	if command.State == "" || command.StateCookie == "" || command.Code == "" ||
		subtle.ConstantTimeCompare([]byte(command.State), []byte(command.StateCookie)) != 1 {
		return CompleteLoginResult{}, ErrUnauthorized
	}

	flow, err := s.store.ConsumeLoginFlow(ctx, digest(command.State), s.clock.Now())
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return CompleteLoginResult{}, ErrUnauthorized
		}
		return CompleteLoginResult{}, fmt.Errorf("consume oauth flow: %w", err)
	}
	user, err := s.oauth.Exchange(ctx, command.Code, flow.Verifier)
	if err != nil {
		return CompleteLoginResult{}, fmt.Errorf("exchange github oauth code: %w", err)
	}
	if !githubIDPattern.MatchString(user.ID) {
		return CompleteLoginResult{}, fmt.Errorf("github returned invalid stable user id: %w", ErrUnauthorized)
	}

	sessionToken, err := s.tokens.New()
	if err != nil {
		return CompleteLoginResult{}, fmt.Errorf("generate session token: %w", err)
	}
	csrfToken, err := s.tokens.New()
	if err != nil {
		return CompleteLoginResult{}, fmt.Errorf("generate csrf token: %w", err)
	}
	expiresAt := s.clock.Now().Add(s.sessionTTL)
	identity := WebIdentity{GitHubUserID: user.ID, Login: user.Login}
	session := Session{
		TokenDigest: digest(sessionToken),
		CSRFDigest:  digest(csrfToken),
		Identity:    identity,
		ExpiresAt:   expiresAt,
	}
	if err := s.store.CreateSession(ctx, user, session); err != nil {
		return CompleteLoginResult{}, fmt.Errorf("create browser session: %w", err)
	}

	return CompleteLoginResult{
		Identity:     identity,
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		ReturnTo:     flow.ReturnTo,
		ExpiresAt:    expiresAt,
	}, nil
}

// Authenticate resolves a session and optionally verifies its CSRF token.
func (s *Service) Authenticate(ctx context.Context, sessionToken, csrfToken string, requireCSRF bool) (WebIdentity, error) {
	if sessionToken == "" {
		return WebIdentity{}, ErrUnauthorized
	}
	session, err := s.store.GetSession(ctx, digest(sessionToken))
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return WebIdentity{}, ErrUnauthorized
		}
		return WebIdentity{}, fmt.Errorf("read browser session: %w", err)
	}
	if session.RevokedAt != nil || !s.clock.Now().Before(session.ExpiresAt) {
		return WebIdentity{}, ErrUnauthorized
	}
	if requireCSRF {
		provided := digest(csrfToken)
		if csrfToken == "" || subtle.ConstantTimeCompare(provided[:], session.CSRFDigest[:]) != 1 {
			return WebIdentity{}, ErrUnauthorized
		}
	}
	return session.Identity, nil
}

// Logout revokes the current server-side session.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return ErrUnauthorized
	}
	if err := s.store.RevokeSession(ctx, digest(sessionToken), s.clock.Now()); err != nil {
		return fmt.Errorf("revoke browser session: %w", err)
	}
	return nil
}

func digest(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

func validReturnTo(returnTo string) bool {
	if returnTo == "" {
		return true
	}
	parsed, err := url.Parse(returnTo)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.Path != "" && parsed.Path[0] == '/'
}
