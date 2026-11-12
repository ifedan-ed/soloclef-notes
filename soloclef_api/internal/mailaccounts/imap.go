package mailaccounts

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/imapmail"
)

// The IMAP client is soloclef_assistant/imapmail: the chat's mail tools
// read a mailbox with the same client these routes test one with, so that
// there is one guard on where this process connects. The names below are
// that package's, kept under the names these routes were written with.

// Settings is one mailbox as a connection is made to it.
type Settings = imapmail.Settings

// ConnectionError is a mailbox that could not be signed in to. Message is
// for the person at the form.
type ConnectionError = imapmail.ConnectionError

// IMAP is the real Prober.
type IMAP = imapmail.IMAP

// Prober signs in to a mailbox and counts its folders. IMAP in a
// deployment; a stand-in in the route tests.
type Prober interface {
	Folders(ctx context.Context, settings Settings) (int, error)
	// Reachable is the save-time half: the host resolves, now, to public
	// addresses only.
	Reachable(ctx context.Context, host string) bool
}
