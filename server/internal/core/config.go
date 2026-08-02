package core

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Settings holds runtime configuration, populated from .env with
// case-insensitive keys.
type Settings struct {
	AppName     string
	APIPrefix   string
	CORSOrigins []string
	Port        string

	// SampleInterval is how often each sampler polls the machine. It is
	// the most-tuned value in the app, so it belongs in .env rather than
	// in a rebuild.
	SampleInterval time.Duration
}

// Load reads .env (if present) and returns the resulting settings.
// A missing .env is not an error — the defaults below stand on their own.
func Load() *Settings {
	_ = godotenv.Load()

	s := &Settings{
		AppName:        env("APP_NAME", "net-monitor"),
		APIPrefix:      env("API_PREFIX", "/api/v1"),
		Port:           env("PORT", "8000"),
		CORSOrigins:    []string{"http://localhost:5173"},
		SampleInterval: envDuration("SAMPLE_INTERVAL", time.Second),
	}

	// CORS_ORIGINS is JSON, e.g. ["http://localhost:5173"]. Unmarshal into
	// a temporary so a malformed value leaves the default intact.
	if v := env("CORS_ORIGINS", ""); v != "" {
		var origins []string
		if err := json.Unmarshal([]byte(v), &origins); err != nil {
			log.Printf("config: CORS_ORIGINS is not valid JSON (%v); using %v", err, s.CORSOrigins)
		} else {
			s.CORSOrigins = origins
		}
	}

	return s
}

// envDuration reads a Go duration string ("1s", "500ms"). A malformed or
// non-positive value falls back rather than reaching time.NewTicker,
// which panics on anything <= 0.
func envDuration(key string, fallback time.Duration) time.Duration {
	v := env(key, "")
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("config: %s %q is not a duration (%v); using %v", key, v, err, fallback)
		return fallback
	}
	if d <= 0 {
		log.Printf("config: %s must be positive, got %v; using %v", key, d, fallback)
		return fallback
	}
	return d
}

// env resolves key case-insensitively, returning fallback when unset or
// empty.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if v := os.Getenv(strings.ToLower(key)); v != "" {
		return v
	}
	return fallback
}
