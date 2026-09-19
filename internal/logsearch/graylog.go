package logsearch

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// maxJSONAnswerBytes bounds a JSON envelope answer so a broken backend cannot
// stream unbounded data into memory.
const maxJSONAnswerBytes = 32 << 20

// graylogFields lists the record fields the adapter reads. Only these are copied
// into a result, so restricted fields never leave Graylog.
var graylogFields = []string{
	"timestamp",
	"message",
	"level",
	"log_level",
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
	"application",
	"service",
	"server",
	"node",
	"environment",
	"source_type",
	"logger",
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
	// A `fields` query answers with one CSV row per message, but only when CSV is
	// accepted: with `Accept: application/json` Graylog 7 replies with a JSON
	// envelope whose `messages` array is empty for this endpoint, which would look
	// like "no matches". The decoder still tolerates a JSON envelope so a version
	// difference cannot silently empty the panel.
	request.Header.Set("Accept", "text/csv")
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

	payload, err := decodeGraylogCSV(response.Body, result.Effective.Limit)
	if err != nil {
		return b.failed(result, "Graylog returned an unreadable response", err)
	}

	result.Lines = payload.Lines
	result.MatchCount = payload.Total
	if result.MatchCount < len(payload.Lines) {
		result.MatchCount = len(payload.Lines)
	}
	result.Truncated = payload.Total > len(payload.Lines)
	result.Available = true
	result.Status = "ok"
	if clamped {
		result.Detail = fmt.Sprintf("time range clamped to %d hours", b.cfg.MaxRangeHours)
	}
	b.markSuccess()
	result.LastSuccessAt = b.lastSuccess()
	return result
}

// graylogAnswer is the decoded answer of an absolute search.
type graylogAnswer struct {
	Lines []Line
	Total int
}

// firstNonSpace returns the first non-whitespace byte of a peeked buffer.
func firstNonSpace(data []byte) byte {
	for _, char := range data {
		switch char {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return char
	}
	return 0
}

// decodeGraylogJSON reads the JSON envelope (or the newline-delimited stream of
// envelopes) Graylog returns when it does not answer with CSV. Only the fields
// requested from Graylog are copied out, so the result stays bounded.
func decodeGraylogJSON(body io.Reader, limit int) (graylogAnswer, error) {
	answer := graylogAnswer{Lines: make([]Line, 0, 32)}
	decoder := json.NewDecoder(io.LimitReader(body, maxJSONAnswerBytes))
	for {
		envelope := struct {
			TotalResults int `json:"total_results"`
			Messages     []struct {
				Message map[string]any `json:"message"`
			} `json:"messages"`
		}{}
		err := decoder.Decode(&envelope)
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(answer.Lines) > 0 {
				// A truncated trailing chunk still leaves the parsed lines usable.
				break
			}
			return graylogAnswer{}, err
		}
		if envelope.TotalResults > answer.Total {
			answer.Total = envelope.TotalResults
		}
		for _, wrapper := range envelope.Messages {
			message := sanitizeGraylogFields(wrapper.Message)
			if len(message) == 0 {
				continue
			}
			if answer.Total < len(answer.Lines)+1 {
				answer.Total = len(answer.Lines) + 1
			}
			if limit > 0 && len(answer.Lines) >= limit {
				continue
			}
			answer.Lines = append(answer.Lines, graylogLine(message))
		}
	}
	sort.SliceStable(answer.Lines, func(i, j int) bool {
		return answer.Lines[i].At.After(answer.Lines[j].At)
	})
	return answer, nil
}

// sanitizeGraylogFields keeps only the allow-listed fields of a JSON message and
// bounds every value, mirroring what the CSV path does.
func sanitizeGraylogFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	clean := make(map[string]any, len(fields))
	for _, key := range graylogFields {
		value, ok := fields[key]
		if !ok || value == nil {
			continue
		}
		if text, isString := value.(string); isString {
			clean[key] = trace.SanitizeString(text)
			continue
		}
		clean[key] = value
	}
	return clean
}

// decodeGraylogCSV parses the answer Graylog returns for an absolute search once
// `fields` is requested (mandatory in Graylog 7): a header row of field names
// followed by one row per message. Only the documented fields are requested, so
// restricted fields stay inside Graylog.
//
// The answer of a Graylog version that ignores the CSV content type is a JSON
// envelope (or a stream of them); that shape is decoded as well, because an
// envelope whose `messages` array cannot be read would silently look like
// "no matches".
func decodeGraylogCSV(body io.Reader, limit int) (graylogAnswer, error) {
	buffered := bufio.NewReader(body)
	peek, _ := buffered.Peek(64)
	if firstNonSpace(peek) == '{' {
		return decodeGraylogJSON(buffered, limit)
	}

	reader := csv.NewReader(buffered)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true

	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return graylogAnswer{Lines: make([]Line, 0)}, nil
		}
		return graylogAnswer{}, err
	}
	names := make([]string, 0, len(header))
	for _, name := range header {
		names = append(names, strings.TrimSpace(name))
	}

	answer := graylogAnswer{Lines: make([]Line, 0, 32)}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return graylogAnswer{}, err
		}
		if len(record) != len(names) {
			// A malformed row is ignored rather than turned into a fake line.
			continue
		}
		answer.Total++
		if limit > 0 && len(answer.Lines) >= limit {
			// Keep counting so the UI can report the real match count.
			continue
		}
		fields := make(map[string]any, len(names))
		for index, name := range names {
			if value := strings.TrimSpace(record[index]); value != "" {
				fields[name] = value
			}
		}
		answer.Lines = append(answer.Lines, graylogLine(fields))
	}

	// Graylog's answer carries no explicit ordering parameter in 7.x, so the
	// newest line is put first here.
	sort.SliceStable(answer.Lines, func(i, j int) bool {
		return answer.Lines[i].At.After(answer.Lines[j].At)
	})
	return answer, nil
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
	// The stream restriction is sent as the `streams` request parameter, which
	// replaces the legacy `stream:` query clause in Graylog 7.
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
	if b.cfg.StreamID != "" {
		// Graylog 7 replaced the `stream:` query clause with this parameter.
		values.Set("streams", b.cfg.StreamID)
	}
	// `fields` is mandatory in Graylog 7 and makes the answer a CSV document
	// containing only these fields.
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
	line.Level = trace.SanitizeString(stringField(fields, "log_level"))
	if line.Level == "" {
		// GELF's own `level` is numeric (syslog); map it back to a name.
		line.Level = graylogLevelName(stringField(fields, "level"))
	}
	line.Component = trace.SanitizeString(stringField(fields, "component"))
	line.Stage = trace.SanitizeString(stringField(fields, "stage"))
	line.Provider = trace.SanitizeString(stringField(fields, "provider"))
	line.Application = trace.SanitizeString(stringField(fields, "application"))
	line.Service = trace.SanitizeString(stringField(fields, "service"))
	line.Server = trace.SanitizeString(stringField(fields, "server"))
	line.Node = trace.SanitizeString(stringField(fields, "node"))
	line.Environment = trace.SanitizeString(stringField(fields, "environment"))
	line.SourceType = trace.SanitizeString(stringField(fields, "source_type"))
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

// graylogLevelName maps the numeric GELF level back onto a trace level name so
// the same filters work against every backend.
func graylogLevelName(value string) string {
	switch strings.TrimSpace(value) {
	case "0", "1", "2", "3":
		return trace.LevelError
	case "4":
		return trace.LevelWarn
	case "5", "6":
		return trace.LevelInfo
	case "7":
		return trace.LevelDebug
	}
	return strings.ToLower(strings.TrimSpace(value))
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
