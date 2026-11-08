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
	// RedisURL is the Python tier's REDIS_APP_URL: the same server and the same
	// database, because the rate limit counter is shared with that tier rather
	// than kept twice (internal/ratelimit).
	RedisURL string
	// CeleryBrokerURL is the Python tier's CELERY_BROKER_URL: the broker a
	// write's background work is enqueued on (internal/celeryqueue). It is a
	// setting of its own rather than RedisURL above, because they are two
	// settings there: the same server today, and a queue published to the
	// wrong database is work no worker will ever see.
	CeleryBrokerURL string
	// CeleryDefaultQueue is CELERY_TASK_DEFAULT_QUEUE, which the connectors
	// and heavy queue names are built from, exactly as app/celery_app.py
	// builds them.
	CeleryDefaultQueue string
	// AgentIssuer is the identity provider that signs AI agents' access tokens,
	// mirroring MCP_OAUTH_ISSUER, exactly as the provider prints it. Empty is
	// valid and means this tier verifies no agent token itself: such a request
	// is forwarded to the Python tier, which answers as it always did.
	AgentIssuer string
	// AgentClientID mirrors MCP_OAUTH_CLIENT_ID: a token is accepted only when
	// its audience is this client.
	AgentClientID string
	// AgentProviderName mirrors OIDC_PROVIDER_NAME: the oauth_account.oauth_name
	// the web sign-in links identities under, so an identity linked there is
	// found here.
	AgentProviderName string
	// BrandName mirrors app.brand.BRAND_NAME, which two of the agent refusals
	// name word for word.
	BrandName string
	// PostHogAPIKey mirrors POSTHOG_API_KEY. Empty is valid and means this
	// deployment has telemetry off, as there: nothing is queued and nothing is
	// sent (internal/gatedanalytics).
	PostHogAPIKey string
	// PostHogHost mirrors POSTHOG_HOST. It is required whenever the key is set
	// and has no default, because a default here would be a vendor's address
	// written into code this repository ships.
	PostHogHost string
	// ErrorReportURL is app.exceptions.ISSUES_URL, which every error envelope
	// carries: this deployment's own contact page.
	ErrorReportURL string
	// AllowedOrigins is the list app.py builds from NEXT_FRONTEND_URL, so a
	// cross-origin read gets the same CORS headers back. Empty is valid: a
	// deployment with no address of its own allows only loopback, as there.
	AllowedOrigins []string
	// CSRFAllowedOrigins mirrors CSRF_ALLOWED_ORIGINS: the origins a
	// cookie-authenticated write may come from besides this deployment's own
	// two addresses and its own host (internal/csrforigin).
	CSRFAllowedOrigins []string
	// PublicURL mirrors SURFSENSE_PUBLIC_URL and FrontendURL mirrors
	// NEXT_FRONTEND_URL, which falls back to the public one there and does so
	// here. Only their origins are read, and only by the CSRF check; empty is
	// valid and contributes none, exactly as an unset one does there.
	PublicURL   string
	FrontendURL string
	// KnowledgeStoreEnabled mirrors KNOWLEDGE_STORE_ENABLED, the git content
	// store's process-wide master switch. This tier writes nothing to that
	// store, so while the switch is on it hands the folder writes back to the
	// Python tier (internal/folders.StoreDelegate). Read as that config does:
	// the exact word TRUE, in any case, and nothing else.
	KnowledgeStoreEnabled bool
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
		RedisURL:               required("REDIS_URL"),
		CeleryBrokerURL:        required("CELERY_BROKER_URL"),
		CeleryDefaultQueue:     required("CELERY_DEFAULT_QUEUE"),
		DatabaseMaxConnections: int32(number("DATABASE_MAX_CONNECTIONS", "10")),
		// app/config: SESSION_COOKIE_NAME, MIN_ISSUED_AT.
		SessionCookieName: optional("SESSION_COOKIE_NAME", "surfsense_session"),
		AgentIssuer:       optional("AGENT_ISSUER", ""),
		AgentClientID:     optional("AGENT_CLIENT_ID", ""),
		// app/agent_sign_in/settings.py and app/brand.py, same defaults.
		AgentProviderName: optional("AGENT_PROVIDER_NAME", "authentik"),
		BrandName:         optional("BRAND_NAME", "Soloclef Notes"),
		PostHogAPIKey:     optional("POSTHOG_API_KEY", ""),
		PostHogHost:       strings.TrimRight(optional("POSTHOG_HOST", ""), "/"),
		MinIssuedAt:       number("MIN_ISSUED_AT", "0"),
		// app/auth/csrf.py's _allowed_origins and app/config: the same three
		// inputs, with NEXT_FRONTEND_URL's own fallback below.
		CSRFAllowedOrigins: splitList(optional("CSRF_ALLOWED_ORIGINS", "")),
		PublicURL:          optional("PUBLIC_URL", ""),
		FrontendURL:        optional("FRONTEND_URL", ""),
		// app/config: os.getenv("KNOWLEDGE_STORE_ENABLED", "FALSE").upper() == "TRUE".
		KnowledgeStoreEnabled: strings.EqualFold(optional("KNOWLEDGE_STORE_ENABLED", "FALSE"), "TRUE"),
		ShutdownGrace:         time.Duration(number("SHUTDOWN_GRACE_SECONDS", "15")) * time.Second,
		LogLevel:              optional("LOG_LEVEL", "info"),
	}

	if config.FrontendURL == "" {
		config.FrontendURL = config.PublicURL
	}

	// Half-configured agent verification would be a guess at which client a
	// token must be addressed to, and that guess is the audience check.
	if (config.AgentIssuer == "") != (config.AgentClientID == "") {
		problems = append(problems,
			prefix+"AGENT_ISSUER and "+prefix+"AGENT_CLIENT_ID are set together or not at all")
	}
	if config.PostHogAPIKey != "" && config.PostHogHost == "" {
		problems = append(problems, prefix+"POSTHOG_HOST is required when "+prefix+"POSTHOG_API_KEY is set")
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
