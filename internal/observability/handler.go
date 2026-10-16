package observability

import "log/slog"

// LogAttr is the process-wide slog ReplaceAttr rule. Installing it on the JSON
// handler means every log record — control plane and Runner alike — is scrubbed
// before it is written, instead of being written and hidden later.
//
// The shape of a record does not change: keys stay where they were, only string
// values are masked and an error value is rendered as its scrubbed message
// (the default JSON handler cannot render an error's fields anyway, so nothing
// readable is lost).
func LogAttr(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Key {
	case slog.TimeKey, slog.LevelKey:
		// Structured values the platform itself reads; a clock is not a secret.
		return attr
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		if SensitiveFieldName(attr.Key) {
			return slog.String(attr.Key, Mask)
		}
		return slog.String(attr.Key, Redact(attr.Value.String()))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok {
			return slog.String(attr.Key, Redact(err.Error()))
		}
	}
	return attr
}
