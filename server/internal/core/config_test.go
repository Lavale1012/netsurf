package core

import (
	"testing"
	"time"
)

func TestEnvDuration(t *testing.T) {
	tests := []struct {
		name string
		set  string
		want time.Duration
	}{
		{"unset falls back", "", time.Second},
		{"parses seconds", "5s", 5 * time.Second},
		{"parses millis", "250ms", 250 * time.Millisecond},
		{"malformed falls back", "banana", time.Second},
		// time.NewTicker panics on <= 0, so these must never pass through.
		{"zero falls back", "0s", time.Second},
		{"negative falls back", "-3s", time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set != "" {
				t.Setenv("SAMPLE_INTERVAL", tt.set)
			}
			if got := envDuration("SAMPLE_INTERVAL", time.Second); got != tt.want {
				t.Errorf("envDuration(%q) = %v, want %v", tt.set, got, tt.want)
			}
		})
	}
}

func TestEnvIsCaseInsensitive(t *testing.T) {
	t.Setenv("api_prefix", "/lower")
	if got := env("API_PREFIX", "/default"); got != "/lower" {
		t.Errorf("env fell through to %q, want the lowercase key's value", got)
	}
}
