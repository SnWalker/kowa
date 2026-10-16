// Command webharness runs the real control-plane composition root for
// browser-level acceptance of frontend:S01 and frontend:S02.
//
// It is test-only and contains no product behaviour: it starts the unchanged
// `internal/server` graph, substitutes the external adapters the acceptance
// boundary isolates (a local GitHub OAuth stub and a controlled repository
// visibility adapter), and serves the built Web app plus the mounted API from
// one HTTPS origin with a certificate generated at start time. For frontend:S02
// it also builds the local bare repositories the frozen admission materialises
// (project plus three knowledge variants) and publishes their ids over the
// stub. No private key, token or credential is written to the repository.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/SnWalker/kowa/internal/fixedflow"
	"github.com/SnWalker/kowa/internal/gitremote"
	"github.com/SnWalker/kowa/internal/identity"
	"github.com/SnWalker/kowa/internal/platform/config"
	"github.com/SnWalker/kowa/internal/runtimeops"
	"github.com/SnWalker/kowa/internal/server"
	"github.com/SnWalker/kowa/internal/workflow"
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

// Controlled repositories of the S02 acceptance boundary. The project and the
// knowledge repository are real local bare repositories the frozen admission
// materialises; the empty variant proves that a successful read of an empty
// index is not a read failure, and the unavailable id stays out of the
// registry so a missing source is refused instead of guessed.
const (
	harnessProjectRepository     = "9001"
	harnessKnowledgeRepository   = "9002"
	harnessEmptyKnowledgeRepo    = "9003"
	harnessUnavailableRepository = "9004"
	harnessInstallationID        = "1"
)

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
	// repositories are the controlled local bare repositories of the S02
	// acceptance boundary plus the materialisation cache root the control plane
	// reads them through.
	materializeRoot string
	repositories    map[string]string
	unavailableID   string
}

func loadOptions() (options, error) {
	webAddress := envOr("KOWA_HARNESS_ADDR", defaultWebAddress)
	stubAddress := envOr("KOWA_HARNESS_STUB_ADDR", defaultStubAddress)
	database := os.Getenv("KOWA_DATABASE_URL")
	if database == "" {
		return options{}, errors.New("KOWA_DATABASE_URL is required")
	}
	return options{
		webOrigin:     "https://" + webAddress,
		webAddress:    webAddress,
		stubOrigin:    "http://" + stubAddress,
		distDir:       envOr("KOWA_HARNESS_DIST", "dist"),
		database:      database,
		clientID:      envOr("KOWA_HARNESS_CLIENT_ID", "harness-client-id"),
		clientSecret:  envOr("KOWA_HARNESS_CLIENT_SECRET", "harness-client-secret"),
		runtimeToken:  envOr("KOWA_HARNESS_RUNTIME_TOKEN", "harness-runtime-token-0123456789"),
		runtimeID:     envOr("KOWA_HARNESS_RUNTIME_ID", "runtime-harness"),
		unavailableID: harnessUnavailableRepository,
	}, nil
}

// buildRepositories creates the controlled bare repositories the frozen
// admission resolves its baseline from: a project repository and three
// knowledge variants (a frozen index, a successfully read empty index, and a
// repository the registry deliberately does not know).
func buildRepositories(root string) (map[string]string, error) {
	project, err := buildRepository(root, "project", map[string]string{"README.md": "# harness project\n"})
	if err != nil {
		return nil, err
	}
	design := "The project uses small deterministic helpers.\n"
	api := "The delivery API is frozen per repository role.\n"
	runbook := "Operators keep one runbook per service.\n"
	index := func(entries string) string {
		return fmt.Sprintf(`{"schemaVersion":"kowa.web-application.v1","entries":[%s]}`, entries)
	}
	entry := func(path, content, scope string, required bool) string {
		return fmt.Sprintf(`{"path":%q,"digest":%q,"scope":%q,"required":%t}`,
			path, digestOf(content), scope, required)
	}
	knowledge, err := buildRepository(root, "knowledge", map[string]string{
		"docs/design.md": design,
		"docs/api.md":    api,
		"ops/runbook.md": runbook,
		"kowa-index.json": index(strings.Join([]string{
			entry("docs/design.md", design, "design", true),
			entry("docs/api.md", api, "api", false),
			entry("ops/runbook.md", runbook, "operations", false),
		}, ",")),
	})
	if err != nil {
		return nil, err
	}
	empty, err := buildRepository(root, "knowledge-empty", map[string]string{
		"kowa-index.json": index(""),
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{
		harnessProjectRepository:   project,
		harnessKnowledgeRepository: knowledge,
		harnessEmptyKnowledgeRepo:  empty,
	}, nil
}

func buildRepository(root, name string, files map[string]string) (string, error) {
	remote := filepath.Join(root, name+".git")
	if _, err := runGit("", "init", "--quiet", "--bare", "--initial-branch=main", remote); err != nil {
		return "", err
	}
	work := filepath.Join(root, name+".work")
	if _, err := runGit("", "clone", "--quiet", remote, work); err != nil {
		return "", err
	}
	for path, content := range files {
		target := filepath.Join(work, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return "", err
		}
	}
	if _, err := runGit(work, "add", "--all"); err != nil {
		return "", err
	}
	if _, err := runGit(work, "-c", "user.email=harness@example.com", "-c", "user.name=harness",
		"commit", "--quiet", "-m", "harness "+name); err != nil {
		return "", err
	}
	if _, err := runGit(work, "push", "--quiet", "origin", "HEAD:refs/heads/main"); err != nil {
		return "", err
	}
	return remote, nil
}

func runGit(directory string, arguments ...string) (string, error) {
	command := exec.Command("git", arguments...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output), nil
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// runtimeCredentials binds the configured runtime plus one engine-scoped
// runtime per browser project. Each engine's journey drives its own runtime and
// its own token (the transport binds a token to exactly one runtime id), so an
// observation one engine reported can never change another engine's freshness
// assertions.
func runtimeCredentials(opts options) map[string]string {
	credentials := map[string]string{opts.runtimeID: opts.runtimeToken}
	for _, engine := range []string{"chromium", "firefox", "webkit"} {
		credentials[opts.runtimeID+"-"+engine] = opts.runtimeToken + "-" + engine
		// frontend:S02 drives its own runtime per engine: its repeated
		// registrations must never supersede the epoch of the runtime journey
		// that frontend:S01 asserts on.
		credentials[opts.runtimeID+"-"+engine+"-s02"] = opts.runtimeToken + "-" + engine + "-s02"
		// frontend:S04 drives the run view's own runtime for the same reason: its
		// journeys admit a run, and a shared runtime would make one stage's
		// registration supersede another's epoch.
		credentials[opts.runtimeID+"-"+engine+"-s04"] = opts.runtimeToken + "-" + engine + "-s04"
		// frontend:S05 admits its own runs too (the human-task journeys start a
		// real run and then answer or cancel it), so it registers an engine-scoped
		// runtime of its own instead of advancing another stage's epoch.
		credentials[opts.runtimeID+"-"+engine+"-s05"] = opts.runtimeToken + "-" + engine + "-s05"
		// frontend:S06 admits its own run for the delivery-evidence journey, for
		// the same reason: a runtime id is bound to exactly one token, so sharing
		// one would let a stage's re-registration supersede another's epoch.
		credentials[opts.runtimeID+"-"+engine+"-s06"] = opts.runtimeToken + "-" + engine + "-s06"
		// frontend:S07 audits every page and admits its own run on the way, so it
		// keeps an engine-scoped runtime too; the same rule applies.
		credentials[opts.runtimeID+"-"+engine+"-s07"] = opts.runtimeToken + "-" + engine + "-s07"
	}
	return credentials
}

// repositoryFixture names one controlled repository the browser may bind.
type repositoryFixture struct {
	RepositoryID   string `json:"repositoryId"`
	InstallationID string `json:"installationId"`
	DisplayName    string `json:"displayName"`
}

type capabilityFixture struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type runtimeFixture struct {
	RuntimeID       string `json:"runtimeId"`
	Epoch           string `json:"epoch"`
	ProviderID      string `json:"providerId"`
	ProviderVersion string `json:"providerVersion"`
}

// acceptanceFacts is what the stub publishes to the acceptance runner: the
// controlled repository ids, the compiled definition the admission freezes and
// the scripted runtime. The runtime token stays in the process environment and
// is never served over HTTP.
type acceptanceFacts struct {
	Project              repositoryFixture `json:"project"`
	Knowledge            repositoryFixture `json:"knowledge"`
	KnowledgeEmpty       repositoryFixture `json:"knowledgeEmpty"`
	KnowledgeUnavailable repositoryFixture `json:"knowledgeUnavailable"`
	Definition           struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
		Digest  string `json:"digest"`
	} `json:"definition"`
	Capabilities []capabilityFixture `json:"capabilities"`
	Runtime      runtimeFixture      `json:"runtime"`
}

func buildHarnessFacts(opts options) (acceptanceFacts, error) {
	plan, err := fixedflow.Compile()
	if err != nil {
		return acceptanceFacts{}, fmt.Errorf("compile the fixed definition: %w", err)
	}
	capabilities := make([]capabilityFixture, 0, len(plan.Nodes))
	seen := map[string]bool{}
	for _, node := range plan.Order {
		current := plan.Nodes[node]
		if current.Type != workflow.NodeCapability || seen[current.Capability.ID] {
			continue
		}
		seen[current.Capability.ID] = true
		capabilities = append(capabilities, capabilityFixture{ID: current.Capability.ID, Version: current.Capability.Version})
	}
	repository := func(id, name string) repositoryFixture {
		return repositoryFixture{RepositoryID: id, InstallationID: harnessInstallationID, DisplayName: name}
	}
	return acceptanceFacts{
		Project:              repository(harnessProjectRepository, "harness/project"),
		Knowledge:            repository(harnessKnowledgeRepository, "harness/knowledge"),
		KnowledgeEmpty:       repository(harnessEmptyKnowledgeRepo, "harness/knowledge-empty"),
		KnowledgeUnavailable: repository(opts.unavailableID, "harness/knowledge-unavailable"),
		Definition: struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
			Digest  string `json:"digest"`
		}{Name: plan.Name, Version: plan.Version, Digest: plan.Digest},
		Capabilities: capabilities,
		Runtime: runtimeFixture{
			RuntimeID: opts.runtimeID, Epoch: "epoch-harness",
			ProviderID: "scripted", ProviderVersion: "scripted-1",
		},
	}, nil
}

func run() error {
	opts, err := loadOptions()
	if err != nil {
		return err
	}
	// The controlled repositories and the rebuildable materialisation cache the
	// real admission reads: both are created at start time and removed on exit.
	opts.materializeRoot, err = os.MkdirTemp("", "kowa-harness-materialize-")
	if err != nil {
		return fmt.Errorf("create the materialisation root: %w", err)
	}
	defer func() { _ = os.RemoveAll(opts.materializeRoot) }()
	repositoriesRoot, err := os.MkdirTemp("", "kowa-harness-repos-")
	if err != nil {
		return fmt.Errorf("create the repository root: %w", err)
	}
	defer func() { _ = os.RemoveAll(repositoriesRoot) }()
	opts.repositories, err = buildRepositories(repositoriesRoot)
	if err != nil {
		return err
	}
	facts, err := buildHarnessFacts(opts)
	if err != nil {
		return err
	}
	stub := newGitHubStub(facts)
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
				Credentials: runtimeCredentials(opts),
				// The S02 admission resolves its frozen baselines from these
				// local repositories and materialises them under this root; the
				// reconciler advances the knowledge step promptly so the browser
				// journey does not wait on the deployment default.
				MaterializeRoot:    opts.materializeRoot,
				RepositoryRegistry: opts.repositories,
				ReconcileInterval:  200 * time.Millisecond,
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
//
// The identity of a login is selected per browser context through the
// stubIdentityCookie the acceptance runner sets on the stub origin, so parallel
// or failed journeys can never leak an identity into another context. Without
// the cookie the stub answers as the first acceptance identity.
type githubStub struct {
	login   string // documented default; the cookie overrides it per context
	fixture acceptanceFacts
}

const stubIdentityCookie = "kowa-harness-login"

func newGitHubStub(fixture acceptanceFacts) *githubStub {
	return &githubStub{login: firstGitHubLogin, fixture: fixture}
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
		target := fmt.Sprintf("%s?code=%s&state=%s", redirect, s.identityFor(request), state)
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
	mux.HandleFunc("GET /stub/health", func(writer http.ResponseWriter, _ *http.Request) {
		writeStubJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	// The controlled facts of the S02 acceptance boundary: the local bare
	// repositories, the compiled definition and the scripted runtime identity.
	mux.HandleFunc("GET /stub/harness/fixtures", func(writer http.ResponseWriter, _ *http.Request) {
		writeStubJSON(writer, http.StatusOK, s.fixture)
	})
	return mux
}

// identityFor picks the login the acceptance runner selected for this browser
// context, falling back to the documented default identity.
func (s *githubStub) identityFor(request *http.Request) string {
	cookie, err := request.Cookie(stubIdentityCookie)
	if err != nil {
		return s.login
	}
	if _, known := stubIdentities[cookie.Value]; !known {
		return s.login
	}
	return cookie.Value
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
