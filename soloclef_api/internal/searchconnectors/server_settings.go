package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// This file is what a person may say about an MCP server and what is done
// with it before a row is written: read the form strictly, seal its secrets
// (a mask sent back keeps the stored secret, sealed as it is), and decide
// where the server may be. Creating, editing and testing a server all pass
// through it, so the three cannot disagree about what a server is.
//
// The form is the "Add MCP server" form's fields, one for one:
//
//	{
//	  "url": "https://mcp.example.com/mcp",
//	  "transport": "streamable-http" | "sse",
//	  "auth": {"type": "none"}
//	        | {"type": "header", "header_name": "X-API-Key", "value": "…"}
//	        | {"type": "bearer", "token": "…"}
//	        | {"type": "oauth", "client_id": "…", "client_secret": "…"},
//	  "headers": {"X-Extra": "…"}
//	}
//
// What is stored is mcpserver.Stored: every secret sealed under the owner,
// each with its mask, and the address sealed when its query carries a key.
// What a read shows is publicServer (server_public.go): the same shape with
// masks for values.

// What the settings are refused with. Each is a sentence for the person at
// the form.
const (
	stdioRefused      = "Local (stdio) MCP servers are not supported. Use a remote server URL."
	addressRequired   = "A server URL is required"
	headersNotText    = "Each header needs a name and a text value"
	headerNameBad     = "A header name may hold letters, digits and - _ . ! # $ % & ' * + ^ ` | ~ only"
	headerTwice       = "Each header may be given once"
	headerValueBad    = "A header value must be text on one line"
	tooManyHeaders    = "At most 20 extra headers"
	headerIsAuth      = "Remove the extra header that repeats the authentication header"
	transportRefused  = "The transport must be streamable-http or sse"
	authRequired      = "Choose how the server is signed in to: none, an API key header, a bearer token or OAuth"
	authKindRefused   = "The authentication must be none, header, bearer or oauth"
	authKeyRequired   = "Enter the API key"
	authHeaderNeeded  = "Enter the header the API key goes in"
	authTokenRequired = "Enter the bearer token"
	settingsUnstored  = "These settings contain a character that cannot be saved"
	memberNeedsDomain = "Use the server's public address (a full domain name)"
	keyAgainInAddress = "Enter the key in the server URL again"
	keyAgain          = "Enter the API key or token again"
	clientSecretAgain = "Enter the client secret again"
	nameTaken         = "You already have an MCP server with this name"
	connectionTaken   = "You already have a connection with this name"
	serverUneditable  = "This MCP server's stored settings cannot be edited. Remove it and add it again."
	notAServerHere    = "Add MCP servers from Connectors > MCP server"
	settingsNotObject = "The server settings must be an object"
)

// The bounds of a form: generous for any real server, small enough that a
// row stays a row.
const (
	mostHeaders    = 20
	mostValueBytes = 8 << 10
)

// readServerSettings reads the settings out of a request, or says in a
// sentence why they are refused. It is strict: a key it does not know is
// refused by name, so a typo is not silently a server without its header.
func readServerSettings(body *pythonjson.Object) (mcpserver.Settings, string) {
	settings := mcpserver.Settings{Headers: map[string]string{}}
	authSeen := false
	for _, key := range body.Keys() {
		value, _ := body.Get(key)
		switch key {
		case "url":
			address, isText := value.(string)
			if !isText || strings.TrimSpace(address) == "" {
				return mcpserver.Settings{}, addressRequired
			}
			settings.Address = strings.TrimSpace(address)
		case "transport":
			transport, isText := value.(string)
			switch {
			case isText && transport == "stdio":
				return mcpserver.Settings{}, stdioRefused
			case !isText || (transport != mcpserver.StreamableHTTP && transport != mcpserver.SSE):
				return mcpserver.Settings{}, transportRefused
			}
			settings.Transport = transport
		case "auth":
			auth, refusal := readAuth(value)
			if refusal != "" {
				return mcpserver.Settings{}, refusal
			}
			settings.Auth, authSeen = auth, true
		case "headers":
			if value == nil {
				continue
			}
			headers, isObject := value.(*pythonjson.Object)
			if !isObject {
				return mcpserver.Settings{}, headersNotText
			}
			if headers.Len() > mostHeaders {
				return mcpserver.Settings{}, tooManyHeaders
			}
			for _, name := range headers.Keys() {
				text, isText := headers.GetOrNil(name).(string)
				if !isText {
					return mcpserver.Settings{}, headersNotText
				}
				if refusal := checkHeader(name, text); refusal != "" {
					return mcpserver.Settings{}, refusal
				}
				for seen := range settings.Headers {
					if strings.EqualFold(seen, name) {
						return mcpserver.Settings{}, headerTwice
					}
				}
				settings.Headers[name] = text
			}
		case "command", "args", "env":
			// A local server is a command run inside this process's
			// container.
			return mcpserver.Settings{}, stdioRefused
		default:
			return mcpserver.Settings{}, "Unknown server setting: " + printable(key)
		}
	}
	switch {
	case settings.Address == "":
		return mcpserver.Settings{}, addressRequired
	case settings.Transport == "":
		return mcpserver.Settings{}, transportRefused
	case !authSeen:
		return mcpserver.Settings{}, authRequired
	case cannotBeStored(settings.Address):
		return mcpserver.Settings{}, settingsUnstored
	}
	// The authentication's own header may not be given twice: which of the
	// two would be sent would depend on map order.
	authHeader := settings.Auth.HeaderName
	if settings.Auth.Kind == mcpserver.AuthBearer || settings.Auth.Kind == mcpserver.AuthOAuth {
		authHeader = "Authorization"
	}
	for name := range settings.Headers {
		if authHeader != "" && strings.EqualFold(name, authHeader) {
			return mcpserver.Settings{}, headerIsAuth
		}
	}
	return settings, ""
}

// readAuth reads the `auth` object.
func readAuth(value any) (mcpserver.Auth, string) {
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return mcpserver.Auth{}, authRequired
	}
	text := func(key string) (string, string) {
		held, present := object.Get(key)
		if !present || held == nil {
			return "", ""
		}
		value, isText := held.(string)
		if !isText {
			return "", "The '" + key + "' setting must be text"
		}
		if cannotBeStored(value) || strings.ContainsAny(value, "\r\n") || len(value) > mostValueBytes {
			return "", headerValueBad
		}
		return strings.TrimSpace(value), ""
	}
	kind, refusal := text("type")
	if refusal != "" {
		return mcpserver.Auth{}, refusal
	}
	allowed := map[string]bool{"type": true}
	auth := mcpserver.Auth{Kind: mcpserver.AuthKind(kind)}
	switch auth.Kind {
	case mcpserver.AuthNone:
	case mcpserver.AuthHeader:
		allowed["header_name"], allowed["value"] = true, true
		name, refusal := text("header_name")
		if refusal != "" {
			return mcpserver.Auth{}, refusal
		}
		if name == "" {
			return mcpserver.Auth{}, authHeaderNeeded
		}
		if !mcpserver.HeaderNameValid(name) {
			return mcpserver.Auth{}, headerNameBad
		}
		secret, refusal := text("value")
		if refusal != "" {
			return mcpserver.Auth{}, refusal
		}
		if secret == "" {
			return mcpserver.Auth{}, authKeyRequired
		}
		auth.HeaderName, auth.Secret = name, secret
	case mcpserver.AuthBearer:
		allowed["token"] = true
		token, refusal := text("token")
		if refusal != "" {
			return mcpserver.Auth{}, refusal
		}
		if token == "" {
			return mcpserver.Auth{}, authTokenRequired
		}
		auth.Secret = token
	case mcpserver.AuthOAuth:
		allowed["client_id"], allowed["client_secret"] = true, true
		id, refusal := text("client_id")
		if refusal != "" {
			return mcpserver.Auth{}, refusal
		}
		secret, refusal := text("client_secret")
		if refusal != "" {
			return mcpserver.Auth{}, refusal
		}
		auth.ClientID, auth.ClientSecret = id, secret
	default:
		return mcpserver.Auth{}, authKindRefused
	}
	for _, key := range object.Keys() {
		if !allowed[key] {
			return mcpserver.Auth{}, "Unknown authentication setting: " + printable(key)
		}
	}
	return auth, ""
}

// checkHeader is one extra header's refusal, or "".
func checkHeader(name, value string) string {
	switch {
	case strings.TrimSpace(name) == "":
		return headersNotText
	case !mcpserver.HeaderNameValid(name):
		return headerNameBad
	case cannotBeStored(value) || strings.ContainsAny(value, "\r\n") || len(value) > mostValueBytes || !utf8.ValidString(value):
		return headerValueBad
	}
	return ""
}

// printable is a key as it may be repeated in a refusal: cut short, and
// without anything that is not plain text.
func printable(key string) string {
	var out strings.Builder
	for _, character := range key {
		if out.Len() >= 40 {
			break
		}
		if character >= ' ' && character != 0x7f && character != 0xfffd {
			out.WriteRune(character)
		}
	}
	return out.String()
}

// storedServerOf is the stored settings of a server's row, opening
// nothing. found is false for a row whose settings are not the sealed form
// (a damaged row): no mask can stand for anything in it.
func storedServerOf(connection Connection) (mcpserver.Stored, bool) {
	config, isObject := connection.Config.(*pythonjson.Object)
	if !isObject {
		return mcpserver.Stored{}, false
	}
	server, present := config.Get("server_config")
	if !present {
		return mcpserver.Stored{}, false
	}
	raw, err := pythonjson.Marshal(server)
	if err != nil {
		return mcpserver.Stored{}, false
	}
	stored, err := mcpserver.Parse(raw)
	return stored, err == nil
}

// sealSettings is the stored form of a person's settings: their secrets
// sealed, and every mask standing for the secret stored under it in
// previous (nil for a server not saved yet). refusal is the sentence for a
// mask that stands for nothing.
func sealSettings(seal sealedsecret.Seal, ownerID string, settings mcpserver.Settings, previous *mcpserver.Stored) (stored mcpserver.Stored, refusal string, err error) {
	stored, err = mcpserver.Build(seal, ownerID, settings, previous)
	var masked *mcpserver.Refusal
	if errors.As(err, &masked) {
		switch masked.Part {
		case "header":
			return mcpserver.Stored{}, "Enter the value for the '" + printable(masked.Field) + "' header again", nil
		case "url":
			return mcpserver.Stored{}, keyAgainInAddress, nil
		case "client_secret":
			return mcpserver.Stored{}, clientSecretAgain, nil
		}
		return mcpserver.Stored{}, keyAgain, nil
	}
	return stored, "", err
}

// Destinations decides whether an MCP server may be saved at an address:
// https, the default port, a named host among hosts, resolving to public
// addresses only. mcptools.PublicInternet in a deployment, which is the
// same guard every call to the server then goes through. A refusal is a
// *mcptools.Refusal.
type Destinations interface {
	Verify(ctx context.Context, address string, hosts []string) error
}

// ServerPolicy is who provides a server and where it may therefore be.
//
// A server an administrator adds is a site server and must be on the
// site's list of allowed hosts. Anyone else's is their own, on their own
// key, and may be on any public host; it may then reach that host and
// nothing else. Which of the two a server is, is decided here when it is
// created, stored in its row, and never taken from a request, so nobody
// can promote a row of their own.
type ServerPolicy struct {
	Standing Standing
	// SiteHostsWhenUnset is the allowed hosts while the operator has saved
	// no list. The chat is given the same one (internal/chat), so a server
	// that can be saved can be called.
	SiteHostsWhenUnset []string
	Destinations       Destinations
	// Seal seals a server's secrets before they are stored, and opens them
	// for a test (sealedsecret under the MCP server purpose).
	Seal sealedsecret.Seal
}

// Standing reads what the policy decides from.
type Standing interface {
	// IsAdministrator is whether the account administers the site: its
	// own `is_superuser` flag, as everywhere else in this tier.
	IsAdministrator(ctx context.Context, userID string) (bool, error)
	// SiteAllowedHosts is site_settings.mcp_allowed_hosts as stored, or nil
	// when the operator has saved none.
	SiteAllowedHosts(ctx context.Context) ([]byte, error)
}

// administers reports whether the caller's servers are site servers.
func (p ServerPolicy) administers(ctx context.Context, userID string) (bool, error) {
	return p.Standing.IsAdministrator(ctx, userID)
}

// originForNew is the origin a server the caller adds now is given.
func (p ServerPolicy) originForNew(ctx context.Context, userID string) (string, error) {
	administers, err := p.administers(ctx, userID)
	if err != nil {
		return "", err
	}
	if administers {
		return mcptools.OriginSite, nil
	}
	return mcptools.OriginMember, nil
}

// siteHosts is the site's allowed hosts as they stand now.
func (p ServerPolicy) siteHosts(ctx context.Context) ([]string, error) {
	stored, err := p.Standing.SiteAllowedHosts(ctx)
	if err != nil {
		return nil, err
	}
	return mcptools.SiteHosts(stored, p.SiteHostsWhenUnset), nil
}

// hostsFor is the hosts a server of this origin at this address may reach.
// The refusal is the one case with its own sentence: a member's server
// that is not named by a public domain.
func (p ServerPolicy) hostsFor(ctx context.Context, origin, address string) (hosts []string, refusal string, err error) {
	if origin == mcptools.OriginMember {
		hosts = mcptools.ReachableHosts(origin, address, nil)
		if len(hosts) == 0 && mcptools.Host(address) != "" {
			return nil, memberNeedsDomain, nil
		}
		return hosts, "", nil
	}
	site, err := p.siteHosts(ctx)
	return site, "", err
}

// oauthHosts is the rule each OAuth address of a server of this origin is
// held to: the very rule its connections are (a person's server reaches
// each address's own public host; a site server the site's list).
func (p ServerPolicy) oauthHosts(ctx context.Context, origin string) (mcptools.HostsFor, error) {
	if origin == mcptools.OriginMember {
		return mcptools.OAuthHosts(origin, nil), nil
	}
	site, err := p.siteHosts(ctx)
	if err != nil {
		return nil, err
	}
	return mcptools.OAuthHosts(origin, site), nil
}

// admit is the destination check at save time. refusal is a sentence for
// the form; it names the host at most, never the address, which can hold a
// key.
func (p ServerPolicy) admit(ctx context.Context, origin, address string) (refusal string, err error) {
	hosts, refusal, err := p.hostsFor(ctx, origin, address)
	if err != nil || refusal != "" {
		return refusal, err
	}
	if err := p.Destinations.Verify(ctx, address, hosts); err != nil {
		var refused *mcptools.Refusal
		if errors.As(err, &refused) {
			return "This MCP server cannot be used: " + refused.Reason, nil
		}
		return "", err
	}
	return "", nil
}

// originOf is the origin a row holds. Anything not marked a member's is a
// site server, as the chat reads it (mcptools): rows from before the two
// kinds existed carry no marker and were all the site's.
func originOf(connection Connection) string {
	if config, isObject := connection.Config.(*pythonjson.Object); isObject {
		if origin, _ := config.GetOrNil("server_origin").(string); origin == mcptools.OriginMember {
			return mcptools.OriginMember
		}
	}
	return mcptools.OriginSite
}

// serverColumn is the stored form of a server's settings.
func serverColumn(stored mcpserver.Stored) ([]byte, error) {
	return json.Marshal(stored)
}
