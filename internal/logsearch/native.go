package logsearch

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/metatube-community/metatube-sdk-go/internal/logbuffer"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// NativeRetention is the label the UI must show for the in-process buffer.
const NativeRetention = "Recent logs; cleared on restart"

var errInvalidStatus = errors.New("invalid status code")

var (
	traceIDPattern  = regexp.MustCompile(`(?:trace|trace_id|traceId)[=:]\s*"?([A-Za-z0-9_.:-]{8,64})"?`)
	providerPattern = regexp.MustCompile(`/v1/(?:movies|actors|reviews|images)/(?:primary|thumb|backdrop/)?([A-Za-z0-9][A-Za-z0-9_.-]{0,63})`)
	statusPattern   = regexp.MustCompile(`\|\s*(\d{3})\s*\|`)
)

// NativeBackend searches the bounded in-process log buffer. It is fast and
// always available, but its contents are lost when MetaTube restarts.
type NativeBackend struct {
	// Entries reads the buffer; overridable in tests.
	Entries func(limit int) []logbuffer.Entry
}

// NewNativeBackend returns a backend backed by the process log buffer.
func NewNativeBackend() *NativeBackend {
	return &NativeBackend{Entries: logbuffer.Entries}
}

// Source implements Backend.
func (b *NativeBackend) Source() Source { return SourceNative }

// Search implements Backend.
func (b *NativeBackend) Search(_ context.Context, query Query) Result {
	query = query.Normalize()
	result := Result{
		Source:     SourceNative,
		Badge:      SourceBadge(SourceNative),
		Configured: true,
		Available:  true,
		Status:     "ok",
		Effective:  query,
		Retention:  NativeRetention,
	}

	entries := b.Entries(0)
	lines := make([]Line, 0, len(entries))
	for _, entry := range entries {
		line := nativeLine(entry)
		if !nativeMatches(line, entry.Message, query) {
			continue
		}
		lines = append(lines, line)
	}

	result.MatchCount = len(lines)
	// Newest first, like the general log viewer.
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	if len(lines) > query.Limit {
		result.Truncated = true
		lines = lines[:query.Limit]
	}
	result.Lines = lines
	return result
}

// nativeLine derives safe structured fields from a raw log line.
func nativeLine(entry logbuffer.Entry) Line {
	line := Line{
		Source:  SourceNative,
		Badge:   SourceBadge(SourceNative),
		At:      entry.At,
		Level:   trace.LevelInfo,
		Message: trace.SanitizeString(entry.Message),
	}
	if match := traceIDPattern.FindStringSubmatch(entry.Message); match != nil {
		line.TraceID = match[1]
	}
	if match := providerPattern.FindStringSubmatch(entry.Message); match != nil {
		line.Provider = match[1]
	}
	switch {
	case strings.Contains(entry.Message, "[GORM]"):
		line.Component = trace.ComponentDatabase
	case strings.Contains(entry.Message, "[GIN]"), strings.Contains(entry.Message, "[TRACE]"):
		line.Component = trace.ComponentMetaTube
	case strings.Contains(entry.Message, "flaresolverr") || strings.Contains(entry.Message, "FlareSolverr"):
		line.Component = trace.ComponentFlareSolverr
	}
	if match := statusPattern.FindStringSubmatch(entry.Message); match != nil {
		if status, err := parseStatus(match[1]); err == nil {
			switch {
			case status >= 500:
				line.Level = trace.LevelError
			case status >= 400:
				line.Level = trace.LevelWarn
			}
		}
	}
	if strings.Contains(strings.ToLower(entry.Message), "panic") {
		line.Level = trace.LevelError
	}
	line.Fingerprint = Fingerprint(SourceNative, line.At, line.Level, line.Component, line.TraceID, entry.Message)
	return line
}

func parseStatus(value string) (int, error) {
	status := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errInvalidStatus
		}
		status = status*10 + int(char-'0')
	}
	return status, nil
}

// nativeMatches applies the shared filters to a candidate line.
func nativeMatches(line Line, raw string, query Query) bool {
	if len(query.TraceIDs) > 0 {
		if !matchesAnyTraceID(raw, query.TraceIDs) {
			return false
		}
	}
	if query.RunID != "" && !strings.Contains(raw, query.RunID) {
		return false
	}
	if query.WindmillJobID != "" && !strings.Contains(raw, query.WindmillJobID) {
		return false
	}
	if query.Provider != "" && !strings.EqualFold(line.Provider, query.Provider) {
		return false
	}
	if query.Component != "" && !strings.EqualFold(line.Component, query.Component) {
		return false
	}
	if query.Level != "" && !strings.EqualFold(line.Level, query.Level) {
		return false
	}
	if query.Since != nil && line.At.Before(*query.Since) {
		return false
	}
	if query.Until != nil && line.At.After(*query.Until) {
		return false
	}
	if query.Text != "" {
		pattern, err := safePattern(query.Text)
		if err != nil {
			return false
		}
		if !pattern.MatchString(raw) {
			return false
		}
	}
	return true
}

// matchesAnyTraceID matches a trace ID exactly. The ID is extracted and compared
// with string equality, so a prefix or a pattern metacharacter can never broaden
// the match.
func matchesAnyTraceID(raw string, traceIDs []string) bool {
	matches := traceIDPattern.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		for _, traceID := range traceIDs {
			if match[1] == traceID {
				return true
			}
		}
	}
	return false
}

func safePattern(value string) (*regexp.Regexp, error) {
	return regexp.Compile(`(?i)` + regexp.QuoteMeta(value) + `(?:\b|$)`)
}
