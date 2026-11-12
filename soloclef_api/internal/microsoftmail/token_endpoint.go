package microsoftmail

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"
)

// A grant, the token endpoint and its two kinds of failure are
// soloclef_assistant/mailsignin's, which refreshes a stored mailbox's
// token with the same request from every tier.
type grant = mailsignin.Grant

// microsoft is the requests this package makes to Microsoft.
type microsoft struct {
	settings Settings
	endpoint *mailsignin.Endpoint
}

func newMicrosoft(settings Settings, transport http.RoundTripper) *microsoft {
	return &microsoft{settings: settings,
		endpoint: mailsignin.NewEndpoint(settings.ClientID, settings.ClientSecret, settings.TokenURL, transport)}
}

// consentURL is Microsoft's sign-in page for one attempt.
// `prompt=select_account` always shows Microsoft's account picker, so a
// mailbox is never linked without the person choosing it there, and
// someone signed in to several Microsoft accounts picks the right one.
// loginHint preselects the mailbox being reconnected.
func (m *microsoft) consentURL(a attempt, loginHint string) string {
	query := url.Values{
		"client_id":             {m.settings.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {m.settings.RedirectURI},
		"response_mode":         {"query"},
		"scope":                 {scopes},
		"state":                 {a.State},
		"code_challenge":        {a.challenge()},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	if loginHint != "" {
		query.Set("login_hint", loginHint)
	}
	return m.settings.AuthorizeURL + "?" + query.Encode()
}

// redeem trades the callback's one-time code for a grant.
func (m *microsoft) redeem(ctx context.Context, code, verifier string, now time.Time) (grant, error) {
	return m.endpoint.Token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {m.settings.RedirectURI},
	}, now)
}

// revoked reports whether err is Microsoft refusing the grant itself, and
// with which word.
func revoked(err error) (string, bool) { return mailsignin.Revoked(err) }
