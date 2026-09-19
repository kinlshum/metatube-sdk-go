// Package gelf sends structured GELF messages to a Graylog GELF HTTP input and
// implements trace.Mirror, so every structured MetaTube trace record can also be
// stored and searched in the central Graylog deployment.
//
// The sender is deliberately best-effort: records are handed to a bounded
// in-process queue and a background worker, so a slow, unreachable or
// misconfigured Graylog can never delay or fail a metadata lookup. Sends that
// cannot be queued are counted instead of blocking.
package gelf

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment variables for the GELF sender. The ingestion token is a separate
// credential from the search adapter's Graylog API token: the sender must never
// use the search token, and the search adapter must never use the ingestion
// token.
const (
	// EnabledEnv turns the sender on.
	EnabledEnv = "METATUBE_GELF_ENABLED"
	// URLEnv is the GELF HTTP input, e.g. the deployment default
	// `http://192.168.10.155:12203/gelf`. A missing path defaults to `/gelf`.
	URLEnv = "METATUBE_GELF_URL"
	// TokenEnv is the ingestion token, sent as the `X-Graylog-Token` header. It
	// is never logged, returned, exported, or placed in a URL.
	TokenEnv = "METATUBE_GELF_TOKEN"
	// TokenFileEnv points at a file holding the ingestion token, so the token can
	// live in a Docker/host secret instead of the process environment. A readable
	// file wins over TokenEnv.
	TokenFileEnv = "METATUBE_GELF_TOKEN_FILE"
	// ApplicationEnv, ServiceEnv, ServerEnv, NodeEnv and EnvironmentEnv are the
	// required common GELF fields.
	ApplicationEnv = "METATUBE_GELF_APPLICATION"
	ServiceEnv     = "METATUBE_GELF_SERVICE"
	ServerEnv      = "METATUBE_GELF_SERVER"
	NodeEnv        = "METATUBE_GELF_NODE"
	EnvironmentEnv = "METATUBE_GELF_ENVIRONMENT"
	// VersionEnv is the MetaTube build version reported as the `version` field.
	VersionEnv = "METATUBE_GELF_VERSION"
	// TimeoutEnv bounds one delivery attempt, in seconds.
	TimeoutEnv = "METATUBE_GELF_TIMEOUT_SECONDS"
	// QueueEnv bounds how many records may wait in memory.
	QueueEnv = "METATUBE_GELF_QUEUE"
	// MaxRetriesEnv bounds delivery retries per record.
	MaxRetriesEnv = "METATUBE_GELF_MAX_RETRIES"
	// RetryBackoffEnv is the base retry backoff, in milliseconds.
	RetryBackoffEnv = "METATUBE_GELF_RETRY_BACKOFF_MS"
)

const (
	defaultTimeout      = 3 * time.Second
	maxTimeout          = 15 * time.Second
	defaultQueue        = 512
	minQueue            = 16
	maxQueue            = 8192
	defaultMaxRetries   = 2
	maxMaxRetries       = 5
	defaultBackoff      = 200 * time.Millisecond
	maxBackoff          = 5 * time.Second
	defaultApplication  = "metatube"
	defaultService      = "metatube-server"
	defaultEnvironment  = "production"
	maxShortMessage     = 2000
	maxDetailsFieldSize = 4096
)

// Config configures the GELF sender.
type Config struct {
	Enabled     bool
	URL         string
	Token       string
	Application string
	Service     string
	Server      string
	Node        string
	Environment string
	Version     string

	// TokenFile records where the token was read from, so status output can say
	// which secret is in use without revealing it. TokenFileErr reports an
	// unreadable token file.
	TokenFile    string
	TokenFileErr string

	Timeout      time.Duration
	MaxRetries   int
	RetryBackoff time.Duration
	QueueSize    int
}

// ConfigFromEnv reads the METATUBE_GELF_* variables.
func ConfigFromEnv() Config {
	host, _ := os.Hostname()
	config := Config{
		Enabled:      envBool(EnabledEnv, false),
		URL:          strings.TrimSpace(os.Getenv(URLEnv)),
		Token:        strings.TrimSpace(os.Getenv(TokenEnv)),
		Application:  strings.TrimSpace(os.Getenv(ApplicationEnv)),
		Service:      strings.TrimSpace(os.Getenv(ServiceEnv)),
		Server:       strings.TrimSpace(os.Getenv(ServerEnv)),
		Node:         strings.TrimSpace(os.Getenv(NodeEnv)),
		Environment:  strings.TrimSpace(os.Getenv(EnvironmentEnv)),
		Version:      strings.TrimSpace(os.Getenv(VersionEnv)),
		Timeout:      time.Duration(envInt(TimeoutEnv, int(defaultTimeout/time.Second))) * time.Second,
		MaxRetries:   envInt(MaxRetriesEnv, defaultMaxRetries),
		RetryBackoff: time.Duration(envInt(RetryBackoffEnv, int(defaultBackoff/time.Millisecond))) * time.Millisecond,
		QueueSize:    envInt(QueueEnv, defaultQueue),
	}
	if path := strings.TrimSpace(os.Getenv(TokenFileEnv)); path != "" {
		config.TokenFile = path
		if content, err := os.ReadFile(path); err == nil {
			config.Token = strings.TrimSpace(string(content))
		} else if config.Token == "" {
			// The message is built from the path only, never from content.
			config.TokenFileErr = err.Error()
		}
	}
	return config.WithDefaults(host)
}

// TokenSourceLabel names the secret the sender reads its token from, without
// revealing the token itself.
func (c Config) TokenSourceLabel() string {
	if c.TokenFile != "" {
		return TokenFileEnv + " (" + c.TokenFile + ")"
	}
	return TokenEnv + " (server-side only)"
}

// WithDefaults fills in missing or out-of-range values. The host name is only a
// last resort for `server`/`node`, so configuration drift stays visible.
func (c Config) WithDefaults(hostname string) Config {
	if hostname == "" {
		hostname = "metatube"
	}
	if c.Application == "" {
		c.Application = defaultApplication
	}
	if c.Service == "" {
		c.Service = defaultService
	}
	if c.Server == "" {
		c.Server = hostname
	}
	if c.Node == "" {
		c.Node = c.Server
	}
	if c.Environment == "" {
		c.Environment = defaultEnvironment
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.Timeout > maxTimeout {
		c.Timeout = maxTimeout
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.MaxRetries > maxMaxRetries {
		c.MaxRetries = maxMaxRetries
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = defaultBackoff
	}
	if c.RetryBackoff > maxBackoff {
		c.RetryBackoff = maxBackoff
	}
	if c.QueueSize <= 0 {
		c.QueueSize = defaultQueue
	}
	if c.QueueSize < minQueue {
		c.QueueSize = minQueue
	}
	if c.QueueSize > maxQueue {
		c.QueueSize = maxQueue
	}
	return c
}

// Validate reports why the sender cannot be used. A missing URL or token is a
// configuration problem, not a runtime failure, so it is reported once at
// startup and as `configured: false` afterwards.
func (c Config) Validate() error {
	if !c.Enabled {
		return fmt.Errorf("GELF sender disabled (%s=false)", EnabledEnv)
	}
	if c.URL == "" {
		return fmt.Errorf("%s is not set", URLEnv)
	}
	parsed, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", URLEnv, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", URLEnv)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s has no host", URLEnv)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not embed credentials; use %s", URLEnv, TokenEnv)
	}
	if parsed.RawQuery != "" {
		return fmt.Errorf("%s must not carry query secrets; use %s", URLEnv, TokenEnv)
	}
	if c.Token == "" {
		if c.TokenFileErr != "" {
			return fmt.Errorf("%s could not be read: %s", TokenFileEnv, c.TokenFileErr)
		}
		return fmt.Errorf("%s is not set", TokenEnv)
	}
	return nil
}

// Configured reports whether the sender can deliver records.
func (c Config) Configured() bool { return c.Validate() == nil }

// Endpoint normalises the configured URL: a bare host gets the default `/gelf`
// path, and any fragment is dropped.
func (c Config) Endpoint() string {
	if c.URL == "" {
		return ""
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/gelf"
	}
	parsed.Fragment = ""
	return parsed.String()
}

// HostDisplay returns the host:port of the endpoint for status output. It never
// contains credentials.
func (c Config) HostDisplay() string {
	parsed, err := url.Parse(c.Endpoint())
	if err != nil {
		return ""
	}
	return parsed.Host
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
