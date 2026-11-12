package oidcsignin

import "strings"

// defaultReturnPath is where a sign-in lands when it was not sent away from
// anywhere in particular.
const defaultReturnPath = "/dashboard"

// safeReturnPath is the requested page if it is a path on the web app, and
// the dashboard otherwise. The sign-in page passes the page someone was
// sent away from (a shared notebook's link, say); anything a browser could
// read as another site (`//host`, `/\host`, a scheme) must not become the
// place a fresh session is delivered to.
func safeReturnPath(requested string) string {
	if requested == "" || len(requested) > 2048 {
		return defaultReturnPath
	}
	if !strings.HasPrefix(requested, "/") || strings.HasPrefix(requested, "//") || strings.HasPrefix(requested, `/\`) {
		return defaultReturnPath
	}
	path, _, _ := strings.Cut(requested, "?")
	if strings.ContainsAny(requested, "\\\r\n\t") || strings.Contains(path, ":") {
		return defaultReturnPath
	}
	// Back to the sign-in page, or into these routes, is a loop.
	if strings.HasPrefix(requested, "/login") || strings.HasPrefix(requested, "/auth/") {
		return defaultReturnPath
	}
	return requested
}
