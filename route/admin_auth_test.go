package route

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/metatube-community/metatube-sdk-go/internal/trace"
)

func TestAdminStaysOpenWithoutToken(t *testing.T) {
	t.Setenv(AdminTokenEnv, "")

	router, _ := newTraceTestRouter(t, nil)
	recorder := doRequest(router, http.MethodGet, "/admin", nil, nil)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.NotEmpty(t, recorder.Body.String())
}

func TestAdminRequiresTokenWhenConfigured(t *testing.T) {
	t.Setenv(AdminTokenEnv, "super-secret-admin-token")

	router, _ := newTraceTestRouter(t, nil)

	protected := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/admin"},
		{http.MethodGet, "/admin/api/stats"},
		{http.MethodGet, "/admin/api/logs?limit=1"},
		{http.MethodGet, "/admin/api/provider-throttles"},
		{http.MethodPost, "/admin/api/provider-health/AVBASE/check"},
		{http.MethodGet, "/admin/api/movie-search-policy"},
		{http.MethodPut, "/admin/api/movie-search-policy"},
		{http.MethodGet, "/admin/api/trace-stats"},
		{http.MethodGet, "/admin/api/traces"},
		{http.MethodPost, "/admin/api/traces/start"},
	}
	for _, unit := range protected {
		recorder := doRequest(router, unit.method, unit.path, nil, nil)
		assert.Equal(t, http.StatusUnauthorized, recorder.Code, unit.path)
	}
	assert.Contains(t, doRequest(router, http.MethodGet, "/admin", nil, nil).Header().Get("WWW-Authenticate"), "Bearer")
}

func TestAdminAcceptsTokenHeaderQueryAndCookie(t *testing.T) {
	t.Setenv(AdminTokenEnv, "super-secret-admin-token")

	router, _ := newTraceTestRouter(t, nil)

	// Wrong token still fails.
	wrong := doRequest(router, http.MethodGet, "/admin/api/stats", map[string]string{
		"X-MetaTube-Admin-Token": "nope",
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)

	// Bearer header.
	bearer := doRequest(router, http.MethodGet, "/admin/api/stats", map[string]string{
		"Authorization": "Bearer super-secret-admin-token",
	}, nil)
	assert.Equal(t, http.StatusOK, bearer.Code)

	// Dedicated header.
	header := doRequest(router, http.MethodGet, "/admin/api/stats", map[string]string{
		"X-MetaTube-Admin-Token": "super-secret-admin-token",
	}, nil)
	assert.Equal(t, http.StatusOK, header.Code)

	// Query token plants a cookie that authenticates later requests.
	bootstrap := doRequest(router, http.MethodGet, "/admin?token=super-secret-admin-token", nil, nil)
	require.Equal(t, http.StatusOK, bootstrap.Code)
	cookies := bootstrap.Result().Cookies()
	require.NotEmpty(t, cookies, "the query token must plant a cookie")
	cookie := cookies[0]
	assert.Equal(t, adminTokenCookie, cookie.Name)
	assert.True(t, cookie.HttpOnly)

	followUp := doRequest(router, http.MethodGet, "/admin/api/traces?limit=1", map[string]string{
		"Cookie": cookie.Name + "=" + cookie.Value,
	}, nil)
	assert.Equal(t, http.StatusOK, followUp.Code)
}

func TestClientIPCannotBeSpoofedByDefault(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)

	spoofed := doRequest(router, http.MethodGet, "/v1/movies/NoSuchProvider/SSIS-777", map[string]string{
		headerClient:       "spoofer",
		"X-Forwarded-For":  "203.0.113.9",
		"X-Real-IP":        "203.0.113.9",
		"X-Forwarded-Host": "evil.example.com",
	}, nil)
	require.Equal(t, http.StatusNotFound, spoofed.Code)

	traceID := spoofed.Header().Get(headerTraceID)
	require.NotEmpty(t, traceID)

	detail, err := service.Get(traceID)
	require.NoError(t, err)
	assert.NotEqual(t, "203.0.113.9", detail.Run.ClientIP, "forwarded headers must be ignored by default")
	assert.Equal(t, "192.0.2.1", detail.Run.ClientIP)

	// The catalog code becomes the query so lists and filters stay meaningful.
	assert.Equal(t, "SSIS-777", detail.Run.Query)
	assert.Equal(t, "NoSuchProvider", detail.Events[0].Provider)
}

func TestTrustedProxyRestoresForwardedClientIP(t *testing.T) {
	t.Setenv(TrustedProxiesEnv, "192.0.2.1")

	router, service := newTraceTestRouter(t, nil)

	recorder := doRequest(router, http.MethodGet, "/v1/actors/NoSuchActor/Saeki", map[string]string{
		headerClient:      "proxied-client",
		"X-Forwarded-For": "198.51.100.77",
	}, nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	detail, err := service.Get(recorder.Header().Get(headerTraceID))
	require.NoError(t, err)
	assert.Equal(t, "198.51.100.77", detail.Run.ClientIP, "a trusted proxy may supply the client address")
}

func TestImageTracesUseTheProviderSegment(t *testing.T) {
	router, service := newTraceTestRouter(t, nil)

	// An unknown provider fails fast without any provider network call, which is
	// enough to assert how the image path is classified.
	recorder := doRequest(router, http.MethodGet, "/v1/images/primary/NoSuchProvider/SSIS-888", map[string]string{
		headerTraceID: "trace-image-0001",
	}, nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Equal(t, "trace-image-0001", recorder.Header().Get(headerTraceID))

	detail, err := service.Get("trace-image-0001")
	require.NoError(t, err)
	assert.Equal(t, trace.KindVideo, detail.Run.Kind)
	assert.Equal(t, trace.OperationEnrich, detail.Run.Operation)
	require.NotEmpty(t, detail.Events)
	assert.Equal(t, "NoSuchProvider", detail.Events[0].Provider, "the provider, not the image type, must be recorded")
	assert.Equal(t, "SSIS-888", detail.Run.Query)
}
