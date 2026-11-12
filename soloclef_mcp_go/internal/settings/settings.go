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

// DefaultOAuthScopes is what claude.ai and ChatGPT ask for when a person signs
// in: openid/email/profile so the access token names the person, and
// offline_access for a refresh token so the connection outlives the access
// token's hour.
var DefaultOAuthScopes = []string{"openid", "email", "profile", "offline_access"}

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

	// PublicURL is this server's address exactly as people paste it into
	// claude.ai or ChatGPT; AuthorizationServer is the identity provider's
	// issuer, trailing slash included. Both empty: only API keys are accepted
	// and a request without one gets a plain 401.
	PublicURL           string
	AuthorizationServer string
	OAuthScopes         []string
}

// OAuthEnabled reports whether agent sign-in is configured.
func (s Settings) OAuthEnabled() bool {
	return s.PublicURL != "" && s.AuthorizationServer != ""
}

// APIBase is where every REST call is addressed.
func (s Settings) APIBase() string { return s.BaseURL + s.APIPrefix }

// FromEnvironment reads the same variable names the Python server reads, so one
// deployment's configuration serves either.
func FromEnvironment() Settings {
	resolved := Settings{
		APIKey:              trimmed("SURFSENSE_API_KEY"),
		BaseURL:             strings.TrimRight(withDefault("SURFSENSE_BASE_URL", DefaultBaseURL), "/"),
		APIPrefix:           "/" + strings.Trim(withDefault("SURFSENSE_API_PREFIX", DefaultAPIPrefix), "/"),
		TimeoutSeconds:      seconds("SURFSENSE_TIMEOUT", DefaultTimeoutSeconds),
		Host:                withDefault("SURFSENSE_MCP_HOST", DefaultHTTPHost),
		Port:                port("SURFSENSE_MCP_PORT", DefaultHTTPPort),
		AuthorizationServer: trimmed("MCP_OAUTH_ISSUER"),
		OAuthScopes:         scopes("MCP_OAUTH_SCOPES"),
	}
	// The public address defaults to the site's own /mcp, so one line in the
	// deployment's environment turns sign-in on for the server and the backend.
	resolved.PublicURL = strings.TrimRight(trimmed("SURFSENSE_MCP_PUBLIC_URL"), "/")
	if resolved.PublicURL == "" {
		if site := strings.TrimRight(trimmed("SURFSENSE_PUBLIC_URL"), "/"); site != "" {
			resolved.PublicURL = site + "/mcp"
		}
	}
	return resolved
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

func scopes(name string) []string {
	if fields := strings.Fields(os.Getenv(name)); len(fields) > 0 {
		return fields
	}
	return DefaultOAuthScopes
}
