package httptransport_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/httptransport"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/settings"
)

func served(t *testing.T, configuration settings.Settings) *httptest.Server {
	t.Helper()
	server, _, err := mcpserver.Build(configuration)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(httptransport.Build(server))
	t.Cleanup(front.Close)
	return front
}

// keysOnly is a backend nothing answers on: these tests stop at the
// transport.
func keysOnly() settings.Settings {
	return settings.Settings{
		BaseURL:        "http://127.0.0.1:1",
		APIPrefix:      "/api/v1",
		TimeoutSeconds: 1,
	}
}

func post(t *testing.T, address string, header, value string) (int, http.Header, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), "POST", address,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if header != "" {
		request.Header.Set(header, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, string(body)
}

// A keyless request gets a plain 401 that says how to send a key, and no
// challenge: there is no sign-in to point a client at.
func TestAKeylessRequestIsToldToSendAKey(t *testing.T) {
	front := served(t, keysOnly())
	status, headers, body := post(t, front.URL+"/mcp", "", "")
	if status != 401 {
		t.Errorf("status %d, want 401", status)
	}
	if challenge := headers.Get("WWW-Authenticate"); challenge != "" {
		t.Errorf("a challenge was sent: %s", challenge)
	}
	const want = `{"error":"unauthorized","message":"Missing Soloclef Notes API key. ` +
		`Send 'Authorization: Bearer ss_pat_...' (or an X-API-Key header)."}`
	if body != want {
		t.Errorf("body is\n %s\nwant\n %s", body, want)
	}
}

// A client that cannot set Authorization sends the key as X-API-Key instead.
func TestAKeyMayArriveInEitherHeader(t *testing.T) {
	front := served(t, keysOnly())
	for _, each := range []struct{ header, value string }{
		{"Authorization", "Bearer ss_pat_probe"},
		{"X-API-Key", "ss_pat_probe"},
	} {
		status, _, _ := post(t, front.URL+"/mcp", each.header, each.value)
		if status != 200 {
			t.Errorf("%s: status %d, want the request to get through", each.header, status)
		}
	}
}

// An Authorization header whose scheme is not Bearer carries no credential, and
// neither does an empty bearer.
func TestANonBearerOrEmptyCredentialIsNoCredential(t *testing.T) {
	front := served(t, keysOnly())
	for _, value := range []string{"Bearer ", "Bearer    ", "Basic abc123"} {
		status, _, _ := post(t, front.URL+"/mcp", "Authorization", value)
		if status != 401 {
			t.Errorf("%q: status %d, want 401", value, status)
		}
	}
}

// The health probe is read with no credential; the OAuth discovery
// documents some clients probe first are not served at all, so no client is
// pointed at a sign-in that does not exist.
func TestOnlyTheHealthProbeNeedsNoCredential(t *testing.T) {
	front := served(t, keysOnly())
	response, err := http.Get(front.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(body) != `{"status":"ok"}` {
		t.Errorf("the probe answered %d %s", response.StatusCode, body)
	}
	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
		"/.well-known/oauth-authorization-server",
	} {
		// With a key, so the answer is the router's and not the key check's.
		request, _ := http.NewRequest(http.MethodGet, front.URL+path, nil)
		request.Header.Set("Authorization", "Bearer ss_pat_probe")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %d, want 404", path, response.StatusCode)
		}
	}
}

// A keyless browser preflight is answered before the credential check, and the
// session id is exposed so a browser-based client can read it.
func TestAPreflightIsAnsweredWithoutACredential(t *testing.T) {
	front := served(t, keysOnly())
	request, err := http.NewRequestWithContext(context.Background(), "OPTIONS",
		front.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Errorf("preflight answered %d", response.StatusCode)
	}
	if exposed := response.Header.Get("Access-Control-Expose-Headers"); exposed != "Mcp-Session-Id" {
		t.Errorf("exposed headers are %q", exposed)
	}
}
