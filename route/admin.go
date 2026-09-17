package route

import (
	_ "embed"
	"net/http"

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

func getAdminStats(app *engine.Engine) gin.HandlerFunc {
	return func(c *gin.Context) { c.JSON(http.StatusOK, app.Stats()) }
}
