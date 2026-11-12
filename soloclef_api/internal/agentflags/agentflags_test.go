package agentflags

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

func ask(route Route, signedIn bool) (int, string) {
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Errors, route.Logger = errorenvelope.Writer{}, slog.New(slog.NewTextHandler(io.Discard, nil))
	route.Register(api)
	request := httptest.NewRequest(http.MethodGet, Path, nil)
	if signedIn {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", Method: principal.MethodPersonalAccessToken}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

func TestTheFlagsSayWhatThisTierDoes(t *testing.T) {
	for _, c := range []struct {
		name  string
		route Route
		want  string
	}{
		{"permission rules on", Route{PermissionEnabled: true},
			`{"disable_new_agent_stack":false,"enable_permission":true,"enable_action_log":true,"enable_revert_route":true,"enable_desktop_local_filesystem":false}`},
		{"the kill-switch and the rules are answered as set", Route{Disabled: true, PermissionEnabled: true},
			`{"disable_new_agent_stack":true,"enable_permission":true,"enable_action_log":true,"enable_revert_route":true,"enable_desktop_local_filesystem":false}`},
		{"nothing on", Route{},
			`{"disable_new_agent_stack":false,"enable_permission":false,"enable_action_log":true,"enable_revert_route":true,"enable_desktop_local_filesystem":false}`},
	} {
		if status, body := ask(c.route, true); status != 200 || body != c.want {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
	}
}

// The undo is promised whatever the deployment's settings say, because the
// log and its undo routes are always served: no setting can hide one that
// works.
func TestTheUndoIsAlwaysOffered(t *testing.T) {
	for _, route := range []Route{{}, {Disabled: true}, {PermissionEnabled: true}} {
		_, body := ask(route, true)
		if !strings.Contains(body, `"enable_action_log":true`) || !strings.Contains(body, `"enable_revert_route":true`) {
			t.Errorf("%+v: the undo is not offered: %s", route, body)
		}
	}
}

func TestNobodyIsAnsweredWithoutACaller(t *testing.T) {
	if status, body := ask(Route{PermissionEnabled: true}, false); status != 500 || strings.Contains(body, "enable_") {
		t.Errorf("%d %s", status, body)
	}
}
