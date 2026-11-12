package mcptools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Network makes the HTTP client one MCP connection uses. It is the one place
// that decides where a connection may go.
//
// It is an interface for one reason: the tests run MCP servers on loopback,
// which the real implementation exists to refuse. A deployment sets
// PublicInternet. There is no switch that weakens PublicInternet; the only
// way round it is to hand Settings a different Network, which is a line
// somebody has to write and a reviewer can see.
type Network interface {
	// Client returns a client for a connection to address that may reach
	// only the named hosts. It refuses, with a *Refusal, an address the
	// connection should never be opened to.
	Client(address string, hosts []string) (*http.Client, error)
}

// Refusal says a server's address, or what its name resolves to, is not
// somewhere a connection may go. Its text is for the log: the model and the
// person are told only that the tool is not available, so the answer cannot
// be used to map what is reachable from here.
type Refusal struct {
	Reason string
}

func (r *Refusal) Error() string { return "the MCP server is refused: " + r.Reason }

func refuse(format string, args ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, args...)}
}

// PublicInternet is the production Network: https on the default port, to a
// named host on the list, at an address checked to be publicly routable.
//
// The check and the connection are one step. The dialer resolves the name,
// refuses unless every answer is public, and then connects to one of the
// addresses it just checked, by number. The name is never resolved a second
// time, so an answer that changes between the check and the connect (DNS
// rebinding) cannot land on a private address. TLS still verifies the
// certificate against the name. Redirects are not followed, so a public
// server cannot send the connection, and the key in its headers, somewhere
// else; and proxy environment variables are ignored, because a proxy would
// do the connecting and none of the above would hold.
type PublicInternet struct {
	// Resolve returns every address a host name has. SystemResolve is the
	// operating system's resolver.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// ConnectTimeout bounds one TCP connect, and the TLS handshake after it.
	ConnectTimeout time.Duration

	// dial replaces the TCP connect in this package's tests, which check
	// which address the dialer chose without reaching it. Nil is the real
	// connect. It is unexported so that nothing outside the package can set
	// it.
	dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// SystemResolve asks the operating system's resolver for a host's addresses,
// both families.
func SystemResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// Check reports what is missing. Called once at start-up.
func (p PublicInternet) Check() error {
	switch {
	case p.Resolve == nil:
		return errors.New("mcptools: PublicInternet.Resolve is not set")
	case p.ConnectTimeout <= 0:
		return errors.New("mcptools: PublicInternet.ConnectTimeout must be positive")
	}
	return nil
}

func (p PublicInternet) Client(address string, hosts []string) (*http.Client, error) {
	if _, err := checkAddress(address, hosts); err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(hosts))
	for _, host := range hosts {
		allowed[normalizeHost(host)] = true
	}
	return newClient(p.pinnedDial(allowed), p.ConnectTimeout), nil
}

// httpsPort is the only port a connection is made to.
const httpsPort = "443"

// checkAddress is the part of the guard that needs no network: the address
// must be https, carry no credentials, use the default port, and name (not
// number) a host on the list. It returns that host.
func checkAddress(address string, hosts []string) (string, error) {
	if strings.TrimSpace(address) == "" {
		return "", refuse("no address")
	}
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		// The parse error would repeat the address, which can hold a key.
		return "", refuse("the address is not a valid URL")
	}
	if parsed.Scheme != "https" {
		return "", refuse("the address is not https")
	}
	if parsed.User != nil {
		return "", refuse("the address contains credentials")
	}
	host := normalizeHost(parsed.Hostname())
	if host == "" {
		return "", refuse("the address has no host")
	}
	if port := parsed.Port(); port != "" && port != httpsPort {
		return "", refuse("the address names a port other than %s", httpsPort)
	}
	// A number instead of a name skips the list and the certificate check
	// that goes with a name.
	if _, err := netip.ParseAddr(host); err == nil {
		return "", refuse("the address is an IP address, not a name")
	}
	for _, allowed := range hosts {
		if normalizeHost(allowed) == host {
			return host, nil
		}
	}
	return "", refuse("host %q is not one this server may reach", host)
}

// normalizeHost is how two spellings of one host name are made equal: no
// surrounding space, no trailing dot, lower case.
func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(host), "."))
}

// pinnedDial is the connect step: allowed host and port, resolve, refuse
// unless every address is public, connect to a checked address.
func (p PublicInternet) pinnedDial(allowed map[string]bool) func(ctx context.Context, network, address string) (net.Conn, error) {
	connect := p.dial
	if connect == nil {
		connect = (&net.Dialer{Timeout: p.ConnectTimeout}).DialContext
	}
	return func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, refuse("the connection's address cannot be read")
		}
		name := normalizeHost(host)
		// Asked again here although Client asked already: this is the
		// question for every connection the client ever makes, not only
		// the first, and it is what stops a redirect being followed by a
		// caller that forgot not to.
		if port != httpsPort || !allowed[name] {
			return nil, refuse("host %q is not one this server may reach", name)
		}
		addresses, err := p.Resolve(ctx, name)
		if err != nil || len(addresses) == 0 {
			// Unresolvable is refused, not waved through: otherwise a
			// broken resolver would be a way past the guard.
			return nil, refuse("host %q could not be resolved", name)
		}
		checked := make([]netip.Addr, 0, len(addresses))
		for _, candidate := range addresses {
			// All of them, not any of them: a name with one public and
			// one private answer is refused whole.
			if !publicAddress(candidate) {
				return nil, refuse("host %q resolves to an address that is not public", name)
			}
			checked = append(checked, candidate.Unmap())
		}
		// IPv4 first: a container often has no IPv6 route, and trying
		// that family first would cost a timeout on every connection.
		slices.SortStableFunc(checked, func(a, b netip.Addr) int {
			switch {
			case a.Is4() == b.Is4():
				return 0
			case a.Is4():
				return -1
			}
			return 1
		})
		var last error
		for _, target := range checked {
			conn, err := connect(ctx, "tcp", netip.AddrPortFrom(target, 443).String())
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, fmt.Errorf("the MCP server is unreachable: %w", last)
	}
}

// newClient is the HTTP client every MCP connection uses, whatever dials
// for it: no redirects, no proxy from the environment, and no whole-request
// timeout, because a tool's answer may stream and each call carries its own
// deadline in its context.
func newClient(dial func(ctx context.Context, network, address string) (net.Conn, error), handshake time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dial,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: handshake,
			// One server, one turn: there is nothing to pool beyond the
			// connection or two the protocol's POST and its stream use.
			MaxIdleConns:    4,
			IdleConnTimeout: 90 * time.Second,
		},
		// The response to a redirect is handed back as it is. The MCP
		// client then sees a status that is not a success and fails the
		// request, which is the outcome wanted.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// notPublic are the ranges that are not loopback, private or link-local (the
// standard library names those) and still are not the public internet:
// carrier-grade NAT, documentation and benchmarking ranges, the reserved
// blocks, and the IPv6 ranges that carry an IPv4 address inside them, which
// could be a private one.
var notPublic = func() []netip.Prefix {
	ranges := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32",
		"2002::/16", "3fff::/20", "5f00::/16",
	}
	out := make([]netip.Prefix, len(ranges))
	for i, text := range ranges {
		out[i] = netip.MustParsePrefix(text)
	}
	return out
}()

// publicAddress reports whether the public internet can route to address.
// An IPv4 address written as IPv6 (::ffff:127.0.0.1) is judged as the IPv4
// address it is: read as IPv6 it is none of loopback, private or link-local.
func publicAddress(address netip.Addr) bool {
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() {
		return false
	}
	for _, block := range notPublic {
		if block.Contains(address) {
			return false
		}
	}
	return true
}

// The two kinds of server, by who added it. A site server was added by an
// administrator and runs on the site's key; a member's was added by a
// person on their own.
const (
	originSite   = "site"
	originMember = "member"
)

// privateEndings are name endings that only mean something inside a network.
var privateEndings = []string{".local", ".localhost", ".internal", ".lan", ".home.arpa"}

// reachableHosts is the hosts a server's connection may reach.
//
// A site server may reach the hosts on the administrators' list. A member's
// server may reach its own host and nothing else, provided that host is a
// public domain name: one with a dot and without a private ending, so the
// name of a neighbouring container is refused before any lookup.
func reachableHosts(origin, address string, siteHosts []string) []string {
	if origin != originMember {
		return siteHosts
	}
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return nil
	}
	host := normalizeHost(parsed.Hostname())
	if !strings.Contains(host, ".") {
		return nil
	}
	for _, ending := range privateEndings {
		if strings.HasSuffix(host, ending) {
			return nil
		}
	}
	return []string{host}
}
