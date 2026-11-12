// Package appreturn is what a flow the phone app started in its browser
// sheet ends with: the app's own address to send the sheet back to, and,
// for a connect, how the provider's grant is held until the app confirms
// it.
//
// It is its own package so that the providers' packages (driveconnect,
// microsoftmail), whose callbacks end such a flow, and the hand-off
// (internal/apphandoff), which starts it and keeps what is held, share
// these words without importing each other.
package appreturn

import (
	"context"
	"net/url"
)

// The purposes a hand-off can be for.
const (
	DrivePicker   = "drive_picker"
	DriveConnect  = "drive_connect"
	MicrosoftMail = "microsoft_mail"
)

// The app's own addresses. They are fixed, never taken from a request, so
// a forged hand-off cannot send a code, a completion or a count anywhere
// else; and they are constants, not settings, because they are the app's
// URL scheme, which is compiled into the app.
const (
	Connectors = "soloclef://connectors/done"
	Drive      = "soloclef://drive/done"
)

// The statuses a connector return carries beside the provider's own
// outcome words (driveconnect's and microsoftmail's), which the app words
// (soloclef_mobile/src/features/workspace/connectors/handoff.ts).
const (
	// StatusConfirm comes with a completion: nothing is saved yet.
	StatusConfirm = "confirm"
	// StatusExpired is a code that was never issued, was spent or ran out.
	StatusExpired = "expired"
	// StatusNotConfigured is a provider this deployment has not set up.
	StatusNotConfigured = "not_configured"
	// StatusFailed is a flow that could not be finished here.
	StatusFailed = "failed"
)

// ConnectorReturn is where a connector's flow sends the browser sheet: the
// app's address with the purpose, how it ended and, for StatusConfirm, the
// completion to save it with.
func ConnectorReturn(purpose, status, completion string) string {
	query := url.Values{"status": {status}}
	if purpose != "" {
		query.Set("purpose", purpose)
	}
	if completion != "" {
		query.Set("completion", completion)
	}
	return Connectors + "?" + query.Encode()
}

// DriveReturn is where the Drive Picker's page sends the sheet: the app's
// address with query (count and unsupported, or error).
func DriveReturn(query url.Values) string {
	if len(query) == 0 {
		return Drive
	}
	return Drive + "?" + query.Encode()
}

// Holder keeps a grant a connect's callback was given until the app that
// asked for it saves it. The providers are handed one and never see how it
// is kept (internal/apphandoff keeps it).
type Holder interface {
	// Hold keeps sealed (the grant, already sealed by the provider) for
	// userID's app, saveable with the verifier behind challenge, and
	// answers the completion the sheet carries back to the app.
	Hold(ctx context.Context, userID, challenge, sealed string) (completion string, err error)
}
