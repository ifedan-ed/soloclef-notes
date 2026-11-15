package e2eseed

import (
	"encoding/json"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// A mail draft waiting for its owner, as an automation's run leaves one: the
// Drafts waiting page's test needs one, and the suite has no mail server
// for a model to draft through. The row is the send guard's own shape (its
// canonical message and that message's hash), so the card the page draws
// asks the product's confirmation route for it and gets a real single-use
// go-ahead; only the mailbox behind it is not a server anybody runs.
//
//	POST /__e2e__/seed/mail-draft  {"email", "subject", "body", "to": [...], "mailbox"}

// mailDraftSeed is the request. Every field but the owner's address has a
// value a test that does not care about it can leave out.
type mailDraftSeed struct {
	Email   string   `json:"email"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	To      []string `json:"to"`
	// Mailbox is the mailbox's label, as its owner would have named it.
	Mailbox string `json:"mailbox"`
}

func (r Routes) seedMailDraft(x *exchange) {
	var seed mailDraftSeed
	if !x.body(&seed) {
		return
	}
	if seed.Subject == "" {
		seed.Subject = "Seeded draft"
	}
	if len(seed.To) == 0 {
		seed.To = []string{"ben@example.test"}
	}
	if seed.Mailbox == "" {
		seed.Mailbox = "Work"
	}
	ctx := x.ctx.Context()
	queries := New(r.Pool)
	owner, err := queries.AccountByAddress(ctx, seed.Email)
	if err != nil {
		x.refuse(http.StatusNotFound, "no account has that address")
		return
	}
	username := strings.ToLower(strings.TrimSpace(seed.Email))
	mailbox, err := queries.SeedJMAPMailbox(ctx, SeedJMAPMailboxParams{UserID: owner, Label: seed.Mailbox, Username: username})
	if err != nil {
		x.fail("could not seed the mailbox", err)
		return
	}
	message := maildrafts.Message{
		AccountID: mailbox, IdentityID: "e2e-identity", From: maildrafts.Address{Email: username},
		Cc: []maildrafts.Address{}, Bcc: []maildrafts.Address{}, Subject: seed.Subject, Body: seed.Body,
		InReplyTo: []string{}, References: []string{}, ThreadParticipants: []string{}, Attachments: []maildrafts.Attachment{},
	}
	for _, address := range seed.To {
		message.To = append(message.To, maildrafts.Address{Email: address})
	}
	// Every list is non-nil above, which is the guard's canonical form: the
	// row then decodes to exactly the message hashed here.
	encoded, err := json.Marshal(message)
	if err != nil {
		x.fail("could not encode the draft", err)
		return
	}
	id, err := queries.SeedMailDraft(ctx, SeedMailDraftParams{
		UserID: owner, JmapAccountID: &mailbox, Message: encoded, ContentHash: message.ContentHash(),
		ExpiresAt: r.now().Add(maildrafts.DraftLifetime),
	})
	if err != nil {
		x.fail("could not seed the draft", err)
		return
	}
	x.send(http.StatusCreated, map[string]string{"draft_id": id, "mailbox_id": mailbox})
}
