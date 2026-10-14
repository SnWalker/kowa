// Command webharness runs the real control-plane composition root for
// browser-level acceptance of frontend:S01.
//
// It is test-only and contains no product behaviour: it starts the unchanged
// `internal/server` graph, substitutes the external adapters the acceptance
// boundary isolates (a local GitHub OAuth stub and a controlled repository
// visibility adapter), and serves the built Web app plus the mounted API from
// one HTTPS origin with a certificate generated at start time. No private key,
// token or credential is written to the repository.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/server"
	"github.com/SnWalker/kowa/internal/workspace"
	"go.uber.org/fx"
)

const (
	defaultWebAddress  = "127.0.0.1:8443"
	defaultStubAddress = "127.0.0.1:8099"
	readTimeout        = 5 * time.Second
	firstGitHubID      = "101"
	firstGitHubLogin   = "member-a"
	secondGitHubID     = "202"
	secondGitHubLogin  = "member-b"
)

// readOnlyRepositoryID is the repository the controlled facts adapter reports
// without push permission, so the projection can show a real denial instead of
// a substituted success.
const readOnlyRepositoryID = "3002"

func main() {
	if err := run(); err != nil {
		log.Fatalf("webharness: %v", err)
	}
}

type options struct {
	webOrigin    string
	webAddress   string
	stubOrigin   string
	distDir      string
	database     string
	clientID     string
	clientSecret string
	runtimeToken string
	runtimeID    string
}

func loadOptions() (options, error) {
	webAddress := envOr("KOWA_HARNESS_ADDR", defaultWebAddress)
	stubAddress := envOr("KOWA_HARNESS_STUB_ADDR", defaultStubAddress)
	database := os.Getenv("KOWA_DATABASE_URL")
	if database == "" {
		return options{}, errors.New("KOWA_DATABASE_URL is required")
	}
	return options{
		webOrigin:    "https://" + webAddress,
		webAddress:   webAddress,
		stubOrigin:   "http://" + stubAddress,
		distDir:      envOr("KOWA_HARNESS_DIST", "dist"),
		database:     database,
		clientID:     envOr("KOWA_HARNESS_CLIENT_ID", "harness-client-id"),
		clientSecret: envOr("KOWA_HARNESS_CLIENT_SECRET", "harness-client-secret"),
		runtimeToken: envOr("KOWA_HARNESS_RUNTIME_TOKEN", "harness-runtime-token-0123456789"),
		runtimeID:    envOr("KOWA_HARNESS_RUNTIME_ID", "runtime-harness"),
	}, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func run() error {
	opts, err := loadOptions()
	if err != nil {
		return err
	}
	stub := newGitHubStub(opts)
	stubListener, err := net.Listen("tcp", opts.stubOrigin[len("http://"):])
	if err != nil {
		return fmt.Errorf("listen on stub address: %w", err)
	}
	defer func() { _ = stubListener.Close() }()

	certificate, err := selfSignedCertificate(strings.TrimPrefix(opts.webOrigin, "https://"))
	if err != nil {
		return err
	}

	var controlPlane *http.Server
	app := server.New(
		fx.Replace(
			config.Server{Address: "127.0.0.1:0"},
			config.Identity{
				Enabled:       true,
				DatabaseURL:   opts.database,
				TrustedOrigin: opts.webOrigin,
				ClientID:      opts.clientID,
				ClientSecret:  opts.clientSecret,
				AdminIDs:      []string{firstGitHubID, secondGitHubID},
			},
			config.Runtime{
				Enabled:     true,
				DatabaseURL: opts.database,
				Credentials: map[string]string{opts.runtimeID: opts.runtimeToken},
			},
		),
		fx.Decorate(func(identity.OAuthClient) (identity.OAuthClient, error) {
			return identity.NewGitHubOAuthClient(identity.GitHubOAuthConfig{
				ClientID:     opts.clientID,
				ClientSecret: opts.clientSecret,
				RedirectURL:  opts.webOrigin + "/api/v2/auth/github/callback",
				AuthorizeURL: opts.stubOrigin + "/stub/github/authorize",
				TokenURL:     opts.stubOrigin + "/stub/github/token",
				UserURL:      opts.stubOrigin + "/stub/github/user",
				HTTPClient:   &http.Client{Timeout: readTimeout},
			})
		}),
		fx.Decorate(func(workspace.RepositoryVisibility) workspace.RepositoryVisibility {
			return harnessVisibility{}
		}),
		fx.Decorate(func(original *gitremote.Service, ops *runtimeops.Service) *gitremote.Service {
			return gitremote.NewService(harnessFacts{}, harnessIdentities{ops: ops}, harnessCommandRunner, nil, time.Now)
		}),
		fx.Populate(&controlPlane),
	)
	if err := app.Err(); err != nil {
		return err
	}
	startContext := context.Background()
	if err := app.Start(startContext); err != nil {
		return err
	}
	defer func() {
		_ = app.Stop(context.Background())
	}()

	static := spaHandler(controlPlane.Handler, opts.distDir)
	webServer := &http.Server{Addr: opts.webAddress, Handler: static, ReadHeaderTimeout: readTimeout}
	stubServer := &http.Server{Addr: opts.stubOrigin, Handler: stub.handler(), ReadHeaderTimeout: readTimeout}
	webListener, err := tls.Listen("tcp", opts.webAddress, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("listen on web address: %w", err)
	}
	defer func() { _ = webListener.Close() }()

	go func() { _ = stubServer.Serve(stubListener) }()
	go func() {
		if serveErr := webServer.Serve(webListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("webharness: web server stopped: %v", serveErr)
		}
	}()
	log.Printf("webharness: web=%s stub=%s", opts.webOrigin, opts.stubOrigin)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownContext, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	_ = webServer.Shutdown(shutdownContext)
	_ = stubServer.Shutdown(shutdownContext)
	return nil
}

// spaHandler serves the built app with the SPA fallback the deployment contract
// requires and routes API paths to the real mounted control plane.
func spaHandler(api http.Handler, distDir string) http.Handler {
	files := http.FileServer(http.Dir(distDir))
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") || request.URL.Path == "/healthz" {
			api.ServeHTTP(writer, request)
			return
		}
		cleaned := filepath.Clean("/" + request.URL.Path)
		if info, err := os.Stat(filepath.Join(distDir, cleaned)); err == nil && !info.IsDir() {
			files.ServeHTTP(writer, request)
			return
		}
		http.ServeFile(writer, request, filepath.Join(distDir, "index.html"))
	})
}

// harnessVisibility is the controlled installation adapter: repositories are
// visible except for a marker id, which fails closed exactly like an
// unverifiable installation observation.
type harnessVisibility struct{}

func (harnessVisibility) RepositoryVisible(_ context.Context, _, repositoryID string) error {
	if repositoryID == "999999" {
		return workspace.ErrResourceUnavailable
	}
	return nil
}

// harnessFacts answers the GitHub read facts without touching the network; the
// permission answer is derived from the repository id so a denial is a real
// reported denial rather than a missing fact.
type harnessFacts struct{}

func (harnessFacts) RepositoryFullName(_ context.Context, repositoryID string) (string, string, error) {
	return "kowa-sandbox/repo-" + repositoryID, firstGitHubID, nil
}

func (harnessFacts) PushPermission(_ context.Context, repositoryID string) (bool, string, error) {
	return repositoryID != readOnlyRepositoryID, firstGitHubID, nil
}

// harnessIdentities reads registered machine identities through the real
// runtime-operations projection, so authorization changes take effect here too.
type harnessIdentities struct{ ops *runtimeops.Service }

func (i harnessIdentities) MachineIdentities(ctx context.Context, workspaceID string) ([]string, []string, error) {
	if i.ops == nil {
		return nil, nil, nil
	}
	views, err := i.ops.WorkspaceRuntimes(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	runtimeIDs := make([]string, 0, len(views))
	userIDs := make([]string, 0, len(views))
	seen := map[string]bool{}
	for _, view := range views {
		runtimeIDs = append(runtimeIDs, view.RuntimeID)
		if view.GitHubUserID != "" && !seen[view.GitHubUserID] {
			seen[view.GitHubUserID] = true
			userIDs = append(userIDs, view.GitHubUserID)
		}
	}
	return runtimeIDs, userIDs, nil
}

func harnessCommandRunner(context.Context, gitremote.CommandSpec) (gitremote.CommandOutcome, error) {
	return gitremote.CommandOutcome{ExitCode: 0}, nil
}

// githubStub plays the external OAuth provider. It is isolated test plumbing:
// the authorization code carries the identity, the token endpoint echoes it and
// the user endpoint answers with the numeric GitHub ids used by the fixtures.
type githubStub struct {
	origin string
	mu     sync.Mutex
	login  string
}

func newGitHubStub(opts options) *githubStub {
	return &githubStub{origin: opts.stubOrigin, login: firstGitHubLogin}
}

var stubIdentities = map[string]string{
	firstGitHubLogin:  firstGitHubID,
	secondGitHubLogin: secondGitHubID,
}

func (s *githubStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stub/github/authorize", func(writer http.ResponseWriter, request *http.Request) {
		redirect := request.URL.Query().Get("redirect_uri")
		state := request.URL.Query().Get("state")
		if redirect == "" || state == "" {
			http.Error(writer, "missing redirect_uri or state", http.StatusBadRequest)
			return
		}
		target := fmt.Sprintf("%s?code=%s&state=%s", redirect, s.currentLogin(), state)
		http.Redirect(writer, request, target, http.StatusFound)
	})
	mux.HandleFunc("POST /stub/github/token", func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			http.Error(writer, "invalid form", http.StatusBadRequest)
			return
		}
		if request.Form.Get("code_verifier") == "" {
			http.Error(writer, "missing PKCE verifier", http.StatusBadRequest)
			return
		}
		writeStubJSON(writer, http.StatusOK, map[string]string{
			"access_token": request.Form.Get("code"), "token_type": "bearer",
		})
	})
	mux.HandleFunc("GET /stub/github/user", func(writer http.ResponseWriter, request *http.Request) {
		login := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		id, known := stubIdentities[login]
		if !known {
			writeStubJSON(writer, http.StatusUnauthorized, map[string]string{"error": "unknown token"})
			return
		}
		writeStubJSON(writer, http.StatusOK, map[string]string{"id": id, "login": login})
	})
	// The acceptance runner selects the identity of the next login here; the
	// GitHub App browser flow itself stays untouched.
	mux.HandleFunc("POST /stub/identity", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Login string `json:"login"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid body", http.StatusBadRequest)
			return
		}
		if _, known := stubIdentities[body.Login]; !known {
			http.Error(writer, "unknown identity", http.StatusBadRequest)
			return
		}
		s.setLogin(body.Login)
		writeStubJSON(writer, http.StatusOK, map[string]string{"login": body.Login})
	})
	mux.HandleFunc("GET /stub/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeStubJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func (s *githubStub) currentLogin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.login
}

func (s *githubStub) setLogin(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.login = login
}

func writeStubJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// selfSignedCertificate generates a short-lived certificate for one loopback
// host at start time; nothing is persisted.
func selfSignedCertificate(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "kowa-webharness"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certificatePEM, keyPEM)
}
