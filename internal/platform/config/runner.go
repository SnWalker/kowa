package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/SnWalker/kowa/internal/runnertransport"
)

const (
	defaultLeaseSeconds  = 1800
	defaultEnvAllowlist  = "PATH,HOME,LANG,LC_ALL,TERM"
	defaultProtectedRefs = "main,master"
	repositoryIDPattern  = `^[1-9][0-9]{0,19}$`
)

var (
	runnerIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	runnerUserIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
	envNamePattern      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	envSecretPattern    = regexp.MustCompile(`(?i)(token|secret|passw|credential|api_?key|_key|^key|ssh)`)
	capabilityPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+@[0-9]{1,5}$`)
)

// CapabilitySpec is one declared capability of this Runner process.
type CapabilitySpec struct {
	ID      string
	Version int
}

// Runner contains the independent Runner process configuration. The Runtime
// token is never logged or printed by the composition root.
type Runner struct {
	ServerURL       string
	RuntimeID       string
	Token           string
	WorkRoot        string
	Provider        string
	ProviderVersion string
	ProviderModel   string
	ProviderCLI     string
	ProviderSandbox string
	ProviderSession bool
	ProbeExecution  bool
	Script          string
	Capabilities    []CapabilitySpec
	GitHubUserID    string
	LeaseSeconds    int
	EnvAllowlist    []string
	// Repositories maps frozen repository ids to this machine's clone URLs.
	// Unknown ids are refused rather than guessed.
	Repositories map[string]string
	// ProtectedRefs are refs the controlled Git channel never commits to or
	// pushes.
	ProtectedRefs []string
}

// NewRunner loads and validates Runner deployment configuration.
func NewRunner() (Runner, error) {
	runner := Runner{
		ServerURL:       os.Getenv("KOWA_RUNNER_SERVER_URL"),
		RuntimeID:       os.Getenv("KOWA_RUNNER_RUNTIME_ID"),
		Token:           os.Getenv("KOWA_RUNNER_RUNTIME_TOKEN"),
		WorkRoot:        os.Getenv("KOWA_RUNNER_WORK_ROOT"),
		Provider:        os.Getenv("KOWA_RUNNER_PROVIDER"),
		ProviderVersion: os.Getenv("KOWA_RUNNER_PROVIDER_VERSION"),
		ProviderModel:   os.Getenv("KOWA_RUNNER_PROVIDER_MODEL"),
		ProviderCLI:     os.Getenv("KOWA_RUNNER_PROVIDER_CLI"),
		ProviderSandbox: os.Getenv("KOWA_RUNNER_PROVIDER_SANDBOX"),
		ProviderSession: os.Getenv("KOWA_RUNNER_PROVIDER_SESSION") == "true",
		ProbeExecution:  os.Getenv("KOWA_RUNNER_PROBE") == "execute",
		Script:          os.Getenv("KOWA_RUNNER_SCRIPT"),
		GitHubUserID:    os.Getenv("KOWA_RUNNER_GITHUB_USER_ID"),
		LeaseSeconds:    defaultLeaseSeconds,
		EnvAllowlist:    strings.Split(defaultEnvAllowlist, ","),
		ProtectedRefs:   strings.Split(defaultProtectedRefs, ","),
	}
	if configured := os.Getenv("KOWA_RUNNER_REPOSITORIES"); configured != "" {
		runner.Repositories = map[string]string{}
		for _, entry := range strings.Split(configured, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			repositoryID, url, found := strings.Cut(entry, "=")
			if !found {
				return Runner{}, errors.New("runner repositories must be repositoryId=url pairs")
			}
			runner.Repositories[repositoryID] = url
		}
	}
	if configured := strings.TrimSpace(os.Getenv("KOWA_RUNNER_PROTECTED_REFS")); configured != "" {
		runner.ProtectedRefs = strings.Split(configured, ",")
	}
	if configured := strings.TrimSpace(os.Getenv("KOWA_RUNNER_LEASE_SECONDS")); configured != "" {
		seconds, err := strconv.Atoi(configured)
		if err != nil {
			return Runner{}, errors.New("runner lease seconds must be an integer")
		}
		runner.LeaseSeconds = seconds
	}
	if configured := strings.TrimSpace(os.Getenv("KOWA_RUNNER_ENV_ALLOWLIST")); configured != "" {
		runner.EnvAllowlist = strings.Split(configured, ",")
	}
	for _, spec := range strings.Split(os.Getenv("KOWA_RUNNER_CAPABILITIES"), ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		if !capabilityPattern.MatchString(spec) {
			return Runner{}, fmt.Errorf("invalid capability spec %q", spec)
		}
		id, versionText, _ := strings.Cut(spec, "@")
		version, err := strconv.Atoi(versionText)
		if err != nil || version < 1 || version > 65535 {
			return Runner{}, fmt.Errorf("invalid capability version in %q", spec)
		}
		runner.Capabilities = append(runner.Capabilities, CapabilitySpec{ID: id, Version: version})
	}
	return runner, runner.Validate()
}

func (r Runner) Validate() error {
	parsed, err := url.Parse(r.ServerURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("runner server url must be an absolute http(s) url")
	}
	if !runnerIDPattern.MatchString(r.RuntimeID) {
		return errors.New("runtime id is invalid")
	}
	if len(r.Token) < 16 || strings.ContainsAny(r.Token, " \t\r\n") {
		return errors.New("runtime token must be at least 16 non-whitespace characters")
	}
	if !filepath.IsAbs(r.WorkRoot) {
		return errors.New("runner work root must be an absolute path")
	}
	switch r.Provider {
	case "codex", "claude", "scripted":
	default:
		return errors.New("runner provider must be one of codex, claude, scripted")
	}
	// The host CLI's default model is never treated as frozen configuration,
	// so the codex adapter refuses to execute without an explicit model.
	if r.Provider == "codex" && strings.TrimSpace(r.ProviderModel) == "" {
		return errors.New("codex requires an explicit KOWA_RUNNER_PROVIDER_MODEL")
	}
	if r.ProviderSandbox != "" && r.ProviderSandbox != "read-only" && r.ProviderSandbox != "workspace-write" {
		return errors.New("provider sandbox must be read-only or workspace-write")
	}
	if !runnerUserIDPattern.MatchString(r.GitHubUserID) {
		return errors.New("machine github user id must be a decimal id")
	}
	if len(r.Capabilities) == 0 {
		return errors.New("at least one capability must be declared")
	}
	seen := map[string]bool{}
	for _, capability := range r.Capabilities {
		key := fmt.Sprintf("%s@%d", capability.ID, capability.Version)
		if seen[key] {
			return fmt.Errorf("duplicate capability %s", key)
		}
		seen[key] = true
	}
	if r.LeaseSeconds < runnertransport.MinLeaseSeconds || r.LeaseSeconds > runnertransport.MaxLeaseSeconds {
		return errors.New("lease seconds are outside the frozen bounds")
	}
	if len(r.EnvAllowlist) == 0 {
		return errors.New("environment allowlist must not be empty")
	}
	for _, name := range r.EnvAllowlist {
		name = strings.TrimSpace(name)
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("invalid environment allowlist entry %q", name)
		}
		if envSecretPattern.MatchString(name) {
			return fmt.Errorf("environment allowlist entry %q may carry a secret and is refused", name)
		}
	}
	repositoryPattern := regexp.MustCompile(repositoryIDPattern)
	for repositoryID, url := range r.Repositories {
		if !repositoryPattern.MatchString(repositoryID) {
			return fmt.Errorf("invalid repository id %q", repositoryID)
		}
		if url == "" || strings.ContainsAny(url, " \t\r\n") {
			return fmt.Errorf("repository %s has an invalid clone url", repositoryID)
		}
	}
	if len(r.ProtectedRefs) == 0 {
		return errors.New("protected refs must not be empty")
	}
	for _, ref := range r.ProtectedRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\r\n") {
			return fmt.Errorf("invalid protected ref %q", ref)
		}
	}
	return nil
}
