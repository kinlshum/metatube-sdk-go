package gelf

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

const (
	// gelfVersion is the GELF protocol version, which is unrelated to the
	// application build version reported in the `version` field.
	gelfVersion = "1.1"
	// defaultSourceType labels records that do not name their producer.
	defaultSourceType = "metatube"
)

// payload renders one record as a GELF 1.1 JSON message. Standard GELF fields
// stay unprefixed; every additional field is prefixed with an underscore as the
// spec requires (Graylog stores them without the prefix, so `_trace_id` is
// searchable as `trace_id`).
func (s *Sender) payload(event trace.MirrorEvent) ([]byte, error) {
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	fields := map[string]any{
		"version":       gelfVersion,
		"host":          s.cfg.Server,
		"short_message": shortMessage(event.Message),
		"timestamp":     float64(at.UTC().UnixMilli()) / 1000,
		"level":         syslogLevel(event.Level),
		"_log_level":    normalizedLevel(event.Level),
		"_application":  s.cfg.Application,
		"_service":      s.cfg.Service,
		"_server":       s.cfg.Server,
		"_node":         s.cfg.Node,
		"_environment":  s.cfg.Environment,
		"_source_type":  sourceType(event.SourceType),
		"_logger":       loggerName(s.cfg.Service, event.Component),
		"_trace_id":     event.TraceID,
	}
	if s.cfg.Version != "" {
		fields["_version"] = bounded(s.cfg.Version, 64)
	}
	setField(fields, "_run_id", event.RunID, 64)
	setField(fields, "_parent_trace_id", event.ParentTraceID, 64)
	setField(fields, "_windmill_job_id", event.WindmillJobID, 128)
	setField(fields, "_emby_item_id", event.EmbyItemID, 128)
	setField(fields, "_client", event.Client, 64)
	setField(fields, "_kind", event.Kind, 32)
	setField(fields, "_operation", event.Operation, 32)
	setField(fields, "_status", event.Status, 32)
	setField(fields, "_component", event.Component, 32)
	setField(fields, "_stage", event.Stage, 48)
	setField(fields, "_provider", event.Provider, 128)
	if event.Attempt != 0 {
		fields["_attempt"] = event.Attempt
	}
	if event.HTTPStatus != 0 {
		fields["_http_status"] = event.HTTPStatus
	}
	if event.DurationMS != 0 {
		fields["_duration_ms"] = event.DurationMS
	}
	if details := trace.SanitizeDetails(event.Details); len(details) > 0 {
		if encoded, err := json.Marshal(details); err == nil {
			fields["_details"] = bounded(string(encoded), maxDetailsFieldSize)
		}
	}
	return json.Marshal(fields)
}

// setField stores a sanitized, bounded string field and skips empty values.
func setField(fields map[string]any, key, value string, limit int) {
	sanitized := bounded(trace.SanitizeString(value), limit)
	if sanitized == "" {
		return
	}
	fields[key] = sanitized
}

func bounded(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		return trace.Truncate(value, limit)
	}
	return value
}

// shortMessage keeps a human-readable message inside the collector's limits.
func shortMessage(message string) string {
	sanitized := bounded(trace.SanitizeString(message), maxShortMessage)
	if sanitized == "" {
		return "metatube trace record"
	}
	return sanitized
}

func sourceType(value string) string {
	sanitized := bounded(value, 32)
	if sanitized == "" {
		return defaultSourceType
	}
	return sanitized
}

// loggerName reports which producer emitted the record, so a Graylog search can
// filter by component without a second field.
func loggerName(service, component string) string {
	switch {
	case component != "" && service != "":
		return service + "." + component
	case component != "":
		return component
	case service != "":
		return service
	}
	return defaultSourceType
}

// normalizedLevel maps a trace level onto the level name the spec reports.
func normalizedLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case trace.LevelDebug:
		return trace.LevelDebug
	case trace.LevelWarn, "warning":
		return trace.LevelWarn
	case trace.LevelError, "fatal", "critical", "panic":
		return trace.LevelError
	case trace.LevelInfo, "":
		return trace.LevelInfo
	}
	return trace.LevelInfo
}

// syslogLevel maps a trace level onto the numeric GELF level.
func syslogLevel(level string) int {
	switch normalizedLevel(level) {
	case trace.LevelDebug:
		return 7
	case trace.LevelWarn:
		return 4
	case trace.LevelError:
		return 3
	}
	return 6
}
