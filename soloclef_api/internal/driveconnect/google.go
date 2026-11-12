package driveconnect

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
)

// grant is what Google hands over for a consent or a renewal, and the two
// ways asking for one fails: both are soloclef_connectors/drivegrant's,
// because the worker that reads the picked files renews the same grant.
type grant = drivegrant.Grant

var errInvalidGrant = drivegrant.ErrInvalidGrant

// googleTimeout bounds one request to Google.
const googleTimeout = 15 * time.Second

// google is the handful of requests this package makes to Google.
type google struct {
	settings Settings
	http     *http.Client
}

func newGoogle(settings Settings, transport http.RoundTripper) *google {
	return &google{settings: settings, http: &http.Client{
		Transport: transport,
		Timeout:   googleTimeout,
		// A reply cannot send a code, a secret or a token anywhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// consentURL is Google's consent page for one attempt. `prompt=consent`
// and `access_type=offline` together are what make Google return a refresh
// token every time, not only the first.
func (g *google) consentURL(a attempt) string {
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {g.settings.ClientID},
		"redirect_uri":          {g.settings.RedirectURI},
		"scope":                 {driveFileScope},
		"state":                 {a.State},
		"code_challenge":        {a.challenge()},
		"code_challenge_method": {"S256"},
		"access_type":           {"offline"},
		"prompt":                {"consent"},
	}
	separator := "?"
	if strings.Contains(g.settings.AuthURL, "?") {
		separator = "&"
	}
	return g.settings.AuthURL + separator + query.Encode()
}

// token posts a form to Google's token endpoint as this deployment's
// Google project.
func (g *google) token(ctx context.Context, form url.Values) (grant, error) {
	return drivegrant.Client{
		ClientID: g.settings.ClientID, ClientSecret: g.settings.ClientSecret,
		TokenURL: g.settings.TokenURL, HTTP: g.http,
	}.Token(ctx, form)
}

// exchange trades the callback's code for a grant.
func (g *google) exchange(ctx context.Context, code, verifier string) (grant, error) {
	return g.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {g.settings.RedirectURI},
	})
}

// refresh gets a fresh access token for a stored refresh token.
func (g *google) refresh(ctx context.Context, refreshToken string) (grant, error) {
	return g.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}

// accountEmail asks Drive which Google account a grant belongs to. The
// `drive.file` scope may read this, so no profile scope is asked for. ""
// when Drive does not say; a connection is still made, without the label.
func (g *google) accountEmail(ctx context.Context, accessToken string) string {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		g.settings.AboutURL+"?fields="+url.QueryEscape("user(emailAddress,displayName)"), nil)
	if err != nil {
		return ""
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := g.http.Do(request)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	var about struct {
		User struct {
			Email string `json:"emailAddress"`
		} `json:"user"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&about) != nil {
		return ""
	}
	return about.User.Email
}

// revoke ends a grant at Google and reports whether it is gone. 400 is
// "invalid_token": already revoked or expired, which is the goal.
func (g *google) revoke(ctx context.Context, token string) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.settings.RevokeURL,
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := g.http.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return response.StatusCode == http.StatusOK || response.StatusCode == http.StatusBadRequest
}
