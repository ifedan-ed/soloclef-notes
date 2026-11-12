package microsoftmail

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/mailaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"
)

// SignIn turns a stored Microsoft mailbox into IMAP settings with an
// access token that works (mailaccounts.MicrosoftSignIn).
//
// The work is soloclef_assistant/mailsignin's, which the chat's mail tools
// sign in with from the worker too: when the stored token is about to
// lapse it is refreshed and the new pair stored sealed, and a mailbox
// whose grant Microsoft refuses is marked `needs_reconnect`.
type SignIn struct{ mailsignin.SignIn }

// NewSignIn checks the parts. A deployment that has not set Microsoft mail
// up is not an error: its mailboxes answer that it is not set up.
func NewSignIn(rows Rows, settings Settings, transport http.RoundTripper, now func() time.Time, logger *slog.Logger) (SignIn, error) {
	if err := checked(rows, settings, transport, now, logger); err != nil {
		return SignIn{}, err
	}
	signIn := mailsignin.SignIn{SecretKey: settings.SecretKey, Rows: rows, Now: now, Logger: logger}
	if settings.Configured() {
		signIn.Microsoft = newMicrosoft(settings, transport).endpoint
	}
	return SignIn{SignIn: signIn}, nil
}

// Mailbox is the IMAP settings for one of ownerID's stored Microsoft
// mailboxes. A *mailaccounts.ConnectionError is a mailbox that cannot be
// signed in to, in words for its owner; any other error is a fault.
func (s SignIn) Mailbox(ctx context.Context, ownerID string, stored []byte) (mailaccounts.Settings, error) {
	return s.MicrosoftMailbox(ctx, ownerID, stored)
}

// What a person is told when a Microsoft mailbox cannot be signed in to.
const (
	reconnectMessage     = mailsignin.ReconnectMessage
	unreachableMessage   = mailsignin.UnreachableMessage
	serverRefusedMessage = mailsignin.ServerRefusedMessage
	notSetUpMessage      = mailsignin.NotSetUpMessage
)
