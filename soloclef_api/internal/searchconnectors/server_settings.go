package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
)

// This file is what a person may say about an MCP server and what is done
// with it before a row is written: read the settings strictly, put back
// the keys the form only ever saw masked, and decide where the server may
// be. Creating, editing and testing a server all pass through it, so the
// three cannot disagree about what a server is.

// serverSettings is an MCP server as the form describes it: a remote
// address, the headers that carry its key, and how to speak to it.
type serverSettings struct {
	Address   string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Transport string            `json:"transport"`
}

// What the settings are refused with. Each is a sentence for the person at
// the form.
const (
	stdioRefused      = "Local (stdio) MCP servers are not supported. Use a remote server URL."
	addressRequired   = "A server URL is required"
	headersNotText    = "Each header needs a name and a text value"
	transportRefused  = "The transport must be streamable-http, http or sse"
	settingsUnstored  = "These settings contain a character that cannot be saved"
	memberNeedsDomain = "Use the server's public address (a full domain name)"
	keyAgainInAddress = "Enter the key in the server URL again"
	nameTaken         = "You already have an MCP server with this name"
	connectionTaken   = "You already have a connection with this name"
	serverUneditable  = "This MCP server's stored settings cannot be edited. Remove it and add it again."
	notAServerHere    = "Add MCP servers from Connectors > MCP server"
	settingsNotObject = "The server settings must be an object"
)

// transportDefault is what a form that names no transport means.
const transportDefault = "streamable-http"

// remoteTransports are the three spellings of "a remote server".
var remoteTransports = map[string]bool{"streamable-http": true, "http": true, "sse": true}

// readServerSettings reads the settings out of a request, or says in a
// sentence why they are refused.
//
// It is strict: a key it does not know is refused by name, so a typo is
// not silently a server without its header. Two things are let through and
// dropped, because clients in use send them: the three fields of a local
// (stdio) server when they are empty, and a `name`.
func readServerSettings(body *pythonjson.Object) (serverSettings, string) {
	settings := serverSettings{Headers: map[string]string{}, Transport: transportDefault}
	for _, key := range body.Keys() {
		value, _ := body.Get(key)
		switch key {
		case "url":
			address, isText := value.(string)
			if !isText || strings.TrimSpace(address) == "" {
				return serverSettings{}, addressRequired
			}
			settings.Address = strings.TrimSpace(address)
		case "headers":
			if value == nil {
				continue
			}
			headers, isObject := value.(*pythonjson.Object)
			if !isObject {
				return serverSettings{}, headersNotText
			}
			for _, name := range headers.Keys() {
				held, _ := headers.Get(name)
				text, isText := held.(string)
				if !isText || strings.TrimSpace(name) == "" {
					return serverSettings{}, headersNotText
				}
				settings.Headers[name] = text
			}
		case "transport":
			transport, isText := value.(string)
			if isText && transport == "stdio" {
				return serverSettings{}, stdioRefused
			}
			if !isText || !remoteTransports[transport] {
				return serverSettings{}, transportRefused
			}
			settings.Transport = transport
		case "command", "args", "env":
			// A local server is a command run inside this process's
			// container. Empty, the field is an old form's blank.
			if !isEmptyServerField(value) {
				return serverSettings{}, stdioRefused
			}
		case "name":
		default:
			return serverSettings{}, "Unknown server setting: " + printable(key)
		}
	}
	if settings.Address == "" {
		return serverSettings{}, addressRequired
	}
	// Text the column takes and cannot give back (public_config.go).
	if cannotBeStored(settings.Address) {
		return serverSettings{}, settingsUnstored
	}
	for name, value := range settings.Headers {
		if cannotBeStored(name) || cannotBeStored(value) {
			return serverSettings{}, settingsUnstored
		}
	}
	return settings, ""
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

// isMasked reports whether a value is one this package's reads produced
// (secrets.go): the four bullets, with or without the last four characters.
func isMasked(value string) bool {
	return strings.HasPrefix(value, mask)
}

// storedServer is the settings a server's row holds now, read for one
// purpose: to stand in for the values an edit sent back masked.
type storedServer struct {
	address string
	headers map[string]string
}

// storedServerOf reads them from a row's config. A row without readable
// settings has none, and then no masked value can be restored.
func storedServerOf(connection Connection) storedServer {
	stored := storedServer{headers: map[string]string{}}
	config, isObject := connection.Config.(*pythonjson.Object)
	if !isObject {
		return stored
	}
	server, isObject := config.GetOrNil("server_config").(*pythonjson.Object)
	if !isObject {
		return stored
	}
	stored.address, _ = server.GetOrNil("url").(string)
	if headers, isObject := server.GetOrNil("headers").(*pythonjson.Object); isObject {
		for _, name := range headers.Keys() {
			if text, isText := headers.GetOrNil(name).(string); isText {
				stored.headers[name] = text
			}
		}
	}
	return stored
}

// restoreMasked puts the stored value back wherever the request carries a
// masked one, or says which value has to be typed again.
//
// A read never shows a key, so a form that was opened on a saved server
// holds bullets where the key is, and saving it sends them back. They stand
// for the stored key on one condition: the edit still points at the same
// host. Without it a person could point a saved server at a host of their
// choosing and have the stored key sent there. For a server that is not
// saved yet (stored is nil) nothing can be restored, and a masked value is
// refused.
func restoreMasked(settings serverSettings, stored *storedServer) (serverSettings, string) {
	sameHost := stored != nil && mcptools.Host(settings.Address) != "" &&
		mcptools.Host(settings.Address) == mcptools.Host(stored.address)

	restored := serverSettings{Address: settings.Address, Transport: settings.Transport,
		Headers: make(map[string]string, len(settings.Headers))}
	for name, value := range settings.Headers {
		if !isMasked(value) {
			restored.Headers[name] = value
			continue
		}
		held, found := "", false
		if sameHost {
			// Header names are not case-sensitive, and a form may have
			// tidied the spelling.
			for storedName, storedValue := range stored.headers {
				if strings.EqualFold(storedName, name) {
					held, found = storedValue, true
					break
				}
			}
		}
		if !found {
			return serverSettings{}, "Enter the value for the '" + printable(name) + "' header again"
		}
		restored.Headers[name] = held
	}

	// The same for a key carried in the address's query string.
	parts, err := urlSplit(settings.Address)
	if err != nil {
		// Not an address at all; the destination check says so in its own
		// words.
		return restored, ""
	}
	pairs := parseQuery(parts.query)
	anyMasked := false
	for _, pair := range pairs {
		anyMasked = anyMasked || isMasked(pair.value)
	}
	if !anyMasked {
		return restored, ""
	}
	if !sameHost {
		return serverSettings{}, keyAgainInAddress
	}
	storedParts, err := urlSplit(stored.address)
	if err != nil {
		return serverSettings{}, keyAgainInAddress
	}
	storedValues := map[string]string{}
	for _, pair := range parseQuery(storedParts.query) {
		if _, seen := storedValues[pair.name]; !seen {
			storedValues[pair.name] = pair.value
		}
	}
	for index, pair := range pairs {
		if !isMasked(pair.value) {
			continue
		}
		held, found := storedValues[pair.name]
		if !found {
			return serverSettings{}, "Enter the '" + printable(pair.name) + "' URL value again"
		}
		pairs[index].value = held
	}
	query, err := encodeQuery(pairs)
	if err != nil {
		return serverSettings{}, keyAgainInAddress
	}
	parts.query = query
	restored.Address = urlUnsplit(parts)
	return restored, ""
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
	stored, err := p.Standing.SiteAllowedHosts(ctx)
	if err != nil {
		return nil, "", err
	}
	return mcptools.SiteHosts(stored, p.SiteHostsWhenUnset), "", nil
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
func serverColumn(settings serverSettings) ([]byte, error) {
	return json.Marshal(settings)
}
