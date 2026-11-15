package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/zerosync"
)

// cookieGate stands in for the credential gate: a request carrying the
// session cookie is that account's, anything else is the gate's 401.
func cookieGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, err := request.Cookie(sessionCookie); err != nil {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		caller := principal.Principal{UserID: "account-1", Method: principal.MethodSession}
		next.ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	})
}

// zero-cache asks the API which rows a named query may see, forwarding the
// browser's cookie and nothing else: no Origin, and always from its own one
// address. So the route must be reached past the CSRF check (a cookie write
// with no Origin is that check's 403) and past the per-client limiter
// (which would count every tab's queries as one client's), while still
// meeting the credential gate.
func TestZeroCachesQueryRouteIsGatedButNeitherCSRFCheckedNorCounted(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	served := servedRoutes()
	counted := &recordingCounter{}
	limiter := ratelimit.Middleware{
		Counter: counted,
		Limit:   ratelimit.DefaultLimit,
		Covers:  ratelimit.CoveringRoutes(served...),
		Errors:  errorenvelope.Writer{},
		Logger:  logger,
	}
	handler, err := frontDoor{
		Logger:    logger,
		CORS:      corsmirror.NewPolicy([]string{webOrigin}),
		Forgeries: csrforigin.NewPolicy(sessionCookie, nil, webOrigin),
		Limiter:   limiter,
		Ungated:   standInRoutes(t, served),
		Gated:     refusingGate(),
		Served:    served,
		Internal: map[string]http.Handler{
			http.MethodPost + " " + zerosync.Path: cookieGate(zerosync.Handler{Logger: logger}),
		},
	}.handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	post := func(withCookie bool) (int, map[string]any) {
		request, _ := http.NewRequest(http.MethodPost, server.URL+zerosync.Path,
			strings.NewReader(`["transform",[{"id":"q","name":"folders.mine","args":[]}]]`))
		if withCookie {
			request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "token"})
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(response.Body).Decode(&body)
		return response.StatusCode, body
	}

	for range 3 {
		status, body := post(true)
		if status != http.StatusOK || body["kind"] != "QueryResponse" || body["userID"] != "account-1" {
			t.Fatalf("with the cookie and no Origin: %d %v", status, body)
		}
	}
	if status, _ := post(false); status != http.StatusUnauthorized {
		t.Errorf("without a credential: %d, want the gate's 401", status)
	}
	if len(counted.keys) != 0 {
		t.Errorf("the limiter counted the zero queries: %v", counted.keys)
	}
	// A product route from the same client is counted, so the recorder
	// above would have seen one.
	ask(t, server, http.MethodGet, "/api/v1/sign-in-options", nil)
	if len(counted.keys) != 1 {
		t.Errorf("a product route was counted %d times, want 1", len(counted.keys))
	}
}

// recordingCounter is the limiter's counter, keeping every key it was hit
// with and never limiting.
type recordingCounter struct {
	mu   sync.Mutex
	keys []string
}

func (c *recordingCounter) Hit(_ context.Context, key string, _ time.Duration, _ int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, key)
	return 1, nil
}
