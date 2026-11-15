// Package jmapconnect connects an account's mailboxes on a JMAP server
// (Stalwart): by Stalwart's own sign-in (OAuth with PKCE, preferred) or
// with an app password, and lists, tests and removes them.
//
//	GET    /api/v1/connectors/jmap/accounts                    the caller's JMAP mailboxes
//	POST   /api/v1/connectors/jmap/accounts                    connect one with an app password
//	DELETE /api/v1/connectors/jmap/accounts/{account_id}       remove one, and revoke its grant
//	POST   /api/v1/connectors/jmap/accounts/{account_id}/test  does it still sign in, may it send?
//	GET    /api/v1/connectors/jmap/oauth                       whether Stalwart's sign-in is set up here
//	POST   /api/v1/connectors/jmap/oauth/connect               start: the server's sign-in page
//	GET    /api/v1/auth/jmap/mail/connector/callback           where the server sends the person back
//	GET    /api/v1/connectors/jmap/oauth-client.json           this app's client metadata document
//
// # How this app signs in to a Stalwart server
//
// It is a public client identified by a client ID metadata document: its
// client id is the https address of oauth-client.json, served here, which
// names the one redirect URI. Stalwart fetches it during the sign-in and
// shows the person a consent screen naming this site's host. (Its open
// dynamic registration is for desktop and phone apps: it refuses https
// redirect URIs.) The server's administrator must allow such documents
// (Settings, Authentication, OIDC Provider: client metadata documents,
// "same domain" or "any"); the start asks the server first and says so on
// this site when it does not.
//
// The server is the one the typed address's metadata confirms: a Stalwart
// reachable under several names may name another as its issuer, which is
// then read once and, if it names itself, used for the whole sign-in in
// place of the typed one (jmapaccounts.OAuth.Discover). An app password
// follows the same rule (jmapaccounts.Mail.ConnectAppPassword).
//
// The start seals an attempt (who is connecting, the server, its
// endpoints and issuer, the PKCE verifier, the state, where to return)
// into an HttpOnly cookie sent only with the callback, and answers the
// server's sign-in page. The callback has no session: the cookie is its
// proof, so a sign-in started in one account and finished in another
// browser saves nothing. It checks the state and the issuer (RFC 9207),
// trades the code for a grant at the token endpoint the attempt recorded
// (never one the callback names), refuses a grant without the mail scope,
// finds whose mailbox it is from the server's session, and stores the
// grant sealed (soloclef_assistant/jmapaccounts). Every ending lands back
// on the website with `?jmap_mail=<outcome>`.
//
// # Where this process connects
//
// A server's address is typed by a person, so every request to it goes
// through jmapaccounts.PublicNetwork: https on 443, that host only, public
// addresses only, checked at every connection, no redirects.
//
// # What is never returned
//
// No answer holds a password, a token or their ciphertext: a mailbox is
// always shown as the fields of shownAccount.
package jmapconnect

// The paths.
const (
	AccountsPath       = "/api/v1/connectors/jmap/accounts"
	AccountPath        = "/api/v1/connectors/jmap/accounts/{account_id}"
	AccountTestPath    = "/api/v1/connectors/jmap/accounts/{account_id}/test"
	AvailabilityPath   = "/api/v1/connectors/jmap/oauth"
	ConnectPath        = "/api/v1/connectors/jmap/oauth/connect"
	CallbackPath       = "/api/v1/auth/jmap/mail/connector/callback"
	ClientDocumentPath = "/api/v1/connectors/jmap/oauth-client.json"
)

// ServedRoutes is every route of this package as "METHOD path".
func ServedRoutes() []string {
	return []string{
		"GET " + AccountsPath, "POST " + AccountsPath, "DELETE " + AccountPath, "POST " + AccountTestPath,
		"GET " + AvailabilityPath, "POST " + ConnectPath, "GET " + CallbackPath, "GET " + ClientDocumentPath,
	}
}

// Public is the two routes with no signed-in caller: the server's callback,
// whose proof is the attempt's cookie, and the client document, which the
// mail server fetches.
func Public() []string { return []string{"GET " + CallbackPath, "GET " + ClientDocumentPath} }

// BodyRoutes are the two that read a small JSON body.
func BodyRoutes() []string { return []string{"POST " + AccountsPath, "POST " + ConnectPath} }

// What the routes say, and the code the web app tells the 503 by.
const (
	sessionRequired   = "This action requires an interactive session"
	accountAbsent     = "Account not found"
	notConfiguredCode = "jmap_mail_oauth_not_configured"
	notConfigured     = "Stalwart sign-in isn't set up on this server; connect with an app password"
	addressInvalid    = "The server's address must be an https address, such as https://mail.example.org"
)

// outcomeParam is the query key the callback hands the website an outcome
// under (soloclef_web/features/jmap-mail-connect/lib/jmap-mail-outcome.ts).
const outcomeParam = "jmap_mail"

// The outcomes.
const (
	outcomeConnected = "connected"
	// The person declined on the server's consent page.
	outcomeDenied = "denied"
	// No sign-in in progress in this browser: the window passed, the
	// callback was opened elsewhere, or its state or issuer did not match.
	outcomeExpired = "expired"
	// The grant has no mail scope, or the session no mail account.
	outcomeNoMail        = "no_mail"
	outcomeFailed        = "failed"
	outcomeNotConfigured = "not_configured"
)

// connectorsPage is where a connect that named no page of its own lands.
const connectorsPage = "/dashboard/settings/connectors"
