package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/health"
)

func TestProbeAddressDialsLoopbackForAServerBoundToEveryInterface(t *testing.T) {
	for listen, want := range map[string]string{
		":8080":            "127.0.0.1:8080",
		"0.0.0.0:8080":     "127.0.0.1:8080",
		"[::]:8080":        "[::1]:8080",
		"127.0.0.1:8081":   "127.0.0.1:8081",
		"10.1.2.3:9000":    "10.1.2.3:9000",
		"api.internal:443": "api.internal:443",
	} {
		got, err := probeAddress(listen)
		if err != nil || got != want {
			t.Errorf("%q: got %q and %v, want %q", listen, got, err, want)
		}
	}
	for _, listen := range []string{"", "8080", "localhost"} {
		if got, err := probeAddress(listen); err == nil {
			t.Errorf("%q: got %q, want it refused", listen, got)
		}
	}
}

// probedServer is a server that answers the health path with `status`, and
// the environment a healthcheck beside it would have.
func probedServer(t *testing.T, status int) func(string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != health.Path {
			t.Errorf("the probe asked %s %s", request.Method, request.URL.Path)
		}
		response.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return func(name string) string {
		if name == "SOLOCLEF_API_LISTEN_ADDRESS" {
			return strings.TrimPrefix(server.URL, "http://")
		}
		// Nothing else is read: a probe that needed the secrets would fail
		// here for want of them.
		return ""
	}
}

func TestHealthcheckPassesOnlyOnTwoHundred(t *testing.T) {
	if err := healthcheck(probedServer(t, http.StatusOK)); err != nil {
		t.Errorf("a serving replica: %v", err)
	}
	// The route's own answer for an unreachable database.
	err := healthcheck(probedServer(t, http.StatusServiceUnavailable))
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("a replica with no database: got %v, want the 503 reported", err)
	}
}

func TestHealthcheckFailsWhenNothingListens(t *testing.T) {
	// A server that has been closed: its port is one nothing answers on.
	server := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(server.URL, "http://")
	server.Close()

	err := healthcheck(func(string) string { return address })
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("got %v, want it to say the server did not answer", err)
	}
	if err := healthcheck(func(string) string { return "" }); err == nil ||
		!strings.Contains(err.Error(), "SOLOCLEF_API_LISTEN_ADDRESS is required") {
		t.Errorf("no address: got %v", err)
	}
}
