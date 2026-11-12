package gatedanalytics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

type recordingSink struct{ events []Event }

func (s *recordingSink) Record(event Event) { s.events = append(s.events, event) }

// Only a caller this tier resolved and found gated is recorded, which is
// AuthContext.is_gated: an API key or an agent, never a session.
func TestOnlyAGatedCallerIsRecorded(t *testing.T) {
	cases := map[string]struct {
		caller   *principal.Principal
		recorded bool
		method   string
	}{
		"an API key":             {&principal.Principal{UserID: "a", Method: principal.MethodPersonalAccessToken}, true, "pat"},
		"an agent":               {&principal.Principal{UserID: "a", Method: principal.MethodAgent}, true, "agent"},
		"an interactive session": {&principal.Principal{UserID: "a", Method: principal.MethodSession}, false, ""},
		"nobody at all":          {nil, false, ""},
	}
	for name, test := range cases {
		sink := &recordingSink{}
		handler := Middleware{
			Sink:     sink,
			Template: RouteTemplates("GET /api/v1/notebooks/{notebook_id}/notes"),
		}.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusOK)
		}))

		request := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks/1/notes", nil)
		if test.caller != nil {
			request = request.WithContext(principal.WithPrincipal(context.Background(), *test.caller))
		}
		handler.ServeHTTP(httptest.NewRecorder(), request)

		if recorded := len(sink.events) == 1; recorded != test.recorded {
			t.Errorf("%s: recorded=%v, want %v", name, recorded, test.recorded)
			continue
		}
		if test.recorded && sink.events[0].AuthMethod != test.method {
			t.Errorf("%s: auth_method %q, want %q", name, sink.events[0].AuthMethod, test.method)
		}
	}
}

// The route property is the template, so it stays low-cardinality however many
// notebooks there are.
func TestTheRouteRecordedIsTheTemplate(t *testing.T) {
	templates := RouteTemplates(
		"GET /api/v1/notebooks/{notebook_id}/notes",
		"GET /api/v1/artifacts/{artifact_id}/files")

	for path, want := range map[string]string{
		"/api/v1/notebooks/1/notes":   "/api/v1/notebooks/{notebook_id}/notes",
		"/api/v1/notebooks/987/notes": "/api/v1/notebooks/{notebook_id}/notes",
		"/api/v1/artifacts/5/files":   "/api/v1/artifacts/{artifact_id}/files",
		// A path no route of this tier answers keeps its own path, as the
		// Python middleware's `or request.url.path` fallback does.
		"/api/v1/documents": "/api/v1/documents",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if got := templates(request); got != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
}

func TestTheStatusAndTheClientAreRecorded(t *testing.T) {
	sink := &recordingSink{}
	handler := Middleware{
		Sink:     sink,
		Template: RouteTemplates("GET /api/v1/notebooks/{notebook_id}/notes"),
	}.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))

	caller := principal.Principal{UserID: "account", Method: principal.MethodPersonalAccessToken}
	for header, want := range map[string]string{"mcp": "mcp", "": "pat_script", "something-else": "pat_script"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks/1/notes", nil)
		if header != "" {
			request.Header.Set(mcpClientHeader, header)
		}
		request = request.WithContext(principal.WithPrincipal(context.Background(), caller))
		handler.ServeHTTP(httptest.NewRecorder(), request)

		event := sink.events[len(sink.events)-1]
		if event.Client != want {
			t.Errorf("%q: client %q, want %q", header, event.Client, want)
		}
		if event.Status != http.StatusForbidden {
			t.Errorf("%q: status %d, want 403", header, event.Status)
		}
	}
}

// connection stands in for net/http's own writer: it can take a reader, and
// remembers which.
type connection struct {
	*httptest.ResponseRecorder
	handed io.Reader
}

func (c *connection) ReadFrom(source io.Reader) (int64, error) {
	c.handed = source
	return io.Copy(c.ResponseRecorder, source)
}

// A route that streams a file copies it into the response. The copy must
// reach the writer underneath as the reader it was -- that is what lets
// net/http send a file from the kernel -- and the event must still carry the
// status the route answered with.
func TestACopiedBodyIsHandedDownAndItsStatusStillRecorded(t *testing.T) {
	caller := principal.Principal{UserID: "account", Method: principal.MethodPersonalAccessToken}
	// What io.Copy hands a destination with ReadFrom: a reader and nothing
	// else.
	source := &struct{ io.Reader }{}

	for name, test := range map[string]struct {
		answer func(http.ResponseWriter)
		status int
	}{
		"a partial answer": {func(response http.ResponseWriter) {
			response.WriteHeader(http.StatusPartialContent)
			_, _ = io.Copy(response, source)
		}, http.StatusPartialContent},
		// A body with no WriteHeader is a 200, and a status announced after
		// the body has started changes nothing on the wire or in the event.
		"a body and then a late status": {func(response http.ResponseWriter) {
			_, _ = io.Copy(response, source)
			response.WriteHeader(http.StatusInternalServerError)
		}, http.StatusOK},
	} {
		source.Reader = strings.NewReader("the body")
		sink := &recordingSink{}
		bottom := &connection{ResponseRecorder: httptest.NewRecorder()}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks/1/notes", nil)
		request = request.WithContext(principal.WithPrincipal(context.Background(), caller))
		Middleware{Sink: sink, Template: RouteTemplates()}.Wrap(
			http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				test.answer(response)
			})).ServeHTTP(bottom, request)

		if bottom.handed != io.Reader(source) {
			t.Errorf("%s: the writer underneath was handed %T, not the reader itself", name, bottom.handed)
		}
		if bottom.Body.String() != "the body" {
			t.Errorf("%s: body %q", name, bottom.Body.String())
		}
		if len(sink.events) != 1 || sink.events[0].Status != test.status {
			t.Errorf("%s: recorded %+v, want status %d", name, sink.events, test.status)
		}
	}
}

// A writer underneath that cannot take a reader still gets the bytes.
func TestACopiedBodyReachesAWriterThatCannotTakeAReader(t *testing.T) {
	recorder := httptest.NewRecorder()
	Middleware{Sink: &recordingSink{}, Template: RouteTemplates()}.Wrap(
		http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			if _, err := response.(io.ReaderFrom).ReadFrom(strings.NewReader("the body")); err != nil {
				t.Error(err)
			}
		})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil))
	if recorder.Body.String() != "the body" {
		t.Errorf("body %q", recorder.Body.String())
	}
}
