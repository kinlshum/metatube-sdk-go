package route

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/internal/logsearch"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// parseLogQuery reads the shared log filter set from the query string. The same
// filters drive the Native and Graylog backends, so one filter bar controls both.
func parseLogQuery(c *gin.Context) logsearch.Query {
	query := logsearch.Query{
		TraceIDs:      c.QueryArray("trace_id"),
		RunID:         c.Query("run_id"),
		WindmillJobID: c.Query("windmill_job_id"),
		Text:          c.Query("q"),
		Component:     c.Query("component"),
		Level:         c.Query("level"),
		Provider:      c.Query("provider"),
	}
	if value := c.Query("limit"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			query.Limit = parsed
		}
	}
	if value := c.Query("since"); value != "" {
		query.Since = parseTraceTime(value)
	}
	if value := c.Query("until"); value != "" {
		query.Until = parseTraceTime(value)
	}
	return query.Normalize()
}

// parseLogSources reads the requested sources, defaulting to every source.
func parseLogSources(c *gin.Context) []logsearch.Source {
	values := c.QueryArray("source")
	sources := make([]logsearch.Source, 0, len(values))
	for _, value := range values {
		if logsearch.ValidSource(value) {
			sources = append(sources, logsearch.Source(strings.ToLower(strings.TrimSpace(value))))
		}
	}
	return sources
}

// getAdminLogs serves the recent native log buffer. The legacy `entries` field is
// kept for the general LOGS tab, and the same request now applies server-side
// filters so the UI never has to download every line to filter it.
func getAdminLogs(searcher *logsearch.Searcher) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := parseLogQuery(c)
		result := searcher.Search(c.Request.Context(), query, []logsearch.Source{logsearch.SourceNative})[0]

		entries := make([]gin.H, 0, len(result.Lines))
		for _, line := range result.Lines {
			entries = append(entries, gin.H{
				"at":          line.At,
				"message":     line.Message,
				"source":      line.Source,
				"badge":       line.Badge,
				"level":       line.Level,
				"component":   line.Component,
				"provider":    line.Provider,
				"trace_id":    line.TraceID,
				"fingerprint": line.Fingerprint,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"entries":     entries,
			"truncated":   result.Truncated,
			"match_count": result.MatchCount,
			"retention":   result.Retention,
			"effective":   result.Effective,
		})
	}
}

// getAdminLogSearch returns every requested source side by side, each with its own
// status, so a slow or broken Graylog can never delay or fail the trace timeline.
func getAdminLogSearch(searcher *logsearch.Searcher) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := parseLogQuery(c)
		sources := parseLogSources(c)
		results := searcher.Search(c.Request.Context(), query, sources)

		lines := make([]logsearch.Line, 0, 64)
		for _, result := range results {
			lines = append(lines, result.Lines...)
		}
		deduped, hidden := logsearch.Deduplicate(lines)

		cfg := searcher.GraylogConfig()
		c.JSON(http.StatusOK, gin.H{
			"results":      results,
			"deduplicated": deduped,
			"hidden":       hidden,
			"effective":    query,
			"graylog": gin.H{
				"configured":        cfg.Configured(),
				"enabled":           cfg.Enabled,
				"missing_token":     cfg.EnabledWithoutToken(),
				"external_url":      cfg.ExternalURL,
				"max_results":       cfg.MaxResults,
				"max_range_hours":   cfg.MaxRangeHours,
				"timeout_seconds":   int(cfg.Timeout.Seconds()),
				"search_api":        "search/universal/absolute",
				"credential_source": cfg.TokenSourceLabel(),
				"credential_error":  cfg.TokenFileErr,
			},
			"retention": gin.H{
				"native":  logsearch.NativeRetention,
				"graylog": "Durable; retention is managed by Graylog",
				"trace":   "Structured trace events follow the configured trace retention",
			},
		})
	}
}

// getTraceRun returns one grouped run: its explicitly related traces and their
// ordered events in a single bounded response.
func getTraceRun(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		eventLimit, _ := strconv.Atoi(c.DefaultQuery("events", strconv.Itoa(trace.MaxRunEvents)))
		group, err := service.ResolveRun(c.Param("runID"), eventLimit)
		if err != nil {
			abortWithTraceError(c, err)
			return
		}
		downstream := make([]gin.H, 0, len(group.Traces))
		for _, run := range group.Traces {
			downstream = append(downstream, gin.H{
				"trace_id":         run.TraceID,
				"downstream":       trace.DownstreamFor(run),
				"awaiting_report":  trace.DownstreamFor(run).Awaiting,
				"events_available": run.EventCount,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"run":             group,
			"downstream":      downstream,
			"bounds":          gin.H{"max_traces": trace.MaxRunTraces, "max_events": trace.MaxRunEvents},
			"awaiting_report": awaitingAny(group.Traces),
		})
	}
}

func awaitingAny(runs []trace.Run) bool {
	for _, run := range runs {
		if trace.DownstreamFor(run).Awaiting {
			return true
		}
	}
	return false
}
