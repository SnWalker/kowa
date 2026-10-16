package config

import (
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Identity enables the separately configured browser identity surface.
// Secrets are never printed by the composition root.
type Identity struct {
	Enabled       bool
	DatabaseURL   string
	TrustedOrigin string
	ClientID      string
	ClientSecret  string
	AdminIDs      []string
	// GitHubAppID and GitHubAppPrivateKeyPath configure the read-only GitHub
	// App installation seam. The key is deployment material referenced by an
	// external path; the value is never carried in the configuration struct.
	// Both unset keeps the frozen fail-closed adapter.
	GitHubAppID             string
	GitHubAppPrivateKeyPath string
}

// NewIdentity loads and validates browser identity deployment configuration.
func NewIdentity() (Identity, error) {
	c := Identity{Enabled: os.Getenv("KOWA_IDENTITY_ENABLED") == "true",
		DatabaseURL:             os.Getenv("KOWA_DATABASE_URL"),
		TrustedOrigin:           os.Getenv("KOWA_WEB_ORIGIN"),
		ClientID:                os.Getenv("KOWA_GITHUB_CLIENT_ID"),
		ClientSecret:            os.Getenv("KOWA_GITHUB_CLIENT_SECRET"),
		GitHubAppID:             os.Getenv("KOWA_GITHUB_APP_ID"),
		GitHubAppPrivateKeyPath: os.Getenv("KOWA_GITHUB_APP_PRIVATE_KEY_PATH"),
	}
	if ids := os.Getenv("KOWA_BOOTSTRAP_ADMIN_IDS"); ids != "" {
		c.AdminIDs = strings.Split(ids, ",")
	}
	return c, c.Validate()
}

func (c Identity) Validate() error {
	if !c.Enabled {
		return nil
	}
	u, err := url.Parse(c.TrustedOrigin)
	if err != nil {
		return errors.New("invalid identity web origin")
	}
	httpsOrigin := u.Scheme == "https" && u.Host != "" && u.User == nil
	originOnly := u.Path == "" && u.RawQuery == "" && u.Fragment == ""
	configured := c.DatabaseURL != "" && c.ClientID != "" && c.ClientSecret != ""
	if !httpsOrigin || !originOnly || !configured {
		return errors.New("identity requires database, exact https origin and github oauth configuration")
	}
	for _, id := range c.AdminIDs {
		if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(id) {
			return errors.New("invalid bootstrap admin id")
		}
	}
	// The installation seam is either fully configured or absent. A half
	// configuration must not silently fall back to the fail-closed adapter:
	// that would turn an operator mistake into a mysterious refusal.
	appIDSet := c.GitHubAppID != ""
	keyPathSet := c.GitHubAppPrivateKeyPath != ""
	if appIDSet != keyPathSet {
		return errors.New("github app installation seam requires both KOWA_GITHUB_APP_ID and KOWA_GITHUB_APP_PRIVATE_KEY_PATH")
	}
	if appIDSet && !regexp.MustCompile(`^[1-9][0-9]{0,19}$`).MatchString(c.GitHubAppID) {
		return errors.New("invalid github app id")
	}
	return nil
}
