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
}

// NewIdentity loads and validates browser identity deployment configuration.
func NewIdentity() (Identity, error) {
	c := Identity{Enabled: os.Getenv("KOWA_IDENTITY_ENABLED") == "true",
		DatabaseURL:   os.Getenv("KOWA_DATABASE_URL"),
		TrustedOrigin: os.Getenv("KOWA_WEB_ORIGIN"),
		ClientID:      os.Getenv("KOWA_GITHUB_CLIENT_ID"),
		ClientSecret:  os.Getenv("KOWA_GITHUB_CLIENT_SECRET"),
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
	return nil
}
