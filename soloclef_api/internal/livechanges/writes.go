package livechanges

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// Writes publishes, after every write request that succeeded, the changes
// that route's write makes visible: a note saved on the phone appears in the
// open web tab, a notebook renamed in one tab is renamed in the other.
//
// # Why one middleware and a table, not a call in every route
//
// The API has nearly two hundred write routes. A publish written into each
// one would be two hundred places to forget it, and the next route added
// would be the two hundred and first. Here every write route is named in one
// table (Rules), cmd/api's tests fail when a served write route matches no
// rule, and a route that changes nothing anyone watches says so explicitly.
// It runs after the handler returned, so after its transaction committed:
// a client told now reads the new row.
//
// Background work is not a request and is not seen here: the Studio runner,
// research imports, the chat's turn flag, the worker and the indexer
// publish their own changes where they write.
type Writes struct {
	Publisher livechange.Publisher
	// Routes is every served "METHOD /path" pattern (cmd/api's
	// servedRoutes), to find which one a request matched.
	Routes []string
	Rules  []Rule
}

// Rule says what the routes under one path prefix change. The longest
// matching prefix wins, so a narrower rule overrides a broader one (the
// Studio routes under a notebook, the test routes under a connector).
//
// An empty Changes is "nothing a client watches changes": a sign-in, a
// search, a setting no other screen shows live. It must be said, so a route
// is never left out by accident.
type Rule struct {
	// Prefix is a route template's path prefix, as servedRoutes spells it
	// ("/api/v1/notebooks/{notebook_id}/studio"). It matches at a segment
	// boundary only.
	Prefix string
	// Method narrows the rule to one method; "" is every write method.
	Method  string
	Changes []Template
}

// Template is one change a route owes; the ids come from the request's own
// path, never from its body (which a client wrote).
type Template struct {
	Kind livechange.Kind
	// IDFrom and NotebookIDFrom name a path parameter of the matched
	// template, or are "" when the change names none.
	IDFrom         string
	NotebookIDFrom string
}

// Wrap publishes after next has answered a write with a 2xx.
func (w Writes) Wrap(next http.Handler) http.Handler {
	match := matcher(w.Routes)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !isWrite(request.Method) {
			// Reads are most requests and every stream: they pass untouched,
			// with no wrapper in the way of a sendfile or a flush.
			next.ServeHTTP(response, request)
			return
		}
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		if recorder.status < 200 || recorder.status > 299 {
			return
		}
		caller, ok := principal.FromContext(request.Context())
		if !ok || caller.UserID == "" {
			return
		}
		pattern, value := match(request)
		if pattern == "" {
			return
		}
		rule, found := Classify(w.Rules, pattern)
		if !found || len(rule.Changes) == 0 {
			return
		}
		changes := make([]livechange.Change, 0, len(rule.Changes))
		for _, template := range rule.Changes {
			changes = append(changes, livechange.Change{
				Kind:       template.Kind,
				ID:         number(value, template.IDFrom),
				NotebookID: number(value, template.NotebookIDFrom),
			})
		}
		w.Publisher.Publish(request.Context(), caller.UserID, changes...)
	})
}

// Classify finds the rule for a served "METHOD /path" pattern: the longest
// prefix that matches, preferring one that names the method.
func Classify(rules []Rule, pattern string) (Rule, bool) {
	method, path, _ := strings.Cut(pattern, " ")
	best, bestLength, found := Rule{}, -1, false
	for _, rule := range rules {
		if rule.Method != "" && rule.Method != method {
			continue
		}
		if !underPrefix(path, rule.Prefix) {
			continue
		}
		length := len(rule.Prefix) * 2
		if rule.Method != "" {
			length++
		}
		if length > bestLength {
			best, bestLength, found = rule, length, true
		}
	}
	return best, found
}

func underPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// number reads a path parameter as an id; anything that is not a positive
// integer (a share token, a provider name) names nothing.
func number(value func(string) string, name string) int64 {
	if name == "" {
		return 0
	}
	n, err := strconv.ParseInt(value(name), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// matcher answers which served pattern a request matched and its path
// values, by asking a mux of the same patterns (the one net/http router
// that understands them).
func matcher(routes []string) func(*http.Request) (string, func(string) string) {
	type found struct {
		pattern string
		value   func(string) string
	}
	mux := http.NewServeMux()
	for _, route := range routes {
		mux.HandleFunc(route, func(_ http.ResponseWriter, r *http.Request) {
			if into, ok := r.Context().Value(matchKey{}).(*found); ok {
				into.pattern, into.value = route, r.PathValue
			}
		})
	}
	return func(request *http.Request) (string, func(string) string) {
		into := &found{}
		// A shallow copy: the mux sets the pattern and the path values on
		// the request it is given, which must not be the route's own.
		copied := request.WithContext(contextWith(request, into))
		mux.ServeHTTP(discard{}, copied)
		return into.pattern, into.value
	}
}

// statusRecorder keeps the status the route answered.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.written {
		r.status, r.written = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	r.written = true
	return r.ResponseWriter.Write(body)
}

// Unwrap keeps the writer underneath reachable: the chat's turn is a write
// that streams, and flushes and moves its deadline through it.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

type matchKey struct{}

func contextWith(request *http.Request, into any) context.Context {
	return context.WithValue(request.Context(), matchKey{}, into)
}

// discard is where the matching mux's own answer (a 404 for a path no
// pattern serves) goes: nowhere.
type discard struct{}

func (discard) Header() http.Header         { return http.Header{} }
func (discard) Write(b []byte) (int, error) { return len(b), nil }
func (discard) WriteHeader(int)             {}
