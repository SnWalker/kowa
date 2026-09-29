package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const responseBodyLimit = 1 << 20

// GitHubOAuthConfig configures GitHub App user authorization endpoints.
type GitHubOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AuthorizeURL string
	TokenURL     string
	UserURL      string
	HTTPClient   *http.Client
}

// GitHubOAuthClient exchanges an OAuth code and keeps the user access token
// inside the adapter boundary.
type GitHubOAuthClient struct {
	config GitHubOAuthConfig
	client *http.Client
}

func NewGitHubOAuthClient(config GitHubOAuthConfig) (*GitHubOAuthClient, error) {
	if config.ClientID == "" || config.ClientSecret == "" || config.RedirectURL == "" {
		return nil, errors.New("github oauth client configuration is incomplete")
	}
	if config.AuthorizeURL == "" {
		config.AuthorizeURL = "https://github.com/login/oauth/authorize"
	}
	if config.TokenURL == "" {
		config.TokenURL = "https://github.com/login/oauth/access_token"
	}
	if config.UserURL == "" {
		config.UserURL = "https://api.github.com/user"
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHubOAuthClient{config: config, client: client}, nil
}

func (c *GitHubOAuthClient) AuthorizationURL(state, codeChallenge string) string {
	values := url.Values{
		"client_id":             {c.config.ClientID},
		"redirect_uri":          {c.config.RedirectURL},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return c.config.AuthorizeURL + "?" + values.Encode()
}

func (c *GitHubOAuthClient) Exchange(
	ctx context.Context,
	code string,
	codeVerifier string,
) (result GitHubUser, err error) {
	values := url.Values{
		"client_id":     {c.config.ClientID},
		"client_secret": {c.config.ClientSecret},
		"code":          {code},
		"redirect_uri":  {c.config.RedirectURL},
		"code_verifier": {codeVerifier},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return GitHubUser{}, fmt.Errorf("create github token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client.Do(request)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("exchange github oauth code: %w", errors.Join(ErrResourceUnavailable, err))
	}
	defer func() {
		err = errors.Join(err, response.Body.Close())
	}()
	tokenBody, err := readResponseBody(response.Body)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("read github token response: %w", errors.Join(ErrResourceUnavailable, err))
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= http.StatusInternalServerError {
			return GitHubUser{}, fmt.Errorf("github token endpoint status %d: %w", response.StatusCode, ErrResourceUnavailable)
		}
		return GitHubUser{}, ErrUnauthorized
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(tokenBody, &tokenResponse); err != nil {
		return GitHubUser{}, fmt.Errorf("decode github token response: %w", errors.Join(ErrResourceUnavailable, err))
	}
	if tokenResponse.Error != "" || tokenResponse.AccessToken == "" {
		return GitHubUser{}, ErrUnauthorized
	}

	userRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, c.config.UserURL, nil)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("create github user request: %w", err)
	}
	userRequest.Header.Set("Accept", "application/vnd.github+json")
	userRequest.Header.Set("Authorization", "Bearer "+tokenResponse.AccessToken)
	userResponse, err := c.client.Do(userRequest)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("read github user: %w", errors.Join(ErrResourceUnavailable, err))
	}
	defer func() {
		err = errors.Join(err, userResponse.Body.Close())
	}()
	userBody, err := readResponseBody(userResponse.Body)
	if err != nil {
		return GitHubUser{}, fmt.Errorf("read github user response: %w", errors.Join(ErrResourceUnavailable, err))
	}
	if userResponse.StatusCode != http.StatusOK {
		if userResponse.StatusCode == http.StatusUnauthorized || userResponse.StatusCode == http.StatusForbidden {
			return GitHubUser{}, ErrUnauthorized
		}
		return GitHubUser{}, fmt.Errorf("github user endpoint status %d: %w", userResponse.StatusCode, ErrResourceUnavailable)
	}
	var user struct {
		ID    json.Number `json:"id"`
		Login string      `json:"login"`
	}
	userDecoder := json.NewDecoder(strings.NewReader(string(userBody)))
	userDecoder.UseNumber()
	if err := userDecoder.Decode(&user); err != nil {
		return GitHubUser{}, fmt.Errorf("decode github user response: %w", errors.Join(ErrResourceUnavailable, err))
	}
	if _, err := strconv.ParseUint(user.ID.String(), 10, 64); err != nil || user.Login == "" {
		return GitHubUser{}, fmt.Errorf("github user response is invalid: %w", ErrResourceUnavailable)
	}
	return GitHubUser{ID: user.ID.String(), Login: user.Login}, nil
}

func readResponseBody(body io.Reader) ([]byte, error) {
	contents, readErr := io.ReadAll(io.LimitReader(body, responseBodyLimit+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(contents) > responseBodyLimit {
		return nil, errors.New("github response body exceeds limit")
	}
	return contents, nil
}
