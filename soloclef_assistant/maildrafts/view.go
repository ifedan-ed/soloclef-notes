package maildrafts

import (
	"context"
	"time"
)

// View is what the card is sent: the stored draft and, while it can be
// sent, the go-ahead for it. Its JSON is the card's contract
// (soloclef_web/features/mail-draft-confirm).
type View struct {
	Draft        DraftView         `json:"draft"`
	Confirmation *ConfirmationView `json:"confirmation"`
}

// DraftView is a draft as the card shows it.
type DraftView struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	Account     AccountView      `json:"account"`
	From        Address          `json:"from"`
	To          []Address        `json:"to"`
	Cc          []Address        `json:"cc"`
	Bcc         []Address        `json:"bcc"`
	Subject     string           `json:"subject"`
	Body        string           `json:"body"`
	Attachments []AttachmentView `json:"attachments"`
	ReplyTo     *ReplyContext    `json:"reply_to"`
	// OutsideThread is the recipients not on the message replied to.
	OutsideThread []string   `json:"outside_thread"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	SentAt        *time.Time `json:"sent_at"`
	Failure       *string    `json:"failure"`
	SupersededBy  *string    `json:"superseded_by"`
	// Blocked is why a waiting draft cannot be sent now (a file of it was
	// changed or removed), nil when nothing stops it.
	Blocked *string `json:"blocked"`
}

// AttachmentView is one file as the card lists it: exactly what goes out
// under what name, how large, and from where.
type AttachmentView struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Origin string `json:"origin"`
}

// AccountView names the mailbox a draft is from.
type AccountView struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Username string `json:"username"`
}

// ReplyContext is the message a reply answers.
type ReplyContext struct {
	Subject string `json:"subject"`
	From    string `json:"from"`
}

// ConfirmationView is a go-ahead as the browser holds it: the token itself
// is in this answer and nowhere else.
type ConfirmationView struct {
	Token       string    `json:"token"`
	ContentHash string    `json:"content_hash"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Unusual is what about this send the person confirms a second time,
	// each with its own tick box; the send presents the codes ticked.
	// Empty for an everyday message.
	Unusual []Unusual `json:"unusual"`
}

func (g *Guard) view(ctx context.Context, ownerID string, draft Draft, now time.Time) (View, error) {
	message := draft.Message
	account, err := g.Accounts.Describe(ctx, ownerID, draft.Provider, draft.AccountID)
	if err != nil {
		return View{}, err
	}
	shown := DraftView{
		ID: draft.ID, Status: draft.shownStatus(now), Account: account, From: message.From,
		To: message.To, Cc: message.Cc, Bcc: message.Bcc, Subject: message.Subject, Body: message.Body,
		Attachments: attachmentViews(message.Attachments), OutsideThread: message.OutsideThread(),
		CreatedAt: draft.CreatedAt, ExpiresAt: draft.ExpiresAt, SentAt: draft.SentAt,
		Failure: optional(draft.Failure), SupersededBy: optional(draft.SupersededBy),
	}
	if message.ReplyToMessageID != "" {
		shown.ReplyTo = &ReplyContext{Subject: message.ReplyToSubject, From: message.ReplyToFrom}
	}
	return View{Draft: shown}, nil
}

func attachmentViews(attachments []Attachment) []AttachmentView {
	out := make([]AttachmentView, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, AttachmentView{Name: attachment.Name, Type: attachment.Type, Size: attachment.Size, Origin: attachment.Origin})
	}
	return out
}
