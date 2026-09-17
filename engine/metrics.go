package engine

import (
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProviderMetric struct {
	Provider      string    `json:"provider"`
	Requests      uint64    `json:"requests"`
	Active        int       `json:"active"`
	PeakActive    int       `json:"peak_active"`
	AverageMS     float64   `json:"average_ms"`
	LastMS        float64   `json:"last_ms"`
	LastRequestAt time.Time `json:"last_request_at,omitempty"`
}

type ClientMetric struct {
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Requests   uint64    `json:"requests"`
	Errors     uint64    `json:"errors"`
	AverageMS  float64   `json:"average_ms"`
	LastPath   string    `json:"last_path"`
	LastStatus int       `json:"last_status"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type RequestMetric struct {
	At        time.Time `json:"at"`
	IP        string    `json:"ip"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	LatencyMS float64   `json:"latency_ms"`
}

type ProviderClientMetric struct {
	Provider   string    `json:"provider"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Requests   uint64    `json:"requests"`
	Errors     uint64    `json:"errors"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type ServerStats struct {
	StartedAt       time.Time              `json:"started_at"`
	UptimeSeconds   int64                  `json:"uptime_seconds"`
	Requests        uint64                 `json:"requests"`
	Errors          uint64                 `json:"errors"`
	ActiveRequests  int                    `json:"active_requests"`
	AverageMS       float64                `json:"average_ms"`
	Goroutines      int                    `json:"goroutines"`
	MemoryAllocMB   float64                `json:"memory_alloc_mb"`
	Clients         []ClientMetric         `json:"clients"`
	Providers       []ProviderMetric       `json:"providers"`
	ProviderClients []ProviderClientMetric `json:"provider_clients"`
	ProviderHealth  []ProviderHealth       `json:"provider_health"`
	FlareSolverr    any                    `json:"flaresolverr,omitempty"`
	Recent          []RequestMetric        `json:"recent"`
}

type engineMetrics struct {
	mu               sync.RWMutex
	started          time.Time
	requests, errors uint64
	active           int
	total            time.Duration
	clients          map[string]*clientMetricState
	providerClients  map[string]*ProviderClientMetric
	recent           []RequestMetric
}

type clientMetricState struct {
	ClientMetric
	total time.Duration
}

func newEngineMetrics() *engineMetrics {
	return &engineMetrics{started: time.Now(), clients: make(map[string]*clientMetricState), providerClients: make(map[string]*ProviderClientMetric)}
}

func requestProvider(requestURI string) string {
	u, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return ""
	}
	if provider := strings.TrimSpace(u.Query().Get("provider")); provider != "" {
		return provider
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if (part == "movies" || part == "actors" || part == "reviews") && i+1 < len(parts) && parts[i+1] != "search" {
			return parts[i+1]
		}
	}
	return ""
}

func (e *Engine) BeginRequest() func(ip, userAgent, method, path string, status int) {
	started := time.Now()
	e.metrics.mu.Lock()
	e.metrics.active++
	e.metrics.mu.Unlock()
	return func(ip, userAgent, method, path string, status int) {
		d := time.Since(started)
		m := e.metrics
		m.mu.Lock()
		defer m.mu.Unlock()
		m.active--
		m.requests++
		m.total += d
		if status >= 400 {
			m.errors++
		}
		key := ip + "\x00" + userAgent
		client := m.clients[key]
		if client == nil {
			client = &clientMetricState{}
			client.IP = ip
			client.UserAgent = userAgent
			m.clients[key] = client
		}
		client.Requests++
		client.total += d
		client.LastPath = path
		client.LastStatus = status
		client.LastSeenAt = time.Now()
		if status >= 400 {
			client.Errors++
		}
		client.AverageMS = float64(client.total.Microseconds()) / 1000 / float64(client.Requests)
		if provider := requestProvider(path); provider != "" {
			providerKey := strings.ToLower(provider) + "\x00" + key
			providerClient := m.providerClients[providerKey]
			if providerClient == nil {
				providerClient = &ProviderClientMetric{Provider: provider, IP: ip, UserAgent: userAgent}
				m.providerClients[providerKey] = providerClient
			}
			providerClient.Requests++
			providerClient.LastSeenAt = time.Now()
			if status >= 400 {
				providerClient.Errors++
			}
		}
		m.recent = append(m.recent, RequestMetric{At: time.Now(), IP: ip, Method: method, Path: path, Status: status, LatencyMS: float64(d.Microseconds()) / 1000})
		if len(m.recent) > 100 {
			m.recent = append([]RequestMetric(nil), m.recent[len(m.recent)-100:]...)
		}
	}
}

func (e *Engine) Stats() ServerStats {
	m := e.metrics
	m.mu.RLock()
	stats := ServerStats{StartedAt: m.started, UptimeSeconds: int64(time.Since(m.started).Seconds()), Requests: m.requests, Errors: m.errors, ActiveRequests: m.active, Goroutines: runtime.NumGoroutine()}
	if m.requests > 0 {
		stats.AverageMS = float64(m.total.Microseconds()) / 1000 / float64(m.requests)
	}
	for _, value := range m.clients {
		stats.Clients = append(stats.Clients, value.ClientMetric)
	}
	for _, value := range m.providerClients {
		stats.ProviderClients = append(stats.ProviderClients, *value)
	}
	stats.Recent = append([]RequestMetric(nil), m.recent...)
	m.mu.RUnlock()
	sort.Slice(stats.Clients, func(i, j int) bool { return stats.Clients[i].LastSeenAt.After(stats.Clients[j].LastSeenAt) })
	sort.Slice(stats.ProviderClients, func(i, j int) bool {
		if stats.ProviderClients[i].Requests == stats.ProviderClients[j].Requests {
			return stats.ProviderClients[i].LastSeenAt.After(stats.ProviderClients[j].LastSeenAt)
		}
		return stats.ProviderClients[i].Requests > stats.ProviderClients[j].Requests
	})
	if len(stats.Clients) > 50 {
		stats.Clients = stats.Clients[:50]
	}
	for i, j := 0, len(stats.Recent)-1; i < j; i, j = i+1, j-1 {
		stats.Recent[i], stats.Recent[j] = stats.Recent[j], stats.Recent[i]
	}
	stats.Providers = e.providerThrottle.Metrics()
	stats.ProviderHealth = e.ProviderHealth()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	stats.MemoryAllocMB = float64(mem.Alloc) / 1024 / 1024
	return stats
}
