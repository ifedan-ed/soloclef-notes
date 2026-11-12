package pagereader

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

// The IPv4 ranges that are not the public internet. Listed one by one
// rather than derived from netip's IsPrivate and friends, because those
// leave out carrier-grade NAT (100.64.0.0/10), the benchmarking and
// documentation ranges and everything from 240.0.0.0 up, and an address
// that is not public must be refused whether or not it has a name.
var refused4 = prefixes(
	"0.0.0.0/8",       // "this network"; 0.0.0.0 reaches the local host
	"10.0.0.0/8",      // private
	"100.64.0.0/10",   // carrier-grade NAT, also used inside clouds
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, where cloud metadata services live
	"172.16.0.0/12",   // private
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"192.88.99.0/24",  // the retired 6to4 relay
	"192.168.0.0/16",  // private
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // documentation
	"203.0.113.0/24",  // documentation
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, and broadcast
)

// IPv6 is judged the other way round: only global unicast (2000::/3) is
// public, so loopback, unique-local (fc00::/7), link-local (fe80::/10),
// multicast and NAT64 (64:ff9b::/96, which would carry a private IPv4
// address through a gateway) are refused by not being in it. What is
// refused inside it are the ranges that are not ordinary hosts.
var (
	global6  = netip.MustParsePrefix("2000::/3")
	refused6 = prefixes(
		"2001::/23",     // protocol assignments, Teredo among them: an IPv4 address in disguise
		"2001:db8::/32", // documentation
		"2002::/16",     // 6to4: an IPv4 address in disguise
		"3fff::/20",     // documentation
	)
)

func prefixes(list ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(list))
	for i, p := range list {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// PublicAddress reports whether a is an address on the public internet. It
// is the value Settings.Allow takes outside tests.
//
// An IPv4 address written as IPv6 (::ffff:127.0.0.1) is judged as the IPv4
// address it is; judged as IPv6 it would look like none of the special
// ranges. An address with a zone is an interface on this machine and is
// refused.
func PublicAddress(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	if a.Is4() {
		for _, p := range refused4 {
			if p.Contains(a) {
				return false
			}
		}
		return true
	}
	if !global6.Contains(a) {
		return false
	}
	for _, p := range refused6 {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// refusal is the guard saying no. It is a type so the reader can tell "we
// would not go there" from "it could not be reached" and say which.
type refusal struct{ host string }

func (r *refusal) Error() string {
	return fmt.Sprintf("pagereader: %s is not a public address", r.host)
}

// guard is the only way a connection is opened. It is the transport's dial
// function, so it runs for every connection of every request: the first
// one and each redirect hop that goes to a new host or port.
//
// The defect it exists to prevent is checking one answer and connecting to
// another. A name is resolved here, once; every address it has must be
// allowed; and the connection is made to those addresses, by number. The
// name is never resolved again, so a server that answers a public address
// to a check and a private one a moment later (DNS rebinding) gains
// nothing: there is no second question.
type guard struct {
	allow   func(netip.Addr) bool
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// GuardedDial is this guard as a dial function for another client that
// must reach only allowed addresses: resolve once, refuse unless every
// address is allowed, connect to those addresses by number. A model
// endpoint a person connected (soloclef_assistant/ownmodels) dials through
// it, so the page reader and that client hold one rule, written once.
//
// resolve is SystemResolve outside tests. IsRefusal tells the guard's no
// from a connection that failed.
func GuardedDial(allow func(netip.Addr) bool, resolve func(ctx context.Context, host string) ([]netip.Addr, error)) func(ctx context.Context, network, address string) (net.Conn, error) {
	return guard{allow: allow, resolve: resolve}.dial
}

// SystemResolve is the operating system's resolver, both families: what
// GuardedDial is given outside tests.
func SystemResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return systemResolve(ctx, host)
}

// IsRefusal reports whether err is, or wraps, the guard refusing an
// address.
func IsRefusal(err error) bool {
	var refused *refusal
	return errors.As(err, &refused)
}

func systemResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func (g guard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		// A number needs no resolver, and asking one would let a name
		// server answer for a host the address already fixed.
		addrs = []netip.Addr{literal}
	} else if addrs, err = g.resolve(ctx, host); err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	// All of them, not any of them: a name with one public and one private
	// address is refused whole. Trying only its public address would let
	// the same name pass here and reach inside from a reader that picks
	// differently, and no honest site is set up that way.
	for _, a := range addrs {
		if !g.allow(a) {
			return nil, &refusal{host: host}
		}
	}
	dialer := net.Dialer{Control: g.control}
	var last error
	for i, a := range addrs {
		// The time left is shared between the addresses still to try, so
		// one address that never answers cannot use up the others' turn.
		attempt, cancel := ctx, context.CancelFunc(func() {})
		if deadline, ok := ctx.Deadline(); ok {
			share := time.Until(deadline) / time.Duration(len(addrs)-i)
			attempt, cancel = context.WithTimeout(ctx, share)
		}
		conn, err := dialer.DialContext(attempt, network, net.JoinHostPort(a.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

// control runs on the socket itself, just before it connects, and asks the
// same question of the address the kernel is about to be given. dial has
// already asked; this is the check that still holds if a later change
// opens a connection some other way through this dialer.
func (g guard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return errors.New("pagereader: the connection's address is not readable")
	}
	if !g.allow(ap.Addr()) {
		return &refusal{host: ap.Addr().String()}
	}
	return nil
}
