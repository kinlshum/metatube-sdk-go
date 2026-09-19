package logsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// graylogFields lists the record fields the adapter reads. Only these are copied
// into a result, so restricted fields never leave Graylog.
var graylogFields = []string{
	"timestamp",
	"message",
	"level",
	"component",
	"stage",
	"provider",
	"attempt",
	"duration_ms",
	"http_status",
	"trace_id",
	"run_id",
	"parent_trace_id",
	"windmill_job_id",
	"emby_item_id",
	"client",
	"container_name",
}

// graylogTimeFormat is the format Graylog accepts and returns for absolute
// ranges.
const graylogTimeFormat = "2006-01-02T15:04:05.000Z"

// GraylogBackend searches Graylog through its search API. It never scrapes the
// Graylog web UI and never exposes credentials to callers.
type GraylogBackend struct {
	cfg    GraylogConfig
	client *http.Client

	mu            sync.Mutex
	lastSuccessAt *time.Time
}

// NewGraylogBackend returns a Graylog backend. When Graylog is disabled or not
// fully configured the backend still answers, with `configured: false`.
func NewGraylogBackend(cfg GraylogConfig) *GraylogBackend {
	cfg = cfg.WithDefaults()
	return &GraylogBackend{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			// Redirects must never leak credentials to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Source implements Backend.
func (b *GraylogBackend) Source() Source { return SourceGraylog }

// Config returns the effective configuration (without secrets in JSON).
func (b *GraylogBackend) Config() GraylogConfig { return b.cfg }

// Search implements Backend.
func (b *GraylogBackend) Search(ctx context.Context, query Query) Result {
	query = query.Normalize()
	result := Result{
		Source:     SourceGraylog,
		Badge:      SourceBadge(SourceGraylog),
		Configured: b.cfg.Configured(),
		Effective:  query,
		Lines:      make([]Line, 0),
		Retention:  "Durable; retention is managed by Graylog",
	}

	// Bound the requested range so one query can never scan an unbounded window.
	since, until, clamped := b.cfg.ClampRange(query.Since, query.Until)
	result.Effective.Since, result.Effective.Until = since, until

	graylogQuery := b.buildQuery(result.Effective)
	result.ExternalSearchURL = b.externalSearchURL(graylogQuery, since, until)

	if !result.Configured {
		result.Status = "not_configured"
		result.Detail = "Graylog not configured"
		return result
	}

	requestCtx, cancel := context.WithTimeout(ctx, b.cfg.Timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet,
		b.searchURL(graylogQuery, since, until, result.Effective.Limit), nil)
	if err != nil {
		return b.failed(result, "Graylog request could not be built", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Requested-By", "metatube-admin")
	// Graylog API tokens authenticate as the username with the literal password
	// "token"; the token itself never leaves this process.
	request.SetBasicAuth(b.cfg.Token, "token")

	response, err := b.client.Do(request)
	if err != nil {
		if requestCtx.Err() == context.DeadlineExceeded {
			return b.failed(result, "Graylog search timed out", err)
		}
		return b.failed(result, "Graylog is unreachable", err)
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return b.failed(result, "Graylog rejected the configured token", nil)
	default:
		return b.failed(result, fmt.Sprintf("Graylog returned HTTP %d", response.StatusCode), nil)
	}

	payload := struct {
		TotalResults int `json:"total_results"`
		Messages     []struct {
			Message map[string]any `json:"message"`
		} `json:"messages"`
	}{}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return b.failed(result, "Graylog returned an unreadable response", err)
	}

	lines := make([]Line, 0, len(payload.Messages))
	for _, wrapper := range payload.Messages {
		lines = append(lines, graylogLine(wrapper.Message))
	}
	result.Lines = lines
	result.MatchCount = payload.TotalResults
	if result.MatchCount < len(lines) {
		result.MatchCount = len(lines)
	}
	result.Truncated = result.MatchCount > len(lines)
	result.Available = true
	result.Status = "ok"
	if clamped {
		result.Detail = fmt.Sprintf("time range clamped to %d hours", b.cfg.MaxRangeHours)
	}
	b.markSuccess()
	result.LastSuccessAt = b.lastSuccess()
	return result
}

func (b *GraylogBackend) failed(result Result, detail string, err error) Result {
	result.Status = "error"
	result.Detail = detail
	if err != nil {
		// The error may echo a URL, so it is sanitized before it is returned.
		result.Error = trace.SanitizeString(err.Error())
	} else {
		result.Error = trace.SanitizeString(detail)
	}
	result.LastSuccessAt = b.lastSuccess()
	return result
}

// graylogValue escapes a value for Graylog query syntax.
func graylogValue(value string) string {
	cleaned := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + cleaned + `"`
}

// buildQuery produces a Graylog query string restricted to the configured stream
// and the correlation IDs, with every value escaped for Graylog query syntax.
func (b *GraylogBackend) buildQuery(query Query) string {
	clauses := make([]string, 0, 8)
	for _, traceID := range query.TraceIDs {
		clauses = append(clauses, "trace_id:"+graylogValue(traceID))
	}
	if query.RunID != "" {
		clauses = append(clauses, "run_id:"+graylogValue(query.RunID))
	}
	if query.WindmillJobID != "" {
		clauses = append(clauses, "windmill_job_id:"+graylogValue(query.WindmillJobID))
	}
	if query.Component != "" {
		clauses = append(clauses, "component:"+graylogValue(query.Component))
	}
	if query.Provider != "" {
		clauses = append(clauses, "provider:"+graylogValue(query.Provider))
	}
	if query.Level != "" {
		clauses = append(clauses, "level:"+graylogValue(query.Level))
	}

	must := make([]string, 0, 3)
	if b.cfg.StreamID != "" {
		must = append(must, "stream:"+graylogValue(b.cfg.StreamID))
	}
	switch len(clauses) {
	case 0:
	case 1:
		must = append(must, clauses[0])
	default:
		must = append(must, "("+strings.Join(clauses, " OR ")+")")
	}
	if query.Text != "" {
		must = append(must, graylogValue(query.Text))
	}
	if len(must) == 0 {
		return "*"
	}
	return strings.Join(must, " AND ")
}

func (b *GraylogBackend) searchURL(query string, since, until *time.Time, limit int) string {
	endpoint := strings.TrimRight(b.cfg.APIURL, "/") + "/search/universal/absolute"
	values := url.Values{}
	values.Set("query", query)
	if since != nil {
		values.Set("from", since.UTC().Format(graylogTimeFormat))
	}
	if until != nil {
		values.Set("to", until.UTC().Format(graylogTimeFormat))
	}
	values.Set("limit", strconv.Itoa(limit))
	values.Set("sort", "timestamp:desc")
	values.Set("fields", strings.Join(graylogFields, ","))
	return endpoint + "?" + values.Encode()
}

func (b *GraylogBackend) externalSearchURL(query string, since, until *time.Time) string {
	if b.cfg.ExternalURL == "" {
		return ""
	}
	values := url.Values{}
	values.Set("q", query)
	values.Set("rangetype", "absolute")
	if since != nil {
		values.Set("from", since.UTC().Format(graylogTimeFormat))
	}
	if until != nil {
		values.Set("to", until.UTC().Format(graylogTimeFormat))
	}
	return strings.TrimRight(b.cfg.ExternalURL, "/") + "/search?" + values.Encode()
}

func (b *GraylogBackend) markSuccess() {
	now := time.Now().UTC()
	b.mu.Lock()
	b.lastSuccessAt = &now
	b.mu.Unlock()
}

func (b *GraylogBackend) lastSuccess() *time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastSuccessAt == nil {
		return nil
	}
	value := *b.lastSuccessAt
	return &value
}

// graylogLine converts one Graylog message into a safe, common line. Only the
// allow-listed fields are read; everything else stays in Graylog.
func graylogLine(fields map[string]any) Line {
	line := Line{
		Source: SourceGraylog,
		Badge:  SourceBadge(SourceGraylog),
		At:     graylogTimestamp(fields["timestamp"]),
	}
	line.Level = trace.SanitizeString(stringField(fields, "level"))
	line.Component = trace.SanitizeString(stringField(fields, "component"))
	line.Stage = trace.SanitizeString(stringField(fields, "stage"))
	line.Provider = trace.SanitizeString(stringField(fields, "provider"))
	line.TraceID = trace.NormalizeID(stringField(fields, "trace_id"))
	line.RunID = trace.NormalizeID(stringField(fields, "run_id"))
	line.WindmillJob = trace.NormalizeID(stringField(fields, "windmill_job_id"))
	line.Message = trace.SanitizeString(stringField(fields, "message"))
	if line.Message == "" {
		// Fall back to a compact, redacted key/value rendering.
		line.Message = trace.SanitizeString(strings.Join(summarizeFields(fields), " "))
	}
	line.Fingerprint = Fingerprint(SourceGraylog, line.At, line.Level, line.Component, line.TraceID, line.Message)
	return line
}

func summarizeFields(fields map[string]any) []string {
	parts := make([]string, 0, len(fields))
	for _, key := range graylogFields {
		if key == "message" || key == "timestamp" {
			continue
		}
		if value := stringField(fields, key); value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return parts
}

func stringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	}
	return ""
}

// graylogTimestamp parses the timestamp formats Graylog may return.
func graylogTimestamp(value any) time.Time {
	if typed, ok := value.(string); ok {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, graylogTimeFormat} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed
			}
		}
	}
	return time.Now().UTC()
}
