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
	assert.Contains(t, body, `id="buildVersion">Build `)
	assert.NotContains(t, body, "__METATUBE_BUILD__")
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
		`id="traceVideoSelectAll"`, `id="traceActorSelectAll"`,
		`id="traceVideoDeleteSelected"`, `id="traceActorDeleteSelected"`,
		`id="traceVideoPrev"`, `id="traceVideoNext"`, `id="traceVideoPager"`,
		`id="traceActorPrev"`, `id="traceActorNext"`, `id="traceActorPager"`,
		`id="traceVideoSize"`, `id="traceActorSize"`,
		`id="traceVideoRefreshBottom"`, `id="traceVideoPauseBottom"`, `id="traceVideoFollowBottom"`,
		`id="traceActorRefreshBottom"`, `id="traceActorPauseBottom"`, `id="traceActorFollowBottom"`,
		`id="traceVideoDeleteSelectedBottom"`, `id="traceActorDeleteSelectedBottom"`,
		`id="traceVideoPrevBottom"`, `id="traceVideoNextBottom"`, `id="traceVideoPagerBottom"`,
		`id="traceActorPrevBottom"`, `id="traceActorNextBottom"`, `id="traceActorPagerBottom"`,
		`id="traceVideoSizeBottom"`, `id="traceActorSizeBottom"`,
		`id="traceVideoRows"`, `id="traceActorRows"`,
		`id="traceVideoDrawer"`, `id="traceActorDrawer"`,
		`id="traceVideoDrawerAnchor"`, `id="traceActorDrawerAnchor"`,
	} {
		assert.Contains(t, body, element)
	}

	// Behavioural requirements.
	assert.Contains(t, body, "setInterval(()=>{if(document.hidden)return;refreshTraces(kind)},5000)",
		"auto-refresh must run only while the tab is visible")
	assert.Contains(t, body, "Awaiting client report")
	assert.Contains(t, body, "Show in LOGS", "traces must link back to the general log lines")
	assert.Contains(t, body, "deleteSelectedTraces(kind)", "selected traces must support confirmed bulk deletion")
	assert.Contains(t, body, "data-select-trace", "each trace row must expose a selection checkbox")
	assert.Contains(t, body, "event.ctrlKey||event.metaKey||event.shiftKey",
		"Ctrl, Command, and Shift row selection must be wired")
	assert.Contains(t, body, "trace-marked", "selected rows must have a visible selection state")
	assert.Contains(t, body, "state.limit=Number(event.target.value)",
		"changing rows per page must update the trace query limit")
	assert.Contains(t, body, "field-changes", "enrichment field changes must be rendered")
	assert.Contains(t, body, "ETATUBE_ADMIN_TOKEN", "a missing admin token must produce a clear hint")

	// Auto-follow must be wired to behaviour, not merely present in the markup.
	assert.Contains(t, body, "traceEl(kind,'Follow').checked",
		"the follow checkbox must be read when refreshing")
	assert.Contains(t, body, "if(follow)state.offset=0;",
		"following must keep the newest page selected")
	assert.Contains(t, body, "if(state.open&&(follow||manual))openTrace(kind,state.open,false)",
		"following (and a manual refresh) must refresh the open drawer")
	assert.Contains(t, body, "traceControls(kind,'Follow')",
		"both follow checkboxes must be synchronized and take effect immediately")
	assert.Contains(t, body, "traceControls(kind,'Pause')",
		"both pause buttons must share the same state")
	assert.Contains(t, body, "traceControls(kind,'Size')",
		"both page-size selectors must share the same state")
	assert.Contains(t, body, "trace-page-arrow", "pagination must use compact arrow buttons")
	assert.Contains(t, body, "trace-page-count", "pagination must show the current count")
	assert.Contains(t, body, "const previousScroll=scroll?0:drawer.scrollTop;",
		"a live drawer refresh must preserve the reader's scroll position")
	assert.Contains(t, body, "drawer.scrollTop=previousScroll")
	assert.Contains(t, body, "function placeTraceDrawer(kind,row)",
		"the detail viewer must be inserted directly below the selected job")
	assert.Contains(t, body, "row.after(detail)",
		"clicking a job must expand its details inline")
	assert.Contains(t, body, "drawer.scrollIntoView({behavior:'smooth',block:'nearest'})",
		"the expanded job must be brought into view")
	assert.Contains(t, body, "if(traceState[kind].open===row.dataset.trace){closeTraceDrawer(kind);return}",
		"clicking an expanded job must collapse it")
	assert.Contains(t, body, "if(state.paused&&!state.manual)return;",
		"pause must stop polling until an explicit refresh")
	assert.Contains(t, body, "${follow?' · following newest':' · page held'}",
		"the operator must be able to see the current follow mode")
}

func TestAdminPolicyReturnsListWhenDisabled(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)
	recorder := doRequest(router, http.MethodGet, "/admin/api/movie-search-policy", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"providers":[]`)
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

// TestTraceDownstreamStatesThroughTheAPI covers the four reporting situations the
// review asked for: server-only, Windmill-only, fully reported, and failed.
func TestTraceDownstreamStatesThroughTheAPI(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	startTrace := func(id, operation string) {
		t.Helper()
		recorder := doRequest(router, http.MethodPost, "/admin/api/traces/start", nil, map[string]any{
			"trace_id": id, "kind": "video", "operation": operation, "query": "SSIS-" + id,
		})
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}
	appendEvent := func(id, component, stage, level string) {
		t.Helper()
		event := map[string]any{"component": component, "stage": stage}
		if level != "" {
			event["level"] = level
		}
		recorder := doRequest(router, http.MethodPost, "/admin/api/traces/"+id+"/events", nil,
			map[string]any{"event": event})
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}
	finishTrace := func(id, status string) {
		t.Helper()
		recorder := doRequest(router, http.MethodPost, "/admin/api/traces/"+id+"/finish", nil,
			map[string]any{"status": status})
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}
	traceDetail := func(id string) map[string]any {
		t.Helper()
		recorder := doRequest(router, http.MethodGet, "/admin/api/traces/"+id, nil, nil)
		require.Equal(t, http.StatusOK, recorder.Code)
		return decodeData(t, recorder)
	}
	downstreamOf := func(data map[string]any) map[string]any {
		t.Helper()
		value, ok := data["downstream"].(map[string]any)
		require.True(t, ok, "the response must carry a downstream object")
		return value
	}

	// 1. Server-only lookup: downstream is not applicable and nothing is awaited.
	startTrace("ds-server", "lookup")
	finishTrace("ds-server", "succeeded")
	data := traceDetail("ds-server")
	assert.Equal(t, trace.DownstreamUnavailable, downstreamOf(data)["status"])
	assert.Equal(t, false, data["awaiting_report"])

	// 2. Identify with no downstream report: awaiting.
	startTrace("ds-identify", "identify")
	finishTrace("ds-identify", "succeeded")
	data = traceDetail("ds-identify")
	assert.Equal(t, trace.DownstreamNone, downstreamOf(data)["status"])
	assert.Equal(t, true, data["awaiting_report"])

	// 3. Windmill reported, Emby not.
	startTrace("ds-windmill", "identify")
	appendEvent("ds-windmill", "windmill", "windmill_step", "")
	finishTrace("ds-windmill", "succeeded")
	data = traceDetail("ds-windmill")
	assert.Equal(t, trace.DownstreamWindmill, downstreamOf(data)["status"])
	assert.Equal(t, true, data["awaiting_report"])

	// 4. Emby reported as well: complete, and no longer awaiting.
	appendEvent("ds-windmill", "emby", "emby_write", "")
	data = traceDetail("ds-windmill")
	assert.Equal(t, trace.DownstreamComplete, downstreamOf(data)["status"])
	assert.Equal(t, false, data["awaiting_report"], "a fully reported trace must not be awaiting")

	// 5. Downstream failure.
	startTrace("ds-failed", "enrich")
	appendEvent("ds-failed", "emby", "emby_write", "error")
	finishTrace("ds-failed", "partial")
	data = traceDetail("ds-failed")
	assert.Equal(t, trace.DownstreamFailed, downstreamOf(data)["status"])
	assert.Equal(t, false, data["awaiting_report"])

	// The list view exposes the same derived state for every row.
	list := doRequest(router, http.MethodGet, "/admin/api/traces?limit=50", nil, nil)
	require.Equal(t, http.StatusOK, list.Code)
	traces, ok := decodeData(t, list)["traces"].([]any)
	require.True(t, ok)
	byID := map[string]map[string]any{}
	for _, raw := range traces {
		row, ok := raw.(map[string]any)
		require.True(t, ok)
		byID[row["trace_id"].(string)] = row
	}
	for traceID, wantStatus := range map[string]string{
		"ds-server":   trace.DownstreamUnavailable,
		"ds-identify": trace.DownstreamNone,
		"ds-windmill": trace.DownstreamComplete,
		"ds-failed":   trace.DownstreamFailed,
	} {
		row, ok := byID[traceID]
		require.True(t, ok, traceID)
		downstream, ok := row["downstream"].(map[string]any)
		require.True(t, ok, traceID)
		assert.Equal(t, wantStatus, downstream["status"], traceID)
	}
}

// TestAdminPageErrorIndexControls guards the expanded-trace error index: the
// error count must name the exact provider, failing stage, and event, and its
// focus control must scroll to that event for both the video and actor tabs.
func TestAdminPageErrorIndexControls(t *testing.T) {
	router, _ := newTraceTestRouter(t, nil)

	recorder := doRequest(router, http.MethodGet, "/admin", nil, nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()

	// The summary renders inside the drawer, above the run panel and timeline.
	assert.Contains(t, body, "data-error-index", "the drawer needs an error summary block")
	assert.Contains(t, body, "${errorIndexHtml(trace,errorEntries)}",
		"the error index must sit between the trace header and the run/timeline details")
	assert.Contains(t, body, "runState.errors=errorEntries",
		"the entries must be kept for the focus and auto-expand behaviour")

	// Classification: level=error, a stage ending in _failed, or HTTP >= 400.
	// A slow duration must never be promoted to an error on its own.
	for _, rule := range []string{
		"if(event.level==='error')return 'level=error';",
		"if(/_failed$/.test(stage))return 'stage ends in _failed';",
		"if(status>=400)return `HTTP ${status}`;",
	} {
		assert.Contains(t, body, rule, "error classification rule missing: %s", rule)
	}
	assert.NotContains(t, body, "duration_ms>=4000", "a slow duration must not be treated as an error")

	// Every entry identifies provider/component, stage, status, and event facts.
	for _, field := range []string{
		"entry.provider||entry.component", "entry.stage", "`HTTP ${entry.status}`",
		"entry.attempt", "entry.duration_ms", "entry.message",
	} {
		assert.Contains(t, body, field, "an error entry must include %s", field)
	}

	// The same failure reported by both the summary and the timeline is listed once.
	assert.Contains(t, body, "if(seen.has(key))return;seen.add(key);entries.push(entry)};")

	// The trace-level summary is only added when it carries information the
	// timeline did not already show, and it is never attributed to whichever
	// provider happened to be selected.
	assert.Contains(t, body, "if(recorded>0&&(!entries.length||(recorded>entries.length&&!entries.some(entry=>entry.stage===summaryStage))))",
		"the trace-level summary must not duplicate timeline failures")
	assert.Contains(t, body, "component:'trace',provider:''")
	assert.NotContains(t, body, "provider:trace.selected_provider||'',stage:trace.error_code",
		"a trace-level error must not be blamed on the selected provider")

	// Regression: a step window used to call getTime() on a millisecond number,
	// which threw a TypeError and broke the whole run/step tree.
	assert.Contains(t, body, "until:new Date(end+2000).toISOString()")
	assert.NotContains(t, body, "end.getTime()+2000")

	// Focus control: open the owning group, scroll to the event, and focus it.
	assert.Contains(t, body, `data-focus-error="${esc(entry.eventKey)}"`)
	assert.Contains(t, body, `data-focus-step="${esc(entry.stepKey)}"`)
	assert.Contains(t, body, `data-event-key="${esc(traceEventKey(event))}"`,
		"timeline events need a stable focus target")
	assert.Contains(t, body, "function focusTraceError(kind,eventKey,stepKey)")
	assert.Contains(t, body, "runState.openSteps[stepKey]=true;renderRunPanel(kind);restoreOpenSteps(kind)")
	assert.Contains(t, body, "target.scrollIntoView({behavior:'smooth',block:'center'});target.focus()")

	// The group holding the first failure opens automatically, once per trace, so
	// a reader's manual collapse is not undone by the five-second refresh.
	assert.Contains(t, body, "if(runState.autoExpanded!==traceId){runState.autoExpanded=traceId;")
	assert.Contains(t, body, "runState.openTraces[trace.trace_id]=true;runState.openSteps[failing.stepKey]=true")

	// A failed run and a successful run with provider errors must read differently.
	assert.Contains(t, body, "text:'run failed'")
	assert.Contains(t, body, "text:`run ${status||'succeeded'} with provider error(s)`")
	assert.Contains(t, body, "text:'run partial · failures recorded'")
	assert.Contains(t, body, "text:'no errors'")
	assert.Contains(t, body, "No failure recorded for this trace.",
		"a zero-error run still shows a clean, explicit index")

	// Both tabs share this implementation: the wiring lives in the common drawer.
	assert.Contains(t, body, "drawer.querySelectorAll('[data-focus-error]').forEach(button=>button.addEventListener('click'",
		"the focus control must be wired for the shared video/actor drawer")
}
