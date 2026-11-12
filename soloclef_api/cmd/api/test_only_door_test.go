package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/e2eseed"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/supportcontact"
)

// The end-to-end test routes hand out sessions. They are reached only in a
// stack that was handed them (frontDoor.TestOnly, which cmd/api fills only
// when SOLOCLEF_API_E2E_ROUTES is true); everywhere else a request for one
// meets the gate like any path this tier does not serve, and never the
// route. They are in neither list the product's routes are in.
func TestTheEndToEndRoutesAreReachedOnlyWhereTheyWereSwitchedOn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	served := servedRoutes()
	// The stand-ins answer the product's routes and the seed routes
	// alike, so that "not reached" below is the front door's doing and
	// not the absence of a handler.
	everything := standInRoutes(t, append(slices.Clone(served), e2eseed.Public()...))
	door := func(testOnly []string) *httptest.Server {
		handler, err := frontDoor{
			Logger:    logger,
			CORS:      corsmirror.NewPolicy([]string{webOrigin}),
			Forgeries: csrforigin.NewPolicy(sessionCookie, nil, webOrigin),
			Limiter: ratelimit.Middleware{Counter: &ratelimit.MemoryCounter{}, Limit: ratelimit.DefaultLimit,
				Covers: ratelimit.CoveringRoutes(served...), Errors: errorenvelope.Writer{}, Logger: logger},
			Ungated:  everything,
			Gated:    refusingGate(),
			Served:   served,
			TestOnly: testOnly,
		}.handler()
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return server
	}
	production, testStack := door(nil), door(e2eseed.Public())

	for _, pair := range e2eseed.Public() {
		method, path, _ := strings.Cut(pair, " ")
		path = strings.NewReplacer("{snapshot_id}", "7", "{link_id}", "7").Replace(path)
		if got := ask(t, production, method, path, nil); got.status != http.StatusUnauthorized || got.standIn != "" {
			t.Errorf("%s on a stack without the routes answered %d from %q; it should stop at the gate", pair, got.status, got.standIn)
		}
		if got := ask(t, testStack, method, path, nil); got.status != http.StatusOK || got.standIn == "" {
			t.Errorf("%s on a stack with the routes answered %d from %q", pair, got.status, got.standIn)
		}
		if slices.Contains(served, pair) || slices.Contains(publicRoutes(), pair) {
			t.Errorf("%s is listed among the product's routes", pair)
		}
	}
	// The support address is the product's, public on both.
	for _, server := range []*httptest.Server{production, testStack} {
		if got := ask(t, server, http.MethodGet, supportcontact.Path, nil); got.status != http.StatusOK || got.standIn != supportcontact.Path {
			t.Errorf("the support address answered %d from %q", got.status, got.standIn)
		}
	}
}
