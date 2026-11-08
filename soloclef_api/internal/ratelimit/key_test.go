package ratelimit

import "testing"

// The key is read by the Python tier as well, so it is pinned here as a literal:
// a change to any part of it splits one budget into two.
func TestTheKeyIsTheOneTheLimitsLibraryWrites(t *testing.T) {
	got := Key(DefaultLimit, "203.0.113.7", "/api/v1/workspaces/1/members")
	want := "LIMITS:LIMITER/203.0.113.7//api/v1/workspaces/1/members/1024/1/minute"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The 429's Retry-After is a phrase rather than a number of seconds, because
// app.py builds it by splitting the limit's own text on the word "per".
func TestTheLimitsTextIsWhatTheHandlerSplits(t *testing.T) {
	if got := DefaultLimit.String(); got != "1024 per 1 minute" {
		t.Errorf("limit text: got %q", got)
	}
	if got := DefaultLimit.RetryAfter(); got != "1 minute" {
		t.Errorf("retry-after: got %q", got)
	}
}
