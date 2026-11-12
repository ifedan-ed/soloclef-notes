// Package driveconnect connects an account's Google Drive: Google's
// consent page and back, the Picker's short-lived token, and disconnecting.
//
//	GET    /api/v1/google-drive/connection                               the caller's connection, and whether one can be made
//	POST   /api/v1/google-drive/connect                                  start: Google's consent page
//	GET    /api/v1/auth/google/drive/connector/add                       the same, from the connectors list
//	GET    /api/v1/auth/google/drive/connector/reauth                    the same, for a connection Google stopped accepting
//	GET    /api/v1/auth/google/drive/connector/callback                  where Google sends the person back
//	GET    /api/v1/connectors/{connector_id}/google-drive/picker-session a fresh access token for the Google Picker
//	DELETE /api/v1/connectors/{connector_id}/google-drive/connection     disconnect, and end the grant at Google
//
// # How a connection is made
//
// The start seals an attempt (who is connecting, the `state`, the PKCE
// verifier, where to return) into an HttpOnly cookie that the browser sends
// only with Google's callback, and answers Google's consent page. The
// callback has no session: the cookie is its proof, so a consent link
// started in one account and opened in another browser saves nothing. It
// trades Google's code for a grant, refuses one without the `drive.file`
// scope or without a refresh token, asks Drive which account it is, and
// writes the grant into the account's one Drive connection with its tokens
// and the client secret sealed (soloclef_connectors/storedtoken) in the fields the
// Python tier wrote, so every reader of a Drive row reads either tier's.
//
// The scope is `drive.file`: the grant reaches only the files a person
// picks in the Google Picker. The Picker runs in the browser and needs an
// access token, which is the one credential this package hands out: short
// lived, the owner's own, and useless beyond the files they picked.
//
// # What changed from the Python
//
//   - The attempt's cookie is sealed, not only signed: the PKCE verifier in
//     it is no longer readable by whoever holds the cookie.
//   - The grant is saved with the account's row locked, so two consents
//     finishing at once make one connection.
//   - Disconnecting and the Picker name the owner in the statement.
//
// # What is not here
//
// Importing picked files and syncing them: internal/drivesync asks for
// both, and the worker's job (soloclef_worker/internal/drivesync) fetches
// the files; the grant's stored form and its renewal are shared with it
// (soloclef_connectors/drivegrant). The phone app's browser hand-off has
// routes of its own (internal/apphandoff); this package gives it the
// methods in app_handoff.go, and an attempt the app started ends with the
// grant held for the app to confirm, never saved at the callback.
package driveconnect

import (
	"regexp"
	"strings"
)

// The paths.
const (
	ConnectionPath = "/api/v1/google-drive/connection"
	ConnectPath    = "/api/v1/google-drive/connect"
	AddPath        = "/api/v1/auth/google/drive/connector/add"
	ReauthPath     = "/api/v1/auth/google/drive/connector/reauth"
	// CallbackPath is the path of the redirect URI registered at Google,
	// so it must not move.
	CallbackPath   = "/api/v1/auth/google/drive/connector/callback"
	PickerPath     = "/api/v1/connectors/{connector_id}/google-drive/picker-session"
	DisconnectPath = "/api/v1/connectors/{connector_id}/google-drive/connection"
)

// ServedRoutes is every route of this package as "METHOD path".
func ServedRoutes() []string {
	return []string{
		"GET " + ConnectionPath, "POST " + ConnectPath, "GET " + AddPath, "GET " + ReauthPath,
		"GET " + CallbackPath, "GET " + PickerPath, "DELETE " + DisconnectPath,
	}
}

// Public is the one route with no signed-in caller: Google's callback,
// whose proof is the attempt's cookie.
func Public() []string { return []string{"GET " + CallbackPath} }

// BodyRoutes is the one route that reads a small JSON body.
func BodyRoutes() []string { return []string{"POST " + ConnectPath} }

// Google's own addresses, which a deployment's Settings name. They are
// constants here and fields there so that a test can stand in for Google.
const (
	GoogleAuthURL   = "https://accounts.google.com/o/oauth2/auth"
	GoogleTokenURL  = "https://oauth2.googleapis.com/token"
	GoogleRevokeURL = "https://oauth2.googleapis.com/revoke"
	GoogleAboutURL  = "https://www.googleapis.com/drive/v3/about"
)

// driveFileScope is the one scope asked for.
const driveFileScope = "https://www.googleapis.com/auth/drive.file"

// Settings is what the deployment says about its Google project. A value
// may be empty: Missing then names it, and the routes answer 503.
type Settings struct {
	ClientID     string
	ClientSecret string
	// RedirectURI is the address registered at Google. Its path is
	// CallbackPath.
	RedirectURI  string
	PickerAPIKey string
	// PickerAppID is the Cloud project number. Empty, it is the number the
	// client ID begins with.
	PickerAppID string
	// FrontendURL is the website, where the callback sends the person.
	FrontendURL string
	// SecretKey seals stored tokens and the attempt's cookie.
	SecretKey string
	// Google's four addresses: the constants above, in a deployment.
	AuthURL, TokenURL, RevokeURL, AboutURL string
}

var projectNumber = regexp.MustCompile(`^(\d+)-`)

// pickerAppID is the project number the Picker grants picked files to.
func (s Settings) pickerAppID() string {
	if explicit := strings.TrimSpace(s.PickerAppID); explicit != "" {
		return explicit
	}
	if found := projectNumber.FindStringSubmatch(s.ClientID); found != nil {
		return found[1]
	}
	return ""
}

// Missing is the names of the settings still to be set, never their
// values: what an operator reads to finish setting Drive up.
func (s Settings) Missing() []string {
	missing := []string{}
	if !strings.HasSuffix(s.ClientID, ".apps.googleusercontent.com") {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_ID")
	}
	if s.ClientSecret == "" {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_SECRET")
	}
	if s.RedirectURI == "" {
		missing = append(missing, "GOOGLE_DRIVE_REDIRECT_URI")
	}
	if s.PickerAPIKey == "" {
		missing = append(missing, "GOOGLE_PICKER_API_KEY")
	}
	if s.pickerAppID() == "" {
		missing = append(missing, "GOOGLE_PICKER_APP_ID")
	}
	return missing
}

// What the routes say, and the codes the web app tells them apart by.
const (
	sessionRequired   = "This action requires an interactive session"
	connectionAbsent  = "Google Drive connection not found"
	notConfiguredCode = "google_drive_not_configured"
	notConfigured     = "Google Drive is not set up on this server"
	reconnectCode     = "google_drive_reconnect"
	reconnect         = "Google no longer accepts this connection; connect again"
	googleSilent      = "Google did not answer"
)

// The outcomes the callback hands the website, as `?google_drive=`.
const (
	outcomeConnected = "connected"
	outcomeExpired   = "google_drive_connect_expired"
	outcomeDenied    = "google_drive_oauth_denied"
	outcomeFailed    = "google_drive_oauth_failed"
	outcomeNoScope   = "google_drive_scope_missing"
	outcomeNoRefresh = "google_drive_no_refresh_token"
)

// resyncMinutes is how often a new connection's picked files are read
// again when they changed in Drive: daily.
const resyncMinutes = 1440
