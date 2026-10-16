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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is a stand-in for the GitHub App API. It verifies the App JWT
// signature before issuing a token, so a broken signing path is a test
// failure rather than a silently accepted request.
type fakeGitHub struct {
	t *testing.T

	mutex          sync.Mutex
	tokenRequests  int
	repositoryHits int

	key            *rsa.PrivateKey
	repositories   []int64
	tokenStatus    int
	repoStatus     int
	malformedToken bool
	malformedRepos bool
	issuedToken    string
	tokenLifetime  time.Duration
}

func newFakeGitHub(t *testing.T, repositories ...int64) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	fake := &fakeGitHub{
		t:             t,
		key:           key,
		repositories:  repositories,
		tokenStatus:   http.StatusCreated,
		repoStatus:    http.StatusOK,
		issuedToken:   "ghs_installation-token-placeholder-value",
		tokenLifetime: time.Hour,
	}
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/", func(writer http.ResponseWriter, request *http.Request) {
		f.mutex.Lock()
		f.tokenRequests++
		f.mutex.Unlock()
		if !strings.HasSuffix(request.URL.Path, "/access_tokens") {
			http.NotFound(writer, request)
			return
		}
		f.verifyJWT(request.Header.Get("Authorization"))
		if f.tokenStatus != http.StatusCreated {
			writer.WriteHeader(f.tokenStatus)
			return
		}
		if f.malformedToken {
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"unexpected":true}`))
			return
		}
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"token":      f.issuedToken,
			"expires_at": time.Now().Add(f.tokenLifetime).UTC(),
		})
	})
	mux.HandleFunc("/installation/repositories", func(writer http.ResponseWriter, request *http.Request) {
		f.mutex.Lock()
		f.repositoryHits++
		f.mutex.Unlock()
		if request.Header.Get("Authorization") != "Bearer "+f.issuedToken {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.repoStatus != http.StatusOK {
			writer.WriteHeader(f.repoStatus)
			return
		}
		if f.malformedRepos {
			_, _ = writer.Write([]byte(`{"repositories": "not-a-list"}`))
			return
		}
		page := 1
		if raw := request.URL.Query().Get("page"); raw != "" {
			_, _ = fmt.Sscanf(raw, "%d", &page)
		}
		size := repositoryPageSize
		start := (page - 1) * size
		end := start + size
		if start > len(f.repositories) {
			start = len(f.repositories)
		}
		if end > len(f.repositories) {
			end = len(f.repositories)
		}
		pageIDs := f.repositories[start:end]
		repositories := make([]map[string]any, 0, len(pageIDs))
		for _, id := range pageIDs {
			repositories = append(repositories, map[string]any{"id": id, "full_name": "sandbox/repo"})
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"total_count":  len(f.repositories),
			"repositories": repositories,
		})
	})
	return mux
}

func (f *fakeGitHub) verifyJWT(header string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if !strings.HasPrefix(header, "Bearer ") {
		f.t.Errorf("token request carries no bearer assertion")
		return
	}
	assertion := strings.TrimPrefix(header, "Bearer ")
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		f.t.Errorf("assertion is not a three-part JWT")
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		f.t.Errorf("assertion signature is not base64url: %v", err)
		return
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&f.key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
		f.t.Errorf("assertion signature does not verify: %v", err)
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		f.t.Errorf("assertion claims are not base64url: %v", err)
		return
	}
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		f.t.Errorf("assertion claims are not JSON: %v", err)
		return
	}
	if claims.Iss != "20260101" {
		f.t.Errorf("assertion issuer is %q", claims.Iss)
	}
	if claims.Exp-claims.Iat > 10*60 {
		f.t.Errorf("assertion lifetime %ds exceeds the platform maximum", claims.Exp-claims.Iat)
	}
}

func (f *fakeGitHub) counts() (int, int) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.tokenRequests, f.repositoryHits
}

// writeKey stores a one-time key in a temporary directory and returns its path.
func writeKey(t *testing.T, key *rsa.PrivateKey, pkcs8 bool) string {
	t.Helper()
	var block *pem.Block
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatalf("marshal pkcs8: %v", err)
		}
		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	} else {
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	}
	path := filepath.Join(t.TempDir(), "app-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func newVisibility(t *testing.T, fake *fakeGitHub, server *httptest.Server, now func() time.Time) *InstallationVisibility {
	t.Helper()
	visibility, err := NewInstallationVisibility(Config{
		AppID:          "20260101",
		PrivateKeyPath: writeKey(t, fake.key, false),
		APIBaseURL:     server.URL,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("new installation visibility: %v", err)
	}
	return visibility
}

func TestTheInstallationConfirmsARepositoryItSees(t *testing.T) {
	fake, server := newFakeGitHub(t, 1001, 1002)
	visibility := newVisibility(t, fake, server, time.Now)

	if err := visibility.RepositoryVisible(context.Background(), "77", "1002"); err != nil {
		t.Fatalf("visible repository refused: %v", err)
	}
}

func TestARepositoryOutsideTheInstallationIsRefused(t *testing.T) {
	fake, server := newFakeGitHub(t, 1001)
	visibility := newVisibility(t, fake, server, time.Now)

	err := visibility.RepositoryVisible(context.Background(), "77", "9999")
	if err == nil {
		t.Fatal("a repository the installation does not see was accepted")
	}
	if !strings.Contains(err.Error(), "does not see") {
		t.Fatalf("refusal does not name the observed fact: %v", err)
	}
}

func TestTheRepositoryListIsPaginated(t *testing.T) {
	ids := make([]int64, 0, 150)
	for id := int64(1); id <= 150; id++ {
		ids = append(ids, id)
	}
	fake, server := newFakeGitHub(t, ids...)
	visibility := newVisibility(t, fake, server, time.Now)

	if err := visibility.RepositoryVisible(context.Background(), "77", "150"); err != nil {
		t.Fatalf("repository on the second page refused: %v", err)
	}
}

func TestTheTokenAndTheRepositoryListAreCachedWithinTheirWindow(t *testing.T) {
	fake, server := newFakeGitHub(t, 1001)
	now := time.Now()
	visibility := newVisibility(t, fake, server, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if err := visibility.RepositoryVisible(context.Background(), "77", "1001"); err != nil {
			t.Fatalf("call %d refused: %v", i, err)
		}
	}
	tokens, repositories := fake.counts()
	if tokens != 1 || repositories != 1 {
		t.Fatalf("cached window issued %d tokens and %d repository reads, want 1 and 1", tokens, repositories)
	}

	now = now.Add(defaultRepositoriesTTL + time.Second)
	if err := visibility.RepositoryVisible(context.Background(), "77", "1001"); err != nil {
		t.Fatalf("call after the repository window refused: %v", err)
	}
	tokens, repositories = fake.counts()
	if tokens != 1 {
		t.Fatalf("the installation token was re-minted inside its lifetime: %d", tokens)
	}
	if repositories != 2 {
		t.Fatalf("the repository list was not re-read after its window: %d", repositories)
	}

	now = now.Add(time.Hour)
	if err := visibility.RepositoryVisible(context.Background(), "77", "1001"); err != nil {
		t.Fatalf("call after token expiry refused: %v", err)
	}
	if tokens, _ = fake.counts(); tokens != 2 {
		t.Fatalf("an expired token was reused: %d token requests", tokens)
	}
}

func TestEveryFailureKeepsTheFrozenFailClosedAnswer(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		setup func(*fakeGitHub)
	}{
		{"token refused", func(f *fakeGitHub) { f.tokenStatus = http.StatusUnauthorized }},
		{"token malformed", func(f *fakeGitHub) { f.malformedToken = true }},
		{"repositories refused", func(f *fakeGitHub) { f.repoStatus = http.StatusInternalServerError }},
		{"repositories malformed", func(f *fakeGitHub) { f.malformedRepos = true }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fake, server := newFakeGitHub(t, 1001)
			testCase.setup(fake)
			visibility := newVisibility(t, fake, server, func() time.Time { return now })
			if err := visibility.RepositoryVisible(context.Background(), "77", "1001"); err == nil {
				t.Fatal("a failed observation was accepted as visibility")
			}
		})
	}

	t.Run("unreachable api", func(t *testing.T) {
		fake, server := newFakeGitHub(t, 1001)
		server.Close()
		visibility := newVisibility(t, fake, server, func() time.Time { return now })
		if err := visibility.RepositoryVisible(context.Background(), "77", "1001"); err == nil {
			t.Fatal("an unreachable api was accepted as visibility")
		}
	})

	t.Run("malformed identifiers", func(t *testing.T) {
		fake, server := newFakeGitHub(t, 1001)
		visibility := newVisibility(t, fake, server, func() time.Time { return now })
		for _, ids := range [][2]string{{"0", "1001"}, {"77", "0"}, {"x", "1001"}, {"77", "-1"}, {"", "1001"}} {
			if err := visibility.RepositoryVisible(context.Background(), ids[0], ids[1]); err == nil {
				t.Fatalf("identifiers %v were accepted", ids)
			}
		}
	})
}

func TestTheSeamRefusesToStartWithoutAUsableKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "absent.pem")
	notPEM := filepath.Join(t.TempDir(), "not-pem.pem")
	if err := os.WriteFile(notPEM, []byte("not a key"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	ecKeyBlock := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("broken")}
	broken := filepath.Join(t.TempDir(), "broken.pem")
	if err := os.WriteFile(broken, pem.EncodeToMemory(ecKeyBlock), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	cases := []struct {
		name   string
		config Config
	}{
		{"no app id", Config{PrivateKeyPath: writeKey(t, key, false)}},
		{"non-decimal app id", Config{AppID: "Iv1.abc", PrivateKeyPath: writeKey(t, key, false)}},
		{"no key path", Config{AppID: "20260101"}},
		{"missing key file", Config{AppID: "20260101", PrivateKeyPath: missing}},
		{"not pem", Config{AppID: "20260101", PrivateKeyPath: notPEM}},
		{"broken key", Config{AppID: "20260101", PrivateKeyPath: broken}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := NewInstallationVisibility(testCase.config); err == nil {
				t.Fatal("a configuration without a usable key was accepted")
			}
		})
	}
}

func TestAPKCS8KeyIsAccepted(t *testing.T) {
	fake, server := newFakeGitHub(t, 1001)
	visibility, err := NewInstallationVisibility(Config{
		AppID:          "20260101",
		PrivateKeyPath: writeKey(t, fake.key, true),
		APIBaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("pkcs8 key refused: %v", err)
	}
	if err := visibility.RepositoryVisible(context.Background(), "77", "1001"); err != nil {
		t.Fatalf("call refused: %v", err)
	}
}

// The private key, the minted token and the signed assertion are deployment
// secrets; nothing this seam produces may carry them into an error string.
func TestErrorsNeverCarryKeyMaterialOrTokens(t *testing.T) {
	fake, server := newFakeGitHub(t, 1001)
	fake.tokenStatus = http.StatusUnauthorized
	keyPath := writeKey(t, fake.key, false)
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	visibility, err := NewInstallationVisibility(Config{
		AppID:          "20260101",
		PrivateKeyPath: keyPath,
		APIBaseURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("new installation visibility: %v", err)
	}
	refusal := visibility.RepositoryVisible(context.Background(), "77", "1001")
	if refusal == nil {
		t.Fatal("a refused token was accepted")
	}
	message := refusal.Error()
	for name, secret := range map[string]string{
		"key body":     strings.TrimSpace(strings.SplitAfter(string(keyPEM), "-----")[2]),
		"issued token": fake.issuedToken,
	} {
		if strings.Contains(message, secret) {
			t.Fatalf("refusal leaks the %s", name)
		}
	}
	for _, fragment := range []string{"PRIVATE KEY", "Bearer ", "eyJ"} {
		if strings.Contains(message, fragment) {
			t.Fatalf("refusal carries key or assertion material: %q", message)
		}
	}
}

// A construction error names the path but never the key material.
func TestConstructionErrorsNeverCarryKeyMaterial(t *testing.T) {
	_, err := NewInstallationVisibility(Config{AppID: "20260101", PrivateKeyPath: filepath.Join(t.TempDir(), "absent.pem")})
	if err == nil {
		t.Fatal("missing key was accepted")
	}
	if !strings.Contains(err.Error(), "absent.pem") {
		t.Fatalf("error does not name the path: %v", err)
	}
}
