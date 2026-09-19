// Package logsearch provides the combined run-log experience for the MetaTube
// admin: durable structured trace events, recent in-process native logs, and an
// optional server-side Graylog adapter behind one common, safe result model.
package logsearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// Source identifies which system produced a log line or a log result set.
type Source string

const (
	// SourceTrace is the durable structured trace timeline (traces.db).
	SourceTrace Source = "trace"
	// SourceNative is the in-process, restart-cleared application log buffer.
	SourceNative Source = "native"
	// SourceGraylog is the optional durable cross-service log backend.
	SourceGraylog Source = "graylog"
)

// SourceBadge returns the badge the UI must show for a source.
func SourceBadge(source Source) string {
	switch source {
	case SourceTrace:
		return "TRACE"
	case SourceNative:
		return "NATIVE"
	case SourceGraylog:
		return "GRAYLOG"
	}
	return strings.ToUpper(string(source))
}

// ValidSource reports whether a client-supplied source name is supported.
func ValidSource(value string) bool {
	switch Source(strings.ToLower(strings.TrimSpace(value))) {
	case SourceNative, SourceGraylog:
		return true
	}
	return false
}

const (
	// MaxSearchLimit bounds how many lines one backend may return.
	MaxSearchLimit = 500
	// DefaultSearchLimit is used when the caller supplies no limit.
	DefaultSearchLimit = 200
	// MaxTraceWindows bounds how many trace IDs may be correlated at once.
	MaxTraceWindows = 50
)

// Query is the shared filter set for every log backend. The same query is used
// for Native and Graylog so both react to one filter bar.
type Query struct {
	TraceIDs      []string   `json:"trace_ids,omitempty"`
	RunID         string     `json:"run_id,omitempty"`
	WindmillJobID string     `json:"windmill_job_id,omitempty"`
	Text          string     `json:"q,omitempty"`
	Component     string     `json:"component,omitempty"`
	Level         string     `json:"level,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	Since         *time.Time `json:"since,omitempty"`
	Until         *time.Time `json:"until,omitempty"`
	Limit         int        `json:"limit"`
}

// Normalize bounds and trims the query before it reaches a backend.
func (q Query) Normalize() Query {
	normalized := q
	normalized.Text = strings.TrimSpace(q.Text)
	normalized.Component = strings.ToLower(strings.TrimSpace(q.Component))
	normalized.Level = strings.ToLower(strings.TrimSpace(q.Level))
	normalized.Provider = strings.TrimSpace(q.Provider)
	normalized.RunID = trace.NormalizeID(q.RunID)
	normalized.WindmillJobID = trace.NormalizeID(q.WindmillJobID)
	// Only well-formed trace IDs are accepted; anything else is dropped so a
	// hostile query cannot inject syntax into a backend.
	ids := make([]string, 0, len(q.TraceIDs))
	seen := make(map[string]bool, len(q.TraceIDs))
	for _, id := range q.TraceIDs {
		clean := trace.NormalizeID(id)
		if clean == "" || seen[clean] {
			continue
		}
		seen[clean] = true
		ids = append(ids, clean)
		if len(ids) >= MaxTraceWindows {
			break
		}
	}
	normalized.TraceIDs = ids
	if normalized.Limit <= 0 {
		normalized.Limit = DefaultSearchLimit
	}
	if normalized.Limit > MaxSearchLimit {
		normalized.Limit = MaxSearchLimit
	}
	if normalized.Since != nil && normalized.Until != nil && normalized.Until.Before(*normalized.Since) {
		normalized.Since, normalized.Until = normalized.Until, normalized.Since
	}
	return normalized
}

// Line is one log line or structured event in a safe, common shape.
type Line struct {
	Source      Source    `json:"source"`
	Badge       string    `json:"badge"`
	At          time.Time `json:"at"`
	Level       string    `json:"level,omitempty"`
	Component   string    `json:"component,omitempty"`
	Stage       string    `json:"stage,omitempty"`
	Provider    string    `json:"provider,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	RunID       string    `json:"run_id,omitempty"`
	WindmillJob string    `json:"windmill_job_id,omitempty"`
	Message     string    `json:"message"`
	Fingerprint string    `json:"fingerprint"`
}

// Result is one backend's answer for a query.
type Result struct {
	Source            Source     `json:"source"`
	Badge             string     `json:"badge"`
	Configured        bool       `json:"configured"`
	Available         bool       `json:"available"`
	Status            string     `json:"status"`
	Detail            string     `json:"detail,omitempty"`
	Error             string     `json:"error,omitempty"`
	Lines             []Line     `json:"lines"`
	MatchCount        int        `json:"match_count"`
	Truncated         bool       `json:"truncated"`
	Effective         Query      `json:"effective"`
	LastSuccessAt     *time.Time `json:"last_success_at,omitempty"`
	ExternalSearchURL string     `json:"external_search_url,omitempty"`
	Retention         string     `json:"retention,omitempty"`
}

// Backend is the narrow interface the trace routes depend on, so they are never
// coupled to Graylog.
type Backend interface {
	Source() Source
	Search(ctx context.Context, query Query) Result
}

// Fingerprint identifies a log line for visual de-duplication across backends.
// Structured trace events are never de-duplicated, because they are not raw log
// lines: they carry their own sequence and purpose.
func Fingerprint(source Source, at time.Time, level, component, traceID, message string) string {
	bucket := at.UTC().Truncate(time.Minute).Format(time.RFC3339)
	normalized := normalizeMessage(message)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		string(source), bucket, strings.ToLower(level), strings.ToLower(component), traceID, normalized,
	}, "\x00")))
	return hex.EncodeToString(sum[:12])
}

// normalizeMessage collapses whitespace and drops volatile prefixes so two
// backends describing the same event produce the same fingerprint.
func normalizeMessage(message string) string {
	fields := strings.Fields(strings.ToLower(message))
	return strings.Join(fields, " ")
}
