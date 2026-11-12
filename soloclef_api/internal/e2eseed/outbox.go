package e2eseed

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// outboxSize is how many messages are kept. A test reads the newest to
// its own address moments after causing it; the bound is there so a long
// run does not grow the process.
const outboxSize = 200

// Outbox is where an end-to-end stack's mail goes: kept in memory for the
// suite to read, never sent. It is the sign-in routes' Mailer in such a
// stack, so the password reset under test is the product's own code up to
// the moment a message would leave.
type Outbox struct {
	mu       sync.Mutex
	messages []mailer.Message
}

// Send keeps the message.
func (o *Outbox) Send(_ context.Context, message mailer.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, message)
	if len(o.messages) > outboxSize {
		o.messages = o.messages[len(o.messages)-outboxSize:]
	}
	return nil
}

// last is the newest message to an address, compared without case as
// addresses are.
func (o *Outbox) last(to string) (mailer.Message, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := len(o.messages) - 1; i >= 0; i-- {
		if strings.EqualFold(o.messages[i].To, to) {
			return o.messages[i], true
		}
	}
	return mailer.Message{}, false
}

func (r Routes) lastMail(x *exchange) {
	address := x.ctx.URL()
	message, found := r.Outbox.last(strings.TrimSpace(address.Query().Get("to")))
	if !found {
		x.refuse(http.StatusNotFound, "no mail for that address")
		return
	}
	x.send(http.StatusOK, map[string]string{
		"to": message.To, "sender": message.Sender, "subject": message.Subject, "text": message.TextBody,
	})
}
