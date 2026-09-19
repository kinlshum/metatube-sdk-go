package route

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

func TestAdminPageExposesTraceTabs(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	recorder := doRequest(router, http.MethodGet, "/admin", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()

	// The two tabs must exist with their exact names and stay separate from LOGS.
	assert.Contains(t, body, `data-tab="traceVideo">LOGS-METATUBE-VIDEO<`)
	assert.Contains(t, body, `data-tab="traceActor">LOGS-METATUBE-ACTOR<`)
	assert.Contains(t, body, `data-tab="logs">LOGS<`)
	assert.NotContains(t, body, `data-tab="traceVideo">LOGS<`)
	assert.NotContains(t, body, `data-tab="traceActor">LOGS<`)

	// Required columns for both tables.
	for _, column := range []string{
		"<th>Time</th>", "<th>Item</th>", "<th>Operation</th>", "<th>Client</th>",
		"<th>Provider</th>", "<th>Status</th>", "<th>Duration</th>",
		"<th>Windmill</th>", "<th>Emby</th>",
	} {
		assert.Contains(t, body, column)
	}

	// Controls required by the design.
	for _, element := range []string{
		`id="traceVideoQ"`, `id="traceActorQ"`,
		`id="traceVideoStatus"`, `id="traceActorComponent"`,
		`id="traceVideoErrors"`, `id="traceActorSince"`,
		`id="traceVideoRefresh"`, `id="traceVideoPause"`, `id="traceVideoFollow"`,
		`id="traceActorRefresh"`, `id="traceActorPause"`, `id="traceActorFollow"`,
		`id="traceVideoPrev"`, `id="traceVideoNext"`, `id="traceVideoPager"`,
		`id="traceActorPrev"`, `id="traceActorNext"`, `id="traceActorPager"`,
		`id="traceVideoRows"`, `id="traceActorRows"`,
		`id="traceVideoDrawer"`, `id="traceActorDrawer"`,
	} {
		assert.Contains(t, body, element)
	}

	// Behavioural requirements.
	assert.Contains(t, body, "setInterval(()=>{if(document.hidden)return;refreshTraces(kind)},5000)",
		"auto-refresh must run only while the tab is visible")
	assert.Contains(t, body, "Awaiting client report")
	assert.Contains(t, body, "Show in LOGS", "traces must link back to the general log lines")
	assert.Contains(t, body, "field-changes", "enrichment field changes must be rendered")
	assert.Contains(t, body, "ETATUBE_ADMIN_TOKEN", "a missing admin token must produce a clear hint")
}

// TestTracePayloadMatchesAdminExpectations guards the contract between the admin
// page and the trace APIs: the fields the UI reads must keep their names.
func TestTracePayloadMatchesAdminExpectations(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	started := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
		"trace_id":        "ui-contract-0001",
		"kind":            "video",
		"operation":       "identify",
		"query":           "SSIS-999",
		"client_name":     "emby-plugin",
		"windmill_job_id": "job-7",
		"emby_item_id":    "item-7",
	})
	require.Equal(t, http.StatusOK, started.Code, started.Body.String())

	posted := doRequest(router, http.MethodPost, "/admin/api/traces/ui-contract-0001/events", nil, map[string]any{
		"events": []map[string]any{{
			"component":   "provider",
			"stage":       "provider_completed",
			"provider":    "JavBus",
			"duration_ms": 42.5,
			"details":     map[string]any{"result_count": 1},
		}},
	})
	require.Equal(t, http.StatusOK, posted.Code)

	finish := doRequest(router, http.MethodPost, "/admin/api/traces/ui-contract-0001/finish", nil, map[string]any{
		"status":               "succeeded",
		"selected_provider":    "JavBus",
		"selected_provider_id": "SSIS-999",
		"result_count":         1,
	})
	require.Equal(t, http.StatusOK, finish.Code)

	list := decodeData(t, doRequest(router, http.MethodGet, "/admin/api/traces?kind=video&limit=25&offset=0", nil, nil))
	for _, key := range []string{"traces", "total", "limit", "offset", "counters"} {
		require.Contains(t, list, key)
	}
	traces, ok := list["traces"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, traces)
	traceObject, ok := traces[0].(map[string]any)
	require.True(t, ok)
	for _, field := range []string{
		"trace_id", "kind", "operation", "query", "client_name", "client_ip", "client_port",
		"started_at", "status", "duration_ms", "selected_provider", "selected_provider_id",
		"windmill_job_id", "emby_item_id", "result_count", "error_count", "event_count",
	} {
		require.Contains(t, traceObject, field, "the trace list must expose %s for the table", field)
	}

	detail := decodeData(t, doRequest(router, http.MethodGet, "/admin/api/traces/ui-contract-0001", nil, nil))
	require.Contains(t, detail, "trace")
	require.Contains(t, detail, "awaiting_report")
	detailTrace, ok := detail["trace"].(map[string]any)
	require.True(t, ok)
	events, ok := detailTrace["events"].([]any)
	require.True(t, ok)
	require.Len(t, events, 1)
	event, ok := events[0].(map[string]any)
	require.True(t, ok)
	for _, field := range []string{"sequence", "at", "level", "component", "stage", "provider", "duration_ms", "details"} {
		require.Contains(t, event, field, "timeline events must expose %s", field)
	}

	exported := doRequest(router, http.MethodGet, "/admin/api/traces/ui-contract-0001/export", nil, nil)
	require.Equal(t, http.StatusOK, exported.Code)
	assert.Contains(t, exported.Body.String(), "ui-contract-0001")

	deleted := doRequest(router, http.MethodDelete, "/admin/api/traces/ui-contract-0001", nil, nil)
	require.Equal(t, http.StatusOK, deleted.Code)
}

func TestTraceTabsRenderAgainstLiveData(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)

	// A traced lookup through the real HTTP path, as a client would make it.
	lookup := doRequest(router, http.MethodGet, "/v1/movies/NoSuchProvider/SSIS-321", map[string]string{
		headerClient:    "jav-master-app",
		headerOperation: "identify",
	}, nil)
	require.Equal(t, http.StatusNotFound, lookup.Code)
	traceID := lookup.Header().Get(headerTraceID)
	require.NotEmpty(t, traceID)

	require.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, "/admin", nil, nil).Code)

	// Every request the page makes must succeed.
	paths := []string{
		"/admin/api/traces?kind=video&limit=25&offset=0",
		"/admin/api/traces?kind=actor&limit=25&offset=0",
		"/admin/api/traces?kind=video&q=SSIS-321",
		"/admin/api/traces?kind=video&component=client",
		"/admin/api/traces?kind=video&errors=true",
		"/admin/api/traces/" + traceID,
		"/admin/api/traces/" + traceID + "/export",
		"/admin/api/trace-stats",
		"/admin/api/logs?limit=1000",
		"/admin/api/stats",
	}
	for _, path := range paths {
		assert.Equal(t, http.StatusOK, doRequest(router, http.MethodGet, path, nil, nil).Code, path)
	}

	detail, err := service.Get(traceID)
	require.NoError(t, err)
	assert.Equal(t, trace.KindVideo, detail.Run.Kind)
	assert.Equal(t, "jav-master-app", detail.Run.ClientName)
}
