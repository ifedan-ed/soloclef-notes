// Package signin is how a person gets a session and keeps it: the password
// sign-in of the web app and of the phone app, the rotation of a refresh
// token, signing out, and the read of when a session expires. It is also how
// an account gets a password in the first place: the sign-up form, and the
// reset by mail.
//
//	POST   /auth/jwt/login           the web app's password sign-in (session cookies)
//	POST   /auth/desktop/login       the phone app's password sign-in (a token pair)
//	POST   /auth/jwt/refresh         a refresh token for a new access token
//	POST   /auth/jwt/revoke          sign this device out
//	GET    /auth/session             when the caller's access token expires
//	POST   /auth/register            the sign-up form (register.go)
//	POST   /auth/forgot-password     mail a reset link (password_reset.go)
//	POST   /auth/reset-password      a new password for the link's token
//	GET    /verify-token             who a credential is (verify_token.go)
//	GET    /auth/devices             where the account is signed in (devices.go)
//	DELETE /auth/devices/{id}        sign one of them out
//	POST   /auth/jwt/logout-all      sign every one of them out
//
// Single sign-on is a package of its own (internal/oidcsignin). It ends by
// calling StartWebSession here, so there is one place that makes a session
// whatever proved who the person is.
//
// # What a session is
//
// A sign-in starts a *family* of refresh tokens (refresh_tokens.family_id).
// Each refresh spends the token it was given and mints its successor in the
// same family, so a family is one device's sign-in, and ending a family is
// signing that device out. The access token is a short HS256 JWT naming the
// account and the family (`sid`); the gate (internal/principal) verifies it
// and, in the one read it already made for the account, checks that the
// family still has an unspent token, so a device signed out by any route is
// refused on its very next request. The refresh token is a random string the
// database holds only as a SHA-256 hash.
//
// A spent token presented again ends its whole family: one of the two
// presenters is not the device it was issued to. The exception is a
// presentation inside the grace window right after the rotation, which is
// how two tabs (or the phone app and its background fetch) that refreshed at
// once are told apart from a thief: the late one gets an access token and no
// new refresh token (rotation.go).
//
// # Which contracts it keeps
//
// The web app reads `{"authenticated": true, "access_expires_at": …}` and
// the two HttpOnly cookies; the phone app reads `{access_token,
// refresh_token, token_type, access_expires_at, session_id}` and keeps the
// pair itself. Which of the two a refresh answers is decided by where the
// refresh token came from: a cookie gets cookies, a body gets a body. A
// refused sign-in says `LOGIN_BAD_CREDENTIALS`, the code both apps map to
// their "wrong email or password" text.
//
// # What it does better than the Python
//
//   - **A deactivated account cannot refresh.** The Python rotation checked
//     only that the account existed, so a deactivated account kept a working
//     session for as long as its refresh token lived.
//   - **Password hashing is bounded.** Argon2id costs 64 MiB a try; at most
//     a few run at once (passwords.go), so a burst of sign-ins queues instead
//     of exhausting the container's memory.
//   - **An unknown address costs what a known one does**, so the time a
//     refusal takes does not say which addresses have accounts.
//   - **Signing a device out stops its phone's pushes in the same
//     transaction**, not in a second session after the first committed.
//   - **The lifetimes are constants with names** (lifetimes.go), not five
//     settings with defaults; nothing about them differs by deployment.
//   - **Ending a sign-in ends its access tokens at once.** Signing out, a
//     device signed out from the list, signing out everywhere, a reset or
//     `set-password`, and a replayed refresh token all end the family, and
//     the gate refuses every access token of an ended family. The Python
//     tier ended only the refresh token, and the access token kept working
//     for the rest of its hour.
package signin

import "net/http"

// The paths. The sign-in routes are at the server's root, not under
// /api/v1: both apps were built against these addresses.
const (
	PasswordPath = "/auth/jwt/login"
	PhonePath    = "/auth/desktop/login"
	RefreshPath  = "/auth/jwt/refresh"
	RevokePath   = "/auth/jwt/revoke"
	SessionPath  = "/auth/session"
	RegisterPath = "/auth/register"
	ForgotPath   = "/auth/forgot-password"
	ResetPath    = "/auth/reset-password"
	// VerifyPath is at the root too: the MCP server and the web app's own
	// route handler were built against it there.
	VerifyPath = "/verify-token"
)

// Public is the routes that take no credential, as "METHOD /path" pairs:
// what cmd/api mounts outside the gate. A sign-in, a sign-up and a reset
// have nobody to identify yet, and a refresh or a sign-out is proved by the
// refresh token it carries, not by an access token (which has usually just
// expired).
func Public() []string {
	return append(PasswordAttempts(),
		http.MethodPost+" "+RefreshPath,
		http.MethodPost+" "+RevokePath,
	)
}

// BrowserSessionWrites is the routes that set or clear the web app's session
// cookies: the password sign-in, the refresh and the sign-out. cmd/api has
// the forgery check refuse each from a foreign page even when no session
// cookie came with it (internal/csrforigin, session_writes.go), because a
// sign-in carries none and a page anywhere could otherwise sign the browser
// in to an account of its choosing. The phone app's sign-in is not here: it
// answers a token pair in the body, which a foreign page cannot read, and
// sets no cookie.
func BrowserSessionWrites() []string {
	return []string{
		http.MethodPost + " " + PasswordPath,
		http.MethodPost + " " + RefreshPath,
		http.MethodPost + " " + RevokePath,
	}
}

// PasswordAttempts is the routes that check, set or mail about a password:
// the ones cmd/api holds to the tighter per-client limit. That limit is the
// one control on them. It is what stops a password being guessed at the
// general rate of a thousand requests a minute, a sign-up form being used
// to fill the accounts table, and the reset form being used to fill
// somebody's inbox; there is no captcha and no second counter beside it.
func PasswordAttempts() []string {
	return []string{
		http.MethodPost + " " + PasswordPath,
		http.MethodPost + " " + PhonePath,
		http.MethodPost + " " + RegisterPath,
		http.MethodPost + " " + ForgotPath,
		http.MethodPost + " " + ResetPath,
	}
}

// Served is every route of the package, public and gated.
func Served() []string {
	return append(Public(),
		http.MethodGet+" "+SessionPath,
		http.MethodGet+" "+VerifyPath,
		// The devices list and the two ways of signing devices out
		// (devices.go): behind the gate, a session's own.
		http.MethodGet+" "+DevicesPath,
		http.MethodDelete+" "+DevicePath,
		http.MethodPost+" "+LogoutAllPath,
	)
}

// badCredentials is the one refusal of a sign-in, whatever was wrong: no
// such address, a wrong password, an account with no password, or a
// deactivated account. Saying which would tell a stranger which addresses
// have accounts.
const badCredentials = "LOGIN_BAD_CREDENTIALS"

// refreshRefused is the 401 for every refresh token that does not work:
// never issued, expired, spent, or of a deactivated account.
const refreshRefused = "Invalid or expired refresh token"
