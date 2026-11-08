package gatedanalytics

import (
	"context"
	"net/http"
	"net/http/httptest"
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
			Template: RouteTemplates("GET /api/v1/workspaces/{workspace_id}/members"),
		}.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusOK)
		}))

		request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
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
// workspaces there are.
func TestTheRouteRecordedIsTheTemplate(t *testing.T) {
	templates := RouteTemplates(
		"GET /api/v1/workspaces/{workspace_id}/members",
		"GET /api/v1/logs/workspaces/{workspace_id}/summary")

	for path, want := range map[string]string{
		"/api/v1/workspaces/1/members":      "/api/v1/workspaces/{workspace_id}/members",
		"/api/v1/workspaces/987/members":    "/api/v1/workspaces/{workspace_id}/members",
		"/api/v1/logs/workspaces/5/summary": "/api/v1/logs/workspaces/{workspace_id}/summary",
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
		Template: RouteTemplates("GET /api/v1/workspaces/{workspace_id}/members"),
	}.Wrap(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusForbidden)
	}))

	caller := principal.Principal{UserID: "account", Method: principal.MethodPersonalAccessToken}
	for header, want := range map[string]string{"mcp": "mcp", "": "pat_script", "something-else": "pat_script"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
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
