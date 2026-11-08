package signincheck_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/signincheck"
)

func answering(t *testing.T, status int, body string, asked *atomic.Int64) *signincheck.Check {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if asked != nil {
				asked.Add(1)
			}
			if request.URL.Path != "/verify-token" {
				t.Errorf("asked %s, want /verify-token", request.URL.Path)
			}
			if request.Header.Get("X-SurfSense-Client") != "mcp" {
				t.Error("the question did not carry the client header")
			}
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte(body))
		}))
	t.Cleanup(backend.Close)
	return signincheck.New(backend.URL)
}

// The backend's principal kind decides this. An API key never reaches here, and
// a web session token is not for agents, so only "agent" is let through.
func TestOnlyAnAgentPrincipalIsAccepted(t *testing.T) {
	cases := []struct {
		label    string
		status   int
		body     string
		want     int
		identity string
		message  string
	}{
		{"an agent", 200, `{"method": "agent", "user_id": "u-1"}`, 200, "user:u-1", ""},
		{"a session", 200, `{"method": "session", "user_id": "u-1"}`, 401, "",
			"Sign in through Soloclef, or use an API key"},
		{"no user", 200, `{"method": "agent"}`, 401, "",
			"Sign in through Soloclef, or use an API key"},
		{"refused", 401, `{"detail": "Token has expired"}`, 401, "", "Token has expired"},
		{"refused with no detail", 401, `{}`, 401, "", ""},
		{"no account", 403, `{"detail": "No account matches"}`, 403, "", "No account matches"},
		{"no account, no detail", 403, `{}`, 403, "", ""},
		{"provider down", 503, `{"detail": "Keys unavailable"}`, 503, "", "Keys unavailable"},
		{"backend error", 500, `{"detail": "boom"}`, 503, "", "boom"},
		{"odd status", 418, `{"detail": "teapot"}`, 503, "", "teapot"},
		{"unparseable", 200, `<<<not json>>>`, 503, "",
			"Unexpected answer from Soloclef Notes"},
	}
	for _, each := range cases {
		t.Run(each.label, func(t *testing.T) {
			verdict := answering(t, each.status, each.body, nil).
				Ask(context.Background(), "token."+each.label)
			if verdict.Status != each.want {
				t.Errorf("status %d, want %d", verdict.Status, each.want)
			}
			if verdict.Identity != each.identity {
				t.Errorf("identity %q, want %q", verdict.Identity, each.identity)
			}
			if verdict.Message != each.message {
				t.Errorf("message %q, want %q", verdict.Message, each.message)
			}
		})
	}
}

// A 5xx is 503, never 401: answering 401 would make the client drop a good token
// and ask the person to sign in again over a backend hiccup.
func TestABackendThatCannotDecideIsNotAnInvalidToken(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 418} {
		verdict := answering(t, status, `{}`, nil).Ask(context.Background(), "t")
		if verdict.Status != 503 {
			t.Errorf("a %d answered %d, want 503", status, verdict.Status)
		}
	}
}

func TestAnUnreachableBackendIsNotAnInvalidToken(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := backend.URL
	backend.Close()
	verdict := signincheck.New(address).Ask(context.Background(), "t")
	if verdict.Status != 503 || verdict.Message != "Could not reach Soloclef Notes" {
		t.Errorf("got %+v, want a 503 that says the backend was unreachable", verdict)
	}
}

// A yes is remembered, so one MCP request costs one question rather than one per
// tool call. The backend still checks the token on every call a tool makes.
func TestAnAcceptedTokenIsRememberedForAWhile(t *testing.T) {
	var asked atomic.Int64
	check := answering(t, 200, `{"method": "agent", "user_id": "u-1"}`, &asked)
	for range 5 {
		if verdict := check.Ask(context.Background(), "same-token"); !verdict.Accepted() {
			t.Fatalf("not accepted: %+v", verdict)
		}
	}
	if asked.Load() != 1 {
		t.Errorf("asked the backend %d times, want once", asked.Load())
	}
	if verdict := check.Ask(context.Background(), "other-token"); !verdict.Accepted() {
		t.Fatalf("not accepted: %+v", verdict)
	}
	if asked.Load() != 2 {
		t.Errorf("a different token was answered from memory: %d questions", asked.Load())
	}
}

// A refusal is not remembered: a token refreshed after an expiry must be able to
// succeed on its next try.
func TestARefusalIsNotRemembered(t *testing.T) {
	var asked atomic.Int64
	check := answering(t, 401, `{"detail": "expired"}`, &asked)
	for range 3 {
		check.Ask(context.Background(), "same-token")
	}
	if asked.Load() != 3 {
		t.Errorf("asked %d times, want one per call", asked.Load())
	}
}

// A token that has already expired is not remembered at all, so the next call
// asks again rather than acting on a stale yes.
func TestATokenPastItsExpiryIsNotRemembered(t *testing.T) {
	var asked atomic.Int64
	check := answering(t, 200, `{"method": "agent", "user_id": "u-1"}`, &asked)
	// {"exp": 1} — the first of January 1970, so any clock is past it.
	const expired = "eyJhbGciOiJub25lIn0.eyJleHAiOjF9.x"
	for range 3 {
		if verdict := check.Ask(context.Background(), expired); !verdict.Accepted() {
			t.Fatalf("not accepted: %+v", verdict)
		}
	}
	if asked.Load() != 3 {
		t.Errorf("asked %d times, want one per call", asked.Load())
	}
}
