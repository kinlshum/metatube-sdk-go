package route

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metatube-community/metatube-sdk-go/engine"
)

//go:embed admin.html
var adminHTML string

func getAdminPage() gin.HandlerFunc {
	return func(c *gin.Context) { c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(adminHTML)) }
}

func getProviderThrottles(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"providers": app.ProviderThrottleSettings()})
	}
}

func putProviderThrottles(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Providers []engine.ProviderThrottleSetting `json:"providers"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if err := app.UpdateProviderThrottleSettings(body.Providers); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"saved": true, "providers": app.ProviderThrottleSettings()})
	}
}

func getMovieSearchPolicy(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"policy": app.MovieSearchPolicy(), "providers": app.ProviderThrottleSettings()})
	}
}

func putMovieSearchPolicy(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		var value engine.MovieSearchPolicy
		if err := c.ShouldBindJSON(&value); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		if err := app.UpdateMovieSearchPolicy(value); err != nil {
			abortWithStatusMessage(c, http.StatusBadRequest, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"saved": true, "policy": app.MovieSearchPolicy()})
	}
}

func getAdminStats(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		stats := app.Stats()
		bridgeURL := os.Getenv("METATUBE_PROVIDER_BRIDGE_URL")
		if bridgeURL == "" {
			bridgeURL = "http://metatube-provider-bridge:9210"
		}
		client := &http.Client{Timeout: 10 * time.Second}
		if response, err := client.Get(strings.TrimRight(bridgeURL, "/") + "/admin/stats"); err == nil {
			defer response.Body.Close()
			var value struct {
				FlareSolverr any `json:"flaresolverr"`
			}
			if response.StatusCode == http.StatusOK && json.NewDecoder(response.Body).Decode(&value) == nil {
				stats.FlareSolverr = value.FlareSolverr
			}
		}
		c.JSON(http.StatusOK, stats)
	}
}
