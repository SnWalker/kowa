package config

import "testing"

func TestIdentity_Validate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config Identity
		valid  bool
	}{{"disabled",
		Identity{},
		true},
		{"complete",
			Identity{Enabled: true,
				DatabaseURL:   "postgres://test",
				TrustedOrigin: "https://kowa.test",
				ClientID:      "id",
				ClientSecret:  "secret",
				AdminIDs:      []string{"101"}},
			true},
		{"http origin",
			Identity{Enabled: true,
				DatabaseURL:   "postgres://test",
				TrustedOrigin: "http://kowa.test",
				ClientID:      "id",
				ClientSecret:  "secret"},
			false},
		{"partial configuration",
			Identity{Enabled: true},
			false},
		{"origin path",
			Identity{Enabled: true,
				DatabaseURL:   "postgres://test",
				TrustedOrigin: "https://kowa.test/path",
				ClientID:      "id",
				ClientSecret:  "secret"},
			false},
		{"invalid allowlist",
			Identity{Enabled: true,
				DatabaseURL:   "postgres://test",
				TrustedOrigin: "https://kowa.test",
				ClientID:      "id",
				ClientSecret:  "secret",
				AdminIDs:      []string{"0"}},
			false}} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}
