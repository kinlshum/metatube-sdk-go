package trace

import (
	"net/url"
	"regexp"
	"strings"
)

const (
	// Redacted replaces any sensitive value in trace data.
	Redacted = "[redacted]"

	maxMessageLength      = 600
	maxDetailStringLength = 200
	maxQueryLength        = 512
)

// sensitiveKeys are matched case-insensitively against key names. Substring
// matching is intentionally limited to unambiguous fragments so that ordinary
// metadata keys such as "keyword" or "monkey" are never redacted.
var sensitiveKeys = []string{
	"authorization",
	"cookie",
	"token",
	"api_key",
	"apikey",
	"api-key",
	"x-api-key",
	"access_key",
	"accesskey",
	"private_key",
	"privatekey",
	"secret",
	"password",
	"passwd",
	"credential",
	"bearer",
	"session_id",
	"sessionid",
	"signature",
	"passphrase",
}

// sanitizePatterns scrub well-known secret shapes from free-form text such as
// error messages and URLs, which may embed credentials. Each entry keeps the
// surrounding shape (key, separator, quotes) intact so sanitized text stays
// readable while the secret itself is removed.
var sanitizePatterns = []struct {
	pattern  *regexp.Regexp
	replacer string
}{
	// Authorization/bearer style values.
	{
		pattern:  regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{6,}`),
		replacer: "Bearer " + Redacted,
	},
	// Cookie headers.
	{
		pattern:  regexp.MustCompile(`(?i)\bset-cookie\s*:\s*[^\n]+`),
		replacer: "set-cookie: " + Redacted,
	},
	// key=value / key: value credential pairs, including JSON "key": "value".
	{
		pattern: regexp.MustCompile(`(?i)\b(token|api[_-]?key|apikey|password|passwd|secret|` +
			`access[_-]?key|signature|session[_-]?id|authorization|cookie)\b("?\s*[:=]\s*"?)[^&\s"',}\]]{1,200}`),
		replacer: "${1}${2}" + Redacted,
	},
}

// IsSensitiveKey reports whether a key name must never be stored verbatim.
func IsSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	for _, sensitive := range sensitiveKeys {
		if strings.Contains(normalized, sensitive) {
			return true
		}
	}
	return false
}

// Truncate shortens a string to at most limit runes, appending an ellipsis when
// the value was cut. It never splits multi-byte runes.
func Truncate(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// SanitizeString scrubs known secret shapes and bounds the length of free-form
// text before it is stored in a trace event.
func SanitizeString(value string) string {
	return Truncate(sanitize(value), maxMessageLength)
}

// SanitizeQuery bounds a lookup query so traces cannot grow without limit. The
// query is not a secret, so it is kept as-is when it fits.
func SanitizeQuery(value string) string {
	return Truncate(sanitize(value), maxQueryLength)
}

// SanitizeURL removes userinfo and credential query parameters from a URL.
func SanitizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return SanitizeString(raw)
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if IsSensitiveKey(key) || strings.EqualFold(key, "key") {
			query.Set(key, Redacted)
		}
	}
	parsed.RawQuery = query.Encode()
	return Truncate(parsed.String(), maxDetailStringLength)
}

func sanitize(value string) string {
	if value == "" {
		return ""
	}
	result := value
	for _, entry := range sanitizePatterns {
		result = entry.pattern.ReplaceAllString(result, entry.replacer)
	}
	return result
}

// SanitizeValue redacts or bounds a single detail value.
func SanitizeValue(key string, value any) any {
	if IsSensitiveKey(key) {
		return Redacted
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return Truncate(sanitize(typed), maxDetailStringLength)
	case []string:
		if len(typed) > 20 {
			typed = typed[:20]
		}
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, Truncate(sanitize(item), maxDetailStringLength))
		}
		return values
	case map[string]any:
		return SanitizeDetails(typed)
	default:
		return value
	}
}

// SanitizeDetails recursively redacts a detail map. Depth is bounded so that a
// hostile payload cannot exhaust memory through nesting.
func SanitizeDetails(details map[string]any) map[string]any {
	return sanitizeDetails(details, 0)
}

func sanitizeDetails(details map[string]any, depth int) map[string]any {
	if details == nil {
		return nil
	}
	if depth > 3 {
		return map[string]any{"truncated": true}
	}
	result := make(map[string]any, len(details))
	count := 0
	for key, value := range details {
		if count >= 40 {
			result["truncated"] = true
			break
		}
		count++
		result[Truncate(key, 64)] = SanitizeValue(key, value)
	}
	return result
}

// SanitizeFieldChanges bounds a field-change summary. Only field names, actions,
// source providers and value lengths are stored, never the values themselves.
func SanitizeFieldChanges(changes []FieldChange) []FieldChange {
	result := make([]FieldChange, 0, len(changes))
	for index, change := range changes {
		if index >= 40 {
			break
		}
		result = append(result, change)
	}
	return result
}
