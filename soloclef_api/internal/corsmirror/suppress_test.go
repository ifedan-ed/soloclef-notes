package corsmirror

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func policy() Policy {
	return NewPolicy([]string{"https://notes.example.invalid"})
}

func answer(t *testing.T, origin string, handler http.HandlerFunc) http.Header {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	recorder := httptest.NewRecorder()
	policy().Middleware(handler).ServeHTTP(recorder, request)
	return recorder.Result().Header
}

// The ordinary case: an allowed origin gets all four.
func TestMiddlewareWritesTheHeadersWhenTheResponseStarts(t *testing.T) {
	header := answer(t, "https://notes.example.invalid", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	})
	for name, want := range map[string]string{
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Expose-Headers":    "Content-Disposition",
		"Access-Control-Allow-Origin":      "https://notes.example.invalid",
		"Vary":                             "Origin",
	} {
		if got := header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// A handler that only writes a body, never calling WriteHeader, still gets
// them: the wrapper starts the response on the first Write.
func TestMiddlewareWritesTheHeadersForAHandlerThatOnlyWrites(t *testing.T) {
	header := answer(t, "https://notes.example.invalid", func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("{}"))
	})
	if got := header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Error("a handler that only writes a body got no CORS headers")
	}
}

// A 500 the Python route would not have caught itself carries none. Measured
// against the running tier: GET /api/v1/workspaces/2200000000/memory and
// DELETE /api/v1/pats/2200000000 both answer 500 with no CORS headers there.
func TestSuppressTakesTheHeadersOffAnUncaughtFailure(t *testing.T) {
	header := answer(t, "https://notes.example.invalid", func(response http.ResponseWriter, request *http.Request) {
		Suppress(request.Context())
		response.WriteHeader(http.StatusInternalServerError)
	})
	for _, name := range []string{
		"Access-Control-Allow-Credentials",
		"Access-Control-Expose-Headers",
		"Access-Control-Allow-Origin",
		"Vary",
	} {
		if got := header.Values(name); len(got) != 0 {
			t.Errorf("%s = %q on a suppressed response", name, got)
		}
	}
}

// The delegate suppresses before forwarding so the upstream's headers are the
// only ones, and allows again if the upstream could not be reached.
func TestAllowPutsThemBackForTheTierThatAnswersInstead(t *testing.T) {
	header := answer(t, "https://notes.example.invalid", func(response http.ResponseWriter, request *http.Request) {
		Suppress(request.Context())
		Allow(request.Context())
		response.WriteHeader(http.StatusBadGateway)
	})
	if got := header.Get("Access-Control-Allow-Origin"); got != "https://notes.example.invalid" {
		t.Errorf("Access-Control-Allow-Origin = %q after Allow", got)
	}
}

// A request with no Origin gets none either way, suppressed or not, exactly as
// Starlette serves it.
func TestNoOriginGetsNoHeaders(t *testing.T) {
	for _, suppress := range []bool{false, true} {
		header := answer(t, "", func(response http.ResponseWriter, request *http.Request) {
			if suppress {
				Suppress(request.Context())
			}
			response.WriteHeader(http.StatusOK)
		})
		if got := header.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("suppress=%v: a request with no Origin got %q", suppress, got)
		}
	}
}

// An origin that is not allowed gets the two unconditional headers and not the
// echoed origin, which is CORSMiddleware.send's own split.
func TestAForeignOriginGetsTheUnconditionalHeadersOnly(t *testing.T) {
	header := answer(t, "https://attacker.example.invalid", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	})
	if got := header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q", got)
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q for a foreign origin", got)
	}
	if got := header.Values("Vary"); len(got) != 0 {
		t.Errorf("Vary = %q for a foreign origin", got)
	}
}
