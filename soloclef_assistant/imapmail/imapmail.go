// Package imapmail reads a mailbox over IMAP: sign in, list its folders,
// search a folder and read one message. It never changes a mailbox.
//
// Two tiers use it. The API signs in to test a mailbox a person is adding
// (soloclef_api/internal/mailaccounts), and the chat's mail tools read one
// for a turn (mailtools), in the API and in the worker's automation runs.
// It is here, beside those tools, so that there is one client and one idea
// of where this process may connect.
//
// # Read-only, by construction
//
// A folder is opened with EXAMINE, which the server itself holds
// read-only, and a message is fetched with BODY.PEEK, which does not mark
// it as read. No command this package can send stores a flag, moves,
// deletes or appends: there is no such method to call by mistake.
//
// # Where this process will connect
//
// A mailbox's host is typed by a person. So the host must resolve to
// public addresses only, and the connection goes to the address that was
// checked, not to whatever the name answers a moment later (connect.go).
// Until a server has been reached and has spoken IMAP, every failure is
// one sentence, so a mailbox cannot be used to find out what is listening
// inside this network.
//
// # Bounded
//
// Everything read from a server is bounded: a line, a literal, how many
// folders are listed, how many messages a search returns and how much of
// one message is fetched. A mail server is somebody else's program.
package imapmail

// Settings is one mailbox as a connection is made to it.
type Settings struct {
	Host string
	Port int
	// UseTLS is TLS from the first byte (port 993). Without it the
	// connection is upgraded with STARTTLS before anything is sent: a
	// password never crosses the network in the clear either way.
	UseTLS   bool
	Username string
	Password string
	// AccessToken, when set, signs in with XOAUTH2 instead of the
	// password: a mailbox connected through Microsoft.
	AccessToken string
}

// ConnectionError is a mailbox that could not be signed in to or read.
// Message is for the person whose mailbox it is.
type ConnectionError struct{ Message string }

func (e *ConnectionError) Error() string { return e.Message }

// unreachable is the one sentence for every failure before a server has
// been reached and has spoken IMAP: refused as private, unresolvable,
// closed, timed out, not TLS, not IMAP. One sentence, so that a test
// cannot tell a person what is listening where.
const unreachable = "Could not connect to this mail server"

var errUnreachable = &ConnectionError{Message: unreachable}

// errBroken is a conversation that ended in the middle of an answer: the
// server closed, timed out, or sent more than this client will hold.
var errBroken = &ConnectionError{Message: "The mail server stopped answering"}
