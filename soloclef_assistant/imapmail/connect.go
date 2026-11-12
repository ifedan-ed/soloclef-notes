package imapmail

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
)

// IMAP is the client: every method is one short conversation, signed in
// and signed out, with the connection pinned to an address checked to be
// public.
type IMAP struct {
	// Resolve returns every address a host name has.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// Timeout bounds one whole conversation, connecting included.
	Timeout time.Duration
	// AttachmentTimeout bounds a conversation that fetches an attachment
	// whole (ReadAttachment): megabytes, not a message's text.
	AttachmentTimeout time.Duration

	// dial and roots replace the TCP connect and the trusted certificates
	// in this package's tests, which run a mail server on loopback. Nil is
	// the real connect and the system's roots. Unexported, so nothing
	// outside the package can weaken the guard.
	dial  func(ctx context.Context, network, address string) (net.Conn, error)
	roots *x509.CertPool
}

// Check reports what is missing. Called once at start-up.
func (i IMAP) Check() error {
	switch {
	case i.Resolve == nil:
		return errors.New("imapmail: IMAP.Resolve is not set")
	case i.Timeout <= 0 || i.AttachmentTimeout <= 0:
		return errors.New("imapmail: IMAP.Timeout and IMAP.AttachmentTimeout must both be positive")
	}
	return nil
}

// pinned is the address a connection to host goes to: every address the
// name has must be public, and the first (IPv4 before IPv6, because a
// container often has no IPv6 route) is the one connected to. The name is
// not resolved again, so an answer that changes between this check and
// the connect (DNS rebinding) cannot land on a private address.
func (i IMAP) pinned(ctx context.Context, host string) (netip.Addr, bool) {
	host = strings.TrimSpace(host)
	if literal, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return literal.Unmap(), mcptools.IsPublicAddress(literal)
	}
	addresses, err := i.Resolve(ctx, host)
	if err != nil || len(addresses) == 0 {
		// Unresolvable is refused, not waved through.
		return netip.Addr{}, false
	}
	checked := make([]netip.Addr, 0, len(addresses))
	for _, candidate := range addresses {
		// All of them, not any of them.
		if !mcptools.IsPublicAddress(candidate) {
			return netip.Addr{}, false
		}
		checked = append(checked, candidate.Unmap())
	}
	slices.SortStableFunc(checked, func(a, b netip.Addr) int {
		switch {
		case a.Is4() == b.Is4():
			return 0
		case a.Is4():
			return -1
		}
		return 1
	})
	return checked[0], true
}

// Reachable is the save-time half of the guard: the host resolves, now,
// to public addresses only.
func (i IMAP) Reachable(ctx context.Context, host string) bool {
	_, public := i.pinned(ctx, host)
	return public
}

// signedIn opens a conversation with the mailbox and signs in. The caller
// ends it with end, which says LOGOUT (a session left open counts against
// the mailbox's connection limit) and closes the connection. The context
// it returns carries the conversation's deadline.
func (i IMAP) signedIn(ctx context.Context, settings Settings) (talk *session, end func(), err error) {
	return i.signedInFor(ctx, settings, i.Timeout)
}

// signedInFor is signedIn with a conversation of at most timeout.
func (i IMAP) signedInFor(ctx context.Context, settings Settings, timeout time.Duration) (talk *session, end func(), err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	fail := func(err error) (*session, func(), error) {
		cancel()
		return nil, nil, err
	}
	// A line break in a name or a password would end the LOGIN command and
	// begin another of the sender's choosing.
	for _, text := range []string{settings.Username, settings.Password, settings.AccessToken} {
		// U+0001 separates the fields of an XOAUTH2 sign-in.
		if strings.ContainsAny(text, "\r\n\x00\x01") {
			return fail(&ConnectionError{Message: "The username and password must not contain line breaks"})
		}
	}
	address, public := i.pinned(ctx, settings.Host)
	if !public || settings.Port < 1 || settings.Port > 65535 {
		return fail(errUnreachable)
	}
	connect := i.dial
	if connect == nil {
		connect = (&net.Dialer{}).DialContext
	}
	conn, err := connect(ctx, "tcp", netip.AddrPortFrom(address, uint16(settings.Port)).String())
	if err != nil {
		return fail(errUnreachable)
	}
	fail = func(err error) (*session, func(), error) {
		conn.Close()
		cancel()
		return nil, nil, err
	}
	if deadline, bounded := ctx.Deadline(); bounded {
		_ = conn.SetDeadline(deadline)
	}

	// The certificate is checked against the name the person typed, though
	// the connection went to a number.
	secure := &tls.Config{ServerName: strings.TrimSpace(settings.Host), MinVersion: tls.VersionTLS12}
	if i.roots != nil {
		secure.RootCAs = i.roots
	}
	if settings.UseTLS {
		wrapped := tls.Client(conn, secure)
		if wrapped.HandshakeContext(ctx) != nil {
			return fail(errUnreachable)
		}
		talk = newSession(wrapped)
		if !talk.greeted() {
			return fail(errUnreachable)
		}
	} else {
		talk = newSession(conn)
		if !talk.greeted() {
			return fail(errUnreachable)
		}
		if ok, _ := talk.command(0, nil, "STARTTLS"); !ok {
			return fail(errUnreachable)
		}
		wrapped := tls.Client(conn, secure)
		if wrapped.HandshakeContext(ctx) != nil {
			return fail(errUnreachable)
		}
		talk = newSession(wrapped)
	}

	// From here the destination is a public mail server that was reached,
	// so its own reason for refusing a sign-in is useful to the person and
	// tells them nothing about this network.
	var (
		ok     bool
		reason string
	)
	if settings.AccessToken != "" {
		ok, reason = talk.command(0, func(response reply) {
			// A refused token is answered with a challenge that holds the
			// reason; the empty reply ends the exchange, and the server's
			// tagged NO follows.
			if strings.HasPrefix(response.text, "+") {
				_, _ = talk.conn.Write([]byte("\r\n"))
			}
		}, "AUTHENTICATE XOAUTH2 "+xoauth2(settings.Username, settings.AccessToken))
	} else {
		ok, reason = talk.command(0, nil, "LOGIN "+quoted(settings.Username)+" "+quoted(settings.Password))
	}
	if !ok {
		if reason == "" {
			return fail(errUnreachable)
		}
		return fail(&ConnectionError{Message: "The mail server refused the sign-in: " + reason})
	}
	return talk, func() {
		talk.command(0, nil, "LOGOUT")
		conn.Close()
		cancel()
	}, nil
}

// xoauth2 is the initial response of an XOAUTH2 sign-in: the address and
// the bearer token, base64 of `user=<address>^Aauth=Bearer <token>^A^A`.
func xoauth2(username, accessToken string) string {
	return base64.StdEncoding.EncodeToString([]byte("user=" + username + "\x01auth=Bearer " + accessToken + "\x01\x01"))
}

// quoted is an IMAP quoted string. Sign-in names and passwords are sent
// this way and never as a literal: the checks above have already refused
// what a quoted string cannot hold, and some servers take no literal
// before a sign-in.
func quoted(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
}
