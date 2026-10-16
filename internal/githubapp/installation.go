// Package githubapp implements the GitHub App installation seam of the
// control plane: it mints short-lived installation access tokens from the
// App's private key and answers repository-visibility questions through the
// installation's own repository list.
//
// The seam exists because the frozen identity-workspace contract answers
// repository binding with the fact "the installation can see this repository"
// (`RESOURCE_UNAVAILABLE` otherwise). Every failure here — an unreadable key,
// a refused JWT, a rate-limited or unknown installation — is reported as an
// error so the workspace authority keeps failing closed instead of granting
// visibility it did not observe.
//
// The private key is deployment material: it is read from a path outside the
// repository, never printed, and never attached to an error. The minted
// installation token stays inside this package.
package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIBaseURL = "https://api.github.com"
	// The installation token cache refreshes this long before the token's own
	// expiry, so a token is never used in the last minutes of its lifetime.
	tokenRefreshMargin = 5 * time.Minute
	// The repository list of one installation is cached for a short window:
	// the web session may verify several bindings in a row, and the fact is
	// external and may change, so it must not be cached for long.
	defaultRepositoriesTTL = 60 * time.Second
	// JWT lifetime stays well inside GitHub's ten-minute maximum.
	jwtLifetime        = 9 * time.Minute
	jwtClockSkew       = 60 * time.Second
	responseBodyCap    = 1 << 20
	repositoryPageSize = 100
	// A bounded walk: an installation with more pages than this is refused
	// rather than paginated forever.
	repositoryPageLimit = 20
)

var externalIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// Config configures the installation seam. PrivateKeyPath is a deployment
// path; the key material is read once at construction and never exposed.
type Config struct {
	AppID           string
	PrivateKeyPath  string
	APIBaseURL      string
	HTTPClient      *http.Client
	Now             func() time.Time
	RepositoriesTTL time.Duration
}

// InstallationVisibility verifies repository binding facts against the App's
// own installation observations.
type InstallationVisibility struct {
	appID           string
	key             *rsa.PrivateKey
	apiBaseURL      string
	client          *http.Client
	now             func() time.Time
	repositoriesTTL time.Duration

	mutex        sync.Mutex
	tokens       map[string]installationToken
	repositories map[string]cachedRepositories
}

type installationToken struct {
	value   string
	expires time.Time
}

type cachedRepositories struct {
	ids      map[string]bool
	observed time.Time
}

// NewInstallationVisibility loads the App private key and returns the seam.
// A missing or malformed key is an error at construction time: a deployment
// that configured the seam but cannot read its key must not start pretending
// the unconfigured fail-closed adapter.
func NewInstallationVisibility(config Config) (*InstallationVisibility, error) {
	if !externalIDPattern.MatchString(config.AppID) {
		return nil, errors.New("github app id must be a decimal application id")
	}
	if strings.TrimSpace(config.PrivateKeyPath) == "" {
		return nil, errors.New("github app private key path is required")
	}
	key, err := loadPrivateKey(config.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	apiBaseURL := config.APIBaseURL
	if apiBaseURL == "" {
		apiBaseURL = defaultAPIBaseURL
	}
	if _, err := url.Parse(apiBaseURL); err != nil {
		return nil, errors.New("github api base url is invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	ttl := config.RepositoriesTTL
	if ttl <= 0 {
		ttl = defaultRepositoriesTTL
	}
	return &InstallationVisibility{
		appID:           config.AppID,
		key:             key,
		apiBaseURL:      strings.TrimRight(apiBaseURL, "/"),
		client:          client,
		now:             now,
		repositoriesTTL: ttl,
		tokens:          map[string]installationToken{},
		repositories:    map[string]cachedRepositories{},
	}, nil
}

// loadPrivateKey reads a PEM RSA private key in either PKCS#1 or PKCS#8 form.
// The error names the path only; it never contains key material.
func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read github app private key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("github app private key is not PEM encoded")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("github app private key is not a valid PKCS#1 RSA key")
		}
		return key, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("github app private key is not a valid PKCS#8 key")
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("github app private key must be an RSA key")
		}
		return key, nil
	default:
		return nil, errors.New("github app private key is not an RSA private key")
	}
}

// RepositoryVisible implements workspace.RepositoryVisibility. It answers only
// from the installation's own repository list; any failure is returned as an
// error so the caller keeps the frozen fail-closed mapping.
func (v *InstallationVisibility) RepositoryVisible(ctx context.Context, installationID, repositoryID string) error {
	if !externalIDPattern.MatchString(installationID) || !externalIDPattern.MatchString(repositoryID) {
		return errors.New("installation and repository ids must be decimal")
	}
	ids, err := v.installationRepositories(ctx, installationID)
	if err != nil {
		return err
	}
	if !ids[repositoryID] {
		return fmt.Errorf("installation %s does not see repository %s", installationID, repositoryID)
	}
	return nil
}

func (v *InstallationVisibility) installationRepositories(ctx context.Context, installationID string) (map[string]bool, error) {
	if cached, ok := v.cachedRepositories(installationID); ok {
		return cached, nil
	}
	token, err := v.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	ids, err := v.fetchRepositories(ctx, token)
	if err != nil {
		return nil, err
	}
	v.mutex.Lock()
	v.repositories[installationID] = cachedRepositories{ids: ids, observed: v.now()}
	v.mutex.Unlock()
	return ids, nil
}

func (v *InstallationVisibility) cachedRepositories(installationID string) (map[string]bool, bool) {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	cached, ok := v.repositories[installationID]
	if !ok || v.now().Sub(cached.observed) >= v.repositoriesTTL {
		return nil, false
	}
	return cached.ids, true
}

func (v *InstallationVisibility) installationToken(ctx context.Context, installationID string) (string, error) {
	v.mutex.Lock()
	cached, ok := v.tokens[installationID]
	now := v.now()
	v.mutex.Unlock()
	if ok && now.Before(cached.expires.Add(-tokenRefreshMargin)) {
		return cached.value, nil
	}
	jwt, err := v.appJWT(now)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		v.apiBaseURL+"/app/installations/"+installationID+"/access_tokens", nil)
	if err != nil {
		return "", errors.New("create installation token request")
	}
	request.Header.Set("Authorization", "Bearer "+jwt)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := v.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request installation token: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseBodyCap))
	if err != nil {
		return "", errors.New("read installation token response")
	}
	// GitHub documents 201 for a minted installation token; 200 is accepted
	// as the same success so a documented-compatible gateway is not treated
	// as a refusal, while every non-success keeps the fail-closed answer.
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("installation token refused with status %d", response.StatusCode)
	}
	var parsed struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Token == "" {
		return "", errors.New("installation token response is malformed")
	}
	expires := parsed.ExpiresAt
	if expires.IsZero() {
		expires = now.Add(time.Hour)
	}
	v.mutex.Lock()
	v.tokens[installationID] = installationToken{value: parsed.Token, expires: expires}
	v.mutex.Unlock()
	return parsed.Token, nil
}

func (v *InstallationVisibility) fetchRepositories(ctx context.Context, token string) (map[string]bool, error) {
	ids := map[string]bool{}
	for page := 1; page <= repositoryPageLimit; page++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s/installation/repositories?per_page=%d&page=%d", v.apiBaseURL, repositoryPageSize, page), nil)
		if err != nil {
			return nil, errors.New("create installation repositories request")
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Accept", "application/vnd.github+json")
		response, err := v.client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("request installation repositories: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, responseBodyCap))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, errors.New("read installation repositories response")
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("installation repositories refused with status %d", response.StatusCode)
		}
		var parsed struct {
			TotalCount   int `json:"total_count"`
			Repositories []struct {
				ID int64 `json:"id"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, errors.New("installation repositories response is malformed")
		}
		for _, repository := range parsed.Repositories {
			ids[strconv.FormatInt(repository.ID, 10)] = true
		}
		if len(parsed.Repositories) == 0 || len(ids) >= parsed.TotalCount {
			return ids, nil
		}
	}
	return nil, errors.New("installation repository list exceeds the page limit")
}

// appJWT signs the App assertion: RS256 over the frozen claim set, with a
// backward-dated iat and a lifetime inside GitHub's maximum.
func (v *InstallationVisibility) appJWT(now time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", errors.New("encode jwt header")
	}
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-jwtClockSkew).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": v.appID,
	})
	if err != nil {
		return "", errors.New("encode jwt claims")
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, v.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errors.New("sign jwt")
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
