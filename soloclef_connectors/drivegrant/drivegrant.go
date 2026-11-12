// Package drivegrant is a Google Drive grant as both tiers handle it: the
// request that renews one at Google, and the form it is kept in on a
// connection's row.
//
// The API tier makes the grant when a person connects Drive and renews it
// for the Google Picker (soloclef_api/internal/driveconnect). The worker
// renews it to read the picked files (soloclef_worker/internal/drivesync).
// Both must agree on which keys of the row's config hold what, and which
// of them are sealed, so that is written here once.
//
// # The stored form
//
// The keys are the ones Google's own Python library wrote for a credential
// (`token`, `refresh_token`, `expiry`, ...), because the first tier to
// store a grant was Python. The two tokens are sealed with the deployment's
// secret key (storedtoken) and `_token_encrypted` says so.
package drivegrant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Grant is what Google hands over for a consent or a renewal.
type Grant struct {
	AccessToken string
	// RefreshToken is empty in a renewal's reply: Google sends it once, at
	// the consent.
	RefreshToken string
	// Scopes is what the person actually granted; Google lets them untick.
	Scopes []string
	Expiry time.Time
}

// ErrInvalidGrant is Google refusing a stored refresh token: revoked by the
// person, or expired. The remedy is to connect again.
var ErrInvalidGrant = errors.New("drivegrant: Google no longer accepts this grant")

// Error is Google unreachable, or answering something unusable. Its text
// is a status or a sentence, never a token or a reply's body, so it can be
// logged.
type Error struct{ Reason string }

func (e *Error) Error() string { return "drivegrant: " + e.Reason }

// Client asks Google's token endpoint on behalf of one Google project.
type Client struct {
	ClientID     string
	ClientSecret string
	// TokenURL is Google's token endpoint. A deployment names Google's
	// own; a test names a stand-in.
	TokenURL string
	// HTTP makes the request. The caller bounds it and refuses redirects:
	// a reply must not be able to send a secret anywhere else.
	HTTP *http.Client
}

// Token posts a form to the token endpoint, with the project's own
// credentials added, and reads the grant in the reply.
func (c Client) Token(ctx context.Context, form url.Values) (Grant, error) {
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Grant{}, &Error{"the token request could not be built"}
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.HTTP.Do(request)
	if err != nil {
		// The error's text can repeat the request.
		return Grant{}, &Error{"Google could not be reached"}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Grant{}, &Error{"Google's reply could not be read"}
	}
	var reply struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(raw, &reply)
	if response.StatusCode != http.StatusOK {
		if reply.Error == "invalid_grant" {
			return Grant{}, ErrInvalidGrant
		}
		return Grant{}, &Error{"Google answered " + response.Status + " to the token request"}
	}
	if reply.AccessToken == "" {
		return Grant{}, &Error{"Google's token reply held no access token"}
	}
	return Grant{AccessToken: reply.AccessToken, RefreshToken: reply.RefreshToken,
		Scopes: strings.Fields(reply.Scope), Expiry: time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)}, nil
}

// Refresh gets a fresh access token for a stored refresh token.
func (c Client) Refresh(ctx context.Context, refreshToken string) (Grant, error) {
	return c.Token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}
