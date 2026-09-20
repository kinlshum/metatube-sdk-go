package engine

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestMovieSearchPolicyPersistsCanonicalOrderedProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movie-search-policy.json")
	store := &movieSearchPolicyStore{path: path}
	available := map[string]string{
		"avbase": "AVBASE",
		"jav321": "JAV321",
	}

	err := store.update(MovieSearchPolicy{
		Enabled:   true,
		Providers: []string{"avbase", "JAV321", "AVBASE"},
	}, available)
	if err != nil {
		t.Fatalf("update policy: %v", err)
	}

	want := MovieSearchPolicy{Enabled: true, Providers: []string{"AVBASE", "JAV321"}}
	if got := store.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("policy = %#v, want %#v", got, want)
	}

	reloaded := &movieSearchPolicyStore{path: path}
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload policy: %v", err)
	}
	if got := reloaded.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded policy = %#v, want %#v", got, want)
	}
}

func TestMovieSearchPolicyRejectsInvalidSelections(t *testing.T) {
	store := &movieSearchPolicyStore{path: filepath.Join(t.TempDir(), "policy.json")}
	available := map[string]string{"avbase": "AVBASE"}

	if err := store.update(MovieSearchPolicy{Enabled: true}, available); err == nil {
		t.Fatal("enabled policy without providers must fail")
	}
	if err := store.update(MovieSearchPolicy{Providers: []string{"missing"}}, available); err == nil {
		t.Fatal("unknown provider must fail")
	}
	if err := store.update(MovieSearchPolicy{Enabled: false}, available); err != nil {
		t.Fatalf("disabled empty policy must be accepted: %v", err)
	}
}
