package microsoftmail

import (
	"regexp"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"
)

// loginHost is Microsoft's sign-in service. The two addresses a deployment
// uses are built from it and the tenant (AuthorizeURL, TokenURL); they are
// fields of Settings only so that a test can stand in for Microsoft.
const loginHost = "https://login.microsoftonline.com"

// AuthorizeURL is Microsoft's sign-in page for a tenant.
func AuthorizeURL(tenant string) string { return loginHost + "/" + tenant + "/oauth2/v2.0/authorize" }

// TokenURL is Microsoft's token endpoint for a tenant: the one address
// every tier refreshes a mailbox's token at (mailsignin).
func TokenURL(tenant string) string { return mailsignin.TokenURL(tenant) }

// Settings is what the deployment says about its Microsoft app
// registration. A value may be empty: Missing then names it, the
// availability route answers false and the connect route 503.
type Settings struct {
	ClientID     string
	ClientSecret string
	// Tenant is which accounts may sign in: "common", "organizations",
	// "consumers", a tenant id or a verified domain. It has no default.
	Tenant string
	// RedirectURI is the address registered at Microsoft. Its path is
	// CallbackPath.
	RedirectURI string
	// FrontendURL is the website, where the callback sends the person.
	FrontendURL string
	// SecretKey seals stored tokens and the attempt's cookie.
	SecretKey string
	// Microsoft's two addresses: AuthorizeURL(Tenant) and TokenURL(Tenant)
	// in a deployment.
	AuthorizeURL, TokenURL string
}

var tenantName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

// Missing is the names of the settings still to be set, never their
// values: what an operator reads to finish setting Microsoft mail up.
func (s Settings) Missing() []string {
	missing := []string{}
	if strings.TrimSpace(s.ClientID) == "" {
		missing = append(missing, "MICROSOFT_MAIL_CLIENT_ID")
	}
	if s.ClientSecret == "" {
		missing = append(missing, "MICROSOFT_MAIL_CLIENT_SECRET")
	}
	if !tenantName.MatchString(s.Tenant) {
		missing = append(missing, "MICROSOFT_MAIL_TENANT")
	}
	if s.RedirectURI == "" {
		missing = append(missing, "MICROSOFT_MAIL_REDIRECT_URI")
	}
	return missing
}

// Configured reports whether a mailbox can be connected and signed in to.
func (s Settings) Configured() bool { return len(s.Missing()) == 0 }
