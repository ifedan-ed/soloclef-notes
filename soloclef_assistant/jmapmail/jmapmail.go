// Package jmapmail talks to a mail server over JMAP (RFC 8620, the core,
// and RFC 8621, mail): find the session, list mailboxes, search, read a
// message or a whole thread, fetch an attachment, list the identities a
// mailbox may send as, and send one message the owner confirmed.
//
// It is written against Stalwart, whose JMAP is served on every HTTP
// listener at /.well-known/jmap (a redirect to /jmap/session), and keeps to
// the standard so another JMAP server works too.
//
// # What it never does on its own
//
// Reading changes nothing: Email/get does not touch `$seen`, and no method
// this package calls while reading sets a keyword, moves or destroys a
// message. Send is the one method that writes, and it is called only by the
// send guard (soloclef_assistant/maildrafts) with a message the owner
// confirmed on a card; the chat's tools are never given it.
//
// # Where this process will connect
//
// A server's address is typed by a person, so the HTTP client every
// request is made with comes from a Network, which in a deployment reaches
// only that server's host, over https, at an address checked to be public
// (mcptools.PublicInternet). Every URL the server hands back (the API, the
// downloads) must be on the origin the person named; one elsewhere is
// refused, so a server cannot point this process, and the credential in
// its headers, at somebody else. Redirects are followed by hand, at most
// a few, and only on that origin.
//
// # Bounded
//
// A mail server is somebody else's program. Every response is read up to
// a limit, a message's text is fetched up to a limit, a thread is read up
// to a number of messages, and every conversation has a deadline.
package jmapmail

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// The capabilities this package uses.
const (
	capabilityCore       = "urn:ietf:params:jmap:core"
	capabilityMail       = "urn:ietf:params:jmap:mail"
	capabilitySubmission = "urn:ietf:params:jmap:submission"
)

// Credential is how a request signs in: an app password (HTTP Basic, the
// account name and the password) or an OAuth access token (Bearer).
// Exactly one of the two is set.
type Credential struct {
	Username, Password string
	AccessToken        string
}

// authorize puts the credential on a request.
func (c Credential) authorize(request *http.Request) {
	if c.AccessToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.AccessToken)
		return
	}
	request.SetBasicAuth(c.Username, c.Password)
}

// Network makes the HTTP client a server is reached with. The production
// one reaches only the server's own host, over https, at a public address
// (see the package comment); the tests hand in one that reaches a server on
// loopback, which the production one exists to refuse.
type Network interface {
	Client(serverURL string) (*http.Client, error)
}

// Client is the JMAP client. Every field is required.
type Client struct {
	Network Network
	// Timeout bounds one conversation: finding the session and the calls
	// that follow it.
	Timeout time.Duration
	// AttachmentTimeout bounds a conversation that moves a file: an
	// attachment downloaded to be read or forwarded, or a send that
	// uploads files first. Bytes take longer to come and go than a
	// message's text, as an IMAP attachment's do (imapmail).
	AttachmentTimeout time.Duration
}

// Check reports what is missing. Called once at start-up.
func (c Client) Check() error {
	if c.Network == nil || c.Timeout <= 0 || c.AttachmentTimeout <= 0 {
		return errors.New("jmapmail: Network, a positive Timeout and a positive AttachmentTimeout are required")
	}
	return nil
}

// bounded is ctx with the conversation's deadline.
func (c Client) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.Timeout)
}

// boundedForFiles is ctx with the deadline of a conversation that moves a
// file.
func (c Client) boundedForFiles(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.AttachmentTimeout)
}

// Failure is a server that could not be reached, refused the sign-in or
// refused a request. Message is for the person whose mailbox it is, and
// never carries a credential or a whole server reply.
type Failure struct {
	Message string
	// SignIn says the server refused the credential itself: the password
	// or token is wrong, expired or revoked. A caller marks the mailbox to
	// be reconnected.
	SignIn bool
	// Elsewhere is set when the session's API is on another origin than
	// the one asked: it is that origin, the address the server calls
	// itself. This package never follows it (see the package comment); a
	// caller that can show it is the same server may connect with it
	// (jmapaccounts.Mail.ConnectAppPassword).
	Elsewhere string
}

func (f *Failure) Error() string { return f.Message }

// What a person is told. One sentence for every failure before a server
// has answered as JMAP, so a test of a typed address cannot be used to map
// what is listening where.
const (
	unreachable     = "Could not connect to this mail server"
	notJMAP         = "This address does not offer JMAP mail. Check the server's address (for Stalwart, the address of its web interface)."
	signInRefused   = "The mail server refused the sign-in. Check the username and app password, or connect the mailbox again."
	noMail          = "This sign-in has no mail account on the server."
	brokenAnswer    = "The mail server's answer could not be read"
	messageNotFound = "No message with this id is in the mailbox"
)

var (
	errUnreachable   = &Failure{Message: unreachable}
	errNotJMAP       = &Failure{Message: notJMAP}
	errSignInRefused = &Failure{Message: signInRefused, SignIn: true}
	errNoMail        = &Failure{Message: noMail}
	errBroken        = &Failure{Message: brokenAnswer}
	errNotFound      = &Failure{Message: messageNotFound}
)

// IsSignInRefused reports whether err is the server refusing the
// credential.
func IsSignInRefused(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.SignIn
}

// Address is one mailbox of a header (From, To, Cc...).
type Address struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// String is the address as a person writes it: `Ada <ada@example.org>`,
// or the bare address without a name.
func (a Address) String() string {
	if strings.TrimSpace(a.Name) == "" {
		return a.Email
	}
	return a.Name + " <" + a.Email + ">"
}

// joined is a header's addresses on one line.
func joined(addresses []Address) string {
	parts := make([]string, 0, len(addresses))
	for _, address := range addresses {
		parts = append(parts, address.String())
	}
	return strings.Join(parts, ", ")
}
