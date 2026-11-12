package nextcloud

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// Account is one Nextcloud account as a connection stores it.
type Account struct {
	// BaseURL is the server, normalized: scheme, host and any sub-path.
	BaseURL string
	// Username is the Nextcloud user id: it names the WebDAV files root.
	Username string
	// LoginName is what the app password was issued to. It differs from the
	// user id when people sign in with an email address.
	LoginName   string
	AppPassword string
}

// ReconnectError is a stored connection that cannot be used: incomplete, not
// decryptable with this server's key, or made for another server. Its text
// is safe to show; the remedy is always to connect again.
type ReconnectError struct{ Message string }

func (e *ReconnectError) Error() string { return e.Message }

// NormalizeServerURL is "https://host[/subpath]" with no trailing slash,
// and false unless the text is a plain http(s) URL with a host and nothing
// else: no query, no fragment, no user info. A WebDAV URL copied from
// Nextcloud's settings names the same server, so "/remote.php…" is cut.
func NormalizeServerURL(raw string) (string, bool) {
	text := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(text)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || strings.Contains(text, "?") || strings.Contains(text, "#") {
		return "", false
	}
	path := parsed.Path
	if marker := strings.Index(path, "/remote.php"); marker >= 0 {
		path = path[:marker]
	}
	return parsed.Scheme + "://" + parsed.Host + strings.TrimRight(path, "/"), true
}

// Credentials turns a connection's stored config into an account.
type Credentials struct {
	// SecretKey is the deployment's SECRET_KEY, which the Python tier
	// encrypts every stored token with.
	SecretKey string
	// ServerURL is the one Nextcloud this deployment connects to
	// (NEXTCLOUD_BASE_URL), normalized. People never type an address, so a
	// connection row is only used when it names this server: a row cannot
	// point the app password, or this tier's requests, anywhere else.
	ServerURL string
}

// NewCredentials checks the two settings. Both are required: without the
// key no stored password can be read, and without the server no connection
// may be used.
func NewCredentials(secretKey, serverURL string) (Credentials, error) {
	if secretKey == "" {
		return Credentials{}, errors.New("nextcloud: the secret key that connection passwords are encrypted with is not set")
	}
	server, ok := NormalizeServerURL(serverURL)
	if !ok {
		return Credentials{}, errors.New("nextcloud: the Nextcloud server address must be a plain http(s) URL")
	}
	return Credentials{SecretKey: secretKey, ServerURL: server}, nil
}

// Account reads a connection's config.
func (c Credentials) Account(config []byte) (Account, error) {
	var stored struct {
		ServerURL   string `json:"server_url"`
		Username    string `json:"username"`
		LoginName   string `json:"login_name"`
		AppPassword string `json:"app_password"`
		// Encrypted is true on every row the sign-in flow wrote. Rows saved
		// earlier from a pasted app password hold it in the clear and say
		// so by leaving this out.
		Encrypted bool `json:"_token_encrypted"`
	}
	if err := json.Unmarshal(config, &stored); err != nil {
		return Account{}, &ReconnectError{"This Nextcloud connection is incomplete; connect again"}
	}
	secret := stored.AppPassword
	if stored.Encrypted && secret != "" {
		opened, err := storedtoken.Open(c.SecretKey, secret)
		if err != nil {
			// The key changed, or the row was damaged. Either way the
			// password is gone and a new sign-in makes a new one.
			return Account{}, &ReconnectError{"This Nextcloud connection can no longer be read; connect again"}
		}
		secret = opened
	}
	if stored.ServerURL == "" || stored.Username == "" || secret == "" {
		return Account{}, &ReconnectError{"This Nextcloud connection is incomplete; connect again"}
	}
	if stored.ServerURL != c.ServerURL {
		return Account{}, &ReconnectError{"This Nextcloud connection is for another server; connect again"}
	}
	login := stored.LoginName
	if login == "" {
		login = stored.Username
	}
	return Account{BaseURL: stored.ServerURL, Username: stored.Username, LoginName: login, AppPassword: secret}, nil
}
