package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type MovieSearchPolicy struct {
	Enabled   bool     `json:"enabled"`
	Providers []string `json:"providers"`
}

type movieSearchPolicyStore struct {
	mu    sync.RWMutex
	path  string
	value MovieSearchPolicy
}

func newMovieSearchPolicyStore() *movieSearchPolicyStore {
	path := os.Getenv("MOVIE_SEARCH_POLICY_CONFIG")
	if path == "" {
		path = "/config/movie-search-policy.json"
	}
	s := &movieSearchPolicyStore{path: path}
	_ = s.load()
	return s
}

func (s *movieSearchPolicyStore) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.value)
}

func (s *movieSearchPolicyStore) get() MovieSearchPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return MovieSearchPolicy{Enabled: s.value.Enabled, Providers: append([]string{}, s.value.Providers...)}
}

func (s *movieSearchPolicyStore) update(value MovieSearchPolicy, available map[string]string) error {
	seen := make(map[string]bool, len(value.Providers))
	clean := make([]string, 0, len(value.Providers))
	for _, name := range value.Providers {
		key := throttleKey(name)
		canonical, ok := available[key]
		if !ok {
			return fmt.Errorf("unknown movie provider %q", name)
		}
		if !seen[key] {
			seen[key] = true
			clean = append(clean, canonical)
		}
	}
	if value.Enabled && len(clean) == 0 {
		return errors.New("ordered search requires at least one provider")
	}
	value.Providers = clean
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err = os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err = os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
	return nil
}

func (e *Engine) MovieSearchPolicy() MovieSearchPolicy { return e.movieSearchPolicy.get() }

func (e *Engine) UpdateMovieSearchPolicy(value MovieSearchPolicy) error {
	available := make(map[string]string)
	for _, provider := range e.GetMovieProviders() {
		available[strings.ToLower(strings.TrimSpace(provider.Name()))] = provider.Name()
	}
	return e.movieSearchPolicy.update(value, available)
}
