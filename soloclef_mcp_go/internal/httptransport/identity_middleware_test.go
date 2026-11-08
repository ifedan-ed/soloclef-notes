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
	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/signincheck"
)

func served(t *testing.T, configuration settings.Settings, signIn *signincheck.Check) *httptest.Server {
	t.Helper()
	server, _, err := mcpserver.Build(configuration)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(httptransport.Build(server, configuration, signIn))
	t.Cleanup(front.Close)
	return front
}

func keysOnly() settings.Settings {
	return settings.Settings{
		BaseURL:        "http://127.0.0.1:1",
		APIPrefix:      "/api/v1",
		TimeoutSeconds: 1,
	}
}

func withSignIn() settings.Settings {
	configuration := keysOnly()
	configuration.PublicURL = "https://notes.example.invalid/mcp"
	configuration.AuthorizationServer = "https://sso.example.invalid/application/o/mcp/"
	configuration.OAuthScopes = settings.DefaultOAuthScopes
	return configuration
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

// Without sign-in configured, a keyless request gets a plain 401 and no
// challenge: there is nowhere to sign in.
func TestWithoutSignInAKeylessRequestGetsAPlain401(t *testing.T) {
	front := served(t, keysOnly(), nil)
	status, headers, body := post(t, front.URL+"/mcp", "", "")
	if status != 401 {
		t.Errorf("status %d, want 401", status)
	}
	if challenge := headers.Get("WWW-Authenticate"); challenge != "" {
		t.Errorf("a challenge was sent with no provider configured: %s", challenge)
	}
	const want = `{"error":"unauthorized","message":"Missing Soloclef Notes API key. ` +
		`Send 'Authorization: Bearer ss_pat_...' (or an X-API-Key header)."}`
	if body != want {
		t.Errorf("body is\n %s\nwant\n %s", body, want)
	}
}

// With sign-in on, the 401 carries the challenge that tells claude.ai and
// ChatGPT where to sign in, and says so in different words.
func TestWithSignInAKeylessRequestIsToldWhereToSignIn(t *testing.T) {
	front := served(t, withSignIn(), nil)
	status, headers, body := post(t, front.URL+"/mcp", "", "")
	if status != 401 {
		t.Errorf("status %d, want 401", status)
	}
	const wantChallenge = `Bearer resource_metadata="https://notes.example.invalid/` +
		`.well-known/oauth-protected-resource/mcp", ` +
		`scope="openid email profile offline_access"`
	if got := headers.Get("WWW-Authenticate"); got != wantChallenge {
		t.Errorf("challenge is\n %s\nwant\n %s", got, wantChallenge)
	}
	const want = `{"error":"unauthorized","message":"Sign in to Soloclef Notes, or ` +
		`send a Soloclef Notes API key as 'Authorization: Bearer ss_pat_...'."}`
	if body != want {
		t.Errorf("body is\n %s\nwant\n %s", body, want)
	}
}

// A client that cannot set Authorization sends the key as X-API-Key instead.
func TestAKeyMayArriveInEitherHeader(t *testing.T) {
	front := served(t, keysOnly(), nil)
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
	front := served(t, keysOnly(), nil)
	for _, value := range []string{"Bearer ", "Bearer    ", "Basic abc123"} {
		status, _, _ := post(t, front.URL+"/mcp", "Authorization", value)
		if status != 401 {
			t.Errorf("%q: status %d, want 401", value, status)
		}
	}
}

// The health probe and the protected-resource document are read before a client
// has any credential, so neither is behind the check.
func TestThePublicPathsNeedNoCredential(t *testing.T) {
	front := served(t, withSignIn(), nil)
	for _, path := range []string{
		"/health",
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		response, err := http.Get(front.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Errorf("%s answered %d with no credential", path, response.StatusCode)
		}
		if path == "/health" && string(body) != `{"status":"ok"}` {
			t.Errorf("the probe answered %s", body)
		}
	}
}

// Without a provider configured the document is not served at all, so a client
// cannot be pointed at a sign-in that does not exist.
func TestWithoutSignInThereIsNoProtectedResourceDocument(t *testing.T) {
	front := served(t, keysOnly(), nil)
	response, err := http.Get(front.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode == 200 {
		t.Error("the document was served with no provider configured")
	}
}

// A keyless browser preflight is answered before the credential check, and the
// challenge is exposed so a browser-based client can read where to sign in.
func TestAPreflightIsAnsweredWithoutACredential(t *testing.T) {
	front := served(t, withSignIn(), nil)
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
	exposed := response.Header.Get("Access-Control-Expose-Headers")
	for _, name := range []string{"Mcp-Session-Id", "WWW-Authenticate"} {
		if !strings.Contains(exposed, name) {
			t.Errorf("%s is not exposed: %q", name, exposed)
		}
	}
}

// An API key is not put to the sign-in check: the backend judges it on every
// tool call, as it always has.
func TestAnAPIKeyIsNotPutToTheSignInCheck(t *testing.T) {
	var asked bool
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			asked = true
			writer.WriteHeader(401)
		}))
	defer backend.Close()
	front := served(t, withSignIn(), signincheck.New(backend.URL))
	status, _, _ := post(t, front.URL+"/mcp", "Authorization", "Bearer ss_pat_probe")
	if status != 200 {
		t.Errorf("status %d, want the key to get through", status)
	}
	if asked {
		t.Error("an API key was sent to the sign-in check")
	}
}

// Any other bearer token is a sign-in token, and the backend's verdict decides
// the status, the challenge and the words.
func TestASignInTokenIsRefusedInTheBackendsTerms(t *testing.T) {
	cases := []struct {
		label         string
		status        int
		body          string
		wantStatus    int
		wantBody      string
		wantChallenge bool
	}{
		{"expired", 401, `{"detail": "Token has expired"}`, 401,
			`{"error":"invalid_token","message":"Token has expired"}`, true},
		{"expired with no detail", 401, `{}`, 401,
			`{"error":"invalid_token","message":"The sign-in is not valid or has expired"}`, true},
		{"no account", 403, `{"detail": "No account matches"}`, 403,
			`{"error":"access_denied","message":"No account matches"}`, false},
		{"no account with no detail", 403, `{}`, 403,
			`{"error":"access_denied","message":"No Soloclef Notes account matches this sign-in"}`,
			false},
		{"provider down", 503, `{"detail": "Keys unavailable"}`, 503,
			`{"error":"temporarily_unavailable","message":"Keys unavailable"}`, false},
	}
	for _, each := range cases {
		t.Run(each.label, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(
				func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(each.status)
					_, _ = writer.Write([]byte(each.body))
				}))
			defer backend.Close()
			front := served(t, withSignIn(), signincheck.New(backend.URL))
			status, headers, body := post(t, front.URL+"/mcp",
				"Authorization", "Bearer not.an.apikey")
			if status != each.wantStatus {
				t.Errorf("status %d, want %d", status, each.wantStatus)
			}
			if body != each.wantBody {
				t.Errorf("body is\n %s\nwant\n %s", body, each.wantBody)
			}
			challenged := headers.Get("WWW-Authenticate") != ""
			if challenged != each.wantChallenge {
				t.Errorf("challenge present = %v, want %v (%s)",
					challenged, each.wantChallenge, headers.Get("WWW-Authenticate"))
			}
		})
	}
}

// A 403 carries no challenge: signing in again as the same person cannot fix a
// sign-in with no account behind it.
func TestASignInWithNoAccountIsNotInvitedToTryAgain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(403)
			_, _ = writer.Write([]byte(`{"detail": "No account matches"}`))
		}))
	defer backend.Close()
	front := served(t, withSignIn(), signincheck.New(backend.URL))
	_, headers, _ := post(t, front.URL+"/mcp", "Authorization", "Bearer not.an.apikey")
	if headers.Get("WWW-Authenticate") != "" {
		t.Errorf("a 403 carried a challenge: %s", headers.Get("WWW-Authenticate"))
	}
}
