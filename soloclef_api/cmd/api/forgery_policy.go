package main

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
)

// forgeryPolicy is the cross-site write check the server runs with: the
// cookie rule for every write, plus the routes that write the browser's
// session, which are checked by origin with or without a cookie
// (signin.BrowserSessionWrites). One function, so that the server and the
// tests that hold its front door to this rule build the same policy.
func forgeryPolicy(cookieName string, configured []string, addresses ...string) csrforigin.Policy {
	return csrforigin.NewPolicy(cookieName, configured, addresses...).
		GuardingSessionWrites(signin.BrowserSessionWrites()...)
}
