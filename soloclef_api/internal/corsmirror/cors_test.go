package corsmirror

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, policy Policy, origin string) *httptest.ResponseRecorder {
	t.Helper()
	handler := policy.Middleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/workspaces/1/summary", nil)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAnAllowedOriginGetsTheCredentialedHeaders(t *testing.T) {
	policy := NewPolicy([]string{"https://notes.example.com"})

	for _, origin := range []string{
		"https://notes.example.com",
		"http://localhost:3000",
		"http://127.0.0.1:4100",
	} {
		recorder := serve(t, policy, origin)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("%s: allow-origin is %q", origin, got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("%s: allow-credentials is %q", origin, got)
		}
		if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "Content-Disposition" {
			t.Errorf("%s: expose-headers is %q", origin, got)
		}
		if got := recorder.Header().Get("Vary"); got != "Origin" {
			t.Errorf("%s: vary is %q", origin, got)
		}
	}
}

func TestAnUnknownOriginIsNotEchoedAndDoesNotVary(t *testing.T) {
	recorder := serve(t, NewPolicy(nil), "https://elsewhere.example")

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin is %q, want none", got)
	}
	if got := recorder.Header().Get("Vary"); got != "" {
		t.Errorf("vary is %q, want none", got)
	}
	// Starlette still sends these two, so this tier does too.
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("allow-credentials is %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); got != "Content-Disposition" {
		t.Errorf("expose-headers is %q", got)
	}
}

func TestASameOriginRequestIsUntouched(t *testing.T) {
	recorder := serve(t, NewPolicy(nil), "")

	for _, header := range []string{
		"Access-Control-Allow-Origin", "Vary",
		"Access-Control-Allow-Credentials", "Access-Control-Expose-Headers",
	} {
		if got := recorder.Header().Get(header); got != "" {
			t.Errorf("%s is %q, want none", header, got)
		}
	}
}
