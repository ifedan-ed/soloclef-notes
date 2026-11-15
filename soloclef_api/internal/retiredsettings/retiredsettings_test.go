package retiredsettings

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

// Each removed list is empty in the shape the installed pages parse, so the
// page loads around it.
func TestTheRemovedListsAreEmpty(t *testing.T) {
	for path, want := range map[string]string{
		OperatorConnectionsPath: `[]`,
		CommunityPromptsPath:    `[]`,
		AnnouncementsPath:       `{"announcements":[]}`,
		// Agent sign-in is off for good: nothing to paste, so the installed
		// Connect your agent screen shows the API key steps alone.
		AgentSignInPath: `{"enabled":false,"client_id":null,"client_secret":null}`,
	} {
		if status, body := ask(t, http.MethodGet, path, "", true); status != http.StatusOK || body != want {
			t.Errorf("%s: %d %s", path, status, body)
		}
	}
}

// A new announcement is refused in the error envelope, whatever it says.
func TestSendingAnAnnouncementIsRefused(t *testing.T) {
	status, body := ask(t, http.MethodPost, AnnouncementsPath, `{"title":"Hello","body":"World","reach":"everyone"}`, true)
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if status != http.StatusForbidden || json.Unmarshal([]byte(body), &envelope) != nil || envelope.Error.Message != NotSent {
		t.Errorf("%d %s", status, body)
	}
}

// Nothing else of the removed features is answered.
func TestOnlyTheFiveRoutesAreAnswered(t *testing.T) {
	for _, pair := range [][2]string{
		{http.MethodPost, "/api/v1/admin/announcements/5/withdraw"},
		{http.MethodPost, "/api/v1/prompts/5/copy"},
		{http.MethodPost, OperatorConnectionsPath},
		{http.MethodPost, AgentSignInPath},
	} {
		if status, _ := ask(t, pair[0], pair[1], "", true); status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s answered %d", pair[0], pair[1], status)
		}
	}
}

// The gate names the caller; a route reached without one is a wiring
// mistake and answers 500, never the list.
func TestARouteWithoutACallerIsAFault(t *testing.T) {
	if status, body := ask(t, http.MethodGet, CommunityPromptsPath, "", false); status != http.StatusInternalServerError || strings.Contains(body, "[]") {
		t.Errorf("%d %s", status, body)
	}
}
