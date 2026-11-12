package signinoptions

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

func TestTheAnswerOffersExactlyWhatTheSettingsServe(t *testing.T) {
	for _, c := range []struct {
		name     string
		settings Settings
		want     string
	}{
		{"password only, nothing else switched on", Settings{},
			`{"service":"soloclef","password":true,"forgot_password":false,"sign_up":false,"sso":null}`},
		{"mail and sign-up on", Settings{SMTPEnabled: true, SignupAllowed: true},
			`{"service":"soloclef","password":true,"forgot_password":true,"sign_up":true,"sso":null}`},
		{"single sign-on the app may use, in the server's words", Settings{OIDCEnabled: true, ButtonLabel: "Continue with Example SSO", DisplayName: "Example SSO", AppRedirects: []string{"soloclef-dev://auth", AppReturn}},
			`{"service":"soloclef","password":true,"forgot_password":false,"sign_up":false,"sso":{"label":"Continue with Example SSO","name":"Example SSO","app":true}}`},
		{"single sign-on with the website's own words, not for the app", Settings{OIDCEnabled: true, DisplayName: "Company login"},
			`{"service":"soloclef","password":true,"forgot_password":false,"sign_up":false,"sso":{"label":"Continue with single sign-on","name":"Company login","app":false}}`},
		{"the button's words are not read with single sign-on off", Settings{ButtonLabel: "Continue with X", AppRedirects: []string{AppReturn}},
			`{"service":"soloclef","password":true,"forgot_password":false,"sign_up":false,"sso":null}`},
	} {
		api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
		Route{Settings: c.settings}.Register(api)
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, Path, nil))
		var got, want any
		if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %v %s", c.name, err, recorder.Body)
		}
		_ = json.Unmarshal([]byte(c.want), &want)
		gotText, _ := json.Marshal(got)
		wantText, _ := json.Marshal(want)
		if recorder.Code != http.StatusOK || string(gotText) != string(wantText) {
			t.Errorf("%s: %d %s, want %s", c.name, recorder.Code, gotText, wantText)
		}
		if recorder.Header().Get("Cache-Control") != "public, max-age=60" {
			t.Errorf("%s: cached as %q", c.name, recorder.Header().Get("Cache-Control"))
		}
	}
}
