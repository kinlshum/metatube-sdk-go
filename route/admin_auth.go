package route

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// AdminTokenEnv enables admin authentication when set.
	AdminTokenEnv    = "METATUBE_ADMIN_TOKEN"
	adminTokenCookie = "metatube_admin_token"
	adminTokenHeader = "X-MetaTube-Admin-Token"
	adminCookieAge   = 12 * 60 * 60
)

// adminToken returns the configured admin token, or an empty string when admin
// access is unrestricted (the previous LAN-only behaviour).
func adminToken() string { return strings.TrimSpace(os.Getenv(AdminTokenEnv)) }

// adminAuth guards the /admin routes when METATUBE_ADMIN_TOKEN is set. It
// accepts `Authorization: Bearer <token>`, `X-MetaTube-Admin-Token: <token>`,
// or `?token=<token>`, which also plants a cookie so the single-page UI keeps
// working after the first visit.
func adminAuth(token string) gin.HandlerFunc {
	if token == "" {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if adminTokenMatches(c, token) {
			c.Next()
			return
		}
		c.Header("WWW-Authenticate", `Bearer realm="metatube-admin"`)
		abortWithStatusMessage(c, http.StatusUnauthorized, "admin token required")
	}
}

func adminTokenMatches(c *gin.Context, expected string) bool {
	query := strings.TrimSpace(c.Query("token"))
	for _, candidate := range adminCredentials(c) {
		if candidate == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) != 1 {
			continue
		}
		// Visiting the admin page with ?token=... bootstraps the cookie.
		if query != "" && candidate == query {
			setAdminCookie(c, candidate)
		}
		return true
	}
	return false
}

func adminCredentials(c *gin.Context) []string {
	credentials := make([]string, 0, 4)
	if cookie, err := c.Cookie(adminTokenCookie); err == nil {
		credentials = append(credentials, strings.TrimSpace(cookie))
	}
	if header := strings.TrimSpace(c.GetHeader(adminTokenHeader)); header != "" {
		credentials = append(credentials, header)
	}
	if authorization := strings.TrimSpace(c.GetHeader("Authorization")); authorization != "" {
		if _, value, found := strings.Cut(authorization, " "); found {
			credentials = append(credentials, strings.TrimSpace(value))
		} else {
			credentials = append(credentials, authorization)
		}
	}
	if query := strings.TrimSpace(c.Query("token")); query != "" {
		credentials = append(credentials, query)
	}
	return credentials
}

func setAdminCookie(c *gin.Context, token string) {
	c.SetSameSite(http.SameSiteLaxMode)
	// HttpOnly keeps the token away from scripts; Secure is set whenever the
	// request arrived over TLS (directly or through a TLS-terminating proxy).
	c.SetCookie(adminTokenCookie, token, adminCookieAge, "/", "", requestIsSecure(c), true)
}

func requestIsSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
}

// TrustedProxiesEnv lists proxy CIDRs/IPs whose forwarded-for headers may be
// trusted. It is empty by default, which means client IPs always come from the
// connection itself and can never be spoofed by a caller.
const TrustedProxiesEnv = "METATUBE_TRUSTED_PROXIES"

func trustedProxies() []string {
	value := strings.TrimSpace(os.Getenv(TrustedProxiesEnv))
	if value == "" {
		return nil // gin: trust no proxy, ignore X-Forwarded-For entirely
	}
	parts := strings.Split(value, ",")
	proxies := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			proxies = append(proxies, trimmed)
		}
	}
	if len(proxies) == 0 {
		return nil
	}
	return proxies
}
