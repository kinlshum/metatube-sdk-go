package gelf

import "time"

// Status values reported to the admin status card.
const (
	StatusOK            = "ok"
	StatusDisabled      = "disabled"
	StatusNotConfigured = "not_configured"
	StatusDegraded      = "degraded"
)

// Stats is the secret-free status snapshot the admin UI renders. It never
// contains the ingestion token -- only whether one is present.
type Stats struct {
	Enabled      bool   `json:"enabled"`
	Configured   bool   `json:"configured"`
	MissingToken bool   `json:"missing_token"`
	Status       string `json:"status"`
	Detail       string `json:"detail,omitempty"`

	Endpoint    string `json:"endpoint,omitempty"`
	Host        string `json:"host,omitempty"`
	Application string `json:"application"`
	Service     string `json:"service"`
	Server      string `json:"server"`
	Node        string `json:"node"`
	Environment string `json:"environment"`
	Version     string `json:"version,omitempty"`

	TokenPresent   bool   `json:"token_present"`
	TokenSource    string `json:"token_source"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxRetries     int    `json:"max_retries"`
	RetryBackoffMS int    `json:"retry_backoff_ms"`
	QueueCapacity  int    `json:"queue_capacity"`
	QueueDepth     int    `json:"queue_depth"`

	Sent    uint64 `json:"sent"`
	Failed  uint64 `json:"failed"`
	Retried uint64 `json:"retried"`
	Dropped uint64 `json:"dropped"`

	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`

	ProbeRan    bool       `json:"probe_ran"`
	ProbeOK     bool       `json:"probe_ok"`
	ProbeDetail string     `json:"probe_detail,omitempty"`
	LastProbeAt *time.Time `json:"last_probe_at,omitempty"`
}

// Stats returns the current status. It is safe to call on a nil sender, which is
// reported as disabled.
func (s *Sender) Stats() Stats {
	if s == nil {
		cfg := Config{}.WithDefaults(hostname())
		return Stats{
			Status:      StatusDisabled,
			Detail:      "GELF sender unavailable",
			Application: cfg.Application,
			Service:     cfg.Service,
			Server:      cfg.Server,
			Node:        cfg.Node,
			Environment: cfg.Environment,
			TokenSource: TokenEnv + " (server-side only)",
		}
	}
	cfg := s.cfg
	stats := Stats{
		Enabled:        cfg.Enabled,
		Configured:     cfg.Configured(),
		MissingToken:   cfg.Enabled && cfg.Token == "",
		Endpoint:       cfg.Endpoint(),
		Host:           cfg.HostDisplay(),
		Application:    cfg.Application,
		Service:        cfg.Service,
		Server:         cfg.Server,
		Node:           cfg.Node,
		Environment:    cfg.Environment,
		Version:        cfg.Version,
		TokenPresent:   cfg.Token != "",
		TokenSource:    cfg.TokenSourceLabel(),
		TimeoutSeconds: int(cfg.Timeout.Seconds()),
		MaxRetries:     cfg.MaxRetries,
		RetryBackoffMS: int(cfg.RetryBackoff.Milliseconds()),
		QueueCapacity:  cfg.QueueSize,
		Sent:           s.sent.Load(),
		Failed:         s.failed.Load(),
		Retried:        s.retried.Load(),
		Dropped:        s.dropped.Load(),
	}
	if s.queue != nil {
		stats.QueueDepth = len(s.queue)
	}

	s.mu.Lock()
	stats.LastSuccessAt = s.lastSuccessAt
	stats.LastFailureAt = s.lastFailureAt
	stats.LastError = s.lastError
	stats.ProbeRan = s.lastProbeAt != nil
	stats.ProbeOK = s.probeOK
	stats.ProbeDetail = s.probeDetail
	stats.LastProbeAt = s.lastProbeAt
	s.mu.Unlock()

	switch {
	case !cfg.Enabled:
		stats.Status = StatusDisabled
		stats.Detail = "GELF sender disabled (" + EnabledEnv + "=false)"
	case !stats.Configured:
		stats.Status = StatusNotConfigured
		stats.Detail = cfg.Validate().Error()
	case stats.Dropped > 0 || stats.Failed > 0:
		stats.Status = StatusDegraded
		stats.Detail = "some records were not stored; see failed/dropped counters"
	default:
		stats.Status = StatusOK
		stats.Detail = "all records delivered"
	}
	return stats
}
