package trace

import (
	"context"
	"time"
)

// contextKey is unexported so only this package can attach a trace handle.
type contextKey struct{}

// WithContext attaches a trace handle to a Go context. The same handle is shared
// with every goroutine that derives from this context, which is what makes
// concurrent provider work (for example an all-providers search) attributable.
func WithContext(ctx context.Context, handle *RunHandle) context.Context {
	if handle == nil || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, handle)
}

// FromContext returns the trace handle attached to ctx, or nil when the request
// is not traced.
func FromContext(ctx context.Context) *RunHandle {
	if ctx == nil {
		return nil
	}
	handle, _ := ctx.Value(contextKey{}).(*RunHandle)
	return handle
}

// TraceIDFromContext returns the trace ID attached to ctx, or an empty string.
func TraceIDFromContext(ctx context.Context) string {
	return FromContext(ctx).TraceID()
}

// Emit records an event against the trace carried by ctx. It is a no-op when the
// request is not traced, so instrumentation never changes request behaviour.
func Emit(ctx context.Context, event Event) {
	if handle := FromContext(ctx); handle != nil {
		handle.Event(event)
	}
}

// EmitError records a failed stage against the trace carried by ctx.
func EmitError(ctx context.Context, component, stage, provider, message string, details JSONMap) {
	if handle := FromContext(ctx); handle != nil {
		handle.EventError(component, stage, provider, message, details)
	}
}

// TimedEmit records an event with the duration measured from start.
func TimedEmit(ctx context.Context, start time.Time, event Event) {
	handle := FromContext(ctx)
	if handle == nil {
		return
	}
	event.DurationMS = float64(time.Since(start).Microseconds()) / 1000
	handle.Event(event)
}

// Select records the provider result the server chose for a lookup.
func Select(ctx context.Context, provider, id string) {
	if handle := FromContext(ctx); handle != nil {
		handle.SetSelected(provider, id)
	}
}

// Changes records a redacted enrichment field-change summary.
func Changes(ctx context.Context, provider string, changes []FieldChange) {
	if handle := FromContext(ctx); handle != nil {
		handle.FieldChanges(provider, changes)
	}
}
