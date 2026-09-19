package route

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// Correlation headers defined by the enrichment trace contract.
const (
	headerTraceID       = "X-MetaTube-Trace-ID"
	headerClient        = "X-MetaTube-Client"
	headerOperation     = "X-MetaTube-Operation"
	headerParentTraceID = "X-MetaTube-Parent-Trace-ID"
	headerEmbyItemID    = "X-MetaTube-Emby-Item-ID"
	headerWindmillJobID = "X-MetaTube-Windmill-Job-ID"
	headerCatalogCode   = "X-MetaTube-Catalog-Code"
	headerRunID         = "X-MetaTube-Run-ID"
)

const ginTraceHandleKey = "metatube.trace.handle"

// tracedPath matches the endpoints that represent real lookup/identify/
// enrichment work. Images are included because clients associate them with a
// trace, but they are only traced when the client sends a trace ID.
var tracedPath = regexp.MustCompile(`^/v1/(movies|actors|reviews|images)/`)

// traceRequest describes how a request maps onto a trace.
type traceRequest struct {
	kind      string
	operation string
	provider  string
	query     string
	id        string
	images    bool
}

// classifyTraceRequest inspects the request path and query. It reports false for
// routes that are not part of a workflow trace (index, modules, translate, DB
// version, admin pages).
func classifyTraceRequest(c *gin.Context) (traceRequest, bool) {
	path := c.Request.URL.Path
	if !tracedPath.MatchString(path) {
		return traceRequest{}, false
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 3 {
		return traceRequest{}, false
	}

	request := traceRequest{}
	switch segments[1] {
	case "movies":
		request.kind = trace.KindVideo
	case "actors":
		request.kind = trace.KindActor
	case "reviews":
		request.kind = trace.KindVideo
	case "images":
		request.images = true
	default:
		return traceRequest{}, false
	}

	// /v1/<group>/<provider>/<id>, /v1/<group>/search, or
	// /v1/images/<type>/<provider>/<id>
	last := segments[len(segments)-1]
	switch {
	case last == "search":
		request.provider = c.Query("provider")
		request.query = c.Query("q")
	case request.images && len(segments) >= 4:
		request.provider = segments[3]
		request.id = last
	default:
		request.provider = segments[2]
		request.id = last
	}

	request.operation = trace.OperationLookup
	if request.images {
		request.operation = trace.OperationEnrich
	}
	// An id-based lookup has no free-text query, so the catalog code becomes the
	// query. That keeps trace lists and text filters meaningful.
	if request.query == "" {
		request.query = request.id
	}
	return request, true
}

// traceOperationHeader honours a client-declared operation, which is
// authoritative per the trace contract.
func traceOperationHeader(c *gin.Context) string {
	operation := strings.ToLower(strings.TrimSpace(c.GetHeader(headerOperation)))
	if trace.ValidOperation(operation) {
		return operation
	}
	return ""
}

// guessClient infers a client name from the user agent when no header is sent.
// The result is recorded as inferred so the UI never presents a guess as fact.
func guessClient(userAgent string) string {
	lower := strings.ToLower(userAgent)
	switch {
	case strings.Contains(lower, "metatube/"):
		return "emby-plugin"
	case strings.Contains(lower, "python-httpx"), strings.Contains(lower, "python-urllib"):
		return "windmill"
	case strings.Contains(lower, "curl"), strings.Contains(lower, "wget"):
		return "cli"
	case strings.Contains(lower, "mozilla"):
		return "browser"
	}
	return ""
}

// traceOutcome maps an HTTP status onto a trace status and error code.
func traceOutcome(status int) (string, string) {
	switch {
	case status >= 200 && status < 300:
		return trace.StatusSucceeded, ""
	case status == http.StatusNotFound:
		return trace.StatusFailed, "not_found"
	case status == http.StatusBadRequest:
		return trace.StatusFailed, "invalid_request"
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return trace.StatusFailed, "unauthorized"
	case status == http.StatusTooManyRequests:
		return trace.StatusFailed, "rate_limited"
	case status >= 500:
		return trace.StatusFailed, "internal_error"
	default:
		return trace.StatusFailed, "http_error"
	}
}

// traceFromGin returns the active trace handle for a request, if any.
func traceFromGin(c *gin.Context) *trace.RunHandle {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(ginTraceHandleKey); ok {
		if handle, ok := value.(*trace.RunHandle); ok {
			return handle
		}
	}
	return nil
}

// traceIDFromGin returns the active trace ID for a request, if any.
func traceIDFromGin(c *gin.Context) string {
	if handle := traceFromGin(c); handle != nil {
		return handle.TraceID()
	}
	return trace.TraceIDFromContext(c.Request.Context())
}

// remotePort extracts the ephemeral source port for client attribution.
func remotePort(remoteAddr string) string {
	index := strings.LastIndex(remoteAddr, ":")
	if index < 0 || index == len(remoteAddr)-1 {
		return ""
	}
	return remoteAddr[index+1:]
}

// traceMiddleware opens a trace for workflow requests, propagates it through the
// Go request context (so engine internals can attach events) and finishes it
// with the request outcome. Trace problems never affect the response.
func traceMiddleware(service *trace.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !service.Enabled() {
			c.Next()
			return
		}
		request, ok := classifyTraceRequest(c)
		if !ok {
			c.Next()
			return
		}

		clientTraceID := trace.NormalizeID(c.GetHeader(headerTraceID))
		// Images are high volume and only meaningful when a client ties them to
		// an existing trace.
		if request.images && clientTraceID == "" {
			c.Next()
			return
		}

		clientName := trace.NormalizeID(c.GetHeader(headerClient))
		inferredClient := false
		if clientName == "" {
			if guessed := guessClient(c.Request.UserAgent()); guessed != "" {
				clientName, inferredClient = guessed, true
			}
		}
		operation := traceOperationHeader(c)
		if operation == "" {
			operation = request.operation
		}

		handle, started := service.Start(trace.StartInput{
			TraceID:       clientTraceID,
			ParentTraceID: c.GetHeader(headerParentTraceID),
			RunID:         c.GetHeader(headerRunID),
			Kind:          request.kind,
			Operation:     operation,
			Query:         request.query,
			ClientName:    clientName,
			ClientIP:      c.ClientIP(),
			ClientPort:    remotePort(c.Request.RemoteAddr),
			UserAgent:     c.Request.UserAgent(),
			EmbyItemID:    c.GetHeader(headerEmbyItemID),
			WindmillJobID: c.GetHeader(headerWindmillJobID),
		})
		if !started || handle == nil {
			c.Next()
			return
		}

		// The generated or echoed trace ID is always returned to the caller.
		c.Header(headerTraceID, handle.TraceID())
		c.Set(ginTraceHandleKey, handle)
		c.Request = c.Request.WithContext(trace.WithContext(c.Request.Context(), handle))

		handle.Event(trace.Event{
			Component: trace.ComponentClient,
			Stage:     trace.StageRequestReceived,
			Provider:  request.provider,
			Details: trace.JSONMap{
				"method":          c.Request.Method,
				"path":            c.Request.URL.Path,
				"provider_id":     trace.SanitizeString(request.id),
				"client_inferred": inferredClient,
				"catalog_code":    trace.SanitizeString(c.GetHeader(headerCatalogCode)),
			},
		})

		c.Next()

		// Only the request that opened the trace completes it. Requests that
		// attach to a trace started elsewhere (images, follow-up lookups by the
		// same client) contribute events but never finish someone else's trace.
		if !handle.Created() {
			return
		}

		status, errorCode := traceOutcome(c.Writer.Status())
		finish := trace.FinishInput{
			Status:    status,
			ErrorCode: errorCode,
		}
		if status != trace.StatusSucceeded {
			message := trace.SanitizeString(c.Errors.String())
			if message == "" {
				message = http.StatusText(c.Writer.Status())
			}
			finish.ErrorMessage = message
		}
		handle.Finish(finish)
	}
}
