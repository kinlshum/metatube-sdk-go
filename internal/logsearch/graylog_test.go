package logsearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGraylogNotConfigured(t *testing.T) {
	backend := NewGraylogBackend(GraylogConfig{Enabled: false})

	result := backend.Search(context.Background(), Query{})
	assert.False(t, result.Configured)
	assert.False(t, result.Available)
	assert.Equal(t, "not_configured", result.Status)
	assert.Equal(t, "Graylog not configured", result.Detail)
	assert.Empty(t, result.Lines)
	assert.Empty(t, result.Error)

	// Enabled without a token is also reported as unconfigured.
	half := GraylogConfig{Enabled: true, APIURL: "https://graylog.example/api"}.WithDefaults()
	assert.True(t, half.EnabledWithoutToken())
	assert.False(t, half.Configured())
}

func TestGraylogSearchSuccessAndRedaction(t *testing.T) {
	var captured *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured = request
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"total_results": 12,
			"messages": []any{
				map[string]any{"message": map[string]any{
					"timestamp":     "2026-09-19T04:00:00.000Z",
					"message":       "provider failed with token=topsecretvalue",
					"level":         "error",
					"component":     "provider",
					"provider":      "JavLibrary",
					"trace_id":      "trace-aaaa-1111",
					"run_id":        "run-1111",
					"duration_ms":   42.5,
					"authorization": "Bearer abcdef1234567890",
				}},
			},
		})
	}))
	defer server.Close()

	backend := NewGraylogBackend(GraylogConfig{
		Enabled:     true,
		APIURL:      server.URL,
		Token:       "secret-token",
		ExternalURL: "https://graylog.example",
		StreamID:    "000000000000000000000001",
	})
	require.True(t, backend.Config().Configured())

	from := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 19, 5, 0, 0, 0, time.UTC)
	result := backend.Search(context.Background(), Query{
		TraceIDs:  []string{"trace-aaaa-1111"},
		Component: "provider",
		Text:      `weird "quote" \ slash`,
		Since:     &from,
		Until:     &to,
	})

	require.True(t, result.Available)
	assert.Equal(t, "ok", result.Status)
	require.Len(t, result.Lines, 1)
	line := result.Lines[0]
	assert.Equal(t, SourceGraylog, line.Source)
	assert.Equal(t, "GRAYLOG", line.Badge)
	assert.Equal(t, "trace-aaaa-1111", line.TraceID)
	assert.Equal(t, "JavLibrary", line.Provider)
	assert.Equal(t, "error", line.Level)
	assert.True(t, line.At.Equal(time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)))

	// A secret echoed in a message or an unlisted field must not survive.
	assert.NotContains(t, line.Message, "topsecretvalue")
	assert.NotContains(t, result.ExternalSearchURL, "secret-token")

	// The request uses the documented token authentication and query escaping.
	require.NotNil(t, captured)
	username, password, ok := captured.BasicAuth()
	require.True(t, ok)
	assert.Equal(t, "secret-token", username)
	assert.Equal(t, "token", password)
	assert.Equal(t, "metatube-admin", captured.Header.Get("X-Requested-By"))
	query := captured.URL.Query().Get("query")
	assert.Contains(t, query, `trace_id:"trace-aaaa-1111"`)
	assert.Contains(t, query, `stream:"000000000000000000000001"`)
	assert.Contains(t, query, `\"quote\"`, "quotes in user text must be escaped")
	assert.Contains(t, query, `\\ slash`)
	assert.Equal(t, "200", captured.URL.Query().Get("limit"))
	assert.NotContains(t, captured.URL.RawQuery, "secret-token")

	// The deep link is built from the configured external URL only.
	assert.True(t, strings.HasPrefix(result.ExternalSearchURL, "https://graylog.example/search?"))
	assert.Contains(t, result.ExternalSearchURL, "rangetype=absolute")
	assert.Equal(t, 12, result.MatchCount)
	assert.True(t, result.Truncated, "results beyond the returned page are flagged")
}

func TestGraylogFailureModesDegradeCleanly(t *testing.T) {
	from := time.Now().Add(-time.Hour)
	to := time.Now()
	query := Query{Since: &from, Until: &to}

	unauthorized := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer unauthorized.Close()
	result := NewGraylogBackend(GraylogConfig{Enabled: true, APIURL: unauthorized.URL, Token: "t"}).
		Search(context.Background(), query)
	assert.False(t, result.Available)
	assert.Equal(t, "error", result.Status)
	assert.Contains(t, result.Detail, "token")

	broken := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer broken.Close()
	result = NewGraylogBackend(GraylogConfig{Enabled: true, APIURL: broken.URL, Token: "t"}).
		Search(context.Background(), query)
	assert.False(t, result.Available)
	assert.Contains(t, result.Detail, "HTTP 500")

	slow := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"messages":[]}`))
	}))
	defer slow.Close()
	result = NewGraylogBackend(GraylogConfig{
		Enabled: true, APIURL: slow.URL, Token: "t", Timeout: 50 * time.Millisecond,
	}).Search(context.Background(), query)
	assert.False(t, result.Available)
	assert.Contains(t, result.Detail, "timed out")

	// An empty result is a success: it must never look like a failure signal.
	empty := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"total_results":0,"messages":[]}`))
	}))
	defer empty.Close()
	result = NewGraylogBackend(GraylogConfig{Enabled: true, APIURL: empty.URL, Token: "t"}).
		Search(context.Background(), query)
	assert.True(t, result.Available)
	assert.Equal(t, "ok", result.Status)
	assert.Empty(t, result.Lines)
	assert.NotNil(t, result.LastSuccessAt)
}

func TestGraylogRangeIsClamped(t *testing.T) {
	cfg := GraylogConfig{Enabled: true, APIURL: "https://graylog.example/api", Token: "t", MaxRangeHours: 6}.
		WithDefaults()
	since := time.Now().Add(-48 * time.Hour)
	until := time.Now()
	from, to, clamped := cfg.ClampRange(&since, &until)
	require.NotNil(t, from)
	require.NotNil(t, to)
	assert.True(t, clamped)
	assert.LessOrEqual(t, to.Sub(*from), 6*time.Hour)
}

func TestDeduplicateKeepsTraceEvents(t *testing.T) {
	at := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)
	lines := []Line{
		{Source: SourceTrace, At: at, Message: "provider failed"},
		{Source: SourceNative, At: at, Message: "provider failed", Fingerprint: "same"},
		{Source: SourceGraylog, At: at, Message: "provider  failed", Fingerprint: "same"},
		{Source: SourceNative, At: at, Message: "unrelated", Fingerprint: "other"},
	}
	kept, hidden := Deduplicate(lines)
	assert.Len(t, kept, 3, "the trace event and one representative are kept")
	assert.Equal(t, 1, hidden["same"], "the merged count is reported so both originals stay reachable")
	// The durable copy wins the tie; the ephemeral native buffer copy is hidden.
	representatives := 0
	for _, line := range kept {
		if line.Fingerprint != "same" {
			continue
		}
		representatives++
		assert.Equal(t, SourceGraylog, line.Source)
	}
	assert.Equal(t, 1, representatives)
	assert.Equal(t, SourceTrace, kept[0].Source, "structured trace events are never merged away")
}

func TestFingerprintIsStable(t *testing.T) {
	at := time.Date(2026, 9, 19, 4, 0, 30, 0, time.UTC)
	first := Fingerprint(SourceNative, at, "error", "provider", "trace-1", "Provider  FAILED")
	second := Fingerprint(SourceNative, at.Add(20*time.Second), "ERROR", "Provider", "trace-1", "provider failed")
	assert.Equal(t, first, second, "same minute, case and spacing differences collapse")

	other := Fingerprint(SourceNative, at.Add(2*time.Minute), "error", "provider", "trace-1", "provider failed")
	assert.NotEqual(t, first, other)
}
