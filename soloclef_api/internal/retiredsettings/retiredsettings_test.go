package retiredsettings

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

func ask(t *testing.T, method, path string, signedIn bool) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(method, path, nil)
	if signedIn {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

// The operator's list is empty in the shape the installed page parses, so
// the page loads around it.
func TestTheOperatorConnectionsAreEmpty(t *testing.T) {
	if status, body := ask(t, http.MethodGet, OperatorConnectionsPath, true); status != http.StatusOK || body != `[]` {
		t.Errorf("%d %s", status, body)
	}
}

// Nothing else of the removed features is answered: the routes the older
// bundles alone called (community prompts, announcements, agent sign-in)
// went with them.
func TestOnlyTheOneRouteIsAnswered(t *testing.T) {
	for _, pair := range [][2]string{
		{http.MethodPost, OperatorConnectionsPath},
		{http.MethodGet, "/api/v1/prompts/public"},
		{http.MethodGet, "/api/v1/admin/announcements"},
		{http.MethodGet, "/api/v1/agent-sign-in"},
	} {
		if status, _ := ask(t, pair[0], pair[1], true); status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d", pair[0], pair[1], status)
		}
	}
}

// The gate names the caller; a route reached without one is a wiring
// mistake and answers 500, never the list.
func TestARouteWithoutACallerIsAFault(t *testing.T) {
	if status, body := ask(t, http.MethodGet, OperatorConnectionsPath, false); status != http.StatusInternalServerError || strings.Contains(body, "[]") {
		t.Errorf("%d %s", status, body)
	}
}
