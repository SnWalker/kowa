package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

// Observability is the optional observability backend configuration. An absent or
// incomplete configuration leaves the backend disabled: Kowa keeps running and
// records the observability gap instead of pretending the backend is there.
type Observability struct {
	// Enabled is true only when the endpoint and both project keys are present.
	Enabled     bool
	BaseURL     string
	PublicKey   string
	SecretKey   string
	Environment string
	Release     string
	Timeout     time.Duration
	// ObservationTTL bounds how long a runtime observation stays current. It is
	// shared by the runtime projection and the health statement so the two
	// cannot disagree about staleness.
	ObservationTTL time.Duration
}

// DefaultObservationTTL mirrors the runtime-observation window. The composition
// root passes the same value to both projections.
const DefaultObservationTTL = 15 * time.Minute

// NewObservability loads the observability backend configuration from the workspace
// environment. The secret key is never logged, printed or echoed in an error.
func NewObservability() Observability {
	observability := Observability{
		BaseURL:        strings.TrimSpace(os.Getenv("KOWA_LANGFUSE_BASE_URL")),
		PublicKey:      strings.TrimSpace(os.Getenv("KOWA_LANGFUSE_PUBLIC_KEY")),
		SecretKey:      strings.TrimSpace(os.Getenv("KOWA_LANGFUSE_SECRET_KEY")),
		Environment:    strings.TrimSpace(os.Getenv("KOWA_LANGFUSE_ENVIRONMENT")),
		Release:        strings.TrimSpace(os.Getenv("KOWA_RELEASE")),
		Timeout:        10 * time.Second,
		ObservationTTL: DefaultObservationTTL,
	}
	if configured := strings.TrimSpace(os.Getenv("KOWA_LANGFUSE_TIMEOUT_SECONDS")); configured != "" {
		if seconds, err := time.ParseDuration(configured + "s"); err == nil && seconds > 0 {
			observability.Timeout = seconds
		}
	}
	if configured := strings.TrimSpace(os.Getenv("KOWA_OBSERVATION_TTL_SECONDS")); configured != "" {
		if window, err := time.ParseDuration(configured + "s"); err == nil && window > 0 {
			observability.ObservationTTL = window
		}
	}
	observability.Enabled = observability.BaseURL != "" && observability.PublicKey != "" && observability.SecretKey != ""
	return observability
}

// Validate refuses a half-configured backend: enabling it requires all three
// values, because a backend that cannot authenticate would be reported as
// unavailable forever.
func (o Observability) Validate() error {
	partial := (o.BaseURL != "") || (o.PublicKey != "") || (o.SecretKey != "")
	if partial && !o.Enabled {
		return errors.New(
			"the evaluation backend requires KOWA_LANGFUSE_BASE_URL, KOWA_LANGFUSE_PUBLIC_KEY and KOWA_LANGFUSE_SECRET_KEY together")
	}
	return nil
}
