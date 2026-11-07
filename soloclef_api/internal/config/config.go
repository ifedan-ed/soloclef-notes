// Package config reads this service's settings from the environment.
//
// Every value that could name a server — the address to listen on, the
// database, the Python tier — is required and has no default, because a default
// here would be this estate's own deployment written into code the repository
// ships (custom/README.md, "This server's own settings"). The auth settings
// that must agree with the Python tier mirror that tier's own defaults, so a
// deployment that leaves them unset gets the same behaviour on both tiers
// rather than a guess.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const prefix = "SOLOCLEF_API_"

type Config struct {
	// ListenAddress is host:port; a dev build is expected to bind loopback.
	ListenAddress string
	// DatabaseURL is a libpq/pgx URL for the same database the Python tier uses.
	DatabaseURL string
	// DatabaseMaxConnections caps this replica's share of Postgres.
	DatabaseMaxConnections int32
	// SessionSecret is the Python tier's SECRET_KEY: the HS256 key its access
	// tokens are signed with. Nothing is verifiable without it.
	SessionSecret string
	// SessionCookieName mirrors the Python tier's SESSION_COOKIE_NAME.
	SessionCookieName string
	// MinIssuedAt mirrors the Python tier's MIN_ISSUED_AT: tokens issued before
	// this unix second are refused, which is how every session is ended at once.
	MinIssuedAt int64
	// PythonUpstream is the Python API's base URL. A credential this tier does
	// not verify itself is served by sending the request there unchanged.
	PythonUpstream string
	// ErrorReportURL is app.exceptions.ISSUES_URL, which every error envelope
	// carries: this deployment's own contact page.
	ErrorReportURL string
	// AllowedOrigins is the list app.py builds from NEXT_FRONTEND_URL, so a
	// cross-origin read gets the same CORS headers back. Empty is valid: a
	// deployment with no address of its own allows only loopback, as there.
	AllowedOrigins []string
	// ShutdownGrace is how long in-flight requests have after a signal.
	ShutdownGrace time.Duration
	// LogLevel is debug, info, warn or error.
	LogLevel string
}

// Load reads the environment, or reports every setting that is missing or
// unusable at once, so a misconfigured deployment fails at startup rather than
// on its first request.
func Load(env func(string) string) (Config, error) {
	var problems []string

	required := func(name string) string {
		value := strings.TrimSpace(env(prefix + name))
		if value == "" {
			problems = append(problems, prefix+name+" is required")
		}
		return value
	}
	optional := func(name, fallback string) string {
		value := strings.TrimSpace(env(prefix + name))
		if value == "" {
			return fallback
		}
		return value
	}
	number := func(name, fallback string) int64 {
		text := optional(name, fallback)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			problems = append(problems, prefix+name+" must be a whole number")
			return 0
		}
		return value
	}

	config := Config{
		ListenAddress:          required("LISTEN_ADDRESS"),
		ErrorReportURL:         required("ERROR_REPORT_URL"),
		AllowedOrigins:         splitList(optional("ALLOWED_ORIGINS", "")),
		DatabaseURL:            required("DATABASE_URL"),
		SessionSecret:          required("SESSION_SECRET"),
		PythonUpstream:         required("PYTHON_UPSTREAM"),
		DatabaseMaxConnections: int32(number("DATABASE_MAX_CONNECTIONS", "10")),
		// app/config: SESSION_COOKIE_NAME, MIN_ISSUED_AT.
		SessionCookieName: optional("SESSION_COOKIE_NAME", "surfsense_session"),
		MinIssuedAt:       number("MIN_ISSUED_AT", "0"),
		ShutdownGrace:     time.Duration(number("SHUTDOWN_GRACE_SECONDS", "15")) * time.Second,
		LogLevel:          optional("LOG_LEVEL", "info"),
	}

	if config.DatabaseMaxConnections < 1 {
		problems = append(problems, prefix+"DATABASE_MAX_CONNECTIONS must be at least 1")
	}
	switch config.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, prefix+"LOG_LEVEL must be debug, info, warn or error")
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("configuration: %s", strings.Join(problems, "; "))
	}
	return config, nil
}

// splitList reads a comma-separated setting.
func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			origins = append(origins, trimmed)
		}
	}
	return origins
}

// FromEnvironment is Load against the real environment.
func FromEnvironment() (Config, error) {
	return Load(os.Getenv)
}
