package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file is the guard's face to the settings routes that add, edit and
// test an MCP server (internal/searchconnectors). They ask the same
// questions a turn asks, of an address a person has just typed instead of
// one read from a row, and they must get the same answers: a server that
// can be saved and then never called, or called and never saved, is two
// rules where there is one.

// The two kinds of server as they are stored under `server_origin`.
const (
	OriginSite   = originSite
	OriginMember = originMember
)

// SiteHosts is the hosts a site server may reach: the administrators' saved
// list (site_settings.mcp_allowed_hosts, as stored), or whenUnset while no
// list has been saved.
func SiteHosts(stored []byte, whenUnset []string) []string {
	if hosts, saved := readSiteHosts(stored); saved {
		return hosts
	}
	return whenUnset
}

// ReachableHosts is the hosts a connection of this origin to this address
// may reach: the site's list for a site server, the address's own host for
// a member's, and nothing when a member's address does not name a public
// domain.
func ReachableHosts(origin, address string, siteHosts []string) []string {
	return reachableHosts(origin, address, siteHosts)
}

// Host is the normalised host an address names, or "" when it names none.
// It is what decides whether a masked key sent back with an edit may stand
// for the stored one: only when the edit still points at the same host.
func Host(address string) string {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return ""
	}
	return normalizeHost(parsed.Hostname())
}

// Verify is the whole guard without the connection, for the moment a server
// is saved: the address is https on the default port, names a host on the
// list, and that name resolves, now, to public addresses only. The call-time
// dialer asks all of it again, so this is not what keeps a connection off a
// private address; it is what tells the person at the form, instead of the
// model in the middle of a turn. The error is a *Refusal.
func (p PublicInternet) Verify(ctx context.Context, address string, hosts []string) error {
	host, err := checkAddress(address, hosts)
	if err != nil {
		return err
	}
	addresses, err := p.Resolve(ctx, host)
	if err != nil || len(addresses) == 0 {
		return refuse("host %q could not be resolved", host)
	}
	for _, candidate := range addresses {
		if !publicAddress(candidate) {
			return refuse("host %q resolves to an address that is not public", host)
		}
	}
	return nil
}

// ProbedTool is one tool a server listed when it was tested.
type ProbedTool struct {
	Name        string
	Description string
	// InputSchema is the tool's parameter schema as JSON, always an object.
	InputSchema json.RawMessage
}

// Probe connects to a server once, to answer the form's "test" button:
// handshake, list the tools, hang up. Nothing is stored and no cache is
// touched. All four fields are required.
type Probe struct {
	// Network is the same guard a turn's connections go through.
	Network    Network
	ClientName string
	// Timeout bounds the whole test, connecting included.
	Timeout time.Duration
	// MostTools is where the listing stops, as a turn's does.
	MostTools int
}

// Check reports what is missing. Called once at start-up.
func (p Probe) Check() error {
	switch {
	case p.Network == nil:
		return errors.New("mcptools: Probe.Network is not set")
	case strings.TrimSpace(p.ClientName) == "":
		return errors.New("mcptools: Probe.ClientName is not set")
	case p.Timeout <= 0:
		return errors.New("mcptools: Probe.Timeout must be positive")
	case p.MostTools <= 0:
		return errors.New("mcptools: Probe.MostTools must be positive")
	}
	return nil
}

// Tools lists what the server at address offers, reaching only hosts. The
// error may hold the library's text and is for the log; the route answers
// every failure in the same words so that a test cannot be used to map what
// this process can reach.
func (p Probe) Tools(ctx context.Context, address string, headers map[string]string, hosts []string) ([]ProbedTool, error) {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	// A Source holding only what open reads: the guard and the client.
	source := &Source{
		network: p.Network,
		client: mcp.NewClient(
			&mcp.Implementation{Name: p.ClientName, Version: clientVersion},
			&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
		),
	}
	opened, err := source.open(ctx, target{address: address, headers: headers}, hosts)
	if err != nil {
		return nil, err
	}
	defer opened.close()

	found := []ProbedTool{}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := opened.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, listed := range page.Tools {
			schema, err := json.Marshal(listed.InputSchema)
			if err != nil {
				schema = nil
			}
			found = append(found, ProbedTool{Name: listed.Name, Description: listed.Description, InputSchema: objectSchema(schema)})
			if len(found) == p.MostTools {
				return found, nil
			}
		}
		cursor = page.NextCursor
		// The last page, or a server that hands back a cursor it already
		// gave, which would otherwise be followed for ever.
		if cursor == "" || seen[cursor] {
			return found, nil
		}
		seen[cursor] = true
	}
}

// IsPublicAddress reports whether the public internet can route to address:
// not loopback, private, link-local, carrier-grade NAT, a documentation or
// reserved range, or an IPv6 form that carries one of those inside it. It is
// the one answer to that question in this tier, exported for the other
// places a person names a host this process then connects to (a mailbox's
// IMAP server, internal/mailaccounts).
func IsPublicAddress(address netip.Addr) bool {
	return publicAddress(address)
}
