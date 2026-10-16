// Package observability owns the control plane's collection-time governance:
// the single credential scrubber every log line, structured event and trace
// attribute passes through, the stable correlation ids that tie one attempt's
// Server and Runner records together, and the watermarks that keep a healthy
// heartbeat from being read as business progress.
//
// Nothing here owns business state. A sink that fails, a spool that is lost or
// an evaluation backend that is unreachable are recorded as observations and
// never decide whether a node, a run or a delivery succeeded.
package observability

import (
	"strings"
)

// Mask is the frozen replacement text. It carries no length or prefix
// information about the value it replaced.
const Mask = "***"

// Redact returns message with every recognised credential masked. It is the
// only collection-time filter: logs, events, trace attributes and spool records
// all pass through it before they are written.
//
// The pass is deterministic and terminating: each rule rewrites the text once
// and the replacement text cannot be matched by a later rule.
func Redact(message string) string {
	message = redactURLCredentials(message)
	for _, rule := range valueRules {
		message = rule.pattern.ReplaceAllString(message, rule.replacement)
	}
	return message
}

// RedactField masks one structured field. A field whose *name* says it carries a
// credential is masked whole, because a value's shape cannot be trusted to
// reveal that it is a secret; everything else is passed through the value rules.
func RedactField(name, value string) string {
	if SensitiveFieldName(name) || ContainsCredential(value) {
		return Mask
	}
	return Redact(value)
}

// SensitiveFieldName reports whether a field name is in the frozen secret-shaped
// vocabulary. It mirrors the Runner's environment-allowlist refusal so the two
// rules cannot disagree about what counts as a secret.
func SensitiveFieldName(name string) bool {
	normalized := normalizeFieldName(name)
	if sensitiveFieldNames[normalized] {
		return true
	}
	for _, suffix := range sensitiveFieldSuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

// ContainsCredential reports whether the text still matches a credential rule.
// Callers use it to refuse a value rather than to hide it: a value that looks
// like a credential never reaches an event, a log record or a trace attribute.
func ContainsCredential(value string) bool {
	if redactURLCredentials(value) != value {
		return true
	}
	for _, rule := range valueRules {
		if rule.pattern.MatchString(value) {
			return true
		}
	}
	return false
}

// redactURLCredentials masks the userinfo of every http(s) authority, the rule
// frozen by backend:S05 for the git remote projection. The scan is single-pass
// and always advances past the replacement, so a rewritten URL can never be
// matched again. backend:S07 moved it here and kept the behaviour byte for byte;
// internal/gitremote/redact_characterization_test.go freezes the output.
func redactURLCredentials(message string) string {
	var builder strings.Builder
	builder.Grow(len(message))
	remaining := message
	for {
		index, marker := firstCredentialURL(remaining)
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		end := strings.IndexAny(remaining[index:], " \n\t")
		if end < 0 {
			end = len(remaining) - index
		}
		token := remaining[index : index+end]
		at := strings.Index(token, "@")
		builder.WriteString(remaining[:index])
		if at < 0 {
			// No userinfo: the token carries no credential to strip.
			builder.WriteString(token)
		} else {
			builder.WriteString(marker)
			builder.WriteString(Mask)
			builder.WriteString("@")
			builder.WriteString(token[at+1:])
		}
		remaining = remaining[index+end:]
	}
}

// firstCredentialURL returns the offset of the next http(s) URL and the scheme
// marker that introduced it.
func firstCredentialURL(message string) (int, string) {
	index, marker := -1, ""
	for _, candidate := range []string{"https://", "http://"} {
		found := strings.Index(message, candidate)
		if found < 0 {
			continue
		}
		if index < 0 || found < index {
			index, marker = found, candidate
		}
	}
	return index, marker
}
