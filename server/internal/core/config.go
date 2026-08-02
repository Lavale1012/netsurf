package core

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Settings holds runtime configuration, populated from .env with
// case-insensitive keys. Field defaults apply when a key is absent.
type Settings struct {
	AppName     string
	APIPrefix   string
	CORSOrigins []string
	Port        string
}

// Load reads .env (if present) and returns the resulting settings.
// A missing .env is not an error — the defaults below stand on their own.
func Load() *Settings {
	_ = godotenv.Load()

	s := &Settings{
		AppName:     "net-monitor",
		APIPrefix:   "/api/v1",
		CORSOrigins: []string{"http://localhost:5173"},
		Port:        "8000",
	}

	if v := lookup("APP_NAME"); v != "" {
		s.AppName = v
	}
	if v := lookup("API_PREFIX"); v != "" {
		s.APIPrefix = v
	}
	if v := lookup("PORT"); v != "" {
		s.Port = v
	}
	if v := lookup("CORS_ORIGINS"); v != "" {
		// Matches the Python contract: CORS_ORIGINS is JSON in .env,
		// e.g. ["http://localhost:5173"]. Fall back to a comma-separated
		// list so a non-JSON value still does something sensible.
		var origins []string
		if err := json.Unmarshal([]byte(v), &origins); err == nil {
			s.CORSOrigins = origins
		} else {
			parts := strings.Split(v, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			s.CORSOrigins = parts
		}
	}

	return s
}

// lookup resolves a key case-insensitively, matching pydantic-settings behavior.
func lookup(key string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	if v, ok := os.LookupEnv(strings.ToLower(key)); ok {
		return v
	}
	return ""
}
