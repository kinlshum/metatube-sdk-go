package trace

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultConfigDSN keeps traces beside the provider throttle config, on the
	// volume the deployment already mounts at /config.
	DefaultConfigDSN = "/config/traces.db"

	DefaultRetentionDays   = 30
	DefaultMaxRuns         = 10000
	DefaultMaxEventsPerRun = 500
	DefaultPruneInterval   = 10 * time.Minute
	MinPruneInterval       = time.Minute

	MaxFilterLimit     = 1000
	DefaultFilterLimit = 50

	// DefaultMaxPayloadBytes bounds a single ingest request body.
	DefaultMaxPayloadBytes = 1 << 20 // 1 MiB

	DefaultIngestBurstPerClient  = 120
	DefaultIngestRefillPerSecond = 20.0
)

// Config controls the trace service.
type Config struct {
	Enabled         bool
	DSN             string
	RetentionDays   int
	MaxRuns         int
	MaxEventsPerRun int
	PruneInterval   time.Duration
}

// ConfigFromEnv reads the METATUBE_TRACE_* environment variables documented in
// docs/METATUBE_ENRICHMENT_TRACE_TODO.md.
func ConfigFromEnv() Config {
	config := Config{
		Enabled:         envBool("METATUBE_TRACE_ENABLED", true),
		DSN:             strings.TrimSpace(os.Getenv("METATUBE_TRACE_DSN")),
		RetentionDays:   envInt("METATUBE_TRACE_RETENTION_DAYS", DefaultRetentionDays),
		MaxRuns:         envInt("METATUBE_TRACE_MAX_RUNS", DefaultMaxRuns),
		MaxEventsPerRun: envInt("METATUBE_TRACE_MAX_EVENTS_PER_RUN", DefaultMaxEventsPerRun),
		PruneInterval:   DefaultPruneInterval,
	}
	if config.DSN == "" {
		config.DSN = DefaultConfigDSN
	}
	return config.WithDefaults()
}

// WithDefaults fills in missing or out-of-range values.
func (c Config) WithDefaults() Config {
	if c.RetentionDays <= 0 {
		c.RetentionDays = DefaultRetentionDays
	}
	if c.MaxRuns <= 0 {
		c.MaxRuns = DefaultMaxRuns
	}
	if c.MaxEventsPerRun <= 0 {
		c.MaxEventsPerRun = DefaultMaxEventsPerRun
	}
	if c.PruneInterval < MinPruneInterval {
		c.PruneInterval = DefaultPruneInterval
	}
	return c
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

// ValidKind reports whether a kind is supported.
func ValidKind(kind string) bool {
	return kind == KindVideo || kind == KindActor
}

// ValidOperation reports whether an operation is supported.
func ValidOperation(operation string) bool {
	switch operation {
	case OperationLookup, OperationIdentify, OperationEnrich, OperationRefresh, OperationTest:
		return true
	}
	return false
}

// ValidStatus reports whether a status is supported.
func ValidStatus(status string) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusSucceeded, StatusPartial, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// TerminalStatus reports whether a status means the server-side work is done.
// Only terminal traces are eligible for pruning; running traces never are.
func TerminalStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusPartial, StatusFailed, StatusCancelled:
		return true
	}
	return false
}
