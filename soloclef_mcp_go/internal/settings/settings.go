// Package settings resolves a server process's configuration from the
// environment, once.
//
// Secrets never live in code or in a client's configuration file: the client
// passes a key as an environment variable when it launches a stdio server, and
// a remote caller sends its own per request.
package settings

import (
	"os"
	"strconv"
	"strings"
)

const (
	DefaultBaseURL        = "http://localhost:8000"
	DefaultAPIPrefix      = "/api/v1"
	DefaultTimeoutSeconds = 180.0
	DefaultHTTPHost       = "127.0.0.1"
	DefaultHTTPPort       = 8080
)

// Settings is the resolved configuration for a server process.
type Settings struct {
	BaseURL string
	// APIKey is optional: a remote caller passes its own key per request, and
	// only stdio, which has no header, needs one here.
	APIKey         string
	APIPrefix      string
	TimeoutSeconds float64
	Host           string
	Port           int
}

// APIBase is where every REST call is addressed.
func (s Settings) APIBase() string { return s.BaseURL + s.APIPrefix }

// FromEnvironment reads the same variable names the Python server reads, so one
// deployment's configuration serves either.
func FromEnvironment() Settings {
	return Settings{
		APIKey:         trimmed("SURFSENSE_API_KEY"),
		BaseURL:        strings.TrimRight(withDefault("SURFSENSE_BASE_URL", DefaultBaseURL), "/"),
		APIPrefix:      "/" + strings.Trim(withDefault("SURFSENSE_API_PREFIX", DefaultAPIPrefix), "/"),
		TimeoutSeconds: seconds("SURFSENSE_TIMEOUT", DefaultTimeoutSeconds),
		Host:           withDefault("SURFSENSE_MCP_HOST", DefaultHTTPHost),
		Port:           port("SURFSENSE_MCP_PORT", DefaultHTTPPort),
	}
}

func trimmed(name string) string { return strings.TrimSpace(os.Getenv(name)) }

func withDefault(name, fallback string) string {
	if value := trimmed(name); value != "" {
		return value
	}
	return fallback
}

func seconds(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(trimmed(name), 64)
	if err != nil {
		return fallback
	}
	return value
}

func port(name string, fallback int) int {
	value, err := strconv.Atoi(trimmed(name))
	if err != nil {
		return fallback
	}
	return value
}
