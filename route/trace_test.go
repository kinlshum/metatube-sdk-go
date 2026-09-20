package route

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/database"
	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/internal/logbuffer"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// newTraceTestRouter builds a router with a real engine (no network calls are
// made by these tests) and a tracing service backed by a temporary SQLite file.
func newTraceTestRouter(t *testing.T, mutate func(cfg *trace.Config)) (*gin.Engine, *trace.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := trace.Config{
		Enabled:         true,
		DSN:             filepath.Join(t.TempDir(), "traces.db"),
		RetentionDays:   trace.DefaultRetentionDays,
		MaxRuns:         trace.DefaultMaxRuns,
		MaxEventsPerRun: trace.DefaultMaxEventsPerRun,
		PruneInterval:   trace.MinPruneInterval,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	service := trace.NewService(cfg)
	t.Cleanup(func() { _ = service.Close() })

	db, err := database.Open(&database.Config{
		DSN:                  filepath.Join(t.TempDir(), "metatube.db"),
		DisableAutomaticPing: true,
	})
	require.NoError(t, err)

	app := engine.New(db, engine.WithTraceService(service))
	require.NoError(t, app.DBAutoMigrate(true))

	return New(app, nil), service
}

func doRequest(router *gin.Engine, method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		payload, _ := json.Marshal(body)
		reader = bytes.NewReader(payload)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, "response has no data object: %s", recorder.Body.String())
	return data
}

func TestGuessClientDoesNotMislabelGenericPythonAsWindmill(t *testing.T) {
	assert.Equal(t, "python-client", guessClient("python-httpx/0.28.1"))
	assert.Equal(t, "python-client", guessClient("Python-urllib/3.14"))
	assert.Equal(t, "emby-plugin", guessClient("MetaTube/2026.910.1238.0"))
}

func TestTraceMiddlewareSeparatesVideoAndActor(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)

	video := doRequest(router, http.MethodGet, "/v1/movies/NoSuchProvider/SSIS-001?lazy=true", map[string]string{
		headerClient:    "emby-plugin",
		headerOperation: "identify",
	}, nil)
	require.Equal(t, http.StatusNotFound, video.Code)
	videoTraceID := video.Header().Get(headerTraceID)
	require.NotEmpty(t, videoTraceID, "a generated trace id must be returned")

	actor := doRequest(router, http.MethodGet, "/v1/actors/NoSuchActor/Saeki%20Yumika?lazy=true", map[string]string{
		headerClient: "jav-master-app",
	}, nil)
	require.Equal(t, http.StatusNotFound, actor.Code)
	actorTraceID := actor.Header().Get(headerTraceID)
	require.NotEmpty(t, actorTraceID)
	require.NotEqual(t, videoTraceID, actorTraceID)

	videos, total, err := service.List(trace.Filter{Kind: trace.KindVideo})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, videos, 1)
	assert.Equal(t, videoTraceID, videos[0].TraceID)
	assert.Equal(t, "emby-plugin", videos[0].ClientName)
	assert.Equal(t, trace.OperationIdentify, videos[0].Operation, "client-declared operation wins")
	assert.Equal(t, trace.StatusFailed, videos[0].Status)
	assert.Equal(t, "not_found", videos[0].ErrorCode)
	assert.Equal(t, "192.0.2.1", videos[0].ClientIP)

	actors, total, err := service.List(trace.Filter{Kind: trace.KindActor})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	assert.Equal(t, actorTraceID, actors[0].TraceID)
	assert.Equal(t, trace.OperationLookup, actors[0].Operation, "operation is derived when absent")

	// The general LOGS tab must be able to cross-reference the trace: the same
	// line has to carry both the request and the trace ID.
	entries := logbuffer.Entries(200)
	found := false
	for _, entry := range entries {
		message := []byte(entry.Message)
		if bytes.Contains(message, []byte("trace="+videoTraceID)) &&
			bytes.Contains(message, []byte("/v1/movies/NoSuchProvider/SSIS-001")) {
			found = true
			assert.Contains(t, entry.Message, "[GIN]")
			break
		}
	}
	assert.True(t, found, "ordinary log lines must carry the trace id alongside the request")
}

func TestTraceAdminStartEventFinishRoundTrip(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)
	traceID := "trace-test-0001"

	start := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
		"trace_id":        traceID,
		"kind":            "video",
		"operation":       "identify",
		"query":           "SSIS-001",
		"client_name":     "windmill",
		"windmill_job_id": "job-42",
	})
	require.Equal(t, http.StatusOK, start.Code, start.Body.String())
	data := decodeData(t, start)
	assert.Equal(t, traceID, data["trace_id"])
	assert.Equal(t, false, data["existing"])

	// Re-posting the same trace is idempotent.
	repeat := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
		"trace_id": traceID,
		"kind":     "video",
	})
	require.Equal(t, http.StatusOK, repeat.Code)
	assert.Equal(t, true, decodeData(t, repeat)["existing"])

	events := map[string]any{"events": []map[string]any{{
		"component":       "windmill",
		"stage":           "windmill_step",
		"message":         "translation completed",
		"idempotency_key": "job-42:translate",
		"details":         map[string]any{"provider": "JavBus"},
	}}}
	first := doRequest(router, http.MethodPost, "/admin/api/traces/"+traceID+"/events", nil, events)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.EqualValues(t, 1, decodeData(t, first)["stored"])

	retry := doRequest(router, http.MethodPost, "/admin/api/traces/"+traceID+"/events", nil, events)
	require.Equal(t, http.StatusOK, retry.Code)
	assert.EqualValues(t, 1, decodeData(t, retry)["duplicates"])

	finish := doRequest(router, http.MethodPost, "/admin/api/traces/"+traceID+"/finish", nil, map[string]any{
		"status":               "partial",
		"selected_provider":    "JavBus",
		"selected_provider_id": "SSIS-001",
		"emby_item_id":         "12345",
		"error_code":           "emby_write_failed",
		"error_message":        "metadata not saved",
		"result_count":         1,
		"field_changes": []map[string]any{
			{"field": "title", "action": "updated", "provider": "JavBus", "value_bytes": 12},
			{"field": "genres", "action": "added", "provider": "JavBus", "value_bytes": 8},
		},
	})
	require.Equal(t, http.StatusOK, finish.Code, finish.Body.String())
	finishData := decodeData(t, finish)
	traceData, ok := finishData["trace"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "partial", traceData["status"])
	assert.Equal(t, "emby_write_failed", traceData["error_code"])
	assert.Equal(t, "12345", traceData["emby_item_id"])

	detail := doRequest(router, http.MethodGet, "/admin/api/traces/"+traceID, nil, nil)
	require.Equal(t, http.StatusOK, detail.Code)
	detailData := decodeData(t, detail)
	// The Windmill step reported first, then the finish call reported Emby field
	// changes, so both downstream systems have reported: not awaiting any more.
	assert.Equal(t, false, detailData["awaiting_report"])
	downstream, ok := detailData["downstream"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, trace.DownstreamComplete, downstream["status"])

	traceObject := detailData["trace"].(map[string]any)
	eventsList, ok := traceObject["events"].([]any)
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(eventsList), 2, "the windmill step and the field-change summary must be stored")

	stages := make([]string, 0, len(eventsList))
	for _, raw := range eventsList {
		event := raw.(map[string]any)
		stages = append(stages, event["stage"].(string))
	}
	assert.Contains(t, stages, trace.StageWindmillStep)
	assert.Contains(t, stages, trace.StageFieldChanged)

	// Filters, export, delete.
	list := doRequest(router, http.MethodGet, "/admin/api/traces?kind=video&q=SSIS-001", nil, nil)
	require.Equal(t, http.StatusOK, list.Code)
	assert.EqualValues(t, 1, decodeData(t, list)["total"])

	component := doRequest(router, http.MethodGet, "/admin/api/traces?component=windmill", nil, nil)
	require.Equal(t, http.StatusOK, component.Code)
	assert.EqualValues(t, 1, decodeData(t, component)["total"])

	windowmill := doRequest(router, http.MethodGet, "/admin/api/traces?windmill_job_id=job-42", nil, nil)
	require.Equal(t, http.StatusOK, windowmill.Code)
	assert.EqualValues(t, 1, decodeData(t, windowmill)["total"])

	export := doRequest(router, http.MethodGet, "/admin/api/traces/"+traceID+"/export", nil, nil)
	require.Equal(t, http.StatusOK, export.Code)
	assert.Contains(t, export.Header().Get("Content-Disposition"), traceID)
	assert.Contains(t, export.Body.String(), `"redacted":true`)

	deleted := doRequest(router, http.MethodDelete, "/admin/api/traces/"+traceID, nil, nil)
	require.Equal(t, http.StatusOK, deleted.Code)
	assert.Equal(t, true, decodeData(t, deleted)["deleted"])

	gone := doRequest(router, http.MethodGet, "/admin/api/traces/"+traceID, nil, nil)
	assert.Equal(t, http.StatusNotFound, gone.Code)

	_, total, err := service.List(trace.Filter{})
	require.NoError(t, err)
	assert.EqualValues(t, 0, total)
}

func TestTraceAdminIngestValidation(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	invalidKind := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
		"kind": "person",
	})
	assert.Equal(t, http.StatusBadRequest, invalidKind.Code)

	unknownTrace := doRequest(router, http.MethodPost, "/admin/api/traces/missing-trace-1/events", nil, map[string]any{
		"event": map[string]any{"component": "emby", "stage": "emby_write"},
	})
	assert.Equal(t, http.StatusNotFound, unknownTrace.Code)

	noEvents := doRequest(router, http.MethodPost, "/admin/api/traces/missing-trace-1/events", nil, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, noEvents.Code)

	started := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
		"trace_id": "trace-validate-1",
		"kind":     "video",
	})
	require.Equal(t, http.StatusOK, started.Code)

	missingStage := doRequest(router, http.MethodPost, "/admin/api/traces/trace-validate-1/events", nil, map[string]any{
		"event": map[string]any{"component": "emby"},
	})
	assert.Equal(t, http.StatusBadRequest, missingStage.Code)

	manyEvents := map[string]any{"events": make([]map[string]any, maxIngestEventsPerRequest+1)}
	for index := range manyEvents["events"].([]map[string]any) {
		manyEvents["events"].([]map[string]any)[index] = map[string]any{"component": "emby", "stage": "emby_write"}
	}
	tooMany := doRequest(router, http.MethodPost, "/admin/api/traces/trace-validate-1/events", nil, manyEvents)
	assert.Equal(t, http.StatusRequestEntityTooLarge, tooMany.Code)

	badStatus := doRequest(router, http.MethodPost, "/admin/api/traces/trace-validate-1/finish", nil, map[string]any{
		"status": "exploded",
	})
	assert.Equal(t, http.StatusBadRequest, badStatus.Code)
}

func TestTraceAdminPurgeExpiredRequiresConfirmation(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	unconfirmed := doRequest(router, http.MethodPost, "/admin/api/traces/purge-expired", nil, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, unconfirmed.Code)

	confirmed := doRequest(router, http.MethodPost, "/admin/api/traces/purge-expired", nil, map[string]any{
		"confirm": "purge-expired",
	})
	require.Equal(t, http.StatusOK, confirmed.Code)
	assert.Contains(t, decodeData(t, confirmed), "pruned")
}

func TestTraceSecretsNeverReachTheAPIs(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)
	traceID := "trace-secret-1"

	started := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
		"trace_id": traceID,
		"kind":     "actor",
		"query":    "Saeki Yumika",
	})
	require.Equal(t, http.StatusOK, started.Code)

	posted := doRequest(router, http.MethodPost, "/admin/api/traces/"+traceID+"/events", nil, map[string]any{
		"events": []map[string]any{{
			"component": "metatube",
			"stage":     "provider_failed",
			"level":     "error",
			"message":   "upstream 403 with Bearer abcdef1234567890 and token=topsecretvalue",
			"details": map[string]any{
				"authorization": "Bearer abcdef1234567890",
				"api_key":       "abcdef123456",
				"provider":      "JavLibrary",
			},
		}},
	})
	require.Equal(t, http.StatusOK, posted.Code, posted.Body.String())

	detail := doRequest(router, http.MethodGet, "/admin/api/traces/"+traceID, nil, nil)
	require.Equal(t, http.StatusOK, detail.Code)
	body := detail.Body.String()
	for _, secret := range []string{"abcdef1234567890", "topsecretvalue", "abcdef123456"} {
		assert.NotContains(t, body, secret, "secret leaked through the trace API: %s", secret)
	}
	assert.Contains(t, body, "JavLibrary")

	export := doRequest(router, http.MethodGet, "/admin/api/traces/"+traceID+"/export", nil, nil)
	require.Equal(t, http.StatusOK, export.Code)
	assert.NotContains(t, export.Body.String(), "abcdef123456")
}

func TestTraceStatsReportsConfiguration(t *testing.T) {
	router, _ := newTraceTestRouter(t, func(cfg *trace.Config) {
		cfg.RetentionDays = 14
		cfg.MaxRuns = 42
	})

	recorder := doRequest(router, http.MethodGet, "/admin/api/trace-stats", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)

	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.Equal(t, true, payload["enabled"])
	assert.EqualValues(t, 14, payload["retention_days"])
	assert.EqualValues(t, 42, payload["max_runs"])

	tabs, ok := payload["tabs"].([]any)
	require.True(t, ok)
	assert.Equal(t, []any{"LOGS-METATUBE-VIDEO", "LOGS-METATUBE-ACTOR"}, tabs)
}

func TestDisabledTracingIsInvisibleToClients(t *testing.T) {
	router, service := newTraceTestRouter(t, func(cfg *trace.Config) { cfg.Enabled = false })
	require.False(t, service.Enabled())

	// Workflow request still succeeds and simply has no trace header.
	lookup := doRequest(router, http.MethodGet, "/v1/movies/NoSuchProvider/SSIS-001", nil, nil)
	assert.Equal(t, http.StatusNotFound, lookup.Code)
	assert.Empty(t, lookup.Header().Get(headerTraceID))

	list := doRequest(router, http.MethodGet, "/admin/api/traces", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, list.Code)

	start := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{"kind": "video"})
	assert.Equal(t, http.StatusServiceUnavailable, start.Code)

	stats := doRequest(router, http.MethodGet, "/admin/api/trace-stats", nil, nil)
	require.Equal(t, http.StatusOK, stats.Code)
	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(stats.Body.Bytes(), &payload))
	assert.Equal(t, false, payload["enabled"])
}
