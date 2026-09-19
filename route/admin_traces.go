package route

import (
	goerr "errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/internal/logsearch"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// ingestLimiter is a small per-client token bucket that protects the trace
// ingest endpoints from runaway clients without adding a dependency.
type ingestLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ingestBucket
	burst   float64
	refill  float64
}

type ingestBucket struct {
	tokens float64
	last   time.Time
}

func newIngestLimiter(burst int, refillPerSecond float64) *ingestLimiter {
	if burst <= 0 {
		burst = trace.DefaultIngestBurstPerClient
	}
	if refillPerSecond <= 0 {
		refillPerSecond = trace.DefaultIngestRefillPerSecond
	}
	return &ingestLimiter{
		buckets: make(map[string]*ingestBucket),
		burst:   float64(burst),
		refill:  refillPerSecond,
	}
}

func (l *ingestLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 5000 {
		// Bound memory: drop stale buckets wholesale.
		for bucketKey, bucket := range l.buckets {
			if now.Sub(bucket.last) > 10*time.Minute {
				delete(l.buckets, bucketKey)
			}
		}
	}
	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &ingestBucket{tokens: l.burst, last: now}
		l.buckets[key] = bucket
	}
	elapsed := now.Sub(bucket.last).Seconds()
	bucket.last = now
	bucket.tokens += elapsed * l.refill
	if bucket.tokens > l.burst {
		bucket.tokens = l.burst
	}
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

// registerTraceRoutes wires the enrichment trace admin APIs under /admin.
func registerTraceRoutes(admin *gin.RouterGroup, service *trace.Service, logs *logsearch.Searcher, mirror LogMirror) {
	limiter := newIngestLimiter(trace.DefaultIngestBurstPerClient, trace.DefaultIngestRefillPerSecond)
	group := admin.Group("/api/traces")
	{
		group.POST("/start", ingestionGuard(limiter), postTraceStart(service))
		group.POST("/:traceID/events", ingestionGuard(limiter), postTraceEvents(service))
		group.POST("/:traceID/finish", ingestionGuard(limiter), postTraceFinish(service))
		group.GET("", getTraces(service))
		group.GET("/:traceID", getTrace(service))
		group.GET("/:traceID/export", exportTrace(service))
		group.DELETE("/:traceID", deleteTrace(service))
		group.POST("/purge-expired", ingestionGuard(limiter), postTracePurge(service))
	}
	admin.GET("/api/trace-stats", getTraceStats(service, logs, mirror))
}

// ingestionGuard applies body limits and rate limiting to ingest endpoints.
func ingestionGuard(limiter *ingestLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.GetHeader(headerClient))
		if key == "" {
			key = c.ClientIP()
		}
		if !limiter.allow(key) {
			c.Header("Retry-After", "1")
			abortWithStatusMessage(c, http.StatusTooManyRequests, "trace ingest rate limit exceeded")
			return
		}
		// Bound the request body so a hostile client cannot exhaust memory.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, trace.DefaultMaxPayloadBytes)
		c.Next()
	}
}

// traceErrorStatus maps trace service errors onto HTTP responses.
func abortWithTraceError(c *gin.Context, err error) {
	switch {
	case err == nil:
		return
	case goerr.Is(err, trace.ErrDisabled):
		abortWithStatusMessage(c, http.StatusServiceUnavailable, "tracing is disabled")
	case goerr.Is(err, trace.ErrTraceNotFound):
		abortWithStatusMessage(c, http.StatusNotFound, "trace not found")
	case goerr.Is(err, trace.ErrInvalidTraceID):
		abortWithStatusMessage(c, http.StatusBadRequest, "invalid trace id")
	default:
		abortWithStatusMessage(c, http.StatusInternalServerError, err)
	}
}

// parseTraceFilter reads list filters from the query string.
func parseTraceFilter(c *gin.Context) trace.Filter {
	filter := trace.Filter{
		TraceID:       c.Query("trace_id"),
		RunID:         c.Query("run_id"),
		ParentTraceID: c.Query("parent_trace_id"),
		Kind:          strings.ToLower(strings.TrimSpace(c.Query("kind"))),
		Operation:     strings.ToLower(strings.TrimSpace(c.Query("operation"))),
		Status:        strings.ToLower(strings.TrimSpace(c.Query("status"))),
		Client:        c.Query("client"),
		Provider:      c.Query("provider"),
		Component:     strings.ToLower(strings.TrimSpace(c.Query("component"))),
		Text:          strings.TrimSpace(c.Query("q")),
		EmbyItemID:    c.Query("emby_item_id"),
		WindmillJobID: c.Query("windmill_job_id"),
	}
	if value := c.Query("errors"); value != "" {
		filter.ErrorOnly = value == "1" || strings.EqualFold(value, "true")
	}
	if value := c.Query("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			filter.Limit = parsed
		}
	}
	if value := c.Query("offset"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			filter.Offset = parsed
		}
	}
	if value := c.Query("since"); value != "" {
		filter.Since = parseTraceTime(value)
	}
	if value := c.Query("until"); value != "" {
		filter.Until = parseTraceTime(value)
	}
	return filter
}

func parseTraceTime(value string) *time.Time {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed
	}
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
		parsed := time.Unix(unix, 0)
		return &parsed
	}
	return nil
}

// traceStartBody is the payload of POST /admin/api/traces/start.
type traceStartBody struct {
	TraceID          string `json:"trace_id"`
	ParentTraceID    string `json:"parent_trace_id"`
	RunID            string `json:"run_id"`
	Kind             string `json:"kind"`
	Operation        string `json:"operation"`
	Query            string `json:"query"`
	NormalizedQuery  string `json:"normalized_query"`
	ClientName       string `json:"client_name"`
	ClientIP         string `json:"client_ip"`
	ClientPort       string `json:"client_port"`
	EmbyItemID       string `json:"emby_item_id"`
	WindmillJobID    string `json:"windmill_job_id"`
	WindmillFlowPath string `json:"windmill_flow_path"`
	Status           string `json:"status"`
}

// postTraceStart opens a client-reported trace. Re-posting an existing trace ID
// is idempotent and returns the stored summary.
func postTraceStart(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		body := &traceStartBody{}
		if err := c.ShouldBindJSON(body); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if body.Kind != "" && !trace.ValidKind(body.Kind) {
			abortWithStatusMessage(c, http.StatusBadRequest, "kind must be video or actor")
			return
		}
		if body.Operation != "" && !trace.ValidOperation(body.Operation) {
			abortWithStatusMessage(c, http.StatusBadRequest, "unsupported operation")
			return
		}
		if body.Status != "" && !trace.ValidStatus(body.Status) {
			abortWithStatusMessage(c, http.StatusBadRequest, "unsupported status")
			return
		}

		clientName := body.ClientName
		if clientName == "" {
			clientName = c.GetHeader(headerClient)
		}
		handle, started := service.Start(trace.StartInput{
			TraceID:       body.TraceID,
			ParentTraceID: body.ParentTraceID,
			// Only an explicit run id (body or header) is stored here: a
			// Windmill job still groups traces through the run resolver's
			// documented priority, and `grouped_by` stays truthful.
			RunID:            firstNonEmpty(body.RunID, c.GetHeader(headerRunID)),
			Kind:             body.Kind,
			Operation:        body.Operation,
			Query:            body.Query,
			NormalizedQuery:  body.NormalizedQuery,
			ClientName:       clientName,
			ClientIP:         firstNonEmpty(body.ClientIP, c.ClientIP()),
			ClientPort:       firstNonEmpty(body.ClientPort, remotePort(c.Request.RemoteAddr)),
			UserAgent:        c.Request.UserAgent(),
			EmbyItemID:       body.EmbyItemID,
			WindmillJobID:    body.WindmillJobID,
			WindmillFlowPath: body.WindmillFlowPath,
			Status:           body.Status,
		})
		if !started || handle == nil {
			// Idempotent replay of an existing trace, otherwise tracing is off.
			if traceID := trace.NormalizeID(body.TraceID); traceID != "" {
				if detail, err := service.Get(traceID); err == nil {
					c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
						"trace_id": traceID,
						"existing": true,
						"trace":    detail.Run,
					}})
					return
				}
			}
			abortWithStatusMessage(c, http.StatusServiceUnavailable, "tracing is disabled")
			return
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"trace_id": handle.TraceID(),
			"existing": !handle.Created(),
			"trace":    handle.Run(),
		}})
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// traceEventPayload is one client-reported structured event.
type traceEventPayload struct {
	Sequence       uint64         `json:"sequence"`
	At             *time.Time     `json:"at"`
	DurationMS     float64        `json:"duration_ms"`
	Level          string         `json:"level"`
	Component      string         `json:"component"`
	Stage          string         `json:"stage"`
	Provider       string         `json:"provider"`
	Attempt        int            `json:"attempt"`
	HTTPStatus     int            `json:"http_status"`
	Message        string         `json:"message"`
	Details        map[string]any `json:"details"`
	IdempotencyKey string         `json:"idempotency_key"`
}

func (p traceEventPayload) asEvent() trace.Event {
	event := trace.Event{
		Sequence:       p.Sequence,
		DurationMS:     p.DurationMS,
		Level:          p.Level,
		Component:      p.Component,
		Stage:          p.Stage,
		Provider:       p.Provider,
		Attempt:        p.Attempt,
		HTTPStatus:     p.HTTPStatus,
		Message:        p.Message,
		IdempotencyKey: p.IdempotencyKey,
	}
	if p.At != nil {
		event.At = *p.At
	}
	if len(p.Details) > 0 {
		event.Details = trace.JSONMap(p.Details)
	}
	return event
}

type traceEventsBody struct {
	Event  *traceEventPayload  `json:"event"`
	Events []traceEventPayload `json:"events"`
}

const maxIngestEventsPerRequest = 100

// postTraceEvents appends client-reported events (Windmill steps, Emby writes)
// to an existing trace. Idempotency keys make retries safe.
func postTraceEvents(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Param("traceID")
		body := &traceEventsBody{}
		if err := c.ShouldBindJSON(body); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		payloads := body.Events
		if body.Event != nil {
			payloads = append([]traceEventPayload{*body.Event}, payloads...)
		}
		if len(payloads) == 0 {
			abortWithStatusMessage(c, http.StatusBadRequest, "no events supplied")
			return
		}
		if len(payloads) > maxIngestEventsPerRequest {
			abortWithStatusMessage(c, http.StatusRequestEntityTooLarge, "too many events in one request")
			return
		}
		for _, payload := range payloads {
			if strings.TrimSpace(payload.Component) == "" || strings.TrimSpace(payload.Stage) == "" {
				abortWithStatusMessage(c, http.StatusBadRequest, "each event requires component and stage")
				return
			}
		}

		stored, duplicates := 0, 0
		saved := make([]trace.Event, 0, len(payloads))
		for _, payload := range payloads {
			event, inserted, err := service.Append(traceID, payload.asEvent())
			if err != nil {
				abortWithTraceError(c, err)
				return
			}
			if !inserted {
				duplicates++
				continue
			}
			stored++
			saved = append(saved, event)
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"trace_id":   traceID,
			"stored":     stored,
			"duplicates": duplicates,
			"events":     saved,
		}})
	}
}

// traceListItem is a summary plus its derived downstream state, so the list view
// and the drawer share one source of truth for "awaiting client report".
type traceListItem struct {
	trace.Run
	Downstream trace.DownstreamState `json:"downstream"`
}

// getTraces lists trace summaries with the documented filters.
func getTraces(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter := parseTraceFilter(c)
		runs, total, err := service.List(filter)
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		items := make([]traceListItem, 0, len(runs))
		for _, run := range runs {
			items = append(items, traceListItem{Run: run, Downstream: trace.DownstreamFor(run)})
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"traces":   items,
			"total":    total,
			"limit":    filter.Limit,
			"offset":   filter.Offset,
			"counters": service.Counters(),
		}})
	}
}

// getTrace returns one trace summary with its ordered events. The downstream
// state reports what Windmill and Emby actually sent, so a completed trace is
// never presented as still awaiting a report.
func getTrace(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		detail, err := service.Get(c.Param("traceID"))
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		downstream := trace.DownstreamFor(detail.Run)
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"trace":             detail,
			"downstream":        downstream,
			"awaiting_report":   downstream.Awaiting,
			"downstream_stages": []string{trace.ComponentWindmill, trace.ComponentEmby},
		}})
	}
}

// exportTrace returns a JSON export of one trace. Stored data is already
// sanitized, so the export needs no further filtering.
func exportTrace(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		detail, err := service.Get(c.Param("traceID"))
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		c.Header("Content-Disposition", `attachment; filename="trace-`+detail.TraceID+`.json"`)
		c.JSON(http.StatusOK, gin.H{
			"exported_at": time.Now().UTC(),
			"redacted":    true,
			"trace":       detail,
		})
	}
}

// deleteTrace removes a single trace and its events.
func deleteTrace(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		deleted, err := service.Delete(c.Param("traceID"))
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"trace_id": c.Param("traceID"),
			"deleted":  deleted,
		}})
	}
}

// postTracePurge enforces retention on demand. It requires an explicit
// confirmation value so it can never run by accident.
func postTracePurge(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		body := struct {
			Confirm string `json:"confirm"`
		}{}
		if err := c.ShouldBindJSON(&body); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if body.Confirm != "purge-expired" {
			abortWithStatusMessage(c, http.StatusBadRequest, `confirmation value must be "purge-expired"`)
			return
		}
		pruned, err := service.PurgeExpired()
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"pruned":   pruned,
			"counters": service.Counters(),
		}})
	}
}

// getTraceStats reports trace bookkeeping, the effective configuration, and the
// log-search availability so the UI can render both without guessing.
func getTraceStats(service *trace.Service, logs *logsearch.Searcher, mirror LogMirror) gin.HandlerFunc {
	cfg := service.Config()
	graylog := logs.GraylogConfig()
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"enabled":                 service.Enabled(),
			"mirror_enabled":          service.MirrorEnabled(),
			"retention_days":          cfg.RetentionDays,
			"max_runs":                cfg.MaxRuns,
			"max_events_per_run":      cfg.MaxEventsPerRun,
			"max_payload_bytes":       trace.DefaultMaxPayloadBytes,
			"ingest_burst_per_client": trace.DefaultIngestBurstPerClient,
			"counters":                service.Counters(),
			"bounds": gin.H{
				"max_run_traces": trace.MaxRunTraces,
				"max_run_events": trace.MaxRunEvents,
				"max_search":     logsearch.MaxSearchLimit,
			},
			"logs": gin.H{
				"native_retention":      logsearch.NativeRetention,
				"graylog_configured":    graylog.Configured(),
				"graylog_enabled":       graylog.Enabled,
				"graylog_missing_token": graylog.EnabledWithoutToken(),
				"graylog_external_url":  graylog.ExternalURL,
			},
			"ingestion": ingestionStats(mirror),
			"tabs": []string{
				"LOGS-METATUBE-VIDEO",
				"LOGS-METATUBE-ACTOR",
			},
			"log_sections": []string{
				"TRACE TIMELINE",
				"RECENT NATIVE LOGS",
				"GRAYLOG LOGS",
			},
		})
	}
}

type traceFinishBody struct {
	Status             string              `json:"status"`
	SelectedProvider   string              `json:"selected_provider"`
	SelectedProviderID string              `json:"selected_provider_id"`
	EmbyItemID         string              `json:"emby_item_id"`
	WindmillJobID      string              `json:"windmill_job_id"`
	WindmillFlowPath   string              `json:"windmill_flow_path"`
	ResultCount        *int                `json:"result_count"`
	ErrorCode          string              `json:"error_code"`
	ErrorMessage       string              `json:"error_message"`
	DurationMS         float64             `json:"duration_ms"`
	FieldChanges       []trace.FieldChange `json:"field_changes"`
	Events             []traceEventPayload `json:"events"`
}

// postTraceFinish completes a trace, optionally after appending the final
// client events and a redacted field-change summary.
func postTraceFinish(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Param("traceID")
		body := &traceFinishBody{}
		if err := c.ShouldBindJSON(body); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if body.Status != "" && !trace.ValidStatus(body.Status) {
			abortWithStatusMessage(c, http.StatusBadRequest, "unsupported status")
			return
		}
		if len(body.Events) > maxIngestEventsPerRequest {
			abortWithStatusMessage(c, http.StatusRequestEntityTooLarge, "too many events in one request")
			return
		}

		stored := 0
		for _, payload := range body.Events {
			_, inserted, err := service.Append(traceID, payload.asEvent())
			if err != nil {
				abortWithTraceError(c, err)
				return
			}
			if inserted {
				stored++
			}
		}
		if len(body.FieldChanges) > 0 {
			changes := make([]map[string]any, 0, len(body.FieldChanges))
			for _, change := range trace.SanitizeFieldChanges(body.FieldChanges) {
				changes = append(changes, map[string]any{
					"field":       change.Field,
					"action":      change.Action,
					"provider":    change.Provider,
					"value_bytes": change.ValueBytes,
				})
			}
			if _, _, err := service.Append(traceID, trace.Event{
				Component:      trace.ComponentEmby,
				Stage:          trace.StageFieldChanged,
				Message:        "client reported field changes",
				Details:        trace.JSONMap{"changes": changes, "count": len(changes)},
				IdempotencyKey: "finish:field_changes",
			}); err != nil {
				abortWithTraceError(c, err)
				return
			}
		}

		run, err := service.Finish(traceID, trace.FinishInput{
			Status:             body.Status,
			SelectedProvider:   body.SelectedProvider,
			SelectedProviderID: body.SelectedProviderID,
			EmbyItemID:         body.EmbyItemID,
			WindmillJobID:      body.WindmillJobID,
			WindmillFlowPath:   body.WindmillFlowPath,
			ResultCount:        body.ResultCount,
			ErrorCode:          body.ErrorCode,
			ErrorMessage:       body.ErrorMessage,
			DurationMS:         body.DurationMS,
		})
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"trace_id":        traceID,
			"trace":           run,
			"stored_events":   stored,
			"downstream":      trace.DownstreamFor(*run),
			"awaiting_report": trace.DownstreamFor(*run).Awaiting,
		}})
	}
}
