package config

import (
	"crypto/subtle"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/SnWalker/kowa/internal/runnertransport"
)

var runtimeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Runtime enables the authenticated Runner transport. Each credential binds
// one opaque token to exactly one runtimeId; the token never appears in logs,
// wire bodies or evidence. Revocation is config removal plus restart, and a
// revoked token cannot re-enter through a runtime id alone.
type Runtime struct {
	Enabled     bool
	DatabaseURL string
	Credentials map[string]string
	// GitHubMachineUserID is the machine account the control plane must see as
	// the active gh identity when it reads GitHub facts read-only; empty
	// disables the observation seam.
	GitHubMachineUserID string
}

// NewRuntime loads and validates Runner transport deployment configuration.
func NewRuntime() (Runtime, error) {
	runtimeConfig := Runtime{
		Enabled:             os.Getenv("KOWA_RUNTIME_ENABLED") == "true",
		DatabaseURL:         os.Getenv("KOWA_DATABASE_URL"),
		GitHubMachineUserID: os.Getenv("KOWA_GITHUB_MACHINE_USER_ID"),
	}
	if configured := os.Getenv("KOWA_RUNTIME_CREDENTIALS"); configured != "" {
		runtimeConfig.Credentials = map[string]string{}
		for _, entry := range strings.Split(configured, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			runtimeID, token, found := strings.Cut(entry, "=")
			if !found {
				return Runtime{}, errors.New("runtime credentials must be runtimeId=token pairs")
			}
			runtimeConfig.Credentials[runtimeID] = token
		}
	}
	return runtimeConfig, runtimeConfig.Validate()
}

func (r Runtime) Validate() error {
	if r.GitHubMachineUserID != "" && !runnerUserIDPattern.MatchString(r.GitHubMachineUserID) {
		return errors.New("github machine user id must be a decimal id")
	}
	if !r.Enabled {
		return nil
	}
	if r.DatabaseURL == "" {
		return errors.New("runtime transport requires the database url")
	}
	if len(r.Credentials) == 0 {
		return errors.New("runtime transport requires at least one runtime credential")
	}
	for runtimeID, token := range r.Credentials {
		if !runtimeIDPattern.MatchString(runtimeID) {
			return errors.New("runtime credential id is invalid")
		}
		if len(token) < 16 || strings.ContainsAny(token, " \t\r\n") {
			return errors.New("runtime credential token must be at least 16 non-whitespace characters")
		}
	}
	return nil
}

// AuthenticateRuntime resolves a token to its bound runtimeId. All entries are
// compared so a caller cannot learn how many credentials exist from timing.
func (r Runtime) AuthenticateRuntime(token string) (string, bool) {
	matchedID := ""
	matched := 0
	for runtimeID, candidate := range r.Credentials {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			matchedID = runtimeID
			matched = 1
		}
	}
	if matched != 1 {
		return "", false
	}
	return matchedID, true
}

// LongPollWaitSeconds bounds one long-poll window for the server side.
func (r Runtime) LongPollWaitSeconds() int { return runnertransport.MaxWaitSeconds }
