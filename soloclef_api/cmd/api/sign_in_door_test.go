package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/oidcsignin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
)

// The sign-in routes are reached with no credential at all -- nobody is
// signed in yet -- while the session read and the credential check are
// behind the gate like any other route, and a password is tried, set or
// mailed about at most ten times a minute from one client on each of the
// routes that do so.
func TestTheSignInRoutesAreOutsideTheGateAndPasswordsAreLimited(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	served := servedRoutes()
	counter := &ratelimit.MemoryCounter{}
	handler, err := frontDoor{
		Logger:    logger,
		CORS:      corsmirror.NewPolicy([]string{webOrigin}),
		Forgeries: csrforigin.NewPolicy(sessionCookie, nil, webOrigin),
		Limiter: ratelimit.Middleware{Counter: counter, Limit: ratelimit.DefaultLimit,
			Covers: ratelimit.CoveringRoutes(served...), Errors: errorenvelope.Writer{}, Logger: logger},
		Attempts: ratelimit.Middleware{Counter: counter,
			Limit:  ratelimit.Limit{Amount: 10, Multiples: 1, Granularity: "minute", Window: time.Minute},
			Covers: ratelimit.CoveringRoutes(signin.PasswordAttempts()...), Errors: errorenvelope.Writer{}, Logger: logger},
		Ungated: standInRoutes(t, served),
		Gated:   refusingGate(),
		Served:  served,
	}.handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	for _, path := range []string{signin.PasswordPath, signin.PhonePath, signin.RefreshPath, signin.RevokePath,
		signin.RegisterPath, signin.ForgotPath, signin.ResetPath} {
		if got := ask(t, server, http.MethodPost, path, nil); got.status != http.StatusOK || got.standIn != path {
			t.Errorf("POST %s with no credential answered %d from %q", path, got.status, got.standIn)
		}
	}
	// Single sign-on's two are reached signed in to nothing as well.
	for _, path := range []string{oidcsignin.AuthorizePath, oidcsignin.CallbackPath} {
		if got := ask(t, server, http.MethodGet, path, nil); got.status != http.StatusOK || got.standIn != path {
			t.Errorf("GET %s with no credential answered %d from %q", path, got.status, got.standIn)
		}
	}
	// The session read and the credential check are about a credential,
	// and are behind the gate.
	for _, path := range []string{signin.SessionPath, signin.VerifyPath} {
		if got := ask(t, server, http.MethodGet, path, nil); got.status != http.StatusUnauthorized {
			t.Errorf("GET %s with no credential answered %d", path, got.status)
		}
	}

	// One attempt was made on each sign-in route above; nine more fit.
	for range 9 {
		if got := ask(t, server, http.MethodPost, signin.PasswordPath, nil); got.status != http.StatusOK {
			t.Fatalf("an attempt inside the limit answered %d", got.status)
		}
	}
	if got := ask(t, server, http.MethodPost, signin.PasswordPath, nil); got.status != http.StatusTooManyRequests {
		t.Errorf("the eleventh attempt in a minute answered %d", got.status)
	}
	// The sign-up form and the two reset routes are held to the same limit,
	// each on its own count: one request was made on each above.
	for _, path := range []string{signin.RegisterPath, signin.ForgotPath, signin.ResetPath} {
		for range 9 {
			if got := ask(t, server, http.MethodPost, path, nil); got.status != http.StatusOK {
				t.Fatalf("POST %s inside the limit answered %d", path, got.status)
			}
		}
		if got := ask(t, server, http.MethodPost, path, nil); got.status != http.StatusTooManyRequests {
			t.Errorf("the eleventh POST %s in a minute answered %d", path, got.status)
		}
	}
	// A refresh is not a password attempt and keeps the general limit.
	if got := ask(t, server, http.MethodPost, signin.RefreshPath, nil); got.status != http.StatusOK {
		t.Errorf("a refresh after the attempts ran out answered %d", got.status)
	}
}
