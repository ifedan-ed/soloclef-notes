package pythondelegate

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestARequestReachesThePythonTierUnchangedAndItsResponseComesBack(t *testing.T) {
	var (
		seenPath          string
		seenQuery         string
		seenAuthorization string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.Path
		seenQuery = request.URL.RawQuery
		seenAuthorization = request.Header.Get("Authorization")
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("X-Request-ID", "req_fromthepython")
		response.WriteHeader(http.StatusTeapot)
		_, _ = response.Write([]byte(`{"detail":"from the Python tier"}`))
	}))
	defer upstream.Close()

	delegate, err := New(upstream.URL, discardLogger(), failTest(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/workspaces/1/summary?hours=48", nil)
	request.Header.Set("Authorization", "Bearer an-identity-providers-token")
	recorder := httptest.NewRecorder()
	delegate.ServeHTTP(recorder, request)

	if seenPath != "/api/v1/logs/workspaces/1/summary" || seenQuery != "hours=48" {
		t.Errorf("the upstream saw %q?%q", seenPath, seenQuery)
	}
	if seenAuthorization != "Bearer an-identity-providers-token" {
		t.Errorf("the credential did not survive: %q", seenAuthorization)
	}
	if recorder.Code != http.StatusTeapot {
		t.Errorf("status: got %d", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"detail":"from the Python tier"}` {
		t.Errorf("body: got %s", got)
	}
	if got := recorder.Header().Get("X-Request-ID"); got != "req_fromthepython" {
		t.Errorf("the upstream's request id was not passed back: %q", got)
	}
}

func TestARequestThisTierAlreadyForwardedIsRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the upstream must not be called a second time")
	}))
	defer upstream.Close()

	var reported error
	delegate, err := New(upstream.URL, discardLogger(),
		func(response http.ResponseWriter, _ *http.Request, err error) {
			reported = err
			response.WriteHeader(http.StatusInternalServerError)
		})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs/workspaces/1/summary", nil)
	request.Header.Set(hopHeader, "1")
	delegate.ServeHTTP(httptest.NewRecorder(), request)

	if reported != ErrLoop {
		t.Errorf("got %v, want ErrLoop", reported)
	}
}

func TestAnUpstreamWithoutASchemeIsRefusedAtStartup(t *testing.T) {
	for _, upstream := range []string{"", "backend:8000", "/api"} {
		if _, err := New(upstream, discardLogger(), failTest(t)); err == nil {
			t.Errorf("%q: want an error, got none", upstream)
		}
	}
}

func failTest(t *testing.T) func(http.ResponseWriter, *http.Request, error) {
	return func(_ http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("unexpected delegation failure: %v", err)
	}
}
