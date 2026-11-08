package smtp

import (
	"context"
	"fmt"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer/mime"
)

// Mailer sends over SMTP, keeping one session open across sends.
type Mailer struct {
	settings Settings
	boundary mime.Boundary

	mu      sync.Mutex
	session *session
}

// New refuses bad settings at start, and hands back the warning the Python
// constructor logs so the caller's logger writes it rather than this package
// owning one.
func New(settings Settings) (*Mailer, string, error) {
	warning, err := settings.Validate()
	if err != nil {
		return nil, "", err
	}
	return &Mailer{
		settings: settings,
		boundary: mime.RandomBoundary,
		session:  &session{settings: settings},
	}, warning, nil
}

// Sessions is how many SMTP sessions this mailer has opened: one per sender
// plus one per hundred messages, rather than one per message.
func (m *Mailer) Sessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.session.Opened
}

// Send delivers one message. One at a time: the session is a conversation and
// two messages interleaved in it is two broken messages.
func (m *Mailer) Send(ctx context.Context, message mailer.Message) error {
	sender := message.Sender
	if sender == "" {
		sender = m.settings.From
	}
	wire, err := mime.Build(message, sender, m.boundary)
	if err != nil {
		// Python builds the MIME outside its try, so this is not one of the
		// port's three failures: it is a message that cannot be built, which
		// the queue drops rather than retries.
		return err
	}
	// The envelope addresses, not the headers they came from: a non-ASCII
	// display name is fine -- it goes out as an encoded word -- and it is the
	// address smtplib tests.
	if err := ascii(envelopeAddress(sender), envelopeAddress(message.To)); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deliver(ctx, sender, message.To, wire)
}

func (m *Mailer) deliver(ctx context.Context, sender, recipient, wire string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", mailer.ErrUnavailable, err)
	}
	client, err := m.session.ready()
	if err != nil {
		return err
	}
	m.session.deadline(ctx)

	if err := client.Mail(envelopeAddress(sender)); err != nil {
		return m.afterRefusal(err, senderRefused(err, sender))
	}
	if err := client.Rcpt(envelopeAddress(recipient)); err != nil {
		return m.afterRefusal(err, recipientRefused(err))
	}
	data, err := client.Data()
	if err != nil {
		return m.afterRefusal(err, refusal(err, "DATA"))
	}
	if _, err := data.Write([]byte(wire)); err != nil {
		m.session.drop()
		return unreachable(err)
	}
	if err := data.Close(); err != nil {
		return m.afterRefusal(err, refusal(err, "the end of DATA"))
	}
	m.session.sent()
	return nil
}

// afterRefusal keeps the session when the server answered and drops it when
// the failure was the connection: a broken pipe or a timeout leaves the
// session in a state nobody can describe, and the next message would be
// written into it.
func (m *Mailer) afterRefusal(cause, mapped error) error {
	if responseCode(cause) == 0 {
		m.session.drop()
		return unreachable(cause)
	}
	m.session.keepAfterRefusal()
	return mapped
}

// Close lets the session go when the mail sender stops.
func (m *Mailer) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session.close()
	return nil
}

// ascii refuses an address this transport cannot carry.
//
// smtplib raises SMTPNotSupportedError for a non-ASCII address unless the
// server advertises SMTPUTF8, and then sends the whole message with raw UTF-8
// headers instead of encoded words. That second path is not implemented here,
// so rather than send a different message than Python would have sent, such an
// address is refused -- and refused the way smtplib's refusal is classified,
// as the mail server being unable to take it. AGENTS.md lists this as the one
// message the two senders do not treat the same.
func ascii(sender, recipient string) error {
	for _, address := range []string{sender, recipient} {
		for i := 0; i < len(address); i++ {
			if address[i] >= 0x80 {
				return fmt.Errorf("%w: the address needs SMTPUTF8, which this transport does not speak",
					mailer.ErrUnavailable)
			}
		}
	}
	return nil
}
