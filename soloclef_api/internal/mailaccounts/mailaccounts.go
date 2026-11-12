// Package mailaccounts keeps the mailboxes an account has connected by
// IMAP: adding one with an app password, listing them, removing one, and
// testing that one signs in.
//
//	GET    /api/v1/connectors/email-imap/accounts                    the caller's mailboxes
//	POST   /api/v1/connectors/email-imap/accounts                    add one
//	DELETE /api/v1/connectors/email-imap/accounts/{account_id}       remove one
//	POST   /api/v1/connectors/email-imap/accounts/{account_id}/test  does it still sign in?
//	POST   /api/v1/connectors/email-imap/test                        would these settings sign in?
//
// # Where a mailbox lives
//
// An account has one connection of type EMAIL_IMAP_CONNECTOR, and its
// mailboxes are the `accounts` list in that row's config. Each is a record
// this package builds itself from what the form sent: the app password is
// sealed with the deployment's secret key (soloclef_connectors/storedtoken, the form
// every stored credential has) and marked `_encrypted`. No route writes a
// record a caller sent, and no answer holds a password or its ciphertext:
// a mailbox is always shown as nine public fields.
//
// Every statement names the owner (queries.sql). A mailbox of another
// account is "Account not found", from the same line as one that never
// was.
//
// # Where this process will connect
//
// A mailbox's host is typed by a person, and testing it makes this process
// open a connection there. So the host must resolve to public addresses
// only, when it is saved and again when it is connected to, and the
// connection goes to the address that was checked, not to whatever the
// name answers a moment later (imap.go). Until a server has been reached
// and has spoken IMAP, every failure is one sentence, so a test cannot be
// used to find out what is listening inside this network.
//
// # What changed from the Python
//
//   - Adding a mailbox and both tests are for a person in a session, as
//     adding an MCP server is: they store a credential and make the server
//     connect somewhere. Listing and removing are open to the account's
//     API keys too.
//   - Adding and removing change the list inside one statement or one
//     locked transaction, so two changes made at once both land. Python
//     read the list, changed it in memory and wrote it back.
//   - A sign-in failure is reported in this package's words with the
//     server's own reason bounded and stripped of control characters.
//
// # What is not here
//
// Connecting a mailbox through Microsoft's sign-in is
// internal/microsoftmail. Such a mailbox is listed, removed and tested
// here; its test signs in with an access token that package keeps working
// (MicrosoftSignIn), not with a password.
package mailaccounts

// The paths.
const (
	AccountsPath    = "/api/v1/connectors/email-imap/accounts"
	AccountPath     = "/api/v1/connectors/email-imap/accounts/{account_id}"
	AccountTestPath = "/api/v1/connectors/email-imap/accounts/{account_id}/test"
	TestPath        = "/api/v1/connectors/email-imap/test"
)

// ServedRoutes is every route of this package as "METHOD path".
func ServedRoutes() []string {
	return []string{
		"GET " + AccountsPath,
		"POST " + AccountsPath,
		"DELETE " + AccountPath,
		"POST " + AccountTestPath,
		"POST " + TestPath,
	}
}

// BodyRoutes are the two that read a small JSON body through the buffered
// reader.
func BodyRoutes() []string {
	return []string{"POST " + AccountsPath, "POST " + TestPath}
}

// ConnectionName is what the account's mail connection is called in its
// list of connections. Exported because a mailbox connected through
// Microsoft (internal/microsoftmail) makes the same connection: one name,
// so the two kinds of mailbox share a row.
const ConnectionName = "Email (IMAP)"

// What the routes say.
const (
	sessionRequired    = "This action requires an interactive session"
	accountAbsent      = "Account not found"
	hostRefused        = "This IMAP host could not be reached (private, link-local, or unresolvable addresses are not allowed)"
	passwordUnreadable = "This account's credentials are missing or can no longer be read; add it again"
	settingsUnstored   = "These settings contain a character that cannot be saved"
)
