package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
)

// Through the server's own front door and policy: a page on another site
// cannot sign the browser in (login CSRF), refresh it or sign it out, though
// it sends no session cookie; the web app's own page and the phone app,
// which names no page, can. The phone app's sign-in sets no cookie and is
// left to the cookie rule.
func TestAForeignPageCannotWriteTheBrowsersSession(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	served := servedRoutes()
	handler, err := frontDoor{
		Logger:    logger,
		CORS:      corsmirror.NewPolicy([]string{webOrigin}),
		Forgeries: forgeryPolicy(sessionCookie, nil, webOrigin),
		Limiter: ratelimit.Middleware{Counter: &ratelimit.MemoryCounter{}, Limit: ratelimit.DefaultLimit,
			Covers: ratelimit.CoveringRoutes(served...), Errors: errorenvelope.Writer{}, Logger: logger},
		Ungated: standInRoutes(t, served),
		Gated:   refusingGate(),
		Served:  served,
	}.handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	foreign := map[string]string{"Origin": "https://evil.example.test"}
	own := map[string]string{"Origin": webOrigin}
	for _, path := range []string{signin.PasswordPath, signin.RefreshPath, signin.RevokePath} {
		got := ask(t, server, http.MethodPost, path, foreign)
		if got.status != http.StatusForbidden || got.body != csrforigin.Refusal {
			t.Errorf("POST %s from a foreign page answered %d %q, want the forgery refusal", path, got.status, got.body)
		}
		if got := ask(t, server, http.MethodPost, path, own); got.status != http.StatusOK || got.standIn != path {
			t.Errorf("POST %s from the web app answered %d from %q", path, got.status, got.standIn)
		}
		if got := ask(t, server, http.MethodPost, path, nil); got.status != http.StatusOK || got.standIn != path {
			t.Errorf("POST %s from a native app answered %d from %q", path, got.status, got.standIn)
		}
	}
	if got := ask(t, server, http.MethodPost, signin.PhonePath, foreign); got.status != http.StatusOK {
		t.Errorf("the phone sign-in, which sets no cookie, answered %d", got.status)
	}
}
