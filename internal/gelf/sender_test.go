package gelf

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/logsearch"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

const testToken = "ingest-token-1234567890"

// recorder captures what the Graylog input would have received.
type recorder struct {
	mu       sync.Mutex
	attempts int
	raw      []string
	payloads []map[string]any
	tokens   []string
	gate     chan struct{}
}

// handler answers every request with the given statuses (the last status
// repeats) and records the request.
func (r *recorder) handler(statuses ...int) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.attempts++
		attempt := r.attempts
		r.raw = append(r.raw, string(body))
		r.tokens = append(r.tokens, req.Header.Get(HeaderToken))
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			r.payloads = append(r.payloads, payload)
		}
		gate := r.gate
		r.mu.Unlock()

		if gate != nil {
			<-gate
		}
		code := http.StatusAccepted
		if len(statuses) > 0 && attempt <= len(statuses) {
			code = statuses[attempt-1]
		}
		w.WriteHeader(code)
	}
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.raw)
}

func (r *recorder) record(index int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index >= len(r.payloads) {
		return nil
	}
	return r.payloads[index]
}

func (r *recorder) token(index int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if index >= len(r.tokens) {
		return ""
	}
	return r.tokens[index]
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition was not met before the deadline")
}

func newTestSender(t *testing.T, url string) *Sender {
	t.Helper()
	sender := New(Config{
		Enabled:      true,
		URL:          url,
		Token:        testToken,
		Application:  "metatube",
		Service:      "metatube-server",
		Server:       "kraken",
		Node:         "kraken-docker",
		Environment:  "homelab",
		Version:      "v3.0.0",
		Timeout:      time.Second,
		MaxRetries:   2,
		RetryBackoff: 10 * time.Millisecond,
		QueueSize:    minQueue,
	})
	require.True(t, sender.Enabled(), "the test sender must be configured")
	t.Cleanup(func() { require.NoError(t, sender.Close()) })
	return sender
}

func TestSenderDeliversRequiredAndCorrelationFields(t *testing.T) {
	remote := &recorder{}
	server := httptest.NewServer(remote.handler())
	defer server.Close()
	sender := newTestSender(t, server.URL)

	at := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	sender.Send(trace.MirrorEvent{
		At:            at,
		SourceType:    trace.MirrorSourceTrace,
		TraceID:       "trace-aaaa-1111",
		RunID:         "run-aaaa-1111",
		ParentTraceID: "parent-aaaa-1111",
		WindmillJobID: "job-aaaa-1111",
		EmbyItemID:    "item-aaaa-1111",
		Client:        "emby-plugin",
		Kind:          trace.KindVideo,
		Operation:     trace.OperationIdentify,
		Status:        trace.StatusRunning,
		Component:     trace.ComponentProvider,
		Stage:         trace.StageProviderCompleted,
		Provider:      "JavBus",
		Attempt:       2,
		HTTPStatus:    http.StatusOK,
		DurationMS:    42.5,
		Level:         trace.LevelInfo,
		Message:       "provider responded",
		Details:       trace.JSONMap{"result_count": 3},
	})

	waitFor(t, func() bool { return remote.count() == 1 })
	payload := remote.record(0)
	require.NotNil(t, payload)

	// GELF standard fields, including the protocol version and numeric level.
	assert.Equal(t, gelfVersion, payload["version"])
	assert.Equal(t, "kraken", payload["host"])
	assert.Equal(t, "provider responded", payload["short_message"])
	assert.EqualValues(t, 6, payload["level"])
	assert.EqualValues(t, at.Unix(), int64(payload["timestamp"].(float64)))

	// Required common fields.
	assert.Equal(t, "metatube", payload["_application"])
	assert.Equal(t, "metatube-server", payload["_service"])
	assert.Equal(t, "kraken", payload["_server"])
	assert.Equal(t, "kraken-docker", payload["_node"])
	assert.Equal(t, "homelab", payload["_environment"])
	assert.Equal(t, trace.MirrorSourceTrace, payload["_source_type"])
	assert.Equal(t, "metatube-server.provider", payload["_logger"])
	assert.Equal(t, "v3.0.0", payload["_version"])

	// Correlation fields that let one run be followed across services.
	assert.Equal(t, "trace-aaaa-1111", payload["_trace_id"])
	assert.Equal(t, "run-aaaa-1111", payload["_run_id"])
	assert.Equal(t, "parent-aaaa-1111", payload["_parent_trace_id"])
	assert.Equal(t, "job-aaaa-1111", payload["_windmill_job_id"])
	assert.Equal(t, "item-aaaa-1111", payload["_emby_item_id"])
	assert.Equal(t, "emby-plugin", payload["_client"])
	assert.Equal(t, trace.KindVideo, payload["_kind"])
	assert.Equal(t, trace.OperationIdentify, payload["_operation"])
	assert.EqualValues(t, 2, payload["_attempt"])
	assert.EqualValues(t, http.StatusOK, payload["_http_status"])
	assert.EqualValues(t, 42.5, payload["_duration_ms"])
	assert.Equal(t, trace.LevelInfo, payload["_log_level"])
	assert.Contains(t, payload["_details"], "result_count")

	// The ingestion token travels in the header and never in the payload.
	assert.Equal(t, testToken, remote.token(0))
	assert.NotContains(t, remote.raw, testToken)
}

func TestSenderRedactsSecretsInMessage(t *testing.T) {
	remote := &recorder{}
	server := httptest.NewServer(remote.handler())
	defer server.Close()
	sender := newTestSender(t, server.URL)

	sender.Send(trace.MirrorEvent{
		At:      time.Now(),
		TraceID: "trace-secret-1111",
		Level:   trace.LevelError,
		Message: "bridge failed token=super-secret-value",
	})

	waitFor(t, func() bool { return remote.count() == 1 })
	assert.NotContains(t, remote.raw[0], "super-secret-value")
	assert.Contains(t, remote.record(0)["short_message"], trace.Redacted)
}

func TestSenderNeverBlocksWhenQueueIsFull(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	remote := &recorder{gate: gate}
	server := httptest.NewServer(remote.handler())
	defer server.Close()
	sender := newTestSender(t, server.URL)

	start := time.Now()
	for index := 0; index < 200; index++ {
		sender.Send(trace.MirrorEvent{At: time.Now(), TraceID: "trace-slow-1111", Message: "slow backend"})
	}
	elapsed := time.Since(start)

	// A blocked Graylog must not slow down the caller: excess records are
	// counted as dropped instead of waiting.
	assert.Less(t, elapsed, 500*time.Millisecond, "Send must never block")
	stats := sender.Stats()
	assert.Equal(t, minQueue, stats.QueueCapacity)
	assert.Greater(t, stats.Dropped, uint64(0))
}

func TestSenderRetriesTransientFailure(t *testing.T) {
	remote := &recorder{}
	server := httptest.NewServer(remote.handler(http.StatusInternalServerError, http.StatusBadGateway))
	defer server.Close()
	sender := newTestSender(t, server.URL)

	sender.Send(trace.MirrorEvent{At: time.Now(), TraceID: "trace-retry-1111", Message: "transient"})
	waitFor(t, func() bool { return sender.Stats().Sent == 1 })

	stats := sender.Stats()
	assert.Equal(t, uint64(0), stats.Failed)
	assert.Equal(t, uint64(2), stats.Retried)
	assert.Nil(t, stats.LastFailureAt)
	assert.Equal(t, 3, remote.count())
}

func TestSenderDoesNotRetryClientError(t *testing.T) {
	remote := &recorder{}
	server := httptest.NewServer(remote.handler(http.StatusUnauthorized))
	defer server.Close()
	sender := newTestSender(t, server.URL)

	sender.Send(trace.MirrorEvent{At: time.Now(), TraceID: "trace-auth-1111", Message: "wrong token"})
	waitFor(t, func() bool { return sender.Stats().Failed == 1 })

	// A wrong or rotated token is permanent: retrying it would only flood the
	// endpoint, so exactly one attempt is made.
	time.Sleep(100 * time.Millisecond)
	stats := sender.Stats()
	assert.Equal(t, uint64(0), stats.Sent)
	assert.Equal(t, uint64(0), stats.Retried)
	assert.Equal(t, 1, remote.count())
	assert.Contains(t, stats.LastError, "HTTP 401")
	assert.Equal(t, StatusDegraded, stats.Status)
}

func TestProbeReportsUnreachableInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	sender := newTestSender(t, url)
	require.Error(t, sender.Probe(context.Background()))

	stats := sender.Stats()
	assert.True(t, stats.ProbeRan)
	assert.False(t, stats.ProbeOK)
	assert.NotEmpty(t, stats.ProbeDetail)
	// A probe is a diagnostic, not a delivery.
	assert.Equal(t, uint64(0), stats.Sent)
	assert.Equal(t, uint64(0), stats.Failed)
}

func TestProbeReportsReachableInput(t *testing.T) {
	remote := &recorder{}
	server := httptest.NewServer(remote.handler())
	defer server.Close()
	sender := newTestSender(t, server.URL)

	require.NoError(t, sender.Probe(context.Background()))
	stats := sender.Stats()
	assert.True(t, stats.ProbeRan)
	assert.True(t, stats.ProbeOK)
	assert.NotNil(t, stats.LastProbeAt)

	require.Equal(t, 1, remote.count())
	payload := remote.record(0)
	assert.Equal(t, SourceTypeHealth, payload["_source_type"])
	assert.Equal(t, "metatube gelf probe", payload["short_message"])
}

func TestProbeWithoutConfiguration(t *testing.T) {
	sender := New(Config{Enabled: true, URL: "http://graylog.example:12201/gelf"})
	t.Cleanup(func() { require.NoError(t, sender.Close()) })

	require.ErrorIs(t, sender.Probe(context.Background()), ErrNotConfigured)
	stats := sender.Stats()
	assert.False(t, stats.Configured)
	assert.True(t, stats.MissingToken)
	assert.Equal(t, StatusNotConfigured, stats.Status)
}

func TestStatsNeverExposeToken(t *testing.T) {
	remote := &recorder{}
	server := httptest.NewServer(remote.handler())
	defer server.Close()
	sender := newTestSender(t, server.URL)

	stats := sender.Stats()
	assert.True(t, stats.TokenPresent)
	assert.Contains(t, stats.TokenSource, TokenEnv)

	encoded, err := json.Marshal(stats)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), testToken)
	assert.Equal(t, server.URL+"/gelf", stats.Endpoint)
	assert.Equal(t, "kraken", stats.Server)
	assert.Equal(t, StatusOK, stats.Status)
}

func TestDisabledSenderIsInert(t *testing.T) {
	sender := New(Config{})
	t.Cleanup(func() { require.NoError(t, sender.Close()) })

	assert.False(t, sender.Enabled())
	assert.False(t, sender.Config().Configured())
	sender.Send(trace.MirrorEvent{Message: "ignored"})

	stats := sender.Stats()
	assert.Equal(t, StatusDisabled, stats.Status)
	assert.Equal(t, uint64(0), stats.Sent)
	assert.Equal(t, uint64(0), stats.Dropped)
	assert.False(t, stats.TokenPresent)
}

func TestConfigReadsTokenFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gelf_ingest_token")
	require.NoError(t, os.WriteFile(path, []byte("file-token-value\n"), 0o600))
	t.Setenv(TokenFileEnv, path)
	t.Setenv(EnabledEnv, "true")
	t.Setenv(URLEnv, "http://192.168.10.153:12201/gelf")

	config := ConfigFromEnv()
	require.True(t, config.Configured())
	assert.Equal(t, "file-token-value", config.Token, "trailing newlines must be trimmed")
	assert.Equal(t, path, config.TokenFile)
	assert.Contains(t, config.TokenSourceLabel(), path)

	stats := New(config).Stats()
	assert.True(t, stats.TokenPresent)
	assert.NotContains(t, stats.TokenSource, "file-token-value")
}

// A token file must also work for the search adapter, and must never be echoed.
func TestGraylogSearchReadsTokenFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graylog_api_token")
	require.NoError(t, os.WriteFile(path, []byte("search-token-value"), 0o600))
	t.Setenv(logsearch.GraylogTokenFileEnv, path)
	t.Setenv(logsearch.GraylogEnabledEnv, "true")
	t.Setenv(logsearch.GraylogAPIURLEnv, "https://graylog.madtechinc.com/api")

	cfg := logsearch.GraylogConfigFromEnv()
	require.True(t, cfg.Configured())
	assert.Equal(t, "search-token-value", cfg.Token)
	assert.Contains(t, cfg.TokenSourceLabel(), path)
}

func TestConfigReportsUnreadableTokenFile(t *testing.T) {
	t.Setenv(TokenFileEnv, filepath.Join(t.TempDir(), "missing-token"))
	t.Setenv(EnabledEnv, "true")
	t.Setenv(URLEnv, "http://192.168.10.153:12201/gelf")

	config := ConfigFromEnv()
	require.False(t, config.Configured())
	require.Error(t, config.Validate())
	assert.Contains(t, config.Validate().Error(), TokenFileEnv)
	assert.NotContains(t, config.Validate().Error(), TokenEnv+" is not set",
		"an unreadable secret file must be reported as such, not as a missing env var")
	assert.Contains(t, config.TokenSourceLabel(), TokenFileEnv)
	assert.False(t, config.Configured())
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "disabled", config: Config{}, wantErr: "disabled"},
		{name: "missing url", config: Config{Enabled: true}, wantErr: URLEnv},
		{name: "missing token", config: Config{Enabled: true, URL: "http://graylog:12201/gelf"}, wantErr: TokenEnv},
		{
			name:    "credentials in url",
			config:  Config{Enabled: true, URL: "http://user:pass@graylog:12201/gelf", Token: "t"},
			wantErr: "must not embed credentials",
		},
		{
			name:    "query secrets in url",
			config:  Config{Enabled: true, URL: "http://graylog:12201/gelf?token=abc", Token: "t"},
			wantErr: "must not carry query secrets",
		},
		{
			name:    "unsupported scheme",
			config:  Config{Enabled: true, URL: "ftp://graylog/gelf", Token: "t"},
			wantErr: "http or https",
		},
		{
			name:   "valid",
			config: Config{Enabled: true, URL: "http://192.168.10.153:12201", Token: "t"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				assert.True(t, test.config.Configured())
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErr)
			assert.False(t, test.config.Configured())
		})
	}
}

func TestConfigEndpointDefaultsToGelfPath(t *testing.T) {
	config := Config{URL: "http://192.168.10.153:12201"}
	assert.Equal(t, "http://192.168.10.153:12201/gelf", config.Endpoint())
	assert.Equal(t, "192.168.10.153:12201", config.HostDisplay())

	withPath := Config{URL: "https://graylog.madtechinc.com/gelf"}
	assert.Equal(t, "https://graylog.madtechinc.com/gelf", withPath.Endpoint())
}

func TestConfigDefaultsBoundTheQueueAndRetries(t *testing.T) {
	config := Config{
		Enabled:    true,
		URL:        "http://graylog:12201",
		Token:      "t",
		QueueSize:  100000,
		MaxRetries: 99,
		Timeout:    time.Hour,
	}.WithDefaults("kraken")
	assert.Equal(t, maxQueue, config.QueueSize)
	assert.Equal(t, maxMaxRetries, config.MaxRetries)
	assert.Equal(t, maxTimeout, config.Timeout)
	assert.Equal(t, "kraken", config.Server)
	assert.Equal(t, "kraken", config.Node)
	assert.Equal(t, defaultEnvironment, config.Environment)

	small := Config{QueueSize: 1}.WithDefaults("")
	assert.Equal(t, minQueue, small.QueueSize)
	assert.Equal(t, "metatube", small.Server)
}
