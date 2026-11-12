package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/csrforigin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/health"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/ratelimit"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// These tests hold the assembled server -- frontDoor, which is what main
// listens with -- to what a path must do before any route runs: which route
// it reaches, with which parameters, or none.
//
// They used to replay a recording of the Python router's answer to 1,768
// requests (testdata/router_python.json, from a recorder script),
// and refused a served route the recording had no case for, so that every
// new route meant recording Python again. Both files are deleted. What a
// path must do is written here instead, as rules that are asked of **every**
// served route -- so a new route is covered the moment it is in
// servedRoutes(), with nothing to record -- and as a few hand-written cases
// for the answers that are a decision rather than a rule.
//
// The rules are internal/starlettepath.Guard's, unchanged: the web and mobile
// apps were written against a server that read a path the way Starlette does,
// so an escaped slash still separates two segments and an empty segment still
// matches nothing.
//
// The routes behind the door are stand-ins. That is deliberate and it is
// enough: everything this file is about is decided before a route runs, and
// a stand-in that reports its template and the parameters it was handed
// answers exactly that. The stand-ins are registered the way the real routes
// are (huma on a nested ServeMux), so the mux-inside-a-mux the real server
// has is the one tested -- and registering all of them on that one mux is
// itself the check that no two served patterns conflict, because ServeMux
// panics on a pair it cannot order.

// The deployment the stand-in server believes it is: the one origin its CORS
// and CSRF policies allow, and the name of the session cookie.
const (
	webOrigin     = "https://app.example.test"
	sessionCookie = "soloclef_session"
)

// standInHeader names the template of the stand-in route that ran. A response
// without it was answered by something that is not a route: the guard, a
// middleware, a mux, or the stand-in gate.
const standInHeader = "X-Stand-In-Route"

var templateParameter = regexp.MustCompile(`\{(\w+)\}`)

// standInRoutes registers one operation per served pair. Each answers 200
// with the parameters it was handed, hex-encoded so that a byte that is not
// UTF-8 survives being written as JSON.
func standInRoutes(t *testing.T, pairs []string) http.Handler {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("stand-ins", "0"))
	health.Route{Database: reachableDatabase{}}.Register(api)
	for number, pair := range pairs {
		method, template, _ := strings.Cut(pair, " ")
		var names []string
		for _, found := range templateParameter.FindAllStringSubmatch(template, -1) {
			names = append(names, found[1])
		}
		huma.Register(api, huma.Operation{
			OperationID: "stand-in-" + strconv.Itoa(number),
			Method:      method,
			Path:        template,
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				handed := map[string]string{}
				for _, name := range names {
					handed[name] = hex.EncodeToString([]byte(ctx.Param(name)))
				}
				body, _ := json.Marshal(handed)
				ctx.SetHeader(standInHeader, template)
				ctx.SetHeader("Content-Type", "application/json")
				ctx.SetStatus(http.StatusOK)
				_, _ = ctx.BodyWriter().Write(body)
			}}, nil
		})
	}
	return api.Adapter()
}

// reachableDatabase is the probe's database, answering.
type reachableDatabase struct{}

func (reachableDatabase) Ping(context.Context) error { return nil }

// refusingGate is the credential gate for a caller with no credential: it
// answers 401 and the route behind it never runs. It is what makes "the
// router's 404 comes before the 401" a thing this file can see.
func refusingGate() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"stand_in":"the gate refused"}`))
	})
}

// assembled is frontDoor around the stand-ins, served over a real listener so
// that the headers compared are the ones on the wire.
func assembled(t *testing.T, served []string, gated http.Handler) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	routes := standInRoutes(t, servedRoutes())
	if gated == nil {
		gated = routes
	}
	handler, err := frontDoor{
		Logger:    logger,
		CORS:      corsmirror.NewPolicy([]string{webOrigin}),
		Forgeries: csrforigin.NewPolicy(sessionCookie, nil, webOrigin),
		Limiter: ratelimit.Middleware{
			Counter: &ratelimit.MemoryCounter{},
			Limit:   ratelimit.DefaultLimit,
			Covers:  ratelimit.CoveringRoutes(served...),
			Errors:  errorenvelope.Writer{},
			Logger:  logger,
		},
		Ungated: routes,
		Gated:   gated,
		Served:  served,
	}.handler()
	if err != nil {
		t.Fatalf("assembling the server: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// answer is what came back for one request.
type answer struct {
	status  int
	header  http.Header
	body    string
	standIn string
	// handed is the parameters the stand-in route was given, decoded.
	handed map[string]string
}

// ask sends one request with exactly the path written -- Go's client does
// not tidy a path, which is what lets a literal "." or "//" reach the server
// -- and follows no redirect.
func ask(t *testing.T, server *httptest.Server, method, path string, headers map[string]string) answer {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatalf("%s %s: building the request: %v", method, path, err)
	}
	if sent, _, _ := strings.Cut(request.URL.RequestURI(), "?"); sent != strings.SplitN(path, "?", 2)[0] {
		t.Fatalf("%s %s: the client would send %q instead", method, path, sent)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("%s %s: reading the body: %v", method, path, err)
	}
	got := answer{
		status:  response.StatusCode,
		header:  response.Header,
		body:    string(body),
		standIn: response.Header.Get(standInHeader),
	}
	if got.standIn != "" {
		var encoded map[string]string
		if err := json.Unmarshal(body, &encoded); err != nil {
			t.Fatalf("%s %s: the stand-in's body: %v", method, path, err)
		}
		got.handed = map[string]string{}
		for name, value := range encoded {
			raw, err := hex.DecodeString(value)
			if err != nil {
				t.Fatalf("%s %s: the stand-in's parameter %s: %v", method, path, name, err)
			}
			got.handed[name] = string(raw)
		}
	}
	return got
}

// servedPath writes a served template as a path, with fill deciding what
// goes where each parameter is. It also returns the parameters in the
// template's order.
func servedPath(template string, fill func(index int, name string) string) (string, []string) {
	var names []string
	path := templateParameter.ReplaceAllStringFunc(template, func(found string) string {
		name := found[1 : len(found)-1]
		names = append(names, name)
		return fill(len(names)-1, name)
	})
	return path, names
}

// ordinary fills every parameter with a value that is distinct, that no
// literal segment of any route spells, and that needs no escaping.
func ordinary(index int, _ string) string { return "v" + strconv.Itoa(index+1) }

// routeFor is which served route Starlette's rules give a *decoded* path
// to, written independently of the guard so that the guard is held to
// something other than itself: the same method, the same number of
// segments, each literal equal and each parameter anything that is not
// empty. Empty when there is none.
func routeFor(method, decoded string) string {
	asked := strings.Split(decoded, "/")
	for _, pair := range servedRoutes() {
		verb, template, _ := strings.Cut(pair, " ")
		wanted := strings.Split(template, "/")
		if verb != method || len(wanted) != len(asked) {
			continue
		}
		matches := true
		for i, segment := range wanted {
			if strings.HasPrefix(segment, "{") {
				matches = matches && asked[i] != ""
			} else {
				matches = matches && asked[i] == segment
			}
		}
		if matches {
			return template
		}
	}
	return ""
}

// mustBeTheBare404 holds an answer to the router's own 404: the 22 bytes a
// client of either tier is sent for a path no route matches, as JSON, with
// the request id and -- because the router is inside the CORS policy -- the
// header that lets a browser read it.
func mustBeTheBare404(t *testing.T, what string, got answer) {
	t.Helper()
	if got.standIn != "" {
		t.Errorf("%s: the route %s ran, and no route should", what, got.standIn)
		return
	}
	if got.status != http.StatusNotFound || got.body != starlettepath.RouterNotFound {
		t.Errorf("%s: answered %d %q (Location %q), want the router's 404 %q",
			what, got.status, got.body, got.header.Get("Location"), starlettepath.RouterNotFound)
	}
	if sent := got.header.Get("Content-Type"); sent != "application/json" {
		t.Errorf("%s: Content-Type %q", what, sent)
	}
	if got.header.Get("X-Request-ID") == "" {
		t.Errorf("%s: the 404 carries no request id", what)
	}
	if sent := got.header.Get("Access-Control-Allow-Origin"); sent != webOrigin {
		t.Errorf("%s: Access-Control-Allow-Origin %q, so a browser could not read the 404", what, sent)
	}
}

var fromTheWebApp = map[string]string{"Origin": webOrigin}

// TestEveryServedRouteIsReachedByItsOwnPath asks each served route for its
// own path, written plainly, and requires that it -- and not a neighbour --
// runs, with each parameter in its own place.
//
// This is the check that every pattern sits correctly beside all the others
// on one ServeMux. Two served patterns that ServeMux cannot order stop the
// stand-ins from registering at all; a literal beside a wildcard
// (`/prompts/public` beside `/prompts/{prompt_id}`) must go to the
// literal, which the hand-written cases at the end pin by name.
func TestEveryServedRouteIsReachedByItsOwnPath(t *testing.T) {
	server := assembled(t, servedRoutes(), nil)
	for _, pair := range servedRoutes() {
		method, template, _ := strings.Cut(pair, " ")
		path, names := servedPath(template, ordinary)
		got := ask(t, server, method, path, nil)
		if got.standIn != template {
			t.Errorf("%s as %s: ran %q (status %d)", pair, path, got.standIn, got.status)
			continue
		}
		want := map[string]string{}
		for index, name := range names {
			want[name] = ordinary(index, name)
		}
		if !reflect.DeepEqual(got.handed, want) {
			t.Errorf("%s as %s: the route was handed %q, want %q", pair, path, got.handed, want)
		}
	}

	for _, c := range []struct{ method, path, template string }{
		// A literal that a wildcard beside it would also take.
		{http.MethodGet, "/api/v1/prompts/public", "/api/v1/prompts/public"},
		// The two connector lists beside the routes for one of each.
		{http.MethodGet, "/api/v1/connectors/mcp", "/api/v1/connectors/mcp"},
		{http.MethodGet, "/api/v1/connectors/mcp/7", "/api/v1/connectors/mcp/{connector_id}"},
		{http.MethodGet, "/api/v1/search-source-connectors", "/api/v1/search-source-connectors"},
		// The notebooks, one notebook, its pin, and the report templates,
		// whose path begins with the same word and is not under a notebook.
		{http.MethodGet, "/api/v1/notebooks", "/api/v1/notebooks"},
		{http.MethodGet, "/api/v1/notebooks/3", "/api/v1/notebooks/{notebook_id}"},
		{http.MethodPut, "/api/v1/notebooks/3/feature", "/api/v1/notebooks/{notebook_id}/feature"},
		{http.MethodGet, "/api/v1/notebook-report-templates", "/api/v1/notebook-report-templates"},
		// The caller's public links beside a thread's, and the artifact
		// list beside one artifact's routes.
		{http.MethodGet, "/api/v1/snapshots", "/api/v1/snapshots"},
		{http.MethodDelete, "/api/v1/snapshots/7", "/api/v1/snapshots/{snapshot_id}"},
		{http.MethodGet, "/api/v1/artifacts", "/api/v1/artifacts"},
		{http.MethodGet, "/api/v1/artifacts/7/manifest", "/api/v1/artifacts/{artifact_id}/manifest"},
		{http.MethodGet, "/api/v1/artifacts/7/files/9/content", "/api/v1/artifacts/{artifact_id}/files/{file_id}/content"},
		{http.MethodGet, "/api/v1/notes", "/api/v1/notes"},
		// One path, two methods.
		{http.MethodGet, "/api/v1/podcasts/7", "/api/v1/podcasts/{podcast_id}"},
		{http.MethodDelete, "/api/v1/podcasts/7", "/api/v1/podcasts/{podcast_id}"},
	} {
		if got := ask(t, server, c.method, c.path, nil); got.standIn != c.template {
			t.Errorf("%s %s: ran %q (status %d), want %s", c.method, c.path, got.standIn, got.status, c.template)
		}
	}
}

// TestAnEscapedSlashInAParameterIsTwoSegments: `%2F` inside a parameter, for
// every served route and every parameter of it.
//
// The server in front decodes a path before anything splits it, so
// `a%2Fb` is two segments to every client and proxy that will ever send one,
// and Go's ServeMux alone would hand a route the value "a/b". The path is
// therefore read decoded: it runs whichever served route the *decoded* path
// names, and if it names none it is the router's 404 -- whether or not the
// caller has a credential, because a path that is nobody's is not a secret.
func TestAnEscapedSlashInAParameterIsTwoSegments(t *testing.T) {
	withCredential := assembled(t, servedRoutes(), nil)
	withoutCredential := assembled(t, servedRoutes(), refusingGate())
	reran, refused := 0, 0
	for _, pair := range servedRoutes() {
		method, template, _ := strings.Cut(pair, " ")
		_, names := servedPath(template, ordinary)
		for target := range names {
			path, _ := servedPath(template, func(index int, name string) string {
				if index == target {
					return "a%2Fb"
				}
				return ordinary(index, name)
			})
			decoded, err := url.PathUnescape(path)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			what := method + " " + path
			if other := routeFor(method, decoded); other != "" {
				if got := ask(t, withCredential, method, path, fromTheWebApp); got.standIn != other {
					t.Errorf("%s decodes to a path of %s, but %q ran (status %d)",
						what, other, got.standIn, got.status)
				}
				reran++
				continue
			}
			mustBeTheBare404(t, what+", with a credential", ask(t, withCredential, method, path, fromTheWebApp))
			mustBeTheBare404(t, what+", with none", ask(t, withoutCredential, method, path, fromTheWebApp))
			refused++
		}
	}
	if refused == 0 {
		t.Error("no path was refused, so the test proved nothing")
	}
	t.Logf("%d paths were the router's 404; %d decoded to another served route and ran it", refused, reran)

	// The other direction, by hand: an escaped slash *between* two segments
	// is just a slash, and the route it spells runs.
	got := ask(t, withCredential, http.MethodGet, "/api/v1/folders/7%2Fbreadcrumb", nil)
	if got.standIn != "/api/v1/folders/{folder_id}/breadcrumb" || got.handed["folder_id"] != "7" {
		t.Errorf("GET /api/v1/folders/7%%2Fbreadcrumb: ran %q with %q", got.standIn, got.handed)
	}
}

// TestAnEmptySegmentMatchesNothing: a parameter left empty, and a doubled
// slash anywhere else in a served route's path.
//
// ServeMux alone answers such a path with a 301 to the cleaned one, which
// tells a client that sent a broken address to try a different resource. A
// parameter is never empty and a literal is never empty, so the path names
// nothing, and the answer is the router's 404 with or without a credential.
//
// One shape is not that and is not asserted here: when the empty parameter
// is the path's last segment, the path merely ends in a slash, which is
// TestATrailingSlashRunsNoRoute's.
func TestAnEmptySegmentMatchesNothing(t *testing.T) {
	withCredential := assembled(t, servedRoutes(), nil)
	withoutCredential := assembled(t, servedRoutes(), refusingGate())
	asked := 0
	both := func(method, path string) {
		t.Helper()
		what := method + " " + path
		mustBeTheBare404(t, what+", with a credential", ask(t, withCredential, method, path, fromTheWebApp))
		mustBeTheBare404(t, what+", with none", ask(t, withoutCredential, method, path, fromTheWebApp))
		asked++
	}
	for _, pair := range servedRoutes() {
		method, template, _ := strings.Cut(pair, " ")
		_, names := servedPath(template, ordinary)
		for target := range names {
			path, _ := servedPath(template, func(index int, name string) string {
				if index == target {
					return ""
				}
				return ordinary(index, name)
			})
			if strings.HasSuffix(path, "/") {
				continue
			}
			both(method, path)
		}
		// A doubled slash before the last segment, which every route has.
		plain, _ := servedPath(template, ordinary)
		cut := strings.LastIndex(plain, "/")
		both(method, plain[:cut]+"/"+plain[cut:])
	}
	t.Logf("%d paths with an empty segment, each asked with and without a credential", asked)
}

// TestATrailingSlashRunsNoRoute: a served route's path with a slash after
// it, however the slash is spelled.
//
// No route of this tier ends in a slash (starlettepath.NewServed refuses
// one), so no route of this tier may run. What the answer *is* is left to
// what stands behind the door -- the Python tier redirects such a path to
// the one without the slash, and this tier does not decide that for it
// (internal/starlettepath.Guard, "What it deliberately does not settle") --
// so the one thing held here is that the answer is never a served route's.
func TestATrailingSlashRunsNoRoute(t *testing.T) {
	server := assembled(t, servedRoutes(), nil)
	for _, pair := range servedRoutes() {
		method, template, _ := strings.Cut(pair, " ")
		plain, names := servedPath(template, ordinary)
		spellings := []string{plain + "/", plain + "//"}
		if len(names) > 0 && strings.HasSuffix(template, "}") {
			// The last parameter left empty is the same thing.
			empty, _ := servedPath(template, func(index int, name string) string {
				if index == len(names)-1 {
					return ""
				}
				return ordinary(index, name)
			})
			spellings = append(spellings, empty)
		}
		for _, path := range spellings {
			if got := ask(t, server, method, path, nil); got.standIn != "" {
				t.Errorf("%s %s: the route %s ran", method, path, got.standIn)
			}
		}
	}
}

// TestAForgedWriteIsRefusedBeforeTheRouterSpeaks: the CSRF check stands
// outside the router, so a cookie-authenticated write from a foreign origin
// to a path no route matches is the forgery's 403 and not the path's 404. A
// 404 there would tell a hostile page which paths exist.
func TestAForgedWriteIsRefusedBeforeTheRouterSpeaks(t *testing.T) {
	server := assembled(t, servedRoutes(), refusingGate())
	forged := map[string]string{
		"Origin": "https://elsewhere.example.test",
		"Cookie": sessionCookie + "=anything",
	}
	got := ask(t, server, http.MethodPost, "/api/v1/prompts//copy", forged)
	if got.status != http.StatusForbidden || got.standIn != "" {
		t.Errorf("a forged write to a path with an empty segment answered %d %q", got.status, got.body)
	}
	// The same path from the app's own origin is the path's own answer.
	own := map[string]string{"Origin": webOrigin, "Cookie": sessionCookie + "=anything"}
	mustBeTheBare404(t, "the same write from the app's own origin",
		ask(t, server, http.MethodPost, "/api/v1/prompts//copy", own))
}

// TestThePathsTheRoutersAgreeOnAreNotTouched is the other half of the guard's
// scope: what it must leave alone. An ordinary request reaches whatever is
// behind the door with the very path it arrived with -- the probe, a served
// route, and a path no route matches -- which the stand-in gate here checks by
// writing down what it was handed.
func TestThePathsTheRoutersAgreeOnAreNotTouched(t *testing.T) {
	var handedOver []string
	forwarding := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handedOver = append(handedOver, request.Method+" "+request.URL.EscapedPath())
		response.WriteHeader(http.StatusNoContent)
	})
	server := assembled(t, servedRoutes(), forwarding)

	if got := ask(t, server, http.MethodGet, health.Path, nil); got.status != http.StatusOK {
		t.Errorf("the probe answered %d through the guard", got.status)
	}

	// A public route never reaches the gate at all -- frontDoor mounts it
	// beside the probe -- so there is nothing for the stand-in gate to have
	// been handed. Which routes those are is publicRoutes()'s to say, and
	// reading it here keeps this test from quietly skipping a route that
	// stopped being public.
	public := set(publicRoutes())
	for _, pair := range servedRoutes() {
		if _, ungated := public[pair]; ungated {
			continue
		}
		method, template, _ := strings.Cut(pair, " ")
		path, _ := servedPath(template, ordinary)
		handedOver = nil
		ask(t, server, method, path+"?page=2&q=a%2Fb", nil)
		if want := []string{method + " " + path}; !reflect.DeepEqual(handedOver, want) {
			t.Errorf("%s %s: the gate was handed %q", method, path, handedOver)
		}
	}

	// Paths that are odd but that both routers read alike, or that name no
	// route of this tier. Each must arrive exactly as sent.
	for _, path := range []string{
		"/api/v1/pats/a%20b",         // a space in a parameter
		"/api/v1/pats/%2E%2E",        // an escaped dot is an ordinary value to both
		"/api/v1/pats/a%FFb",         // not UTF-8
		"/api/v1/somewhere/else",     // no route of this tier
		"/api/v1/somewhere%2Felse/x", // an escaped slash, in a path that is nobody's here
	} {
		handedOver = nil
		ask(t, server, http.MethodDelete, path, nil)
		if want := []string{"DELETE " + path}; !reflect.DeepEqual(handedOver, want) {
			t.Errorf("DELETE %s: the gate was handed %q", path, handedOver)
		}
	}

	// And one the guard *does* re-escape, to show what the gate is then
	// handed: a different spelling of the same decoded path, which is all a
	// route's pattern is matched against.
	handedOver = nil
	ask(t, server, http.MethodGet, "/api/v1/folders/7%2Fbreadcrumb", nil)
	if want := []string{"GET /api/v1/folders/7/breadcrumb"}; !reflect.DeepEqual(handedOver, want) {
		t.Errorf("GET /api/v1/folders/7%%2Fbreadcrumb: the gate was handed %q, want %q", handedOver, want)
	}
}

// TestWhatTheGuardLeavesToServeMux pins the answers the guard does not
// settle, so that they are a decision someone can read and not a surprise.
// Each is a difference from the Python tier that is still there; the
// `python` column is what that tier was measured answering, kept as a note.
func TestWhatTheGuardLeavesToServeMux(t *testing.T) {
	server := assembled(t, servedRoutes(), nil)
	for _, c := range []struct {
		method, path, python string
		status               int
		location             string
	}{
		// A dot where no parameter is: ServeMux still redirects.
		{http.MethodGet, "/api/v1/./pats", "404", http.StatusMovedPermanently, "/api/v1/pats"},
		{http.MethodGet, "/api/v1/x/../pats", "404", http.StatusMovedPermanently, "/api/v1/pats"},
		// Only trailing slashes are surplus: ServeMux redirects to one slash.
		{http.MethodGet, "/api/v1/pats//", "307 to /api/v1/pats", http.StatusMovedPermanently, "/api/v1/pats/"},
		// An empty segment in a path that is no route of this tier.
		{http.MethodGet, "/api/v1//somewhere/else", "404", http.StatusMovedPermanently, "/api/v1/somewhere/else"},
		{http.MethodGet, "//healthz", "404", http.StatusMovedPermanently, "/healthz"},
		// An escaped slash that ends the decoded path is a trailing slash to
		// Starlette. Here the route still runs, handed "7/" (the stand-in's
		// 200); a real route then refuses the parameter in its own words.
		{http.MethodDelete, "/api/v1/pats/7%2F", "307 to /api/v1/pats/7", http.StatusOK, ""},
	} {
		got := ask(t, server, c.method, c.path, nil)
		if got.status != c.status || got.header.Get("Location") != c.location {
			t.Errorf("%s %s (Python: %s): answered %d to %q, and this test says it is still %d to %q",
				c.method, c.path, c.python, got.status, got.header.Get("Location"), c.status, c.location)
		}
	}
}

// TestAServedRouteMustBePlainSegments: the door does not open with a route
// the guard cannot reason about.
func TestAServedRouteMustBePlainSegments(t *testing.T) {
	_, err := frontDoor{Served: []string{"GET /api/v1/files/{path...}"}}.handler()
	if err == nil {
		t.Fatal("a served route with a wildcard parameter was accepted")
	}
}
