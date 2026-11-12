// Package connectionrevocation ends a connection's credential at its
// provider when the connection is removed, so that a removed connection is
// dead there too and not only forgotten here.
//
// Two kinds of connection hold a credential a provider can be told to end:
//
//   - Google Drive: the grant's refresh token (or, for a row without one,
//     its access token). Revoking the refresh token ends the whole grant.
//   - Nextcloud: the app password the sign-in flow made, which Nextcloud
//     deletes when asked with that same password.
//
// Every other kind is left alone: an MCP server's key is one the person
// typed and only they can retire, a mailbox password likewise.
//
// # Best effort, by design
//
// Revoke returns nothing. A provider that is unreachable, a token this
// deployment's key no longer opens, a row that names a server other than
// the deployment's: each is logged with its reason and the connection is
// removed regardless. Keeping a connection a person asked to remove
// because a provider did not answer would be worse than a credential that
// outlives it by its own expiry, and the person can always end the grant
// from the provider's own settings page. Nothing here ever logs a token, a
// password or an address that could hold one.
//
// # Where a credential may be sent
//
// A Google token goes to Google's revocation endpoint and nowhere else. A
// Nextcloud password goes only to the one server the deployment names
// (nextcloud.Credentials refuses a row that names another), and a
// redirect is never followed, so neither can be steered by a row or by a
// reply.
package connectionrevocation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// The connection types that hold a credential a provider can end, as the
// `searchsourceconnectortype` enum spells them.
const (
	GoogleDrive = "GOOGLE_DRIVE_CONNECTOR"
	Nextcloud   = "NEXTCLOUD_CONNECTOR"
)

// GoogleRevokeURL is Google's OAuth 2.0 token revocation endpoint. It is a
// fact about Google, not about a deployment, which is why it is a constant
// and not a setting.
const GoogleRevokeURL = "https://oauth2.googleapis.com/revoke"

// Timeout bounds one revocation, start to finish. The person is waiting
// for the connection to be removed; a provider that takes longer than this
// to say "revoked" is treated as one that did not answer.
const Timeout = 10 * time.Second

// Revocation revokes stored credentials.
type Revocation struct {
	// SecretKey is the deployment's SECRET_KEY, which stored tokens are
	// encrypted with.
	SecretKey string
	// Nextcloud reads a Nextcloud connection's stored settings, and is nil
	// in a deployment that names no Nextcloud server: there a Nextcloud
	// connection has no server its password may be sent to, and is removed
	// without being revoked (which is logged).
	Nextcloud *nextcloud.Credentials
	// GoogleURL is where a Google token is revoked: GoogleRevokeURL, except
	// in a test.
	GoogleURL string
	HTTP      *http.Client
	Logger    *slog.Logger
}

// New checks the parts. transport is the process's transport; the client
// built over it follows no redirect.
func New(secretKey string, nextcloud *nextcloud.Credentials, transport http.RoundTripper, logger *slog.Logger) (*Revocation, error) {
	switch {
	case secretKey == "":
		return nil, errors.New("connectionrevocation: the secret key that stored tokens are encrypted with is not set")
	case transport == nil:
		return nil, errors.New("connectionrevocation: no HTTP transport to reach a provider with")
	case logger == nil:
		return nil, errors.New("connectionrevocation: no logger")
	}
	return &Revocation{
		SecretKey: secretKey, Nextcloud: nextcloud, GoogleURL: GoogleRevokeURL, Logger: logger,
		HTTP: &http.Client{
			Transport: transport,
			// A reply cannot send the credential anywhere else.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Revoke ends the credential of a connection of this type with this stored
// config, if its type has one. It returns when the provider has answered,
// or after Timeout.
func (r *Revocation) Revoke(ctx context.Context, connectorID int64, connectorType string, config []byte) {
	ctx, done := context.WithTimeout(ctx, Timeout)
	defer done()
	var outcome string
	switch connectorType {
	case GoogleDrive:
		outcome = r.google(ctx, config)
	case Nextcloud:
		outcome = r.nextcloud(ctx, config)
	default:
		return
	}
	level := slog.LevelInfo
	if outcome != revoked && outcome != nothingStored {
		level = slog.LevelWarn
	}
	r.Logger.LogAttrs(ctx, level, "a removed connection's credential at its provider",
		slog.Int64("connector_id", connectorID), slog.String("connector_type", connectorType), slog.String("outcome", outcome))
}

// The outcomes that are not a problem.
const (
	revoked       = "revoked"
	nothingStored = "no credential was stored"
)

// google revokes the grant and says what happened.
func (r *Revocation) google(ctx context.Context, config []byte) string {
	var stored struct {
		RefreshToken string `json:"refresh_token"`
		Token        string `json:"token"`
		Encrypted    bool   `json:"_token_encrypted"`
	}
	if err := json.Unmarshal(config, &stored); err != nil {
		return "not revoked: the stored settings could not be read"
	}
	// The refresh token when there is one: revoking it ends the whole
	// grant, where revoking an access token ends only that token.
	token := stored.RefreshToken
	if token == "" {
		token = stored.Token
	}
	if token == "" {
		return nothingStored
	}
	if stored.Encrypted {
		opened, err := storedtoken.Open(r.SecretKey, token)
		if err != nil {
			return "not revoked: the stored token cannot be opened with this deployment's key"
		}
		token = opened
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.GoogleURL, strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return "not revoked: the request could not be built"
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	status, err := r.send(request)
	switch {
	case err != nil:
		// The error's type only: its text can repeat the request.
		return fmt.Sprintf("not revoked: Google could not be reached (%T)", errors.Unwrap(err))
	case status == http.StatusOK:
		return revoked
	case status == http.StatusBadRequest:
		// invalid_token: already revoked or expired, which is the goal.
		return revoked
	}
	return fmt.Sprintf("not revoked: Google answered %d", status)
}

// nextcloud deletes the app password and says what happened.
func (r *Revocation) nextcloud(ctx context.Context, config []byte) string {
	if r.Nextcloud == nil {
		return "not revoked: this deployment names no Nextcloud server"
	}
	// Refuses a row that names any server but the deployment's, so the
	// password is sent only there.
	account, err := r.Nextcloud.Account(config)
	if err != nil {
		return "not revoked: the stored settings cannot be used (incomplete, unreadable, or for another server)"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, account.BaseURL+"/ocs/v2.php/core/apppassword", nil)
	if err != nil {
		return "not revoked: the request could not be built"
	}
	request.SetBasicAuth(account.LoginName, account.AppPassword)
	// Every OCS endpoint requires this header.
	request.Header.Set("OCS-APIRequest", "true")
	status, err := r.send(request)
	switch {
	case err != nil:
		return fmt.Sprintf("not revoked: Nextcloud could not be reached (%T)", errors.Unwrap(err))
	case status == http.StatusOK:
		return revoked
	case status == http.StatusUnauthorized:
		// The password no longer signs in: it is already gone.
		return revoked
	}
	// 403 here is a real password, not an app password: not ours to delete.
	return fmt.Sprintf("not revoked: Nextcloud answered %d", status)
}

// send makes the request and returns the status, having read and closed
// the reply so the connection can be reused.
func (r *Revocation) send(request *http.Request) (int, error) {
	response, err := r.HTTP.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, nil
}
