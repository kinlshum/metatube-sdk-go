package route

import (
	goerr "errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/engine"
	"github.com/metatube-community/metatube-sdk-go/errors"
	"github.com/metatube-community/metatube-sdk-go/internal/logbuffer"
	"github.com/metatube-community/metatube-sdk-go/internal/logsearch"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
	V "github.com/metatube-community/metatube-sdk-go/internal/version"
	"github.com/metatube-community/metatube-sdk-go/route/auth"
)

// Option customizes the HTTP router. Options keep the default wiring (and every
// existing caller) unchanged.
type Option func(*routerOptions)

// routerOptions holds the optional dependencies the router reports on.
type routerOptions struct {
	// mirror is the optional durable log sender (Graylog GELF). It is attached
	// to the trace service so the status and probe endpoints describe the exact
	// instance that is sending records.
	mirror LogMirror
}

// WithLogMirror attaches an optional Graylog GELF sender. The sender is also
// attached to the trace service as its log mirror, so the admin status comes
// from the same object that delivers records.
func WithLogMirror(mirror LogMirror) Option {
	return func(options *routerOptions) { options.mirror = mirror }
}

func New(app *engine.Engine, v auth.Validator, options ...Option) *gin.Engine {
	settings := &routerOptions{}
	for _, option := range options {
		if option != nil {
			option(settings)
		}
	}
	if settings.mirror != nil {
		// A mirror must never change lookup behaviour: the trace service treats
		// it as best-effort and the sender queues everything off the request
		// path.
		app.TraceService().SetMirror(settings.mirror)
	}

	app.StartProviderHealthChecks()
	r := gin.New()
	{
		// Client IPs are only derived from X-Forwarded-For when the operator
		// explicitly trusts a proxy, so a caller cannot spoof its own address in
		// metrics or traces.
		if err := r.SetTrustedProxies(trustedProxies()); err != nil {
			_ = r.SetTrustedProxies(nil)
		}
		// support CORS
		r.Use(cors.Default())
		// register middleware
		r.Use(logger(), recovery())
		// fallback behavior
		r.NoRoute(notFound())
		r.NoMethod(notAllowed())
	}

	// redirection middleware
	r.Use(redirect(app))
	r.Use(metrics(app))
	// enrichment trace middleware (no-op when tracing is disabled)
	r.Use(traceMiddleware(app.TraceService()))

	// index page
	r.GET("/", getIndex(app))

	// combined log search (native buffer + optional Graylog adapter)
	logs := logsearch.New(logsearch.GraylogConfigFromEnv())

	// admin page and APIs. When METATUBE_ADMIN_TOKEN is set every /admin route
	// requires that token, including the trace ingest APIs.
	admin := r.Group("/admin", adminAuth(adminToken()))
	admin.GET("", getAdminPage())
	admin.GET("/api/version", getAdminVersion())
	admin.GET("/api/provider-throttles", getProviderThrottles(app))
	admin.PUT("/api/provider-throttles", putProviderThrottles(app))
	admin.GET("/api/movie-search-policy", getMovieSearchPolicy(app))
	admin.PUT("/api/movie-search-policy", putMovieSearchPolicy(app))
	admin.GET("/api/stats", getAdminStats(app))
	admin.POST("/api/provider-health/:provider/check", postProviderHealthCheck(app))
	admin.GET("/api/logs", getAdminLogs(logs))
	admin.GET("/api/logs/search", getAdminLogSearch(logs))
	admin.GET("/api/trace-runs/:runID", getTraceRun(app.TraceService()))
	admin.GET("/api/gelf", getLogIngestion(settings.mirror))
	admin.POST("/api/gelf/probe", postLogProbe(settings.mirror))
	registerTraceRoutes(admin, app.TraceService(), logs, settings.mirror)

	system := r.Group("/v1", cacheNoStore())
	{
		system.GET("/modules", getModules())
		system.GET("/providers", getProviders(app))
	}

	public := r.Group("/v1",
		// It's planned to cache public data for
		// a long time, especially behind a CDN.
		cachePublicSMaxAge(180*24*time.Hour))
	{
		public.GET("/translate", getTranslate())

		images := public.Group("/images")
		{
			images.GET("/primary/:provider/:id", getImage(app, primaryImageType))
			images.GET("/thumb/:provider/:id", getImage(app, thumbImageType))
			images.GET("/backdrop/:provider/:id", getImage(app, backdropImageType))
		}
	}

	private := r.Group("/v1", authentication(v))
	{
		db := private.Group("/db")
		{
			db.GET("/version", getDBVersion(app))
		}

		actors := private.Group("/actors")
		{
			actors.GET("/:provider/:id", getInfo(app, actorInfoType))
			actors.GET("/search", getSearch(app, actorSearchType))
		}

		movies := private.Group("/movies")
		{
			movies.GET("/:provider/:id", getInfo(app, movieInfoType))
			movies.GET("/search", getSearch(app, movieSearchType))
		}

		reviews := private.Group("/reviews")
		{
			reviews.GET("/:provider/:id", getReview(app))
		}
	}

	return r
}

func metrics(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		finish := app.BeginRequest()
		c.Next()
		finish(c.ClientIP(), c.Request.RemoteAddr, c.Request.UserAgent(), c.Request.Method, c.Request.URL.RequestURI(), c.Writer.Status())
	}
}

func logger() gin.HandlerFunc {
	return gin.LoggerWithConfig(gin.LoggerConfig{
		Output: logbuffer.Output(),
		Formatter: func(param gin.LogFormatterParams) string {
			// Ordinary log lines carry the trace ID so an operator can jump
			// between the general LOGS tab and a structured trace.
			traceID := ""
			if value, ok := param.Keys[ginTraceHandleKey]; ok {
				if handle, ok := value.(*trace.RunHandle); ok {
					traceID = handle.TraceID()
				}
			}
			if traceID == "" {
				return defaultLogFormat(param) + "\n"
			}
			return defaultLogFormat(param) + " trace=" + traceID + "\n"
		},
	})
}

// defaultLogFormat mirrors gin's default logger output without the trailing
// newline, which the caller appends after any extra fields.
func defaultLogFormat(param gin.LogFormatterParams) string {
	return fmt.Sprintf("[GIN] %v | %3d | %13v | %15s | %-7s %#v",
		param.TimeStamp.Format("2006/01/02 - 15:04:05"),
		param.StatusCode,
		param.Latency,
		param.ClientIP,
		param.Method,
		param.Path,
	)
}

func recovery() gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, err any) {
		abortWithStatusMessage(c, http.StatusInternalServerError, err)
	})
}

func notFound() gin.HandlerFunc {
	return func(c *gin.Context) {
		abortWithStatusMessage(c, http.StatusNotFound,
			http.StatusText(http.StatusNotFound))
	}
}

func notAllowed() gin.HandlerFunc {
	return func(c *gin.Context) {
		abortWithStatusMessage(c, http.StatusMethodNotAllowed,
			http.StatusText(http.StatusMethodNotAllowed))
	}
}

func getIndex(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, &responseMessage{
			Data: gin.H{
				"app":     app.String(),
				"version": V.BuildString(),
			},
		})
	}
}

func getModules() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"modules": V.Modules(),
		})
	}
}

func getProviders(app *engine.Engine) gin.HandlerFunc {
	data := struct {
		ActorProviders map[string]string `json:"actor_providers"`
		MovieProviders map[string]string `json:"movie_providers"`
	}{
		ActorProviders: make(map[string]string),
		MovieProviders: make(map[string]string),
	}
	for _, provider := range app.GetActorProviders() {
		data.ActorProviders[provider.Name()] = provider.URL().String()
	}
	for _, provider := range app.GetMovieProviders() {
		data.MovieProviders[provider.Name()] = provider.URL().String()
	}
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, &responseMessage{Data: data})
	}
}

func abortWithError(c *gin.Context, err error) {
	var e *errors.HTTPError
	if goerr.As(err, &e) {
		c.AbortWithStatusJSON(e.Code, &responseMessage{Error: e, TraceID: traceIDFromGin(c)})
		return
	}
	code := http.StatusInternalServerError
	if c := errors.StatusCode(err); c != 0 {
		code = c
	}
	abortWithStatusMessage(c, code, err)
}

func abortWithStatusMessage(c *gin.Context, code int, message any) {
	c.AbortWithStatusJSON(code, &responseMessage{
		Error:   errors.New(code, fmt.Sprintf("%v", message)),
		TraceID: traceIDFromGin(c),
	})
}

type responseMessage struct {
	Data    any    `json:"data,omitempty"`
	Error   error  `json:"error,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}
