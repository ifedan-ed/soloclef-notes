// Package mailer is the provider-agnostic contract the mail senders deliver
// through, and the three failures they can report.
//
// app/mailer/protocol.py's port, in Go's spelling: the errors are sentinels to
// match against rather than classes to catch, and the drain loop matches them
// in the order sender.py's except clauses are written. Nothing above this
// package knows whether a message left over SMTP or over an HTTP API.
package mailer

import (
	"context"
	"errors"
)

// Mailer sends one message, or returns one of the three errors below.
//
// One Mailer belongs to one mail sender and is used for one message at a time,
// so a transport may keep its connection open between sends; Close lets it go
// when the sender stops.
type Mailer interface {
	Send(ctx context.Context, message Message) error
	Close() error
}

// Message is one transactional email, fully rendered and addressed. Sender
// empty means the deployment's default From, which the transport applies: the
// same reason app/mailer/protocol.py carries it on the message and not on the
// transport, so one connection can serve features that address their mail
// differently.
type Message struct {
	To          string
	Subject     string
	TextBody    string
	HTMLBody    string
	Sender      string
	ReplyTo     string
	Attachments []Attachment
	// Carried and ignored, as it is in Python: no transport reads it and
	// nothing dedupes on it. soloclef_mail/AGENTS.md says why that was left
	// alone rather than fixed inside a port.
	IdempotencyKey string
}

// Attachment is one file on a message; the transport encodes it for the wire.
type Attachment struct {
	Filename    string
	Content     []byte
	ContentType string
}

var (
	// ErrUnavailable is transient, or our own trouble -- never the recipient's
	// fault. Connect failures, timeouts, SMTP 4xx, an API 429 or 5xx, a
	// refused sender. A message that fails this way is tried again, and the
	// person who asked must never be told their address was rejected.
	ErrUnavailable = errors.New("the mail server could not be reached")

	// ErrMisconfigured is our own settings: credentials refused, sender not
	// allowed. Still never the recipient's fault, but sending the same message
	// again cannot succeed until someone fixes the settings, so it is not
	// retried.
	ErrMisconfigured = errors.New("the mail settings are wrong")

	// ErrRejected is permanent: the provider refused this message in the
	// conversation, or refused the recipient with a 5xx. A hard bounce arrives
	// after an accept, so this never catches every bad address -- a sender
	// promises "sent", not "delivered".
	ErrRejected = errors.New("the mail server refused the message")
)
