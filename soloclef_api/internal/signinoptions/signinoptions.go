// Package signinoptions answers, to anyone, how people can sign in to this
// server:
//
//	GET /api/v1/sign-in-options
//	-> {"service": "soloclef", "password": true, "forgot_password": …,
//	    "sign_up": …, "sso": {"label", "name", "app"} | null}
//
// The phone app reads it before anyone has signed in, for two things. It is
// how the app knows an address someone typed is a Soloclef server at all
// (`service`, a fixed word whatever the operator calls the site), and it is
// how the sign-in screen offers exactly the ways this server takes, in this
// server's words. Nothing in it is secret: no client id, no provider
// address, nothing about any account.
//
// Every field is read from a setting the routes themselves obey, so the
// screen can never offer a way the server then refuses:
//
//   - password: always. This tier always serves the password sign-ins
//     (internal/signin).
//   - forgot_password: the mail switch (SMTP_ENABLED). Without mail the
//     reset is refused in so many words, so the link is not offered.
//   - sign_up: SIGNUP_ALLOWED, the switch the sign-up form obeys.
//   - sso: single sign-on when OIDC_ENABLED is on. `label` is the button's
//     words (OIDC_BUTTON_LABEL, the website's own setting, so the two never
//     differ), `name` the provider as a sentence names it
//     (OIDC_DISPLAY_NAME), and `app` whether the phone app's own return
//     address is one this server sends a sign-in back to
//     (OIDC_APP_REDIRECTS; internal/oidcsignin's app routes refuse any
//     other).
package signinoptions

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
)

// Path is the route.
const Path = "/api/v1/sign-in-options"

// Public is the route: nobody is signed in when it is asked.
func Public() []string { return []string{http.MethodGet + " " + Path} }

// Served is every route of the package.
func Served() []string { return Public() }

// Service is the fixed word that says "this is a Soloclef server".
const Service = "soloclef"

// AppReturn is the phone app's return address, the one its sign-in sheet
// waits for (the app's features/auth/sso.ts, APP_SCHEME_RETURN).
const AppReturn = "soloclef://auth"

// websiteButtonWords is what the website's button says when
// OIDC_BUTTON_LABEL is empty (soloclef_web's runtime config): the app is
// given the same words, so the two screens read the same.
const websiteButtonWords = "Continue with single sign-on"

// Settings is what the answer is made of: the deployment's own switches.
type Settings struct {
	SMTPEnabled   bool
	SignupAllowed bool
	OIDCEnabled   bool
	// ButtonLabel, DisplayName and AppRedirects are read only with
	// OIDCEnabled on (internal/config).
	ButtonLabel  string
	DisplayName  string
	AppRedirects []string
}

type sso struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	App   bool   `json:"app"`
}

// Options is the answer.
type Options struct {
	Service        string `json:"service"`
	Password       bool   `json:"password"`
	ForgotPassword bool   `json:"forgot_password"`
	SignUp         bool   `json:"sign_up"`
	SSO            *sso   `json:"sso"`
}

// Build is the answer for these settings. It is the same for every request,
// so the route builds it once.
func Build(settings Settings) Options {
	options := Options{
		Service:        Service,
		Password:       true,
		ForgotPassword: settings.SMTPEnabled,
		SignUp:         settings.SignupAllowed,
	}
	if settings.OIDCEnabled {
		label := settings.ButtonLabel
		if label == "" {
			label = websiteButtonWords
		}
		options.SSO = &sso{Label: label, Name: settings.DisplayName, App: slices.Contains(settings.AppRedirects, AppReturn)}
	}
	return options
}

// Route serves the answer.
type Route struct {
	Settings Settings
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	// Rendered once: the settings are read at start and never change while
	// the process runs.
	body, err := json.Marshal(Build(r.Settings))
	if err != nil {
		// Unreachable: the answer is strings and booleans.
		panic("signinoptions: " + err.Error())
	}
	huma.Register(api, huma.Operation{
		OperationID: "sign-in-options", Method: http.MethodGet, Path: Path,
		Summary: "How people can sign in to this server", Tags: []string{"sign-in"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response := humawriter.New(ctx)
			response.Header().Set("Content-Type", "application/json")
			// The same for everyone, and it changes only when the
			// deployment's settings do: a proxy or the phone may keep it a
			// minute.
			response.Header().Set("Cache-Control", "public, max-age=60")
			response.WriteHeader(http.StatusOK)
			_, _ = response.Write(body)
		}}, nil
	})
}
