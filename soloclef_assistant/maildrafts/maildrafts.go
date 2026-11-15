// Package maildrafts is the guard between a message the assistant drafted
// and that message being sent. Every mail connector that can send goes
// through it; today that is a JMAP mailbox (jmapaccounts), and a new kind
// of mailbox adds a Sender, not another way to send.
//
// # What the model can and cannot do
//
// The model can make a draft (Guard.Create, from the chat's draft_email
// tool). That is all: no tool sends, deletes, moves, flags, forwards or
// replies-all, so there is nothing a model, or text in an email it read,
// can call to do any of those. A draft goes nowhere and changes nothing in
// the mailbox; it is a row here (mail_drafts) until its owner acts on it.
//
// # How a message is sent
//
//  1. The chat shows the draft on a card. The card draws only what
//     Confirm returns, which is the stored draft, never the model's
//     arguments: what the person reads is what is stored.
//  2. Confirm issues a go-ahead: a random token, given only to the owner's
//     browser and stored only as its hash, bound to the draft, the owner
//     and the SHA-256 of the draft's canonical content as shown, valid for
//     ConfirmationLifetime and spent by the first send that presents it.
//  3. The person presses Send. The browser presents the token, the content
//     hash it was shown, and the unusual points the person ticked (below).
//     Send, in one transaction with the owner and the go-ahead locked: the
//     go-ahead must be the owner's, for this draft, unspent and unexpired;
//     the draft must still be waiting and unexpired; its content hashed
//     again now must equal the hash the go-ahead was issued for and the
//     hash the browser presented; and every unusual point the go-ahead was
//     issued with must be acknowledged. Only then is the go-ahead spent and
//     the draft marked sending, and the message that is sent is built from
//     that same stored row.
//
// So a send without a go-ahead is impossible, a draft changed after it was
// shown (a revision, or anything that touched the row) invalidates the
// go-ahead, a go-ahead is used once, another account's go-ahead or draft
// is refused, and nothing changes between showing and sending.
//
// # Unusual sends: a second confirmation, never a refusal
//
// Nothing about a message the owner confirmed is refused for its size or
// its pace: what stands between a model and a mailing is that the person
// presses Send on the exact message. What is unusual for one person's mail
// is asked about twice instead (unusual.go): more than UnusualRecipients
// recipients, more than UnusualSendsPerHour sends in the past hour,
// recipients the mailbox has never written to, or attachments over
// UnusualAttachmentBytes together. Confirm works out which apply, the card
// shows each in words with its own tick box, and the go-ahead is stored
// with them, so the extra step is bound to the same single-use go-ahead
// and enforced here, not only drawn by the card. Send recomputes what it
// can (the count, the pace, the size) in its transaction: a point that
// became true after the card was drawn refuses the send until the person
// has seen it.
//
// # Attachments
//
// A draft's files are part of its content: each one's name, type, size,
// SHA-256 and where it came from are in the hashed message, so another
// file is another message. The bytes are not stored here; they are read
// from where they live when the message is sent, through a stream that
// fails unless they are exactly the bytes whose digest was confirmed
// (attachments.go), so a file changed after the draft was made is never
// sent in its place.
//
// # Record
//
// Every send, accepted or failed, writes an entry in the owner's activity
// log.
package maildrafts

import (
	"context"
	"errors"
	"time"
)

// The thresholds past which a send is unusual and is confirmed a second
// time (unusual.go). They are the product's, not a deployment's, because
// they describe one person's own mail, which is the same everywhere: a
// person writing to more than twenty people at once, or sending more than
// twenty messages within an hour, is doing something other than their
// everyday correspondence (a circular, a mailing, a loop), which is
// exactly what a draft made by something the person did not intend would
// look like. Past either, the person is asked again in plain words; they
// are never refused.
const (
	UnusualRecipients   = 20
	UnusualSendsPerHour = 20
	// UnusualAttachmentBytes is the total size of a message's files past
	// which it is unusual. A letter, a scan or a slide deck is well under
	// it. Most mail servers refuse a message over about 25 MB, and Base64
	// makes a file a third larger on the way, so a message with more than
	// 10 MB of files is near what many recipients can receive at all, and
	// is worth a second look at what is about to leave.
	UnusualAttachmentBytes = 10 << 20
	// DraftLifetime is how long a draft waits for its owner. A card in an
	// old conversation is then shown as expired, never sent days later.
	DraftLifetime = 24 * time.Hour
	// ConfirmationLifetime is how long a go-ahead lasts: long enough to
	// read a message, short enough that a token left in a page is dead.
	ConfirmationLifetime = 10 * time.Minute
	// mostBodyChars and mostSubjectChars bound a draft's text.
	mostBodyChars    = 100_000
	mostSubjectChars = 400
)

// The kinds of mailbox that can send.
const ProviderJMAP = "jmap"

// The statuses a draft is shown with. "expired" is never stored: it is a
// waiting draft past its lifetime.
const (
	StatusPending    = "pending"
	StatusSending    = "sending"
	StatusSent       = "sent"
	StatusFailed     = "failed"
	StatusDiscarded  = "discarded"
	StatusSuperseded = "superseded"
	StatusExpired    = "expired"
)

// The refusals of the routes, each with the code the card tells it by.
var (
	ErrNoDraft             = &Refusal{Code: "draft_not_found", Message: "No such draft"}
	ErrDraftChanged        = &Refusal{Code: "draft_changed", Message: "The draft changed after it was shown. Check it again before sending."}
	ErrConfirmationExpired = &Refusal{Code: "confirmation_expired", Message: "The confirmation ran out. Check the draft again before sending."}
	ErrConfirmationSpent   = &Refusal{Code: "confirmation_spent", Message: "This confirmation was already used. Check the draft again before sending."}
	ErrConfirmationInvalid = &Refusal{Code: "confirmation_invalid", Message: "This confirmation is not valid for this draft."}
	ErrDraftNotPending     = &Refusal{Code: "draft_not_pending", Message: "This draft is no longer waiting to be sent."}
	// ErrUnusualNotConfirmed is a send that did not acknowledge every
	// unusual point its go-ahead was issued with.
	ErrUnusualNotConfirmed = &Refusal{Code: "unusual_not_confirmed", Message: "This message needs your extra confirmation: tick each point the card lists, then send."}
	// ErrUnusualChanged is a send that became unusual after the card was
	// drawn (another send meanwhile took the past hour over the threshold).
	ErrUnusualChanged = &Refusal{Code: "unusual_changed", Message: "Something about this send became unusual after it was shown. Check the draft again before sending."}
)

// Refusal is a request the guard turns down. Code is the word the card
// tells it by; Message is for the person.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// SendFailure is a send the mail server did not accept, in words for the
// owner. The draft is then 'failed' and can be confirmed and sent again.
type SendFailure struct{ Message string }

func (f *SendFailure) Error() string { return f.Message }

// Sender sends a confirmed draft from one kind of mailbox. A failure the
// owner should read is a *SendFailure.
type Sender interface {
	Send(ctx context.Context, ownerID string, draft Draft) (providerMessageID string, err error)
}

// Accounts names the mailbox a draft is from, for the card.
type Accounts interface {
	Describe(ctx context.Context, ownerID, provider, accountID string) (AccountView, error)
}

// Correspondents knows whom a mailbox has written to before.
type Correspondents interface {
	// NeverMailed is the addresses, of those given, the mailbox has never
	// sent a message to. An error is "cannot tell", which Confirm shows
	// as its own unusual point rather than as no point at all.
	NeverMailed(ctx context.Context, ownerID, provider, accountID string, addresses []string) ([]string, error)
}

// Notifier is told when a draft starts waiting for its owner. It is the
// hook a notification ("a draft is waiting for your confirmation") is
// raised from; it is called after the draft is stored, its failure is
// logged and never fails the draft, and it must not send anything itself.
type Notifier interface {
	DraftWaiting(ctx context.Context, waiting Waiting)
}

// Waiting is what a notification may say about a waiting draft: never its
// body or its recipients' addresses.
type Waiting struct {
	OwnerID      string
	DraftID      string
	AccountLabel string
	Subject      string
	Recipients   int
	ExpiresAt    time.Time
}

// NoNotifications is the Notifier of a deployment that raises none. It is
// named, and passed on purpose, so that a missing notifier is a choice
// someone wrote down, not a nil that happened.
type NoNotifications struct{}

func (NoNotifications) DraftWaiting(context.Context, Waiting) {}

// errDraftInvalid wraps what is wrong with a draft the model asked for.
var errDraftInvalid = errors.New("maildrafts: the draft is not valid")
