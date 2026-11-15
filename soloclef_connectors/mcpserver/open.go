package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// Parse reads `server_config` as stored. Anything that is not this format
// is ErrUnreadable: a row is never guessed at, because a guess could send a
// key to the wrong place or send a server nothing it expects.
func Parse(raw []byte) (Stored, error) {
	var stored Stored
	if err := json.Unmarshal(raw, &stored); err != nil {
		return Stored{}, ErrUnreadable
	}
	if stored.URL == "" || (stored.Transport != StreamableHTTP && stored.Transport != SSE) {
		return Stored{}, ErrUnreadable
	}
	auth := stored.Auth
	switch auth.Type {
	case AuthNone:
	case AuthHeader:
		if auth.HeaderName == "" || auth.Secret == nil {
			return Stored{}, ErrUnreadable
		}
	case AuthBearer:
		if auth.Secret == nil {
			return Stored{}, ErrUnreadable
		}
	case AuthOAuth:
		if auth.OAuth == nil {
			return Stored{}, ErrUnreadable
		}
	default:
		return Stored{}, ErrUnreadable
	}
	return stored, nil
}

// Target is where a connection goes and what it carries, in the clear. It
// exists for the length of one connection and is never stored.
type Target struct {
	Address string
	Headers map[string]string
}

// ErrNotSignedIn is an OAuth server nobody has signed in to yet.
var ErrNotSignedIn = errors.New("mcpserver: the server needs an OAuth sign-in first")

// Open is the address and headers to connect with. It is the one place a
// stored secret is opened for a connection: the MCP client calls it as it
// connects, and nothing it returns is kept.
//
// tokens are the OAuth server's current tokens, opened by the caller (which
// may first have refreshed them, oauth.go); nil for any other kind.
func Open(seal sealedsecret.Seal, owner string, stored Stored, tokens *Tokens) (Target, error) {
	target := Target{Address: stored.URL, Headers: make(map[string]string, len(stored.Headers)+1)}
	if stored.URLSealed != "" {
		address, err := seal.Open(ownerKey(owner), stored.URLSealed)
		if err != nil {
			return Target{}, fmt.Errorf("the server's address: %w", err)
		}
		target.Address = address
	}
	for name, secret := range stored.Headers {
		value, err := seal.Open(ownerKey(owner), secret.Sealed)
		if err != nil {
			return Target{}, fmt.Errorf("the server's %q header: %w", name, err)
		}
		target.Headers[name] = value
	}
	switch stored.Auth.Type {
	case AuthHeader, AuthBearer:
		value, err := seal.Open(ownerKey(owner), stored.Auth.Secret.Sealed)
		if err != nil {
			return Target{}, fmt.Errorf("the server's key: %w", err)
		}
		if stored.Auth.Type == AuthHeader {
			target.Headers[stored.Auth.HeaderName] = value
		} else {
			target.Headers["Authorization"] = "Bearer " + value
		}
	case AuthOAuth:
		if tokens == nil || tokens.AccessToken == "" {
			return Target{}, ErrNotSignedIn
		}
		target.Headers["Authorization"] = "Bearer " + tokens.AccessToken
	}
	return target, nil
}

// OpenAddress is the server's whole address, opened when it carries a key.
// The OAuth sign-in discovers the authorization server from it.
func OpenAddress(seal sealedsecret.Seal, owner string, stored Stored) (string, error) {
	if stored.URLSealed == "" {
		return stored.URL, nil
	}
	address, err := seal.Open(ownerKey(owner), stored.URLSealed)
	if err != nil {
		return "", fmt.Errorf("the server's address: %w", err)
	}
	return address, nil
}

// SealSecret is a new sealed value and its mask: a client secret an
// authorization server issued at registration.
func SealSecret(seal sealedsecret.Seal, owner, value string) (Secret, error) {
	sealed, err := seal.Seal(ownerKey(owner), value)
	return Secret{Mask: MaskOf(value), Sealed: sealed}, err
}

// OpenTokens opens an OAuth server's stored tokens.
func OpenTokens(seal sealedsecret.Seal, owner string, oauth *StoredOAuth) (*Tokens, error) {
	if !oauth.Authorized() {
		return nil, ErrNotSignedIn
	}
	plain, err := seal.Open(ownerKey(owner), oauth.Tokens)
	if err != nil {
		return nil, fmt.Errorf("the server's sign-in: %w", err)
	}
	var tokens Tokens
	if json.Unmarshal([]byte(plain), &tokens) != nil || tokens.AccessToken == "" {
		return nil, ErrUnreadable
	}
	return &tokens, nil
}

// SealTokens is the stored text for an OAuth server's tokens.
func SealTokens(seal sealedsecret.Seal, owner string, tokens Tokens) (string, error) {
	plain, err := json.Marshal(tokens)
	if err != nil {
		return "", err
	}
	return seal.Seal(ownerKey(owner), string(plain))
}

// OpenClientSecret is an OAuth client's secret, or "" for a public client.
func OpenClientSecret(seal sealedsecret.Seal, owner string, oauth *StoredOAuth) (string, error) {
	if oauth == nil || oauth.ClientSecret == nil {
		return "", nil
	}
	secret, err := seal.Open(ownerKey(owner), oauth.ClientSecret.Sealed)
	if err != nil {
		return "", fmt.Errorf("the OAuth client secret: %w", err)
	}
	return secret, nil
}

// HeaderNameValid reports whether a header name is one HTTP accepts (an RFC
// 9110 token): a name with a space or a colon in it would be refused by the
// client at the first call, long after the form was saved.
func HeaderNameValid(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if character > 0x7e || character <= ' ' || strings.ContainsRune("\"(),/:;<=>?@[\\]{}", character) {
			return false
		}
	}
	return http.CanonicalHeaderKey(name) != ""
}
