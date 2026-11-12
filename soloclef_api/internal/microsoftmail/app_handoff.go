package microsoftmail

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// The phone app's side of a Microsoft mailbox connect
// (internal/apphandoff). These are methods and not routes: the hand-off's
// own routes call them once they have settled whose request it is, so a
// mailbox is written here in one place for the website and the app alike.

var (
	ErrNotConfigured = errors.New("microsoftmail: Microsoft accounts aren't set up on this server")
	// ErrHeldUnreadable is a held mailbox that does not open: sealed with
	// another secret key, or not one of this package's.
	ErrHeldUnreadable = errors.New("microsoftmail: the held mailbox does not open")
)

// Configured reports whether this deployment can connect a Microsoft
// mailbox at all.
func (r Routes) Configured() bool { return r.Settings.Configured() }

// BeginForApp starts a connect for ownerID in the browser this response
// goes to (the phone's browser sheet), as the website's button does: the
// attempt sealed into this browser's cookie, and Microsoft's sign-in page
// answered. accountID, when it is one of the owner's Microsoft mailboxes,
// preselects its address (a reconnect). The attempt carries the app's
// challenge, so its callback holds the mailbox for the app instead of
// saving it.
func (r Routes) BeginForApp(ctx context.Context, response http.ResponseWriter, ownerID, accountID, challenge string) (string, error) {
	if !r.Configured() {
		return "", ErrNotConfigured
	}
	if r.AppHold == nil || challenge == "" {
		return "", errors.New("microsoftmail: an app connect needs a holder and the app's challenge")
	}
	loginHint, err := r.loginHint(ctx, ownerID, accountID)
	if err != nil {
		return "", err
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
	return r.microsoft.consentURL(begun, loginHint), nil
}

// heldMailbox is what an app's connect holds until the app confirms it:
// the mailbox and its grant, as the callback would have saved them.
type heldMailbox struct {
	Box   mailbox `json:"box"`
	Grant grant   `json:"grant"`
}

// holdForApp ends an app's callback that Microsoft granted: the mailbox
// sealed with the secret key and held (appreturn.Holder), and the sheet
// sent back to the app to confirm it.
func (r Routes) holdForApp(c call, ctx context.Context, begun attempt, box mailbox, granted grant) {
	plain, err := json.Marshal(heldMailbox{Box: box, Grant: granted})
	var sealed, completion string
	if err == nil {
		sealed, err = storedtoken.Seal(r.Settings.SecretKey, string(plain), r.Now(), rand.Reader)
	}
	if err == nil && r.AppHold == nil {
		err = errors.New("no holder for an app's mailbox")
	}
	if err == nil {
		completion, err = r.AppHold.Hold(ctx, begun.UserID, begun.AppChallenge, sealed)
	}
	if err != nil {
		// Microsoft has no revocation for a single grant; unsaved, its
		// refresh token is simply never used.
		r.Logger.LogAttrs(ctx, slog.LevelError, "a Microsoft mailbox for the app could not be held",
			slog.String("request_id", c.requestID), slog.String("error", err.Error()))
		r.backToApp(c, appreturn.StatusFailed, "")
		return
	}
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "Microsoft mailbox held for the app, to be confirmed",
		slog.String("user_id", begun.UserID))
	r.backToApp(c, appreturn.StatusConfirm, completion)
}

// SaveForApp saves a mailbox an app's callback held, for ownerID, once the
// hand-off has checked that the app confirming it is the one that asked.
func (r Routes) SaveForApp(ctx context.Context, requestID, ownerID, sealed string) error {
	if !r.Configured() {
		return ErrNotConfigured
	}
	plain, err := storedtoken.Open(r.Settings.SecretKey, sealed)
	if err != nil {
		return ErrHeldUnreadable
	}
	var held heldMailbox
	if json.Unmarshal([]byte(plain), &held) != nil || held.Box.Address == "" || held.Grant.RefreshToken == "" {
		return ErrHeldUnreadable
	}
	return r.saveMailbox(ctx, requestID, ownerID, held.Box, held.Grant)
}

// backToApp ends an app's callback: the attempt's cookie cleared, and the
// sheet sent to the app's own address with the outcome, in the words the
// website gets, which the app words the same way.
func (r Routes) backToApp(c call, status, completion string) {
	r.clearAttemptCookie(c.response)
	header := c.response.Header()
	header.Set("Location", appreturn.ConnectorReturn(appreturn.MicrosoftMail, status, completion))
	// The address holds the completion; it is not to be kept or passed on.
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	c.response.WriteHeader(http.StatusSeeOther)
}
