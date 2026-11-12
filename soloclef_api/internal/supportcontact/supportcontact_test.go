package supportcontact

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

func answer(t *testing.T, route Route) *httptest.ResponseRecorder {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	route.Register(api)
	recorder := httptest.NewRecorder()
	// No caller in the context: the route is for people who are not
	// signed in.
	api.Adapter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, Path, nil))
	return recorder
}

func TestThePublishedAddressIsAnsweredToAnyone(t *testing.T) {
	recorder := answer(t, Route{Email: "help@notes.example.test"})
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"email":"help@notes.example.test"}` {
		t.Errorf("answered %d %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Errorf("Cache-Control is %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type is %q", got)
	}
}

func TestADeploymentThatPublishesNoAddressSaysSo(t *testing.T) {
	recorder := answer(t, Route{})
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"email":""}` {
		t.Errorf("answered %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestOnlyOnePlainAddressIsASetting(t *testing.T) {
	for address, usable := range map[string]bool{
		"":                                    true,
		"help@notes.example.test":             true,
		"first.last+tag@sub.example.test":     true,
		"help@localhost":                      false,
		"Help Desk <help@example.test>":       false,
		"a@example.test,b@example.test":       false,
		"a@example.test?cc=b@example.test":    false,
		"a@example.test&bcc=b@example.test":   false,
		"help @example.test":                  false,
		strings.Repeat("a", 250) + "@ex.test": false,
	} {
		if err := (Route{Email: address}).Check(); (err == nil) != usable {
			t.Errorf("%q: %v, want usable %v", address, err, usable)
		}
	}
}
