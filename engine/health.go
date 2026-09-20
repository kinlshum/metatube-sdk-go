package engine

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

type ProviderHealth struct {
	Provider  string    `json:"provider"`
	URL       string    `json:"url"`
	Up        bool      `json:"up"`
	Status    int       `json:"status"`
	LatencyMS float64   `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type providerHealthMonitor struct {
	once   sync.Once
	mu     sync.RWMutex
	values map[string]ProviderHealth
}

func newProviderHealthMonitor() *providerHealthMonitor {
	return &providerHealthMonitor{values: make(map[string]ProviderHealth)}
}

func (e *Engine) StartProviderHealthChecks() {
	e.health.once.Do(func() { go e.providerHealthLoop() })
}

func (e *Engine) providerHealthLoop() {
	providers := make(map[string]mt.Provider)
	for _, provider := range e.GetMovieProviders() {
		providers[throttleKey(provider.Name())] = provider
	}
	for _, provider := range e.GetActorProviders() {
		providers[throttleKey(provider.Name())] = provider
	}
	keys := make([]string, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return
	}
	interval := time.Hour / time.Duration(len(keys))
	for {
		for _, key := range keys {
			e.checkProviderHealth(providers[key])
			time.Sleep(interval)
		}
	}
}

func (e *Engine) checkProviderHealth(provider mt.Provider) ProviderHealth {
	started := time.Now()
	value := ProviderHealth{Provider: provider.Name(), URL: provider.URL().String()}
	response, err := e.Fetch(value.URL, provider)
	value.LatencyMS = float64(time.Since(started).Microseconds()) / 1000
	value.CheckedAt = time.Now()
	if err != nil {
		value.Error = err.Error()
	} else {
		value.Status = response.StatusCode
		value.Up = response.StatusCode >= 200 && response.StatusCode < 400
		_, _ = io.CopyN(io.Discard, response.Body, 1)
		_ = response.Body.Close()
		if !value.Up {
			value.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
		}
	}
	e.health.mu.Lock()
	e.health.values[throttleKey(provider.Name())] = value
	e.health.mu.Unlock()
	return value
}

// CheckProviderHealth runs an immediate provider check and updates the same
// cached value used by the staggered hourly monitor.
func (e *Engine) CheckProviderHealth(name string) (ProviderHealth, error) {
	for _, provider := range e.GetMovieProviders() {
		if strings.EqualFold(strings.TrimSpace(name), provider.Name()) {
			return e.checkProviderHealth(provider), nil
		}
	}
	for _, provider := range e.GetActorProviders() {
		if strings.EqualFold(strings.TrimSpace(name), provider.Name()) {
			return e.checkProviderHealth(provider), nil
		}
	}
	return ProviderHealth{}, mt.ErrProviderNotFound
}

func (e *Engine) ProviderHealth() []ProviderHealth {
	e.health.mu.RLock()
	values := make([]ProviderHealth, 0, len(e.health.values))
	for _, value := range e.health.values {
		values = append(values, value)
	}
	e.health.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool { return strings.ToLower(values[i].Provider) < strings.ToLower(values[j].Provider) })
	return values
}
