// Package apphandoff is the phone app's browser hand-off: how the app runs
// a flow that only works on a web page, in a browser it cannot sign in.
//
//	POST /api/v1/app/handoff                     a one-time code for the browser sheet (the app's session)
//	GET  /api/v1/app/handoff/{code}              the sheet's page: spends the code, starts the flow
//	POST /api/v1/app/handoff/complete            the app saves what a connect held (the app's session)
//	POST /api/v1/app/handoff/drive-picker/import the Picker page's one import (its ticket is the proof)
//
// Three things only run in a browser: Google's Picker (Google's own
// JavaScript), Google's consent page and Microsoft's sign-in. The app
// cannot put its session into the phone's browser sheet, and no token may
// travel in a URL, where providers, proxies and browser history keep it.
// So the app asks for a code with its bearer token, opens the code's page
// in the sheet, and this tier, holding what the code stands for, starts the
// flow inside that browser.
//
// # The purposes
//
//   - drive_picker: a page running Google's Picker with the account's own
//     short-lived Drive token. Its one import is made with a ticket carried
//     in the page's body, and the sheet goes back to
//     soloclef://drive/done?count=N&unsupported=M (or ?error=<why>).
//   - drive_connect and microsoft_mail: a 303 to Google's consent page or
//     Microsoft's sign-in, with the provider's own attempt cookie set in this
//     browser, exactly as the website's Connect buttons do
//     (internal/driveconnect, internal/microsoftmail). Their callbacks save
//     nothing for such an attempt: they hold the grant and send the sheet to
//     soloclef://connectors/done?purpose=…&status=confirm&completion=…, and
//     the grant is saved only when the app posts the completion with the
//     PKCE verifier behind the challenge it asked with, from the same
//     account.
//
// # Why connects are confirmed
//
// The page takes no credential, so whoever opens a code's link runs its
// flow in their own browser. Saved at the callback, a link sent to someone
// else would put the opener's mailbox or Drive into the link maker's
// account. Held instead, the opener holds a completion they cannot spend
// (no verifier, another account) and the link's maker never sees it.
//
// # The secrets
//
// Codes (60 s), tickets (10 minutes, long enough to pick files) and
// completions (5 minutes) are random, spent by their first use, and kept in
// Valkey under the SHA-256 of the secret, so a dump of Valkey holds nothing
// spendable (secrets.go). A completion's grant is sealed by its provider
// with the deployment's secret key before it is held, so no provider token
// is ever in Valkey in the clear.
//
// # What changed from the Python
//
//   - No workspace: a hand-off, its notebook and its connection are the
//     account's, and every row is read with its owner named (driveconnect,
//     drivesync), so another account's notebook or connection is the same
//     404 as one that was never made.
//   - A Valkey that cannot be reached fails the request (503). The Python
//     fell back to the worker's memory, where a code issued by one replica
//     was a dead link on the next.
//   - Valkey's own expiry ends a secret; there is no issued-at to compare
//     with a clock that may differ between replicas.
//   - No rate limits of the package's own beside the tier's general one:
//     every secret is 32 random bytes and single use, so guessing gains
//     nothing the general limit does not already count.
//   - The Picker's import is the website's own import (drivesync.ImportFor),
//     so the app's picks become exactly the documents the website's would.
package apphandoff

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"

// The paths.
const (
	StartPath    = "/api/v1/app/handoff"
	PagePath     = "/api/v1/app/handoff/{code}"
	CompletePath = "/api/v1/app/handoff/complete"
	ImportPath   = "/api/v1/app/handoff/drive-picker/import"
)

// ServedRoutes is every route of the package as "METHOD path".
func ServedRoutes() []string {
	return []string{"POST " + StartPath, "GET " + PagePath, "POST " + CompletePath, "POST " + ImportPath}
}

// Public is the two routes with no signed-in caller: the browser sheet's
// page, whose proof is the code in its path, and the Picker page's import,
// whose proof is the ticket in its body.
func Public() []string { return []string{"GET " + PagePath, "POST " + ImportPath} }

// confirmed are the purposes whose grant the callback holds for the app to
// save (see "Why connects are confirmed").
var confirmed = map[string]bool{appreturn.DriveConnect: true, appreturn.MicrosoftMail: true}

// supported is every purpose a code can be asked for.
var supported = map[string]bool{appreturn.DrivePicker: true, appreturn.DriveConnect: true, appreturn.MicrosoftMail: true}

// The codes a refusal carries, which the app tells apart
// (soloclef_mobile/src/features/workspace/connectors/handoff.ts and
// src/features/sources/drive/driveHandoff.ts).
const (
	codeUnsupported       = "HANDOFF_PURPOSE_UNSUPPORTED"
	codeChallengeRequired = "HANDOFF_CHALLENGE_REQUIRED"
	codeExpired           = "HANDOFF_EXPIRED"
	codeWrongVerifier     = "HANDOFF_WRONG_VERIFIER"
	codeWrongAccount      = "HANDOFF_WRONG_ACCOUNT"
	codeSaveFailed        = "HANDOFF_SAVE_FAILED"
	// The provider's own codes, the ones the website's routes answer with.
	codeDriveNotConfigured     = "google_drive_not_configured"
	codeDriveNotConnected      = "google_drive_not_connected"
	codeDriveReconnect         = "google_drive_reconnect"
	codeMicrosoftNotConfigured = "microsoft_mail_not_configured"
)
