package retiredshare

import (
	"encoding/json"
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

// ask sends one request to the three routes, signed in or not.
func ask(t *testing.T, method, path, body string, signedIn bool) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if signedIn {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

// Settings → Public links on the installed builds reads `links` from this
// answer beside the output links; an empty list lets the page load, so the
// output links can still be seen and revoked.
func TestTheNotebookLinkListIsEmpty(t *testing.T) {
	status, body := ask(t, http.MethodGet, "/api/v1/notebook-links", "", true)
	if status != http.StatusOK || body != `{"links":[]}` {
		t.Fatalf("%d %s", status, body)
	}
}

// Sharing a notebook or a chat is refused with a 403 in the error envelope,
// whatever the notebook or chat: the builds say "This can't be shared
// publicly." for a 403, and "try again" for anything else. The body the
// builds send ({"expires_in_days": …}) is accepted and not read.
func TestSharingANotebookOrAChatIsRefused(t *testing.T) {
	for _, path := range []string{"/api/v1/notebooks/12/public-link", "/api/v1/threads/34/snapshots", "/api/v1/notebooks/999999/public-link"} {
		status, body := ask(t, http.MethodPost, path, `{"expires_in_days": 7}`, true)
		if status != http.StatusForbidden {
			t.Errorf("%s: %d %s", path, status, body)
			continue
		}
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil || envelope.Error.Message != NotShareable || envelope.Error.Code == "" {
			t.Errorf("%s: not the error envelope: %s", path, body)
		}
	}
}

// Nothing else of the old sharing is answered: the notebook link's read and
// revoke, and a notebook link's delete, stay unknown routes.
func TestOnlyTheThreeRoutesAreAnswered(t *testing.T) {
	for _, pair := range [][2]string{
		{http.MethodGet, "/api/v1/notebooks/12/public-link"},
		{http.MethodDelete, "/api/v1/notebooks/12/public-link"},
		{http.MethodDelete, "/api/v1/notebook-links/5"},
	} {
		if status, _ := ask(t, pair[0], pair[1], "", true); status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d", pair[0], pair[1], status)
		}
	}
	if got := len(Served()); got != 3 {
		t.Errorf("%d routes served", got)
	}
}

// The gate names the caller; a route reached without one is a wiring
// mistake and answers 500, never the list or the refusal.
func TestARouteWithoutACallerIsAFault(t *testing.T) {
	if status, body := ask(t, http.MethodGet, "/api/v1/notebook-links", "", false); status != http.StatusInternalServerError || strings.Contains(body, "links") {
		t.Errorf("%d %s", status, body)
	}
}
