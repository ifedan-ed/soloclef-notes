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
