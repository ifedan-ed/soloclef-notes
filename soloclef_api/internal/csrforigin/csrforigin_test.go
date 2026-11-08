package csrforigin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const cookieName = "surfsense_session"

func policy() Policy {
	return NewPolicy(cookieName, []string{"http://configured.example.invalid"},
		"http://frontend.example.invalid", "https://public.example.invalid/some/path")
}

func request(method string, headers map[string]string) *http.Request {
	built := httptest.NewRequest(method, "http://api.example.invalid/api/v1/pats/1", nil)
	for name, value := range headers {
		built.Header.Set(name, value)
	}
	return built
}

// Each row was asked of both tiers over HTTP with the session cookie; the
// verdicts are the Python tier's.
func TestWhichCookieWritesArePermitted(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		permit  bool
	}{
		{"a read is never checked", http.MethodGet,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "http://evil.example.invalid"}, true},
		{"a write with no cookie is not checked", http.MethodPost,
			map[string]string{"Origin": "http://evil.example.invalid"}, true},
		{"another cookie entirely is not the session", http.MethodPost,
			map[string]string{"Cookie": "other=x", "Origin": "http://evil.example.invalid"}, true},
		{"a bearer header short-circuits the check", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Authorization": "Bearer t",
				"Origin": "http://evil.example.invalid"}, true},
		{"so does an Authorization header that is not usable", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Authorization": "Basic nonsense",
				"Origin": "http://evil.example.invalid"}, true},

		{"no origin at all is refused", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x"}, false},
		{"a foreign origin is refused", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "http://evil.example.invalid"}, false},
		{"an origin that is not a URL is refused", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "null"}, false},

		{"the configured list is allowed", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "http://configured.example.invalid"}, true},
		{"this deployment's frontend is allowed", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "http://frontend.example.invalid"}, true},
		{"its public address is allowed, by origin rather than by URL", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "https://public.example.invalid"}, true},

		{"a Referer stands in for a missing Origin", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Referer": "http://frontend.example.invalid/settings"}, true},
		{"a Referer with no scheme names no origin", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Referer": "/settings"}, false},
		{"an Origin is read instead of a Referer when both are there", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "http://evil.example.invalid",
				"Referer": "http://frontend.example.invalid/x"}, false},

		// Any origin whose authority is this request's Host passes, so a
		// deployment at an address nobody configured still works from its own
		// pages. The scheme is not compared, which is _is_same_origin's own
		// netloc-only test and was checked both ways against the Python tier.
		{"same authority as the Host header", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "http://api.example.invalid"}, true},
		{"same authority, another scheme", http.MethodPost,
			map[string]string{"Cookie": cookieName + "=x", "Origin": "https://api.example.invalid"}, true},

		{"every unsafe method is checked", http.MethodPut,
			map[string]string{"Cookie": cookieName + "=x"}, false},
		{"and PATCH", http.MethodPatch,
			map[string]string{"Cookie": cookieName + "=x"}, false},
		{"and DELETE", http.MethodDelete,
			map[string]string{"Cookie": cookieName + "=x"}, false},
		{"HEAD is not one of them", http.MethodHead,
			map[string]string{"Cookie": cookieName + "=x"}, true},
		{"nor is OPTIONS, which the CORS policy answers", http.MethodOptions,
			map[string]string{"Cookie": cookieName + "=x"}, true},
	}

	check := policy()
	for _, test := range cases {
		if got := check.Permits(request(test.method, test.headers)); got != test.permit {
			t.Errorf("%s: permitted=%v, want %v", test.name, got, test.permit)
		}
	}
}

// The refusal is Starlette's own two-field body, not the error envelope: this
// check answers from a middleware, so no exception handler wraps it.
func TestTheRefusalIsTheBytesThePythonMiddlewareWrites(t *testing.T) {
	response := httptest.NewRecorder()
	policy().Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the request reached the route")
	})).ServeHTTP(response, request(http.MethodPost, map[string]string{"Cookie": cookieName + "=x"}))

	if response.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", response.Code)
	}
	if got := response.Body.String(); got != Refusal {
		t.Errorf("body %q, want %q", got, Refusal)
	}
	if got := response.Header().Get("Content-Length"); got != "37" {
		t.Errorf("content-length %q, want 37 — the bytes the Python tier sends", got)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content-type %q", got)
	}
}
