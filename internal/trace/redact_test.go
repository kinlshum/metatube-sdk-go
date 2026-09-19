package trace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeStringRedactsSecrets(t *testing.T) {
	for _, unit := range []struct {
		name     string
		input    string
		mustNot  string
		expected string
	}{
		{
			name:     "bearer token",
			input:    "upstream rejected Bearer abcdef1234567890xyz",
			mustNot:  "abcdef1234567890xyz",
			expected: "upstream rejected Bearer [redacted]",
		},
		{
			name:     "url credential",
			input:    "GET https://host/api?token=supersecretvalue&q=abc",
			mustNot:  "supersecretvalue",
			expected: "GET https://host/api?token=[redacted]&q=abc",
		},
		{
			name:     "json api key",
			input:    `{"api_key": "abcdef123456"}`,
			mustNot:  "abcdef123456",
			expected: `{"api_key": "[redacted]"}`,
		},
		{
			name:     "cookie header",
			input:    "Cookie: session=abcdef123456",
			mustNot:  "abcdef123456",
			expected: "Cookie: [redacted]",
		},
	} {
		t.Run(unit.name, func(t *testing.T) {
			got := SanitizeString(unit.input)
			assert.NotContains(t, got, unit.mustNot)
			assert.Equal(t, unit.expected, got)
		})
	}
}

func TestSanitizeStringTruncates(t *testing.T) {
	got := SanitizeString(strings.Repeat("a", maxMessageLength+50))
	assert.LessOrEqual(t, len([]rune(got)), maxMessageLength+1)
	assert.True(t, strings.HasSuffix(got, "…"))
}

func TestSanitizeDetails(t *testing.T) {
	details := JSONMap{
		"authorization": "Bearer secretvalue123",
		"password":      "hunter2value",
		"keyword":       "SSIS-001",
		"provider":      "JavBus",
		"nested": map[string]any{
			"api_key": "abcdef123456",
			"plain":   "ok",
		},
	}
	got := SanitizeDetails(details)
	assert.Equal(t, Redacted, got["authorization"])
	assert.Equal(t, Redacted, got["password"])
	assert.Equal(t, "SSIS-001", got["keyword"], "ordinary metadata keys must survive")
	assert.Equal(t, "JavBus", got["provider"])

	nested, ok := got["nested"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, Redacted, nested["api_key"])
	assert.Equal(t, "ok", nested["plain"])
}

func TestSanitizeDetailsDepthAndSizeBounded(t *testing.T) {
	deep := map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": map[string]any{"e": "deep"}}}}}
	got := SanitizeDetails(deep)
	assert.NotNil(t, got)

	wide := map[string]any{}
	for i := 0; i < 200; i++ {
		wide[string(rune('a'+i%26))+string(rune('0'+i%10))] = "value"
	}
	got = SanitizeDetails(wide)
	assert.LessOrEqual(t, len(got), 41, "detail maps are capped")
}

func TestIsSensitiveKey(t *testing.T) {
	for _, key := range []string{"Authorization", "X-Api-Key", "cookie", "apiKey", "password", "SESSION_ID", "secret"} {
		assert.True(t, IsSensitiveKey(key), key)
	}
	for _, key := range []string{"keyword", "provider", "monkey", "count", "query", "Trace-ID"} {
		assert.False(t, IsSensitiveKey(key), key)
	}
}

func TestSanitizeURL(t *testing.T) {
	got := SanitizeURL("https://user:pass@example.com/movie/1?token=abcdef123456&q=ok")
	assert.NotContains(t, got, "abcdef123456")
	assert.NotContains(t, got, "user:pass")
	assert.Contains(t, got, "q=ok")
}

func TestValidID(t *testing.T) {
	assert.True(t, ValidID(NewID()))
	assert.True(t, ValidID("abc123-DEF_456.789:1"))
	for _, value := range []string{"", "short", strings.Repeat("a", 65), "abc 123", "abc\n123", "abc/123", "../etc/passwd", "abc123\tdd"} {
		assert.False(t, ValidID(value), value)
	}
}

func TestNormalizeID(t *testing.T) {
	id := NewID()
	assert.Equal(t, id, NormalizeID("  "+id+"  "))
	assert.Equal(t, "", NormalizeID("bad id!"))
}

func TestNewIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		assert.False(t, seen[id], "duplicate id generated")
		seen[id] = true
		assert.Len(t, id, 36)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("METATUBE_TRACE_ENABLED", "false")
	t.Setenv("METATUBE_TRACE_RETENTION_DAYS", "7")
	t.Setenv("METATUBE_TRACE_MAX_RUNS", "25")
	t.Setenv("METATUBE_TRACE_MAX_EVENTS_PER_RUN", "9")
	t.Setenv("METATUBE_TRACE_DSN", "/tmp/custom-traces.db")

	cfg := ConfigFromEnv()
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 7, cfg.RetentionDays)
	assert.Equal(t, 25, cfg.MaxRuns)
	assert.Equal(t, 9, cfg.MaxEventsPerRun)
	assert.Equal(t, "/tmp/custom-traces.db", cfg.DSN)

	t.Setenv("METATUBE_TRACE_ENABLED", "true")
	t.Setenv("METATUBE_TRACE_RETENTION_DAYS", "0")
	t.Setenv("METATUBE_TRACE_DSN", "")
	cfg = ConfigFromEnv()
	assert.True(t, cfg.Enabled)
	assert.Equal(t, DefaultRetentionDays, cfg.RetentionDays, "invalid values fall back to defaults")
	assert.Equal(t, DefaultConfigDSN, cfg.DSN)
}
