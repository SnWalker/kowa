package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGitHubOAuthClient_ExchangeKeepsTokenInsideAdapter(t *testing.T) {
	t.Parallel()

	const accessToken = "sensitive-user-token"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if request.Form.Get("code_verifier") != "pkce-verifier" {
				t.Fatalf("code_verifier = %q", request.Form.Get("code_verifier"))
			}
			_ = json.NewEncoder(writer).Encode(map[string]string{"access_token": accessToken, "token_type": "bearer"})
		case "/user":
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": 123456789012345, "login": "member-a"})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewGitHubOAuthClient(GitHubOAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://localhost/callback",
		AuthorizeURL: server.URL + "/authorize",
		TokenURL:     server.URL + "/token",
		UserURL:      server.URL + "/user",
		HTTPClient:   server.Client(),
	})
	if err != nil {
		t.Fatalf("NewGitHubOAuthClient() error = %v", err)
	}

	authorizationURL, err := url.Parse(client.AuthorizationURL("state", "challenge"))
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if authorizationURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", authorizationURL.Query().Get("code_challenge_method"))
	}
	user, err := client.Exchange(context.Background(), "code", "pkce-verifier")
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if user.ID != "123456789012345" || user.Login != "member-a" {
		t.Fatalf("Exchange() = %#v", user)
	}
}
