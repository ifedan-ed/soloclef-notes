package ownmodels

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
)

// Egress makes the HTTP client a person's connection is called with. It is
// the one place that decides where such a connection may go (package
// comment, "The one essential control").
type Egress struct {
	// Resolve returns every address a host name has:
	// pagereader.SystemResolve outside tests.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// AllowedHosts is the operator's list of hosts a connection may reach
	// although they resolve inside this server's network
	// (Store.AllowedHosts). It is asked on every client, so a host the
	// operator removes is refused from the next call on, with no restart.
	AllowedHosts func(ctx context.Context) ([]string, error)
	// ConnectTimeout bounds one TCP connect and the TLS handshake after it.
	// The answer itself is bounded by the call's context: a reply streams
	// for as long as it is long.
	ConnectTimeout time.Duration
}

// Check reports what is missing. Called once at start-up.
func (e Egress) Check() error {
	switch {
	case e.Resolve == nil:
		return errors.New("ownmodels: Egress.Resolve is not set; pagereader.SystemResolve is the value outside tests")
	case e.AllowedHosts == nil:
		return errors.New("ownmodels: Egress.AllowedHosts is not set")
	case e.ConnectTimeout <= 0:
		return errors.New("ownmodels: Egress.ConnectTimeout must be positive")
	}
	return nil
}

// Refusal is the guard saying no to an address: its scheme, its form, or
// what it resolves to. Reason is a sentence for the person at the form; it
// names the host they typed and nothing this server knows about its own
// network.
type Refusal struct {
	Reason string
}

func (r *Refusal) Error() string { return "ownmodels: the address is refused: " + r.Reason }

// IsRefusal reports whether err is the guard refusing an address, either
// before a request (Refusal) or as the connection was being made (the page
// reader's guard, inside the transport's error).
func IsRefusal(err error) bool {
	var refused *Refusal
	return errors.As(err, &refused) || pagereader.IsRefusal(err)
}

// RefusalSentence is the sentence for a refused address: the Refusal's own
// when it is one, else the one for an address that resolved inside the
// network.
func RefusalSentence(err error, base string) string {
	var refused *Refusal
	if errors.As(err, &refused) {
		return refused.Reason
	}
	return insideSentence(hostOf(base))
}

func insideSentence(host string) string {
	return "The address " + host + " points inside this server's network, which a connection may not reach. Use the endpoint's public address."
}

// CheckAddress is the part of the guard that needs no network: an http or
// https address with a host, no user name or password in it, and nothing
// after the path. It returns the address as it will be called, without a
// trailing slash. Called when a connection is saved, so that a mistake is
// told then, and again before every call.
func CheckAddress(base string) (string, error) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return "", &Refusal{Reason: "An OpenAI-compatible connection needs the endpoint's base URL"}
	}
	parsed, err := url.Parse(trimmed)
	switch {
	case err != nil:
		return "", &Refusal{Reason: "The base URL is not a web address"}
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", &Refusal{Reason: "The base URL must start with http:// or https://"}
	case parsed.User != nil:
		// A key in the address would be logged by every proxy on the way;
		// it belongs in the key field, where it is sealed.
		return "", &Refusal{Reason: "Put the key in the key field, not in the base URL"}
	case parsed.Hostname() == "":
		return "", &Refusal{Reason: "The base URL has no host"}
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return "", &Refusal{Reason: "The base URL ends at its path: no ? or # part"}
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// Client is the client to call base with. It refuses, with a *Refusal, an
// address CheckAddress refuses; an address that resolves inside the network
// is refused when the connection is made (IsRefusal).
func (e Egress) Client(ctx context.Context, base string) (*http.Client, error) {
	if _, err := CheckAddress(base); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(strings.TrimSpace(base))
	host := normalizeHost(parsed.Hostname())
	allowed, err := e.AllowedHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("ownmodels: reading the operator's allowed hosts: %w", err)
	}
	allow := pagereader.PublicAddress
	for _, entry := range allowed {
		if normalizeHost(entry) == host {
			// The operator said this host may be inside the network: a
			// model server on their own machine. Still not an address
			// that is no host at all.
			allow = routable
			break
		}
	}
	dial := pagereader.GuardedDial(allow, e.Resolve)
	timeout := e.ConnectTimeout
	return &http.Client{
		Transport: &http.Transport{
			// Stated, not left to the zero value's meaning: a proxy from the
			// environment would do the connecting, and nothing above would
			// hold.
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				// The client is for one host. A connection to any other,
				// however it was asked for, is refused here.
				dialled, _, err := net.SplitHostPort(address)
				if err != nil || normalizeHost(dialled) != host {
					return nil, &Refusal{Reason: "The connection may reach only " + host}
				}
				connecting, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				return dial(connecting, network, address)
			},
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: timeout,
			MaxIdleConns:        4,
			IdleConnTimeout:     90 * time.Second,
		},
		// A redirect is answered as it is, never followed: following one
		// would carry the request, and the key in it, to wherever the
		// endpoint pointed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// routable is what an allow-listed host may resolve to: any address of a
// host, private ones included, but not "no address" or a group of them.
func routable(a netip.Addr) bool {
	return a.IsValid() && a.Zone() == "" && !a.Unmap().IsUnspecified() && !a.IsMulticast()
}

// normalizeHost makes two spellings of one host equal: no surrounding
// space, no trailing dot, lower case, no IPv6 brackets.
func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	return strings.ToLower(strings.TrimRight(host, "."))
}

// hostOf is the host of an address, for a sentence.
func hostOf(base string) string {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Hostname() == "" {
		return "you gave"
	}
	return parsed.Hostname()
}

// HostOf is the host of a connection's address as the menus show it.
func HostOf(base string) string {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
