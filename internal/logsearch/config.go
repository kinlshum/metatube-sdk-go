package logsearch

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables for the optional Graylog backend.
const (
	// GraylogEnabledEnv turns the adapter on.
	GraylogEnabledEnv = "METATUBE_GRAYLOG_ENABLED"
	// GraylogAPIURLEnv is the Graylog server API base, e.g.
	// `https://graylog.madtechinc.com/api` (the deployment default).
	GraylogAPIURLEnv = "METATUBE_GRAYLOG_API_URL"
	// GraylogStreamIDEnv restricts every query to one stream.
	GraylogStreamIDEnv = "METATUBE_GRAYLOG_STREAM_ID"
	// GraylogTokenEnv holds the API token. It is server-side only and never
	// reaches the browser, a response, an export, a log, or a URL.
	GraylogTokenEnv = "METATUBE_GRAYLOG_TOKEN"
	// GraylogExternalURLEnv is the browser-facing Graylog base used to build
	// deep links.
	GraylogExternalURLEnv = "METATUBE_GRAYLOG_EXTERNAL_URL"
	// GraylogTimeoutEnv bounds one search request, in seconds.
	GraylogTimeoutEnv = "METATUBE_GRAYLOG_TIMEOUT_SECONDS"
	// GraylogMaxResultsEnv caps how many messages one query may return.
	GraylogMaxResultsEnv = "METATUBE_GRAYLOG_MAX_RESULTS"
	// GraylogMaxRangeHoursEnv caps the searchable time window.
	GraylogMaxRangeHoursEnv = "METATUBE_GRAYLOG_MAX_RANGE_HOURS"
)

const (
	defaultGraylogTimeout   = 5 * time.Second
	defaultGraylogMaxResult = 200
	defaultGraylogMaxRange  = 24
)

// GraylogConfig configures the Graylog adapter.
type GraylogConfig struct {
	Enabled       bool
	APIURL        string
	StreamID      string
	Token         string
	ExternalURL   string
	Timeout       time.Duration
	MaxResults    int
	MaxRangeHours int
}

// GraylogConfigFromEnv reads the METATUBE_GRAYLOG_* variables.
func GraylogConfigFromEnv() GraylogConfig {
	return GraylogConfig{
		Enabled:       envBool(GraylogEnabledEnv, false),
		APIURL:        strings.TrimSpace(os.Getenv(GraylogAPIURLEnv)),
		StreamID:      strings.TrimSpace(os.Getenv(GraylogStreamIDEnv)),
		Token:         strings.TrimSpace(os.Getenv(GraylogTokenEnv)),
		ExternalURL:   strings.TrimSpace(os.Getenv(GraylogExternalURLEnv)),
		Timeout:       time.Duration(envInt(GraylogTimeoutEnv, int(defaultGraylogTimeout/time.Second))) * time.Second,
		MaxResults:    envInt(GraylogMaxResultsEnv, defaultGraylogMaxResult),
		MaxRangeHours: envInt(GraylogMaxRangeHoursEnv, defaultGraylogMaxRange),
	}.WithDefaults()
}

// WithDefaults fills in missing or out-of-range values.
func (c GraylogConfig) WithDefaults() GraylogConfig {
	if c.Timeout <= 0 {
		c.Timeout = defaultGraylogTimeout
	}
	if c.Timeout > 30*time.Second {
		c.Timeout = 30 * time.Second
	}
	if c.MaxResults <= 0 {
		c.MaxResults = defaultGraylogMaxResult
	}
	if c.MaxResults > MaxSearchLimit {
		c.MaxResults = MaxSearchLimit
	}
	if c.MaxRangeHours <= 0 {
		c.MaxRangeHours = defaultGraylogMaxRange
	}
	if c.MaxRangeHours > 24*30 {
		c.MaxRangeHours = 24 * 30
	}
	return c
}

// Configured reports whether the adapter can be queried. Without a token the
// feature stays visible but reports `Graylog not configured`.
func (c GraylogConfig) Configured() bool {
	return c.Enabled && c.APIURL != "" && c.Token != ""
}

// EnabledWithoutToken reports a half-configured adapter, which is worth warning
// about because the UI would otherwise look merely unconfigured.
func (c GraylogConfig) EnabledWithoutToken() bool {
	return c.Enabled && (c.APIURL == "" || c.Token == "")
}

// ClampRange bounds the requested window to MaxRangeHours.
func (c GraylogConfig) ClampRange(since, until *time.Time) (from, to *time.Time, clamped bool) {
	maxRange := time.Duration(c.MaxRangeHours) * time.Hour
	now := time.Now().UTC()

	end := now
	if until != nil {
		end = until.UTC()
	}
	start := end.Add(-maxRange)
	if since != nil && since.UTC().After(start) {
		start = since.UTC()
	} else if since != nil {
		clamped = true
	}
	if until == nil {
		// An open-ended query still needs a bounded upper bound.
		to = &end
	} else {
		to = &end
	}
	return &start, to, clamped
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
