package config

import (
	"strings"
	"testing"
	"time"
)

func fakeEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func completeEnvironment() map[string]string {
	return map[string]string{
		"SOLOCLEF_API_LISTEN_ADDRESS":   "127.0.0.1:8081",
		"SOLOCLEF_API_DATABASE_URL":     "postgres://user:pw@db:5432/notes",
		"SOLOCLEF_API_SESSION_SECRET":   "test-secret",
		"SOLOCLEF_API_PYTHON_UPSTREAM":  "http://backend:8000",
		"SOLOCLEF_API_ERROR_REPORT_URL": "https://notes.example.com/contact",
		"SOLOCLEF_API_REDIS_URL":        "redis://redis:6379/0",
		// The broker is a setting of its own: the same server as the app Redis
		// on this host, and two settings in app/config either way.
		"SOLOCLEF_API_CELERY_BROKER_URL":    "redis://redis:6379/0",
		"SOLOCLEF_API_CELERY_DEFAULT_QUEUE": "surfsense",
	}
}

func TestLoadMirrorsThePythonTiersDefaults(t *testing.T) {
	config, err := Load(fakeEnvironment(completeEnvironment()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.SessionCookieName != "surfsense_session" {
		t.Errorf("cookie name: got %q", config.SessionCookieName)
	}
	if config.MinIssuedAt != 0 {
		t.Errorf("min issued at: got %d", config.MinIssuedAt)
	}
	if config.DatabaseMaxConnections != 10 {
		t.Errorf("max connections: got %d", config.DatabaseMaxConnections)
	}
	if config.ShutdownGrace != 15*time.Second {
		t.Errorf("shutdown grace: got %s", config.ShutdownGrace)
	}
	if config.AllowedOrigins != nil {
		t.Errorf("allowed origins: got %v, want none", config.AllowedOrigins)
	}
	if config.AgentProviderName != "authentik" {
		t.Errorf("agent provider name: got %q", config.AgentProviderName)
	}
	if config.BrandName != "Soloclef Notes" {
		t.Errorf("brand name: got %q", config.BrandName)
	}
	if config.PostHogAPIKey != "" || config.PostHogHost != "" {
		t.Errorf("posthog: got %q/%q, want telemetry off", config.PostHogAPIKey, config.PostHogHost)
	}
	if config.AgentIssuer != "" || config.AgentClientID != "" {
		t.Errorf("agent provider: got %q/%q, want none configured",
			config.AgentIssuer, config.AgentClientID)
	}
}

// A project key with nowhere to send to would make this tier guess a vendor's
// address, which is the one thing no setting here does.
func TestLoadRefusesAProjectKeyWithNoHost(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_POSTHOG_API_KEY"] = "phc_a_project_key"
	if _, err := Load(fakeEnvironment(values)); err == nil {
		t.Error("want an error, got none")
	}
	values["SOLOCLEF_API_POSTHOG_HOST"] = "https://telemetry.example.invalid/"
	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.PostHogHost != "https://telemetry.example.invalid" {
		t.Errorf("host: got %q, want it without its trailing slash", config.PostHogHost)
	}
}

// Half of the agent provider's settings would leave the audience check — the
// one thing that stops a token issued to another application being used here —
// with nothing to check against.
func TestLoadRefusesHalfOfTheAgentProvider(t *testing.T) {
	for name, half := range map[string]string{
		"only the issuer":    "SOLOCLEF_API_AGENT_ISSUER",
		"only the client id": "SOLOCLEF_API_AGENT_CLIENT_ID",
	} {
		values := completeEnvironment()
		values[half] = "something"
		if _, err := Load(fakeEnvironment(values)); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}

	values := completeEnvironment()
	values["SOLOCLEF_API_AGENT_ISSUER"] = "https://sso.example.com/application/o/agents/"
	values["SOLOCLEF_API_AGENT_CLIENT_ID"] = "agents"
	if _, err := Load(fakeEnvironment(values)); err != nil {
		t.Errorf("both set: %v", err)
	}
}

func TestLoadReadsTheOriginListAsAList(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_ALLOWED_ORIGINS"] = " https://notes.example.com , https://www.notes.example.com ,"

	config, err := Load(fakeEnvironment(values))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"https://notes.example.com", "https://www.notes.example.com"}
	if len(config.AllowedOrigins) != len(want) {
		t.Fatalf("got %v, want %v", config.AllowedOrigins, want)
	}
	for index, origin := range want {
		if config.AllowedOrigins[index] != origin {
			t.Errorf("origin %d: got %q, want %q", index, config.AllowedOrigins[index], origin)
		}
	}
}

func TestLoadReportsEverySettingThatCouldNameAServer(t *testing.T) {
	_, err := Load(fakeEnvironment(map[string]string{}))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, name := range []string{
		"SOLOCLEF_API_LISTEN_ADDRESS",
		"SOLOCLEF_API_DATABASE_URL",
		"SOLOCLEF_API_SESSION_SECRET",
		"SOLOCLEF_API_PYTHON_UPSTREAM",
		"SOLOCLEF_API_ERROR_REPORT_URL",
		"SOLOCLEF_API_REDIS_URL",
		"SOLOCLEF_API_CELERY_BROKER_URL",
		"SOLOCLEF_API_CELERY_DEFAULT_QUEUE",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%s is not reported as required: %v", name, err)
		}
	}
}

func TestLoadRefusesUnusableValues(t *testing.T) {
	values := completeEnvironment()
	values["SOLOCLEF_API_MIN_ISSUED_AT"] = "yesterday"
	values["SOLOCLEF_API_LOG_LEVEL"] = "chatty"
	values["SOLOCLEF_API_DATABASE_MAX_CONNECTIONS"] = "0"

	_, err := Load(fakeEnvironment(values))
	if err == nil {
		t.Fatal("want an error, got none")
	}
	for _, fragment := range []string{"MIN_ISSUED_AT", "LOG_LEVEL", "DATABASE_MAX_CONNECTIONS"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("%s is not reported: %v", fragment, err)
		}
	}
}
