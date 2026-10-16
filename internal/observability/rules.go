package observability

import (
	"regexp"
	"strings"
)

// valueRule is one credential shape. Rules are applied in order; the masking
// text they produce is never matched by a later rule.
type valueRule struct {
	name        string
	pattern     *regexp.Regexp
	replacement string
}

// valueRules is the frozen credential vocabulary. Two properties matter and are
// tested: a rule never destroys a commit oid, a `sha256:` digest or an opaque
// Kowa id, and every rule terminates.
var valueRules = []valueRule{
	{
		name: "pem-private-key",
		pattern: regexp.MustCompile(
			`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
		replacement: "-----BEGIN PRIVATE KEY-----" + Mask + "-----END PRIVATE KEY-----",
	},
	{
		name:        "github-token",
		pattern:     regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`),
		replacement: Mask,
	},
	{
		name:        "github-fine-grained-pat",
		pattern:     regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
		replacement: Mask,
	},
	{
		name:        "langfuse-key",
		pattern:     regexp.MustCompile(`\b(?:pk|sk)-lf-[A-Za-z0-9]{8,}\b`),
		replacement: Mask,
	},
	{
		name:        "provider-api-key",
		pattern:     regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
		replacement: Mask,
	},
	{
		name:        "slack-token",
		pattern:     regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
		replacement: Mask,
	},
	{
		name:        "aws-access-key-id",
		pattern:     regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		replacement: Mask,
	},
	{
		name:        "json-web-token",
		pattern:     regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\b`),
		replacement: Mask,
	},
	{
		// A named assignment is masked before the bare scheme rule runs, so an
		// authorization scheme word is consumed with the token that follows it
		// and no part of the credential is left behind.
		name: "secret-assignment",
		pattern: regexp.MustCompile(
			`(?i)\b(token|secret|password|passwd|api[_-]?key|client[_-]?secret|access[_-]?token|` +
				`refresh[_-]?token|private[_-]?key|credential|authorization)\s*([:=])\s*"?` +
				`(?:(?:bearer|basic)\s+)?([^\s"',;]{4,})"?`),
		replacement: "$1$2" + Mask,
	},
	{
		name:        "authorization-scheme",
		pattern:     regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`),
		replacement: "$1 " + Mask,
	},
	{
		name:        "url-query-credential",
		pattern:     regexp.MustCompile(`(?i)([?&](?:token|access_token|api_key|apikey|key|password|secret|signature|sig)=)([^&\s]{2,})`),
		replacement: "$1" + Mask,
	},
}

// sensitiveFieldNames is the frozen set of exact field names whose values are
// masked whole. A name that only *contains* one of these words is not sensitive:
// a request key is an idempotency identity, not a credential.
var sensitiveFieldNames = map[string]bool{
	"authorization":       true,
	"proxy_authorization": true,
	"cookie":              true,
	"set_cookie":          true,
	"token":               true,
	"secret":              true,
	"password":            true,
	"passwd":              true,
	"passphrase":          true,
	"credential":          true,
	"credentials":         true,
	"api_key":             true,
	"apikey":              true,
	"private_key":         true,
	"signing_key":         true,
	"client_secret":       true,
	"identity_file":       true,
}

// sensitiveFieldSuffixes catches the deployment-named variants of the same
// secrets (`runtime_token`, `oauth_client_secret`, `accessToken`). The entries
// without an underscore are the camelCase spellings, which normalize to a joined
// lowercase name; a name that merely contains one of these words is not
// matched (`request_key` is an idempotency identity, not a credential).
var sensitiveFieldSuffixes = []string{
	"_token", "_secret", "_password", "_passwd", "_passphrase",
	"_private_key", "_signing_key", "_credential", "_credentials", "_api_key",
	"token", "secret", "password", "passwd", "passphrase",
	"privatekey", "signingkey", "apikey",
}

// normalizeFieldName lowercases a field name and folds the separators so
// `X-Api-Key`, `x_api_key` and `x.api.key` are one name.
func normalizeFieldName(name string) string {
	return strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(name)))
}
