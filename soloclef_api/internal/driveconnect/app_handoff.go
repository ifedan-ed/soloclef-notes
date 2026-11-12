package driveconnect

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// The phone app's side of Drive (internal/apphandoff): its Picker page and
// its connect. These are methods and not routes: the hand-off's own routes
// call them, once they have settled whose request it is, so the connection
// is read and written here in one place for the website and the app alike.

// The reasons the app's Drive flows cannot go ahead, which the hand-off
// turns into the codes and returns the app words.
var (
	ErrNotConfigured = errors.New("driveconnect: Google Drive is not set up on this server")
	ErrNotConnected  = errors.New("driveconnect: the account has no Google Drive connection")
	ErrReconnect     = errors.New("driveconnect: Google no longer accepts this connection")
	// ErrHeldUnreadable is a held grant that does not open: sealed with
	// another secret key, or not one of this package's.
	ErrHeldUnreadable = errors.New("driveconnect: the held grant does not open")
)

// Configured reports whether this deployment can connect Drive at all.
func (r Routes) Configured() bool { return len(r.Settings.Missing()) == 0 }

// UsableConnection is the owner's connection, by id, when it can be used
// now: ErrNotConnected without one, ErrReconnect when Google has refused
// its grant. It asks nothing of Google, so a hand-off can be refused before
// the browser sheet opens.
func (r Routes) UsableConnection(ctx context.Context, ownerID string) (int64, error) {
	held, err := r.Rows.Connection(ctx, ownerID)
	if err != nil {
		return 0, err
	}
	if held == nil {
		return 0, ErrNotConnected
	}
	if stored := drivegrant.Read(held.Config); stored.AuthExpired || stored.RefreshToken == "" {
		return 0, ErrReconnect
	}
	return held.ID, nil
}

// PickerSession is what the Picker's page runs with: the owner's own
// connection and a fresh access token for it (drive.file: it reaches only
// what is picked), and the project's browser key and number.
type PickerSession struct {
	ConnectorID int64
	AccessToken string
	APIKey      string
	AppID       string
}

// PickerFor is the owner's Picker session, read again when the page is
// opened because the connection may have changed in the minute since the
// hand-off was asked for. Google refusing the grant is ErrReconnect.
func (r Routes) PickerFor(ctx context.Context, requestID, ownerID string) (PickerSession, error) {
	if !r.Configured() {
		return PickerSession{}, ErrNotConfigured
	}
	held, err := r.Rows.Connection(ctx, ownerID)
	if err != nil {
		return PickerSession{}, err
	}
	if held == nil {
		return PickerSession{}, ErrNotConnected
	}
	fresh, err := r.pickerToken(ctx, requestID, ownerID, held)
	if errors.Is(err, errInvalidGrant) {
		return PickerSession{}, ErrReconnect
	}
	if err != nil {
		return PickerSession{}, err
	}
	return PickerSession{ConnectorID: held.ID, AccessToken: fresh.AccessToken,
		APIKey: r.Settings.PickerAPIKey, AppID: r.Settings.pickerAppID()}, nil
}

// BeginForApp starts a connect for ownerID in the browser this response
// goes to (the phone's browser sheet), as the website's Connect does: the
// attempt sealed into this browser's cookie, and Google's consent page
// answered. The attempt carries the app's challenge, so its callback holds
// the grant for the app instead of saving it.
func (r Routes) BeginForApp(response http.ResponseWriter, ownerID, challenge string) (string, error) {
	if !r.Configured() {
		return "", ErrNotConfigured
	}
	if r.AppHold == nil || challenge == "" {
		return "", errors.New("driveconnect: an app connect needs a holder and the app's challenge")
	}
	now := r.Now()
	begun, err := newAttempt(ownerID, "", now)
	if err != nil {
		return "", err
	}
	begun.AppChallenge = challenge
	sealed, err := begun.seal(r.Settings.SecretKey, now)
	if err != nil {
		return "", err
	}
	r.setAttemptCookie(response, sealed)
	return r.google.consentURL(begun), nil
}

// heldGrant is what an app's connect holds until the app confirms it: the
// grant and the account it belongs to, as the callback would have saved
// them.
type heldGrant struct {
	Grant grant  `json:"grant"`
	Email string `json:"email"`
}

// holdForApp ends an app's callback that Google granted: the grant sealed
// with the secret key and held (appreturn.Holder), and the sheet sent
// back to the app to confirm it. A grant that cannot be held is revoked:
// nothing will ever save it.
func (r Routes) holdForApp(c call, ctx context.Context, begun attempt, granted grant, email string) {
	plain, err := json.Marshal(heldGrant{Grant: granted, Email: email})
	var sealed, completion string
	if err == nil {
		sealed, err = storedtoken.Seal(r.Settings.SecretKey, string(plain), r.Now(), rand.Reader)
	}
	if err == nil && r.AppHold == nil {
		err = errors.New("no holder for an app's grant")
	}
	if err == nil {
		completion, err = r.AppHold.Hold(ctx, begun.UserID, begun.AppChallenge, sealed)
	}
	if err != nil {
		r.google.revoke(ctx, granted.RefreshToken)
		r.Logger.LogAttrs(ctx, slog.LevelError, "a Google Drive grant for the app could not be held",
			slog.String("request_id", c.requestID), slog.String("error", err.Error()))
		r.backToApp(c, appreturn.StatusFailed, "")
		return
	}
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "Google Drive grant held for the app, to be confirmed",
		slog.String("user_id", begun.UserID))
	r.backToApp(c, appreturn.StatusConfirm, completion)
}

// SaveForApp saves a grant an app's callback held, for ownerID, once the
// hand-off has checked that the app confirming it is the one that asked.
func (r Routes) SaveForApp(ctx context.Context, requestID, ownerID, sealed string) (int64, error) {
	if !r.Configured() {
		return 0, ErrNotConfigured
	}
	plain, err := storedtoken.Open(r.Settings.SecretKey, sealed)
	if err != nil {
		return 0, ErrHeldUnreadable
	}
	var held heldGrant
	if json.Unmarshal([]byte(plain), &held) != nil || held.Grant.RefreshToken == "" {
		return 0, ErrHeldUnreadable
	}
	return r.saveGrant(ctx, requestID, ownerID, held.Grant, held.Email)
}

// backToApp ends an app's callback: the attempt's cookie cleared, and the
// sheet sent to the app's own address with the outcome, in the words the
// website gets, which the app words the same way.
func (r Routes) backToApp(c call, status, completion string) {
	r.clearAttemptCookie(c.response)
	header := c.response.Header()
	header.Set("Location", appreturn.ConnectorReturn(appreturn.DriveConnect, status, completion))
	// The address holds the completion; it is not to be kept or passed on.
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	c.response.WriteHeader(http.StatusSeeOther)
}
