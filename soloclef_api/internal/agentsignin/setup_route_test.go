package agentsignin

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

// askSetup asks the route as a caller signed in by `how`; an empty method is
// a request the gate never saw.
func askSetup(t *testing.T, route SetupRoute, how principal.Method) *httptest.ResponseRecorder {
	t.Helper()
	route.Errors = errorenvelope.Writer{}
	route.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Register(api)
	request := httptest.NewRequest(http.MethodGet, SetupPath, nil)
	if how != "" {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: "11111111-1111-4111-8111-111111111111", Method: how}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

var agentProvider = Settings{Issuer: "https://sso.example.test/application/o/agents/", ClientID: "agents-client"}

func TestASessionIsShownTheClientAndItsSecret(t *testing.T) {
	recorder := askSetup(t, SetupRoute{Settings: agentProvider, ClientSecret: "s3cret"}, principal.MethodSession)
	want := `{"enabled":true,"client_id":"agents-client","client_secret":"s3cret"}`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Errorf("answered %d %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q: the answer holds a secret and must not be kept", got)
	}
}

func TestAPublicClientHasNoSecretToShow(t *testing.T) {
	recorder := askSetup(t, SetupRoute{Settings: agentProvider}, principal.MethodSession)
	want := `{"enabled":true,"client_id":"agents-client","client_secret":null}`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Errorf("answered %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestADeploymentWithNoAgentProviderSaysSoAndShowsNothing(t *testing.T) {
	// A secret left set beside no provider is not shown either.
	recorder := askSetup(t, SetupRoute{ClientSecret: "left over"}, principal.MethodSession)
	want := `{"enabled":false,"client_id":null,"client_secret":null}`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Errorf("answered %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestOnlyABrowserSessionIsShownTheSecret(t *testing.T) {
	for _, how := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
		recorder := askSetup(t, SetupRoute{Settings: agentProvider, ClientSecret: "s3cret"}, how)
		if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), "s3cret") {
			t.Errorf("%s: answered %d %s", how, recorder.Code, recorder.Body.String())
		}
	}
	// No caller at all is a wiring fault, answered as one and with nothing.
	recorder := askSetup(t, SetupRoute{Settings: agentProvider, ClientSecret: "s3cret"}, "")
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "s3cret") {
		t.Errorf("no caller: answered %d %s", recorder.Code, recorder.Body.String())
	}
}
