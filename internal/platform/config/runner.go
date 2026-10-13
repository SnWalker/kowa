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
	defaultLeaseSeconds = 1800
	defaultEnvAllowlist = "PATH,HOME,LANG,LC_ALL,TERM"
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
	Script          string
	Capabilities    []CapabilitySpec
	GitHubUserID    string
	LeaseSeconds    int
	EnvAllowlist    []string
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
		Script:          os.Getenv("KOWA_RUNNER_SCRIPT"),
		GitHubUserID:    os.Getenv("KOWA_RUNNER_GITHUB_USER_ID"),
		LeaseSeconds:    defaultLeaseSeconds,
		EnvAllowlist:    strings.Split(defaultEnvAllowlist, ","),
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
	return nil
}
