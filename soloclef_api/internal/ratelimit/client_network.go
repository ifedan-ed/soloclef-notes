package ratelimit

import "net/netip"

// ipv6ClientBits is how much of an IPv6 address names one client: its /64.
//
// A /64 is the smallest network IPv6 hands out -- a home, a phone, a VPS each
// get at least one -- and the owner of one can send from any of its 2^64
// addresses without asking anybody. Counted by the whole address, a single
// machine could take a fresh budget for every request and the password limit
// (ten a minute) would stop nobody. Counted by the /64, it costs the same to
// dodge as an IPv4 address does: getting another network.
const ipv6ClientBits = 64

// countedAs is the client a request is counted against, from the address
// ClientIP named: an IPv4 address as it is (an IPv4-mapped IPv6 address is
// the IPv4 address it maps), an IPv6 address as its /64. Anything that is not
// an address is counted as the text it is, so that a deployment whose proxy
// writes something odd is still limited rather than let through.
func countedAs(client string) string {
	address, err := netip.ParseAddr(client)
	if err != nil {
		return client
	}
	// A zone names a link on this host, not a different client.
	address = address.WithZone("").Unmap()
	if address.Is4() {
		return address.String()
	}
	network, err := address.Prefix(ipv6ClientBits)
	if err != nil {
		return client
	}
	return network.String()
}
