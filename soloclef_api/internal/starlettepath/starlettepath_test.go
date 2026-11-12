package starlettepath

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// This package is held to its behaviour where the requests can be sent:
// internal/publicchat for a route's own use of it, and cmd/api/router_test.go for the guard in front of the whole server,
// which asks its rules of every served route. The tests here are the small
// ones about the pieces.

func TestTheRoutersNotFoundOnTheWire(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteRouterNotFound(recorder, "req_0123456789ab")
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status %d", recorder.Code)
	}
	if body := recorder.Body.String(); body != `{"detail":"Not Found"}` || len(body) != 22 {
		t.Errorf("body %q (%d bytes); Starlette writes 22", body, len(body))
	}
	for header, want := range map[string]string{
		"Content-Type":   "application/json",
		"Content-Length": "22",
		"X-Request-ID":   "req_0123456789ab",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Errorf("%s %q, want %q", header, got, want)
		}
	}
}

func TestUnroutable(t *testing.T) {
	for _, c := range []struct {
		parameters []string
		want       bool
	}{
		{[]string{"abc"}, false},
		{[]string{"a/b"}, true},
		{[]string{"/"}, true},
		{[]string{"abc", "7", "a/b"}, true},
		{[]string{"abc", "7"}, false},
		// A backslash, a dot and an escaped-looking slash are ordinary: the
		// path was decoded once, and "%2F" left in a parameter is text.
		{[]string{`a\b`, ".", "..", "a%2Fb"}, false},
		{nil, false},
	} {
		if got := Unroutable(c.parameters...); got != c.want {
			t.Errorf("Unroutable(%q) = %v", c.parameters, got)
		}
	}
}

func TestAsDecodedByTheServer(t *testing.T) {
	for given, want := range map[string]string{
		"abc":        "abc",
		"a\xffb":     "a�b",
		"a\xe4\xb8b": "a�b", // a three-byte sequence cut short
		"café 中文":    "café 中文",
		"":           "",
	} {
		if got := AsDecodedByTheServer(given); got != want {
			t.Errorf("AsDecodedByTheServer(%q) = %q, want %q", given, got, want)
		}
	}
}

func served(t *testing.T) Served {
	t.Helper()
	routes, err := NewServed(
		"GET /api/v1/pats",
		"DELETE /api/v1/pats/{pat_id}",
		"GET /api/v1/folders/{folder_id}/breadcrumb",
		"GET /api/v1/notebooks/{notebook_id}",
		"GET /api/v1/notebooks/{notebook_id}/sources",
		"GET /api/v1/notebooks/{notebook_id}/sources/{source_id}",
	)
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

// TestTheThreeOutcomes is the guard's decision, one path at a time. What the
// Python tier answers for each kind is in cmd/api's recording; this is the
// reading of it, where a wrong line is easiest to see.
func TestTheThreeOutcomes(t *testing.T) {
	routes := served(t)
	for _, c := range []struct {
		method, path string
		want         outcome
		routed       string
	}{
		// The routers agree: untouched, whether or not it is a route.
		{"GET", "/api/v1/pats", untouched, ""},
		{"GET", "/api/v1/folders/abc/breadcrumb", untouched, ""},
		{"GET", "/api/v1/folders/a%20b/breadcrumb", untouched, ""},
		{"GET", "/api/v1/folders/a%FFb/breadcrumb", untouched, ""},
		{"GET", "/api/v1/folders/%2E%2E/breadcrumb", untouched, ""},
		{"GET", "/healthz", untouched, ""},
		{"GET", "/api/v1/nothing/here", untouched, ""},
		{"GET", "/api/v1/pats/", untouched, ""},

		// Starlette runs one of this tier's routes; so does this tier.
		{"GET", "/api/v1/folders/./breadcrumb", sameRoute, "/api/v1/folders/%2E/breadcrumb"},
		{"GET", "/api/v1/folders/../breadcrumb", sameRoute, "/api/v1/folders/%2E%2E/breadcrumb"},
		{"GET", "/api/v1/notebooks/7%2Fsources", sameRoute, "/api/v1/notebooks/7/sources"},
		{"GET", "/api/v1%2Fpats", sameRoute, "/api/v1/pats"},
		{"GET", "/api/v1/notebooks/7%2Fsources/a%20b", sameRoute, "/api/v1/notebooks/7/sources/a%20b"},

		// Starlette has no route, and this tier would have answered for one.
		{"GET", "/api/v1/folders/a%2Fb/breadcrumb", noRoute, ""},
		{"GET", "/api/v1/folders/a%2fb/breadcrumb", noRoute, ""},
		{"GET", "/api/v1/folders/%2F/breadcrumb", noRoute, ""},
		{"GET", "/api/v1/folders//breadcrumb", noRoute, ""},
		{"GET", "/api/v1//pats", noRoute, ""},
		{"GET", "//api/v1/pats", noRoute, ""},
		{"GET", "/api/v1/folders//a%2Fb/breadcrumb", noRoute, ""},
		{"GET", "/api/v1/notebooks/a%2Fb/sources/7", noRoute, ""},
		// A method this tier does not serve on the path is not addressed to
		// it as written, but ServeMux reads a GET pattern as HEAD's too and
		// would redirect this towards one.
		{"HEAD", "/api/v1//pats", noRoute, ""},

		// Not this tier's to settle (Guard, "What it deliberately does not
		// settle"): left exactly as it arrived.
		{"GET", "/api/v1/./pats", untouched, ""},
		{"GET", "/api/v1/x/../pats", untouched, ""},
		{"GET", "/api/v1/pats//", untouched, ""},
		{"DELETE", "/api/v1/pats/7%2F", untouched, ""},
		{"DELETE", "/api/v1/pats/%2F", untouched, ""},
		{"GET", "/api/v1//nothing/here", untouched, ""},
		{"GET", "/api/v1/nothing%2Fhere", untouched, ""},
		{"POST", "/api/v1/folders/a%2Fb/breadcrumb", untouched, ""},
		{"GET", "//healthz", untouched, ""},
		{"CONNECT", "/api/v1//pats", untouched, ""},
	} {
		request := httptest.NewRequest(c.method, "http://api.example.invalid"+c.path, nil)
		got, routed := routes.read(request)
		if got != c.want || routed != c.routed {
			t.Errorf("%s %s: outcome %d with path %q, want %d with %q",
				c.method, c.path, got, routed, c.want, c.routed)
		}
	}
}

// TestTheReEscapedPathIsTheOneServeMuxIsShown checks the mechanism outcome 1
// depends on: net/url only uses a RawPath that is a valid escaping of Path,
// and silently falls back to its own escaping otherwise -- which would put a
// literal dot back and have ServeMux redirect after all.
func TestTheReEscapedPathIsTheOneServeMuxIsShown(t *testing.T) {
	for _, decoded := range []string{
		"/api/v1/folders/./breadcrumb",
		"/api/v1/folders/../breadcrumb",
		"/api/v1/folders/a b/breadcrumb",
		"/api/v1/folders/a%2Fb/breadcrumb", // a literal percent sign in the id
		"/api/v1/folders/a;b,c=d&e+f@g:h$i/breadcrumb",
		"/api/v1/folders/café 中文/breadcrumb",
		"/api/v1/folders/a\xffb/breadcrumb",
		"/api/v1/folders/a?b#c/breadcrumb",
	} {
		address := &url.URL{Path: decoded, RawPath: escapedBySegment(decoded)}
		if shown := address.EscapedPath(); shown != address.RawPath {
			t.Errorf("%q: net/url would show ServeMux %q, not the %q it was given", decoded, shown, address.RawPath)
		}
		if tidied(address.RawPath) != address.RawPath {
			t.Errorf("%q: ServeMux would still clean %q", decoded, address.RawPath)
		}
	}
}

// TestTidiedIsServeMuxsOwnCleaning holds the copy of net/http's unexported
// cleanPath to the real thing: ServeMux redirects exactly the paths `tidied`
// says it would change, and to exactly that path.
func TestTidiedIsServeMuxsOwnCleaning(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	for _, path := range []string{
		"/", "/a", "/a/", "/a/b", "//", "//a", "/a//b", "/a/b//", "/a/./b", "/a/../b",
		"/a/b/.", "/a/b/..", "/..", "/a/%2E/b", "/a/%2E%2E/b", "/a/b%2Fc", "/a/%2F/b",
		"/a///b", "/a/.//b", "/a/.b/..c", "/a/b/./",
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://api.example.invalid"+path, nil))
		redirected := recorder.Code == http.StatusMovedPermanently
		if want := tidied(path) != path; redirected != want {
			t.Errorf("%q: ServeMux redirected is %v, tidied says %v", path, redirected, want)
			continue
		}
		if redirected && recorder.Header().Get("Location") != tidied(path) {
			t.Errorf("%q: ServeMux redirects to %q, tidied says %q", path, recorder.Header().Get("Location"), tidied(path))
		}
	}
}

func TestAServedRouteMustBePlainSegments(t *testing.T) {
	for _, pair := range []string{
		"GET /api/v1/files/{path...}", // matches across segments
		"GET /api/v1/pats/",           // a trailing slash is a subtree pattern
		"GET /api/v1/pats/{$}",        // ServeMux's end anchor
		"GET /api/v1/pats/v{version}", // partly a parameter
		"GET /api/v1//pats",           // an empty segment
		"/api/v1/pats",                // no method
		"GET api/v1/pats",             // no leading slash
		"GET /api/v1/pats/{id:path}",  // Starlette's convertor syntax
		"GET /",                       // the catch-all
	} {
		if _, err := NewServed(pair); err == nil {
			t.Errorf("%q was accepted", pair)
		}
	}
	if _, err := NewServed("GET /api/v1/pats", "GET /api/v1/pats"); err == nil {
		t.Error("the same route twice was accepted")
	}
}

// TestTheGuardsThreeHands: each outcome goes where it should, and the
// request that goes on to the mux is the one described.
func TestTheGuardsThreeHands(t *testing.T) {
	var shown []string
	mux := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		shown = append(shown, request.URL.EscapedPath()+"?"+request.URL.RawQuery)
		response.WriteHeader(http.StatusNoContent)
	})
	guard := Guard{Served: served(t), NotFound: NotFound()}.Wrap(mux)

	for _, c := range []struct {
		path   string
		status int
		shown  []string
	}{
		{"/api/v1/folders/abc/breadcrumb?next=a%2Fb", http.StatusNoContent, []string{"/api/v1/folders/abc/breadcrumb?next=a%2Fb"}},
		{"/api/v1/folders/./breadcrumb?next=a%2Fb", http.StatusNoContent, []string{"/api/v1/folders/%2E/breadcrumb?next=a%2Fb"}},
		{"/api/v1/folders/a%2Fb/breadcrumb?next=1", http.StatusNotFound, nil},
		{"/api/v1/folders//breadcrumb", http.StatusNotFound, nil},
		{"/api/v1/x/../pats", http.StatusNoContent, []string{"/api/v1/x/../pats?"}},
	} {
		shown = nil
		recorder := httptest.NewRecorder()
		guard.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://api.example.invalid"+c.path, nil))
		if recorder.Code != c.status {
			t.Errorf("%s: status %d, want %d", c.path, recorder.Code, c.status)
		}
		if len(shown) != len(c.shown) || (len(shown) == 1 && shown[0] != c.shown[0]) {
			t.Errorf("%s: the mux was shown %q, want %q", c.path, shown, c.shown)
		}
		if c.status == http.StatusNotFound && recorder.Body.String() != RouterNotFound {
			t.Errorf("%s: body %q", c.path, recorder.Body.String())
		}
	}
}
