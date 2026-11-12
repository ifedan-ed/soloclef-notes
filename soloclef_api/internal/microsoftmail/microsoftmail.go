// Package microsoftmail connects a Microsoft mailbox (Microsoft 365 or
// Outlook.com) to an account's mail connection: Microsoft's sign-in page
// and back, and a working access token for a mailbox once connected.
//
//	GET  /api/v1/connectors/email-imap/microsoft                whether this deployment can connect one
//	POST /api/v1/connectors/email-imap/microsoft/connect        start: Microsoft's sign-in page
//	GET  /api/v1/auth/microsoft/mail/connector/callback         where Microsoft sends the person back
//
// Microsoft retired passwords for IMAP, so a Microsoft mailbox signs in
// with an OAuth access token (XOAUTH2) instead of an app password. Listing
// and removing such a mailbox, and testing it, are internal/mailaccounts'
// routes; this package is how one comes to exist and how its token is kept
// working (sign_in.go).
//
// # How a mailbox is connected
//
// The start seals an attempt (who is connecting, the `state`, the PKCE
// verifier, where to return) into an HttpOnly cookie that the browser
// sends only with Microsoft's callback, and answers Microsoft's sign-in
// page. The callback has no session: the cookie is its proof, so a link
// started in one account and opened in another browser saves nothing. It
// trades Microsoft's code for a grant, refuses one without IMAP access or
// without a refresh token, reads which mailbox it is from the id_token,
// and writes the mailbox into the account's mail connection with both
// tokens sealed (soloclef_connectors/storedtoken), in the record shape the
// Python tier wrote. Connecting a mailbox that is already there replaces
// its tokens in place, which is what Reconnect does.
//
// Every way the callback can end lands back in the website with
// `?microsoft_mail=<outcome>`, never on a bare error.
//
// # What changed from the Python
//
//   - The attempt's cookie is sealed, not only signed: the PKCE verifier
//     in it is no longer readable by whoever holds the cookie.
//   - The tenant is a setting with no default. Python fell back to
//     "common" when none was named; a deployment now says which accounts
//     it accepts.
//   - A token refresh is written into the one mailbox's record by a
//     single statement. Python read the whole list, changed it in memory
//     and wrote it back, so a mailbox added meanwhile could be lost.
//   - A refresh whose new tokens cannot be saved fails the sign-in.
//     Microsoft may rotate the refresh token, and Python carried on with a
//     row that still held the old one.
//   - Microsoft's reply is bounded and a redirect from the token endpoint
//     is never followed.
//
// # What is not here
//
// The phone app's browser hand-off has routes of its own
// (internal/apphandoff); this package gives it the methods in
// app_handoff.go, and an attempt the app started ends with the mailbox held
// for the app to confirm, never saved at the callback.
package microsoftmail

import "git.danvics.com/danvics/soloclef/soloclef_assistant/mailsignin"

// The paths.
const (
	AvailabilityPath = "/api/v1/connectors/email-imap/microsoft"
	ConnectPath      = "/api/v1/connectors/email-imap/microsoft/connect"
	// CallbackPath is the path of the redirect URI registered at
	// Microsoft, so it must not move.
	CallbackPath = "/api/v1/auth/microsoft/mail/connector/callback"
)

// ServedRoutes is every route of this package as "METHOD path".
func ServedRoutes() []string {
	return []string{"GET " + AvailabilityPath, "POST " + ConnectPath, "GET " + CallbackPath}
}

// Public is the one route with no signed-in caller: Microsoft's callback,
// whose proof is the attempt's cookie.
func Public() []string { return []string{"GET " + CallbackPath} }

// BodyRoutes is the one route that reads a small JSON body.
func BodyRoutes() []string { return []string{"POST " + ConnectPath} }

// Exchange Online's IMAP endpoint and the scopes a sign-in asks for are
// soloclef_assistant/mailsignin's: the token only ever goes where that
// package says, from whichever tier signs in.
const (
	IMAPHost = mailsignin.IMAPHost
	IMAPPort = mailsignin.IMAPPort
	scopes   = mailsignin.Scopes
)

// What the routes say, and the code the web app tells the 503 by.
const (
	sessionRequired   = "This action requires an interactive session"
	notConfiguredCode = "microsoft_mail_not_configured"
	notConfigured     = "Microsoft accounts aren't set up on this server"
)

// outcomeParam is the query key the callback hands the website an outcome
// under; the connectors page turns each into one short message
// (soloclef_web/features/email-imap-connect/lib/microsoft-mail-outcome.ts).
const outcomeParam = "microsoft_mail"

// The outcomes.
const (
	outcomeConnected = "connected"
	// The person cancelled or declined on Microsoft's page.
	outcomeDenied = "denied"
	// No connect in progress in this browser: the window passed, the
	// callback was opened elsewhere, or its state was not this browser's.
	outcomeExpired = "expired"
	// A work or school account whose organization only lets admins consent.
	outcomeAdminApproval = "admin_approval"
	// The person unticked mailbox access on the consent page.
	outcomeScopeMissing = "scope_missing"
	// Microsoft issued no refresh token.
	outcomeNoRefresh = "no_refresh_token"
	// The account has no email address to sign in to IMAP as.
	outcomeNoAddress     = "no_address"
	outcomeFailed        = "failed"
	outcomeNotConfigured = "not_configured"
)

// connectorsPage is where a connect that named no page of its own lands.
const connectorsPage = "/dashboard/settings/connectors"
