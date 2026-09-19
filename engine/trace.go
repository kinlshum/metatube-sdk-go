package engine

import (
	"context"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// traceProviderResult records the outcome of one provider call. It is a no-op
// when the request is not traced, and it never returns an error.
func traceProviderResult(ctx context.Context, provider string, started time.Time, err error, details trace.JSONMap) {
	if details == nil {
		details = trace.JSONMap{}
	}
	event := trace.Event{
		Component: trace.ComponentProvider,
		Provider:  provider,
		Details:   details,
	}
	if err != nil {
		event.Stage = trace.StageProviderFailed
		event.Level = trace.LevelError
		event.Message = err.Error()
	} else {
		event.Stage = trace.StageProviderCompleted
	}
	trace.TimedEmit(ctx, started, event)
}

// traceCacheLookup records a database/cache lookup for a trace timeline.
func traceCacheLookup(ctx context.Context, source, provider string, hit bool, count int, err error) {
	event := trace.Event{
		Component: trace.ComponentDatabase,
		Stage:     trace.StageCacheLookup,
		Provider:  provider,
		Details: trace.JSONMap{
			"source":       source,
			"hit":          hit,
			"result_count": count,
		},
	}
	if err != nil {
		event.Details["error"] = err.Error()
	}
	trace.Emit(ctx, event)
}

// traceFallbackResult records the outcome of a database fallback lookup.
func traceFallbackResult(ctx context.Context, provider string, count int, err error) {
	event := trace.Event{
		Component: trace.ComponentDatabase,
		Stage:     trace.StageFallbackStarted,
		Provider:  provider,
		Details: trace.JSONMap{
			"result_count": count,
			"used":         count > 0,
		},
	}
	if err != nil {
		event.Level = trace.LevelWarn
		event.Details["error"] = err.Error()
	}
	trace.Emit(ctx, event)
}

// traceSelection records which provider result the server chose, together with
// the exact result count, so the summary is correct without reading events.
func traceSelection(ctx context.Context, provider, id string, count int) {
	if provider == "" {
		return
	}
	trace.SelectWithCount(ctx, provider, id, count)
	trace.Emit(ctx, trace.Event{
		Component: trace.ComponentMetaTube,
		Stage:     trace.StageResultSelected,
		Provider:  provider,
		Details: trace.JSONMap{
			"provider_id":  id,
			"result_count": count,
		},
	})
}
