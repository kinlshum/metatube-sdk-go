package logsearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
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
		// Graylog answers an absolute search with CSV once `fields` is set.
		writer.Header().Set("Content-Type", "text/csv")
		_, _ = writer.Write([]byte(strings.Join([]string{
			`"timestamp","message","level","log_level","component","provider","trace_id","run_id","duration_ms"`,
			`"2026-09-19T04:00:00.000Z","provider failed with token=topsecretvalue","3","error","provider","JavLibrary","trace-aaaa-1111","run-aaaa-1111","42.5"`,
		}, "\n")))
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
	assert.NotContains(t, query, "stream:", "Graylog 7 takes the stream as a parameter")
	assert.Equal(t, "000000000000000000000001", captured.URL.Query().Get("streams"))
	assert.Contains(t, query, `\"quote\"`, "quotes in user text must be escaped")
	assert.Contains(t, query, `\\ slash`)
	assert.Equal(t, "200", captured.URL.Query().Get("limit"))
	assert.NotContains(t, captured.URL.RawQuery, "secret-token")

	// The deep link is built from the configured external URL only.
	assert.True(t, strings.HasPrefix(result.ExternalSearchURL, "https://graylog.example/search?"))
	assert.Contains(t, result.ExternalSearchURL, "rangetype=absolute")
	assert.Equal(t, 1, result.MatchCount)
	assert.False(t, result.Truncated)
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
		_, _ = writer.Write([]byte(`"timestamp","message"`))
	}))
	defer slow.Close()
	result = NewGraylogBackend(GraylogConfig{
		Enabled: true, APIURL: slow.URL, Token: "t", Timeout: 50 * time.Millisecond,
	}).Search(context.Background(), query)
	assert.False(t, result.Available)
	assert.Contains(t, result.Detail, "timed out")

	// An empty result is a success: it must never look like a failure signal.
	// Graylog answers with the field header only when nothing matched.
	empty := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/csv")
		_, _ = writer.Write([]byte(`"timestamp","message","trace_id"`))
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

func TestGraylogSearchURLUsesGraylogSevenParameters(t *testing.T) {
	backend := NewGraylogBackend(GraylogConfig{
		Enabled:  true,
		APIURL:   "https://graylog.madtechinc.com/api",
		Token:    "search-token",
		StreamID: "000000000000000000000001",
	}.WithDefaults())

	since := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	until := since.Add(10 * time.Minute)
	query := backend.buildQuery(Query{TraceIDs: []string{"trace-aaaa-1111"}, RunID: "run-aaaa-1111"})
	target := backend.searchURL(query, &since, &until, 25)
	parsed, err := url.Parse(target)
	require.NoError(t, err)
	values := parsed.Query()

	// Graylog 7 requires `fields` (its answer becomes CSV) and takes the stream
	// as its own parameter instead of the legacy `stream:` query clause.
	assert.Equal(t, "25", values.Get("limit"))
	assert.NotEmpty(t, values.Get("from"))
	assert.NotEmpty(t, values.Get("to"))
	assert.Equal(t, "000000000000000000000001", values.Get("streams"))
	assert.Contains(t, values.Get("fields"), "trace_id")
	assert.Contains(t, values.Get("fields"), "application")
	assert.Contains(t, values.Get("query"), `trace_id:"trace-aaaa-1111"`)
	assert.Contains(t, values.Get("query"), `run_id:"run-aaaa-1111"`)
	assert.NotContains(t, values.Get("query"), "stream:")

	// The token never travels in the URL.
	assert.NotContains(t, target, "search-token")
	assert.NotContains(t, values.Get("fields"), "token")
}

func TestGraylogSearchParsesCSVAnswer(t *testing.T) {
	answer := strings.Join([]string{
		`"timestamp","message","level","log_level","component","stage","provider","trace_id","run_id","application","service","server","node","environment","source_type","logger"`,
		`"2026-09-19T15:19:24.487Z","trace started","6","info","metatube","request_received","","b905981c-29a0-4736-8b03-1f5b7f82c876","run-e2e-1","metatube","metatube-server","kraken","kraken-docker","homelab","trace","metatube-server.metatube"`,
		`"2026-09-19T15:19:25.001Z","provider responded","3","error","provider","provider_failed","JavBus","b905981c-29a0-4736-8b03-1f5b7f82c876","run-e2e-1","metatube","metatube-server","kraken","kraken-docker","homelab","trace","metatube-server.provider"`,
		`"broken","row with, a comma","6"`,
	}, "\n")

	decoded, err := decodeGraylogCSV(strings.NewReader(answer), 50)
	require.NoError(t, err)
	require.Len(t, decoded.Lines, 2, "a malformed short row is ignored, not rendered")
	assert.Equal(t, 2, decoded.Total)

	// Newest first, with the required common fields and correlation set intact.
	first := decoded.Lines[0]
	assert.Equal(t, "provider responded", first.Message)
	assert.Equal(t, trace.LevelError, first.Level, "the string log_level wins over the numeric level")
	assert.Equal(t, "provider", first.Component)
	assert.Equal(t, "JavBus", first.Provider)
	assert.Equal(t, "b905981c-29a0-4736-8b03-1f5b7f82c876", first.TraceID)
	assert.Equal(t, "run-e2e-1", first.RunID)
	assert.Equal(t, "metatube", first.Application)
	assert.Equal(t, "metatube-server", first.Service)
	assert.Equal(t, "kraken", first.Server)
	assert.Equal(t, "kraken-docker", first.Node)
	assert.Equal(t, "homelab", first.Environment)
	assert.Equal(t, "trace", first.SourceType)
	assert.True(t, first.At.After(decoded.Lines[1].At) || first.At.Equal(decoded.Lines[1].At))

	// An empty answer is not an error.
	empty, err := decodeGraylogCSV(strings.NewReader(""), 50)
	require.NoError(t, err)
	assert.Empty(t, empty.Lines)

	// The row limit is honoured while the match count keeps counting.
	limited, err := decodeGraylogCSV(strings.NewReader(answer), 1)
	require.NoError(t, err)
	assert.Len(t, limited.Lines, 1)
	assert.Equal(t, 2, limited.Total)
}

func TestGraylogSearchReadsCSVOverHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/search/universal/absolute", r.URL.Path)
		assert.Equal(t, "metatube-admin", r.Header.Get("X-Requested-By"))
		// An API token authenticates as the username with the literal password.
		user, password, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "t", user)
		assert.Equal(t, "token", password)
		assert.Contains(t, r.URL.Query().Get("fields"), "trace_id")
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte(strings.Join([]string{
			`"timestamp","message","log_level","trace_id","run_id"`,
			`"2026-09-19T15:19:24.487Z","trace started","info","trace-aaaa-1111","run-aaaa-1111"`,
		}, "\n")))
	}))
	defer server.Close()

	backend := NewGraylogBackend(GraylogConfig{
		Enabled: true, APIURL: server.URL + "/api", Token: "t",
	}.WithDefaults())
	result := backend.Search(context.Background(), Query{TraceIDs: []string{"trace-aaaa-1111"}, Limit: 10})

	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, result.Lines, 1)
	assert.Equal(t, "trace started", result.Lines[0].Message)
	assert.Equal(t, "run-aaaa-1111", result.Lines[0].RunID)
	assert.Equal(t, "trace-aaaa-1111", result.Lines[0].TraceID)
	assert.True(t, result.Configured)
	assert.True(t, result.Available)
	assert.Equal(t, 1, result.MatchCount)
	assert.False(t, result.Truncated)
	assert.NotNil(t, result.LastSuccessAt)
}

func TestGraylogLineReadsOriginAndLevel(t *testing.T) {
	line := graylogLine(map[string]any{
		"timestamp":       "2026-09-19T10:00:00.000Z",
		"message":         "provider responded",
		"level":           float64(6),
		"application":     "metatube",
		"service":         "metatube-server",
		"server":          "kraken",
		"node":            "kraken-docker",
		"environment":     "homelab",
		"source_type":     "trace",
		"component":       "provider",
		"stage":           "provider_completed",
		"provider":        "JavBus",
		"trace_id":        "trace-aaaa-1111",
		"run_id":          "run-aaaa-1111",
		"windmill_job_id": "job-aaaa-1111",
	})

	// A numeric GELF level is mapped back to a name so the same filters match.
	assert.Equal(t, trace.LevelInfo, line.Level)
	assert.Equal(t, "metatube", line.Application)
	assert.Equal(t, "kraken", line.Server)
	assert.Equal(t, "kraken-docker", line.Node)
	assert.Equal(t, "homelab", line.Environment)
	assert.Equal(t, "trace", line.SourceType)
	assert.Equal(t, "trace-aaaa-1111", line.TraceID)
	assert.Equal(t, "run-aaaa-1111", line.RunID)
	assert.Equal(t, "job-aaaa-1111", line.WindmillJob)
	assert.NotEmpty(t, line.Fingerprint)

	// An explicit log_level wins over the numeric syslog level.
	explicit := graylogLine(map[string]any{"level": float64(6), "log_level": trace.LevelError})
	assert.Equal(t, trace.LevelError, explicit.Level)
	assert.Equal(t, trace.LevelError, graylogLevelName("3"))
	assert.Equal(t, trace.LevelWarn, graylogLevelName("4"))
	assert.Equal(t, trace.LevelDebug, graylogLevelName("7"))
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
