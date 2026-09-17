package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ProviderThrottleSetting struct {
	Provider       string  `json:"provider"`
	MinSeconds     float64 `json:"min_seconds"`
	MaxSeconds     float64 `json:"max_seconds"`
	MaxConcurrency int     `json:"max_concurrency"`
	Actor          bool    `json:"actor"`
	Movie          bool    `json:"movie"`
}

type providerThrottleState struct {
	mu      sync.Mutex
	cond    *sync.Cond
	active  int
	paceMu  sync.Mutex
	lastRun time.Time
}

type ProviderThrottle struct {
	mu       sync.RWMutex
	path     string
	settings map[string]ProviderThrottleSetting
	states   map[string]*providerThrottleState
}

func newProviderThrottle() *ProviderThrottle {
	path := os.Getenv("PROVIDER_THROTTLE_CONFIG")
	if path == "" {
		path = "/config/provider-throttles.json"
	}
	t := &ProviderThrottle{path: path, settings: make(map[string]ProviderThrottleSetting), states: make(map[string]*providerThrottleState)}
	_ = t.load()
	return t
}

func throttleKey(provider string) string { return strings.ToLower(strings.TrimSpace(provider)) }

func defaultThrottle(provider string) (float64, float64) {
	if strings.EqualFold(provider, "JavDB") {
		return 3, 6
	}
	return 0, 0
}

func defaultConcurrency(provider string) int {
	if strings.EqualFold(provider, "JavDB") {
		return 1
	}
	return 2
}

func (t *ProviderThrottle) load() error {
	data, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var values []ProviderThrottleSetting
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	for _, value := range values {
		t.settings[throttleKey(value.Provider)] = value
	}
	return nil
}

func (t *ProviderThrottle) Settings(actorProviders, movieProviders map[string]bool) []ProviderThrottleSetting {
	names := make(map[string]string)
	for name := range actorProviders {
		names[throttleKey(name)] = name
	}
	for name := range movieProviders {
		names[throttleKey(name)] = name
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	values := make([]ProviderThrottleSetting, 0, len(names))
	for key, name := range names {
		value, ok := t.settings[key]
		if !ok {
			value.Provider = name
			value.MinSeconds, value.MaxSeconds = defaultThrottle(name)
		}
		if value.MaxConcurrency == 0 {
			value.MaxConcurrency = defaultConcurrency(name)
		}
		value.Actor = actorProviders[key]
		value.Movie = movieProviders[key]
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return strings.ToLower(values[i].Provider) < strings.ToLower(values[j].Provider) })
	return values
}

func validateThrottle(value ProviderThrottleSetting) error {
	if value.Provider == "" || value.MinSeconds < 0 || value.MaxSeconds < 0 || value.MinSeconds > value.MaxSeconds {
		return fmt.Errorf("%s has an invalid delay range", value.Provider)
	}
	if value.MaxConcurrency < 1 || value.MaxConcurrency > 3 {
		return fmt.Errorf("%s concurrency must stay between 1 and 3", value.Provider)
	}
	if strings.EqualFold(value.Provider, "JavDB") {
		if value.MinSeconds < 1 || value.MaxSeconds > 10 {
			return errors.New("JavDB delays must stay between 1 and 10 seconds")
		}
	} else if value.MaxSeconds > 60 {
		return fmt.Errorf("%s delays must stay between 0 and 60 seconds", value.Provider)
	}
	return nil
}

func (t *ProviderThrottle) Update(values []ProviderThrottleSetting) error {
	next := make(map[string]ProviderThrottleSetting, len(values))
	for _, value := range values {
		if err := validateThrottle(value); err != nil {
			return err
		}
		value.Actor, value.Movie = false, false
		next[throttleKey(value.Provider)] = value
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return err
	}
	temporary := t.path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, t.path); err != nil {
		return err
	}
	t.mu.Lock()
	t.settings = next
	t.mu.Unlock()
	return nil
}

func (t *ProviderThrottle) Begin(provider string) func() {
	key := throttleKey(provider)
	t.mu.RLock()
	setting, ok := t.settings[key]
	if !ok {
		setting.Provider = provider
		setting.MinSeconds, setting.MaxSeconds = defaultThrottle(provider)
	}
	if setting.MaxConcurrency == 0 {
		setting.MaxConcurrency = defaultConcurrency(provider)
	}
	state := t.states[key]
	t.mu.RUnlock()
	if state == nil {
		t.mu.Lock()
		state = t.states[key]
		if state == nil {
			state = &providerThrottleState{}
			state.cond = sync.NewCond(&state.mu)
			t.states[key] = state
		}
		t.mu.Unlock()
	}
	state.mu.Lock()
	for state.active >= setting.MaxConcurrency {
		state.cond.Wait()
	}
	state.active++
	state.mu.Unlock()

	if setting.MaxSeconds > 0 {
		state.paceMu.Lock()
		delay := setting.MinSeconds
		if setting.MaxSeconds > setting.MinSeconds {
			delay += rand.Float64() * (setting.MaxSeconds - setting.MinSeconds)
		}
		remaining := state.lastRun.Add(time.Duration(delay * float64(time.Second))).Sub(time.Now())
		if remaining > 0 {
			time.Sleep(remaining)
		}
		state.lastRun = time.Now()
		state.paceMu.Unlock()
	}
	return func() {
		state.mu.Lock()
		state.active--
		state.cond.Broadcast()
		state.mu.Unlock()
	}
}
