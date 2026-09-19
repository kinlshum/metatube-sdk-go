package engine

import "testing"

func TestJavDBConcurrencyIsAlwaysOne(t *testing.T) {
	for _, configured := range []int{0, 1, 2, 3, 99} {
		if got := effectiveConcurrency("JavDB", configured); got != 1 {
			t.Fatalf("effectiveConcurrency(JavDB, %d) = %d, want 1", configured, got)
		}
	}
	if got := effectiveConcurrency("javdb", 3); got != 1 {
		t.Fatalf("provider matching must be case-insensitive: got %d", got)
	}
}

func TestJavDBRejectsConfigAboveOne(t *testing.T) {
	setting := ProviderThrottleSetting{
		Provider:       "JavDB",
		MinSeconds:     3,
		MaxSeconds:     6,
		MaxConcurrency: 2,
	}
	if err := validateThrottle(setting); err == nil {
		t.Fatal("expected JavDB concurrency above one to be rejected")
	}
}

func TestOtherProviderConcurrencyRemainsConfigurable(t *testing.T) {
	if got := effectiveConcurrency("JavBus", 3); got != 3 {
		t.Fatalf("effectiveConcurrency(JavBus, 3) = %d, want 3", got)
	}
}
