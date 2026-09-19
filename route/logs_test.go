package route

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/internal/gelf"
	"github.com/metatube-community/metatube-sdk-go/internal/logsearch"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// newLogTestRouter builds a router whose optional log mirror is supplied by the
// caller, so the status and probe endpoints can be exercised directly.
func newLogTestRouter(t *testing.T, mirror LogMirror) (*gin.Engine, *trace.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	service := trace.NewService(trace.Config{
		Enabled:         true,
		DSN:             filepath.Join(t.TempDir(), "traces.db"),
		RetentionDays:   trace.DefaultRetentionDays,
		MaxRuns:         trace.DefaultMaxRuns,
		MaxEventsPerRun: trace.DefaultMaxEventsPerRun,
		PruneInterval:   trace.MinPruneInterval,
	})
	t.Cleanup(func() { _ = service.Close() })

	db, err := database.Open(&database.Config{
		DSN:                  filepath.Join(t.TempDir(), "metatube.db"),
		DisableAutomaticPing: true,
	})
	require.NoError(t, err)

	app := engine.New(db, engine.WithTraceService(service))
	require.NoError(t, app.DBAutoMigrate(true))

	return New(app, nil, WithLogMirror(mirror)), service
}

// startRunTrace opens a trace with the supplied correlation ids.
func startRunTrace(t *testing.T, router *gin.Engine, body map[string]any) string {
	t.Helper()
	recorder := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, body)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := decodeData(t, recorder)
	traceID, _ := data["trace_id"].(string)
	require.NotEmpty(t, traceID)
	return traceID
}

func appendRunEvent(t *testing.T, router *gin.Engine, traceID, component, stage, message string) {
	t.Helper()
	recorder := doRequest(router, http.MethodPost, "/admin/api/traces/"+traceID+"/events", nil,
		map[string]any{"event": map[string]any{"component": component, "stage": stage, "message": message}})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func finishRunTrace(t *testing.T, router *gin.Engine, traceID, status string) {
	t.Helper()
	recorder := doRequest(router, http.MethodPost, "/admin/api/traces/"+traceID+"/finish", nil,
		map[string]any{"status": status})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func TestLogSearchReturnsEverySourceSideBySide(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)

	traceID := startRunTrace(t, router, map[string]any{
		"kind":        trace.KindVideo,
		"operation":   trace.OperationIdentify,
		"query":       "SSIS-001",
		"client_name": "emby-plugin",
	})
	appendRunEvent(t, router, traceID, trace.ComponentMetaTube, trace.StageRequestReceived, "video lookup")
	finishRunTrace(t, router, traceID, trace.StatusSucceeded)

	// A request through the middleware puts a matching line in the native buffer.
	doRequest(router, http.MethodGet, "/v1/movies/NoSuchProvider/SSIS-001?lazy=true", map[string]string{
		headerTraceID: traceID,
	}, nil)

	recorder := doRequest(router, http.MethodGet, "/admin/api/logs/search?trace_id="+traceID+"&limit=50", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	// The endpoint answers with a bare object (like /admin/api/logs).
	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	results, ok := payload["results"].([]any)
	require.True(t, ok, "logs/search must return one result per source")
	require.Len(t, results, 2, "native and Graylog are both answered")

	bySource := map[string]map[string]any{}
	for _, raw := range results {
		result, ok := raw.(map[string]any)
		require.True(t, ok)
		bySource[result["source"].(string)] = result
	}
	native := bySource["native"]
	require.NotNil(t, native)
	assert.Equal(t, "NATIVE", native["badge"])
	assert.Equal(t, true, native["configured"])
	assert.Equal(t, logsearch.NativeRetention, native["retention"])
	lines, ok := native["lines"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, lines, "the middleware line carrying the trace id must be found")
	first, ok := lines[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, traceID, first["trace_id"])
	assert.Equal(t, "native", first["source"])

	// Graylog is not configured in tests: it must degrade, not fail.
	graylog := bySource["graylog"]
	require.NotNil(t, graylog)
	assert.Equal(t, "GRAYLOG", graylog["badge"])
	assert.Equal(t, false, graylog["configured"])
	assert.Equal(t, "not_configured", graylog["status"])
	assert.Equal(t, "Graylog not configured", graylog["detail"])

	// Retention must be labelled per source and the shared filters echoed back.
	retention, ok := payload["retention"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, retention["native"], "cleared on restart")
	assert.Contains(t, retention["graylog"], "Graylog")
	effective, ok := payload["effective"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 50, effective["limit"])

	// The trace service still holds the trace; a log backend can never break it.
	detail := doRequest(router, http.MethodGet, "/admin/api/traces/"+traceID, nil, nil)
	assert.Equal(t, http.StatusOK, detail.Code)
	assert.True(t, service.Enabled())
}

func unmarshalBody(recorder *httptest.ResponseRecorder, target any) error {
	return json.Unmarshal(recorder.Body.Bytes(), target)
}

func TestTraceRunGroupsByRunIDThenWindmillThenParent(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	// 1. Explicit run id wins, and every member is returned with its events.
	runID := "run-group-0001"
	parent := startRunTrace(t, router, map[string]any{
		"kind": trace.KindVideo, "operation": trace.OperationIdentify, "query": "SSIS-001",
		"run_id": runID, "windmill_job_id": "job-group-0001",
	})
	appendRunEvent(t, router, parent, trace.ComponentProvider, trace.StageProviderCompleted, "provider responded")
	finishRunTrace(t, router, parent, trace.StatusSucceeded)

	child := startRunTrace(t, router, map[string]any{
		"kind": trace.KindVideo, "operation": trace.OperationEnrich, "query": "SSIS-001",
		"run_id": runID, "parent_trace_id": parent, "windmill_job_id": "job-group-0001",
	})
	appendRunEvent(t, router, child, trace.ComponentEmby, trace.StageEmbyWrite, "saved")
	finishRunTrace(t, router, child, trace.StatusSucceeded)

	recorder := doRequest(router, http.MethodGet, "/admin/api/trace-runs/"+runID, nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &data), recorder.Body.String())
	run, ok := data["run"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, runID, run["run_id"])
	assert.Equal(t, trace.GroupedByRun, run["grouped_by"])
	assert.Equal(t, "job-group-0001", run["windmill_job_id"])
	traces, ok := run["traces"].([]any)
	require.True(t, ok)
	assert.Len(t, traces, 2)
	events, ok := run["events"].([]any)
	require.True(t, ok)
	assert.Len(t, events, 2, "events from every trace in the run are returned in order")
	downstream, ok := data["downstream"].([]any)
	require.True(t, ok)
	assert.Len(t, downstream, 2, "per-trace downstream state is reported")
	bounds, ok := data["bounds"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, trace.MaxRunTraces, bounds["max_traces"])
	assert.EqualValues(t, trace.MaxRunEvents, bounds["max_events"])
	assert.Contains(t, data, "awaiting_report")

	// 2. Without a run id, the Windmill job groups the traces.
	job := startRunTrace(t, router, map[string]any{
		"kind": trace.KindVideo, "operation": trace.OperationLookup, "query": "SSIS-002",
		"windmill_job_id": "job-only-0002",
	})
	finishRunTrace(t, router, job, trace.StatusSucceeded)
	recorder = doRequest(router, http.MethodGet, "/admin/api/trace-runs/job-only-0002", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	run = map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &run))
	assert.Equal(t, trace.GroupedByWindmill, run["run"].(map[string]any)["grouped_by"])

	// 3. Without either, an explicit parent/child relationship groups them.
	onlyParent := startRunTrace(t, router, map[string]any{
		"kind": trace.KindActor, "operation": trace.OperationLookup, "query": "Saeki",
	})
	onlyChild := startRunTrace(t, router, map[string]any{
		"kind": trace.KindActor, "operation": trace.OperationEnrich, "query": "Saeki",
		"parent_trace_id": onlyParent,
	})
	finishRunTrace(t, router, onlyChild, trace.StatusSucceeded)
	recorder = doRequest(router, http.MethodGet, "/admin/api/trace-runs/"+onlyParent, nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	run = map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &run))
	runGroup := run["run"].(map[string]any)
	assert.Equal(t, trace.GroupedByParent, runGroup["grouped_by"])
	assert.EqualValues(t, 1, runGroup["child_count"])

	// 4. An unknown run is a 404, and a malformed id is rejected.
	missing := doRequest(router, http.MethodGet, "/admin/api/trace-runs/does-not-exist-0001", nil, nil)
	assert.Equal(t, http.StatusNotFound, missing.Code)
	invalid := doRequest(router, http.MethodGet, "/admin/api/trace-runs/", nil, nil)
	assert.Equal(t, http.StatusNotFound, invalid.Code)
}

func TestTraceRunIngestsRunIDFromHeader(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)

	// The correlation middleware must adopt X-MetaTube-Run-ID so a caller
	// (bridge, Windmill) can join an existing run without a body field.
	doRequest(router, http.MethodGet, "/v1/movies/NoSuchProvider/SSIS-003?lazy=true", map[string]string{
		headerRunID:  "run-header-0003",
		headerClient: "bridge",
	}, nil)

	runs, total, err := service.List(trace.Filter{RunID: "run-header-0003"})
	require.NoError(t, err)
	require.EqualValues(t, 1, total, "the run id from the header must be stored")
	require.Len(t, runs, 1)
	assert.Equal(t, "run-header-0003", runs[0].RunID)
}

func TestGraylogSearchDegradesWhenNotConfigured(t *testing.T) {
	// Enabled but without an API token: the feature stays visible and explains
	// itself instead of failing the trace timeline.
	t.Setenv(logsearch.GraylogEnabledEnv, "true")
	t.Setenv(logsearch.GraylogAPIURLEnv, "https://graylog.madtechinc.com/api")
	router, _ := newTraceTestRouter(t, nil)

	recorder := doRequest(router, http.MethodGet, "/admin/api/logs/search?source=native&source=graylog&limit=10", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))

	graylogConfig, ok := payload["graylog"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, graylogConfig["configured"])
	assert.Equal(t, true, graylogConfig["enabled"])
	assert.Equal(t, true, graylogConfig["missing_token"], "a half-configured adapter must warn")
	assert.Contains(t, graylogConfig["credential_source"], "METATUBE_GRAYLOG_TOKEN")
	assert.NotContains(t, recorder.Body.String(), "Bearer ")

	// An unknown source is ignored rather than proxied to a backend.
	recorder = doRequest(router, http.MethodGet, "/admin/api/logs/search?source=evil&limit=100000", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	payload = map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	results, ok := payload["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 2, "only the two known sources are searched")
	for _, raw := range results {
		source := raw.(map[string]any)["source"]
		assert.Contains(t, []string{"native", "graylog"}, source)
	}
	effective := payload["effective"].(map[string]any)
	assert.LessOrEqual(t, effective["limit"].(float64), float64(logsearch.MaxSearchLimit))
}

func TestGelfStatusAndProbeWithoutConfiguration(t *testing.T) {
	// Enabled but incomplete: the card must explain the state and the probe must
	// report a configuration error instead of pretending to send.
	sender := gelf.New(gelf.Config{Enabled: true, URL: "http://192.168.10.153:12201/gelf"})
	t.Cleanup(func() { require.NoError(t, sender.Close()) })
	router, _ := newLogTestRouter(t, sender)

	recorder := doRequest(router, http.MethodGet, "/admin/api/gelf", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	ingestion, ok := payload["ingestion"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, gelf.StatusNotConfigured, ingestion["status"])
	assert.Equal(t, false, ingestion["configured"])
	assert.Equal(t, true, ingestion["missing_token"])
	assert.Equal(t, false, ingestion["token_present"])
	assert.Contains(t, ingestion["endpoint"], "192.168.10.153:12201/gelf")
	assert.Contains(t, ingestion["token_source"], "METATUBE_GELF_TOKEN")
	assert.Contains(t, payload["probe_endpoint"], "/admin/api/gelf/probe")

	probe := doRequest(router, http.MethodPost, "/admin/api/gelf/probe", nil, nil)
	assert.Equal(t, http.StatusBadGateway, probe.Code)
	data := decodeData(t, probe)
	assert.Equal(t, false, data["ok"])
	assert.Contains(t, data["detail"], "not configured")
	probeIngestion := data["ingestion"].(map[string]any)
	assert.Equal(t, true, probeIngestion["probe_ran"])
	assert.Equal(t, false, probeIngestion["probe_ok"])
	assert.EqualValues(t, 0, probeIngestion["sent"])

	// Without a sender at all the endpoints still answer, so the UI never breaks.
	plainRouter, _ := newLogTestRouter(t, nil)
	plain := doRequest(plainRouter, http.MethodGet, "/admin/api/gelf", nil, nil)
	require.Equal(t, http.StatusOK, plain.Code)
	payload = map[string]any{}
	require.NoError(t, json.Unmarshal(plain.Body.Bytes(), &payload))
	assert.Equal(t, gelf.StatusDisabled, payload["ingestion"].(map[string]any)["status"])
	plainProbe := doRequest(plainRouter, http.MethodPost, "/admin/api/gelf/probe", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, plainProbe.Code)
}

func TestGelfProbeNeverLeaksTheIngestionToken(t *testing.T) {
	const token = "super-secret-ingest-token"
	// A closed port is a guaranteed delivery failure without network access.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL + "/gelf"
	dead.Close()

	sender := gelf.New(gelf.Config{
		Enabled: true, URL: url, Token: token,
		Server: "kraken", Node: "kraken-docker", Environment: "homelab",
		Timeout: time.Second,
	})
	t.Cleanup(func() { require.NoError(t, sender.Close()) })
	router, _ := newLogTestRouter(t, sender)

	recorder := doRequest(router, http.MethodPost, "/admin/api/gelf/probe", nil, nil)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), token, "the ingestion token must never reach a response")

	data := decodeData(t, recorder)
	ingestion := data["ingestion"].(map[string]any)
	assert.Equal(t, true, ingestion["token_present"])
	assert.NotContains(t, ingestion["token_source"], token)
	assert.Equal(t, true, ingestion["probe_ran"])
	assert.Equal(t, false, ingestion["probe_ok"])
	// A probe is a diagnostic: it must not be counted as a lost record.
	assert.EqualValues(t, 0, ingestion["failed"])
	assert.EqualValues(t, 0, ingestion["dropped"])
	assert.Equal(t, gelf.StatusOK, ingestion["status"])
}

func TestTraceStatsDescribeMirrorAndBounds(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	recorder := doRequest(router, http.MethodGet, "/admin/api/trace-stats", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	data := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &data), recorder.Body.String())

	assert.Contains(t, data, "mirror_enabled")
	ingestion, ok := data["ingestion"].(map[string]any)
	require.True(t, ok, "the stats endpoint must carry the ingestion card")
	assert.Contains(t, ingestion, "queue_capacity")
	assert.Contains(t, ingestion, "timeout_seconds")
	assert.Equal(t, gelf.StatusDisabled, ingestion["status"])

	logsConfig, ok := data["logs"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, logsearch.NativeRetention, logsConfig["native_retention"])
	assert.Equal(t, false, logsConfig["graylog_configured"])

	bounds, ok := data["bounds"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, logsearch.MaxSearchLimit, bounds["max_search"])

	sections, ok := data["log_sections"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"TRACE TIMELINE", "RECENT NATIVE LOGS", "GRAYLOG LOGS"}, sections)
}

func TestAdminPageExposesRunTraceDebugger(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)
	recorder := doRequest(router, http.MethodGet, "/admin", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()

	// The run tree and the step-scoped log panel are part of the page.
	assert.Contains(t, body, "data-run-panel", "the drawer must host the run tree")
	assert.Contains(t, body, "class=\"run-tree\"")
	assert.Contains(t, body, "/admin/api/trace-runs/", "the run endpoint must be used")
	assert.Contains(t, body, "/admin/api/logs/search?", "step logs come from the combined search")
	assert.Contains(t, body, "TRACE TIMELINE")
	assert.Contains(t, body, "NATIVE")
	assert.Contains(t, body, "GRAYLOG")
	assert.Contains(t, body, "Graylog not configured")
	assert.Contains(t, body, "Open this window in Graylog", "a deep link must be offered")
	assert.Contains(t, body, "Hide duplicate lines", "dedupe must be controllable")
	assert.Contains(t, body, "Reveal ${hiddenTotal} hidden", "hidden duplicates must be revealable")
	assert.Contains(t, body, "(result&&result.retention)", "each section must show its retention label")
	assert.Contains(t, body, "data-ingestion-probe", "the ingestion probe button must exist")
	assert.Contains(t, body, "Probe ingestion")
	assert.Contains(t, body, "class=\"ingestion-strip\"", "each trace tab must show the ingestion card")
	assert.Contains(t, body, "refreshIngestion();traceTimer=setInterval", "the card must refresh with the tab")
	assert.Contains(t, body, "traceMeta('Run'", "the drawer must show the run id")
	assert.Contains(t, body, "getTime()-2000", "the step window must start two seconds early")
	// `end` is already a millisecond number: calling getTime() on it threw a
	// TypeError and aborted the whole run/step tree render.
	assert.Contains(t, body, "until:new Date(end+2000).toISOString()",
		"the step window must end two seconds late")
	assert.NotContains(t, body, "end.getTime()+2000", "the step window must not call getTime() on a number")
	assert.Contains(t, body, "data-log-follow", "each step log panel must have its own follow toggle")
	assert.Contains(t, body, "runState.openSteps[", "expanded steps must survive a live refresh")
}
