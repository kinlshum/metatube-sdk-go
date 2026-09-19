package route

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/internal/gelf"
	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

// LogMirror is the durable log sender the admin status and probe endpoints
// report on. The trace service only needs trace.Mirror; these endpoints need the
// status and reachability contract as well.
type LogMirror interface {
	trace.Mirror
	Enabled() bool
	Stats() gelf.Stats
	Probe(ctx context.Context) error
}

// probeTimeout bounds one admin-triggered ingestion probe.
const probeTimeout = 10 * time.Second

// ingestionStats returns the safe ingestion status, also used by the trace stats
// endpoint so every card agrees.
func ingestionStats(mirror LogMirror) gelf.Stats {
	if mirror == nil {
		return gelf.Stats{
			Status:      gelf.StatusDisabled,
			Detail:      "GELF sender unavailable",
			TokenSource: gelf.TokenEnv + " (server-side only)",
		}
	}
	return mirror.Stats()
}

// getLogIngestion returns the ingestion status card: reachability of the last
// probe, delivery counters, queue depth and configuration presence. It never
// returns the token.
func getLogIngestion(mirror LogMirror) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"ingestion":      ingestionStats(mirror),
			"probe_endpoint": "/admin/api/gelf/probe",
			"topology": gin.H{
				"application_input": "GELF HTTP input for MetaTube, Windmill and provider bridge events",
				"container_input":   "Vector/container GELF HTTP input collected from the container host",
				"search":            "Server-side Graylog search adapter (API token, never the ingestion token)",
			},
		})
	}
}

// postLogProbe sends one probe record so an operator can verify ingestion
// reachability from the admin UI.
func postLogProbe(mirror LogMirror) gin.HandlerFunc {
	return func(c *gin.Context) {
		if mirror == nil {
			abortWithStatusMessage(c, http.StatusServiceUnavailable, "Graylog ingestion is not configured")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
		defer cancel()
		if err := mirror.Probe(ctx); err != nil {
			// The error text never contains the token, and it is sanitized
			// again before it reaches the browser.
			c.JSON(http.StatusBadGateway, &responseMessage{Data: gin.H{
				"ok":        false,
				"detail":    trace.Truncate(trace.SanitizeString(err.Error()), 256),
				"ingestion": mirror.Stats(),
			}})
			return
		}
		c.JSON(http.StatusOK, &responseMessage{Data: gin.H{
			"ok":        true,
			"detail":    "ingestion reachable",
			"ingestion": mirror.Stats(),
		}})
	}
}
