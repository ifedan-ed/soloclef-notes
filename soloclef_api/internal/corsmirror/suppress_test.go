package corsmirror

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
// against the running tier: DELETE /api/v1/pats/2200000000 answers 500 with
// no CORS headers there.
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

// connection stands in for net/http's own writer: it can take a reader, and
// remembers which.
type connection struct {
	*httptest.ResponseRecorder
	handed io.Reader
}

func (c *connection) ReadFrom(source io.Reader) (int64, error) {
	c.handed = source
	return io.Copy(c.ResponseRecorder, source)
}

// A handler that copies a file into the response starts it that way, with no
// WriteHeader and no Write. The headers must be on it all the same, and the
// reader must reach the writer underneath as itself, which is what lets
// net/http send a file from the kernel.
func TestACopiedBodyGetsTheHeadersAndIsHandedDown(t *testing.T) {
	bottom := &connection{ResponseRecorder: httptest.NewRecorder()}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	request.Header.Set("Origin", "https://notes.example.invalid")
	// What io.Copy hands a destination with ReadFrom: a reader and nothing
	// else.
	source := struct{ io.Reader }{strings.NewReader("the body")}

	policy().Middleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if _, err := io.Copy(response, source); err != nil {
			t.Error(err)
		}
	})).ServeHTTP(bottom, request)

	if got := bottom.Header().Get("Access-Control-Allow-Origin"); got != "https://notes.example.invalid" {
		t.Errorf("a copied body went out without its CORS headers (%q)", got)
	}
	if bottom.handed != io.Reader(source) {
		t.Errorf("the writer underneath was handed %T, not the reader itself", bottom.handed)
	}
	if bottom.Body.String() != "the body" {
		t.Errorf("body %q", bottom.Body.String())
	}
}

// A writer underneath that cannot take a reader still gets the bytes, and a
// suppressed response is still suppressed when its body is copied.
func TestACopiedBodyIntoAPlainWriterAndASuppressedOne(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	request.Header.Set("Origin", "https://notes.example.invalid")
	policy().Middleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		Suppress(request.Context())
		if _, err := response.(io.ReaderFrom).ReadFrom(strings.NewReader("the body")); err != nil {
			t.Error(err)
		}
	})).ServeHTTP(recorder, request)

	if recorder.Body.String() != "the body" {
		t.Errorf("body %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a suppressed response carried %q", got)
	}
}
