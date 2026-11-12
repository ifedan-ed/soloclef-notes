package accountprofile

import (
	"net/http/httptest"
	"testing"
	"time"
)

// The captured bytes. Each line was produced by the real
// starlette.responses.Response.delete_cookie with the arguments
// app/auth/session_cookies.py:clear_session passes, inside this checkout's own
// virtualenv, and is the reason this is a mirror and not a guess:
//
//   - the value is `""` **with** the quotes, because SimpleCookie quotes an
//     empty value;
//   - `expires=0` renders as the current time and **not** as the epoch, because
//     Starlette passes the integer through to Morsel, whose `expires` for an
//     int is `_getdate(value)`;
//   - the attributes come out in the morsel's own sorted key order — domain,
//     expires, httponly, max-age, path, samesite, secure — and not in the order
//     Starlette sets them;
//   - `expires=` is lower case and the others are not, which is Morsel's table
//     of printed names.
func TestClearWritesTheBytesStarletteWrites(t *testing.T) {
	at := time.Date(2026, 11, 9, 0, 46, 14, 0, time.UTC)

	for _, each := range []struct {
		name   string
		policy CookiePolicy
		proto  string
		want   []string
	}{
		{
			name: "this deployment's defaults, over http",
			policy: CookiePolicy{
				SessionName: "surfsense_session", RefreshName: "surfsense_refresh",
				SameSite: "lax", SecurePolicy: "auto",
			},
			want: []string{
				`surfsense_session=""; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=lax`,
				`surfsense_refresh=""; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=lax`,
			},
		},
		{
			name: "the same request behind a proxy that terminated TLS",
			policy: CookiePolicy{
				SessionName: "surfsense_session", RefreshName: "surfsense_refresh",
				SameSite: "lax", SecurePolicy: "auto",
			},
			proto: "https",
			want: []string{
				`surfsense_session=""; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=lax; Secure`,
				`surfsense_refresh=""; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=lax; Secure`,
			},
		},
		{
			name: "a domain and a stricter same-site",
			policy: CookiePolicy{
				SessionName: "s", RefreshName: "r",
				Domain: ".example.invalid", SameSite: "strict", SecurePolicy: "always",
			},
			want: []string{
				`s=""; Domain=.example.invalid; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=strict; Secure`,
				`r=""; Domain=.example.invalid; expires=Mon, 09 Nov 2026 00:46:14 GMT; HttpOnly; Max-Age=0; Path=/; SameSite=strict; Secure`,
			},
		},
	} {
		t.Run(each.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			each.policy.Clear(response, each.proto, at)
			got := response.Header().Values("Set-Cookie")
			if len(got) != len(each.want) {
				t.Fatalf("got %d headers, want %d: %q", len(got), len(each.want), got)
			}
			for index := range got {
				if got[index] != each.want[index] {
					t.Errorf("header %d =\n  %q\nwant\n  %q", index, got[index], each.want[index])
				}
			}
		})
	}
}

// `_cookie_secure`'s three policies, and the one header `auto` reads. The
// first entry of a list wins, which is what `proto.split(",", 1)[0].strip()`
// does.
func TestSecureForIsCookieSecure(t *testing.T) {
	for _, each := range []struct {
		policy string
		proto  string
		want   bool
	}{
		{policy: "always", proto: "", want: true},
		{policy: "always", proto: "http", want: true},
		{policy: "never", proto: "https", want: false},
		{policy: "auto", proto: "", want: false},
		{policy: "auto", proto: "https", want: true},
		{policy: "auto", proto: "HTTPS", want: true},
		{policy: "auto", proto: "http", want: false},
		{policy: "auto", proto: "https, http", want: true},
		{policy: "auto", proto: " https , http", want: true},
		{policy: "auto", proto: "http, https", want: false},
	} {
		policy := CookiePolicy{SecurePolicy: each.policy}
		if got := policy.secureFor(each.proto); got != each.want {
			t.Errorf("policy %q with X-Forwarded-Proto %q = %v, want %v",
				each.policy, each.proto, got, each.want)
		}
	}
}
