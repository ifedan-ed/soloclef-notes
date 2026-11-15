// Package mcpserver is how an MCP server's settings are kept in its row:
// what is stored in the clear, what is sealed, and the one place each
// sealed part is opened again.
//
// A server's settings live in its connection's `config.server_config`. Every
// part that can carry a credential is sealed (sealedsecret, under the MCP
// server purpose, bound to the owner):
//
//   - each header value, the authentication header's included;
//   - the address, when its query carries a key (`?api_key=…`);
//   - an OAuth client secret, and the OAuth tokens.
//
// Beside each sealed value is its mask ("••••" and the last four
// characters), computed when it was sealed, so the settings screen can show
// which key is stored without anything being opened to show it. The address
// is stored twice when it carries a key: masked in `url`, which every read
// and every decision about the host uses, and whole in `url_sealed`.
//
// Nothing here talks to a network or a database. The API tier seals a
// person's settings with Build; the MCP client opens them with Open at the
// moment it connects; the schema's data migration sealed the rows that were
// written in the clear. One package is one format for all three.
package mcpserver

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

// The two transports a remote server is spoken to with. A local (stdio)
// server would be a command run inside this deployment's container and is
// never accepted.
const (
	StreamableHTTP = "streamable-http"
	SSE            = "sse"
)

// AuthKind is how a server is told who is calling.
type AuthKind string

// The four kinds the form offers.
const (
	AuthNone   AuthKind = "none"
	AuthHeader AuthKind = "header"
	AuthBearer AuthKind = "bearer"
	AuthOAuth  AuthKind = "oauth"
)

// Mask is what a secret is shown as.
const Mask = "••••"

// MaskOf is a secret's mask: the four bullets, and the last four characters
// of anything twelve or more long, which is enough for a person to tell two
// keys apart and too little to guess one from.
func MaskOf(secret string) string {
	if utf8.RuneCountInString(secret) >= 12 {
		runes := []rune(secret)
		return Mask + string(runes[len(runes)-4:])
	}
	return Mask
}

// IsMasked reports whether a value a form sent back is a mask this package
// produced, which stands for the stored secret rather than being one.
func IsMasked(value string) bool { return strings.HasPrefix(value, Mask) }

// Secret is one sealed value and its mask.
type Secret struct {
	Mask   string `json:"mask"`
	Sealed string `json:"sealed"`
}

// Stored is `server_config` as it is kept.
type Stored struct {
	// URL is the address with every key-bearing query value masked. It is
	// the address itself when nothing in it is a key.
	URL string `json:"url"`
	// URLSealed is the whole address, sealed; empty when URL is the whole
	// address.
	URLSealed string            `json:"url_sealed,omitempty"`
	Transport string            `json:"transport"`
	Auth      StoredAuth        `json:"auth"`
	Headers   map[string]Secret `json:"headers,omitempty"`
}

// StoredAuth is the authentication part.
type StoredAuth struct {
	Type AuthKind `json:"type"`
	// HeaderName is the header an API key goes in (AuthHeader only).
	HeaderName string `json:"header_name,omitempty"`
	// Secret is the API key (AuthHeader) or the bearer token (AuthBearer).
	Secret *Secret `json:"secret,omitempty"`
	// OAuth is the sign-in's state (AuthOAuth only).
	OAuth *StoredOAuth `json:"oauth,omitempty"`
}

// StoredOAuth is an OAuth sign-in: the client this deployment is to the
// authorization server, where that server's endpoints are, and the tokens
// once a person has signed in.
type StoredOAuth struct {
	// ClientID is the client: typed by the person, or issued by the
	// server's dynamic registration (DynamicRegistration).
	ClientID            string  `json:"client_id,omitempty"`
	ClientSecret        *Secret `json:"client_secret,omitempty"`
	DynamicRegistration bool    `json:"dynamic_registration"`
	// ClientAuth is how the client proves itself at the token endpoint:
	// "none" (a public client, PKCE alone), "client_secret_basic" or
	// "client_secret_post". Chosen when the sign-in starts, from what the
	// authorization server says it supports.
	ClientAuth string `json:"client_auth,omitempty"`
	// The authorization server and its endpoints, from discovery when the
	// sign-in was started. Empty until then.
	Issuer                string `json:"issuer,omitempty"`
	AuthorizationEndpoint string `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string `json:"token_endpoint,omitempty"`
	RevocationEndpoint    string `json:"revocation_endpoint,omitempty"`
	// Resource is the server's canonical address, sent with every
	// authorization and token request (RFC 8707) so a token is issued for
	// this server and no other.
	Resource string `json:"resource,omitempty"`
	Scope    string `json:"scope,omitempty"`
	// Tokens is the sealed JSON of a Tokens; empty until signed in.
	Tokens string `json:"tokens_sealed,omitempty"`
	// ExpiresAt is when the access token runs out, in Unix seconds; zero
	// when the server did not say.
	ExpiresAt int64 `json:"expires_at,omitempty"`
}

// Tokens are an OAuth sign-in's tokens, as sealed in StoredOAuth.Tokens.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// Authorized reports whether a person has signed in.
func (o *StoredOAuth) Authorized() bool { return o != nil && o.Tokens != "" }

// Settings is a server as a person describes it on the form, secrets in the
// clear or as the masks a read showed them as.
type Settings struct {
	Address   string
	Transport string
	Auth      Auth
	// Headers are the extra headers, sent beside the authentication.
	Headers map[string]string
}

// Auth is the form's authentication part.
type Auth struct {
	Kind AuthKind
	// HeaderName and Secret for AuthHeader; Secret alone for AuthBearer.
	HeaderName string
	Secret     string
	// ClientID and ClientSecret for AuthOAuth, both optional: a server
	// that registers clients dynamically needs neither.
	ClientID     string
	ClientSecret string
}

// sensitiveWords are the words that mark a query parameter as a key: a
// parameter whose name contains one, in any case, has its value masked and
// the address sealed.
var sensitiveWords = []string{"key", "token", "secret", "auth", "pass", "sig", "cred"}

// IsSensitiveName reports whether a query parameter's name marks a key.
func IsSensitiveName(name string) bool {
	lowered := strings.ToLower(name)
	for _, word := range sensitiveWords {
		if strings.Contains(lowered, word) {
			return true
		}
	}
	return false
}

// MaskAddress is the address with each key-bearing query value masked, and
// whether anything was. Only those values change: every other byte of the
// address is kept as typed, so an address with no key comes back exactly.
func MaskAddress(address string) (string, bool) {
	base, query, hasQuery := strings.Cut(address, "?")
	if !hasQuery {
		return address, false
	}
	query, fragment, hasFragment := strings.Cut(query, "#")
	pairs := strings.Split(query, "&")
	masked := false
	for index, pair := range pairs {
		name, value, hasValue := strings.Cut(pair, "=")
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if !hasValue || value == "" || !IsSensitiveName(decoded) {
			continue
		}
		plain, err := url.QueryUnescape(value)
		if err != nil {
			plain = value
		}
		pairs[index] = name + "=" + MaskOf(plain)
		masked = true
	}
	shown := base + "?" + strings.Join(pairs, "&")
	if hasFragment {
		shown += "#" + fragment
	}
	return shown, masked
}

// Host is an address's host, lowered; "" when it has none.
func Host(address string) string {
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// ownerKey is the owner as it is bound into a seal: a uuid in lower case.
// The database compares uuids by value, so one account reaches its rows
// whichever case its id arrives in (a token may spell it in capitals); the
// seal compares bytes, so it is given one spelling.
func ownerKey(owner string) string { return strings.ToLower(strings.TrimSpace(owner)) }

// ErrUnreadable is a server's stored settings that are not this format: a
// damaged row, or one written before its secrets were sealed.
var ErrUnreadable = errors.New("mcpserver: the stored server settings are not readable")
