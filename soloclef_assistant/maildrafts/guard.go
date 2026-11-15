package maildrafts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Guard is the one way a draft is made, shown, sent or discarded.
type Guard struct {
	Rows     Rows
	Accounts Accounts
	// Senders is the Sender of each kind of mailbox that can send.
	Senders map[string]Sender
	// History says whom a mailbox has written to before, for the new
	// recipients point (unusual.go).
	History Correspondents
	// Files reads the rows behind a draft's files, for the look Confirm
	// takes at them before it issues a go-ahead.
	Files AttachmentFiles
	// Notify is told when a draft starts waiting (NoNotifications for a
	// deployment that raises none).
	Notify Notifier
	Now    func() time.Time
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (g *Guard) Check() error {
	if g.Rows == nil || g.Accounts == nil || len(g.Senders) == 0 || g.History == nil || g.Files == nil || g.Notify == nil || g.Now == nil || g.Logger == nil {
		return errors.New("maildrafts: Rows, Accounts, Senders, History, Files, Notify, Now and Logger are all required")
	}
	return nil
}

// Created is a draft just made, as its maker is told about it.
type Created struct {
	ID        string
	ExpiresAt time.Time
}

// Create stores a draft for its owner and returns its id. It sends
// nothing and changes nothing in any mailbox. With replaces set, that
// waiting draft is superseded by this one: a revision is a new draft, so
// a card already on screen keeps showing the message it was made for and
// any go-ahead issued for it no longer matches anything sendable.
func (g *Guard) Create(ctx context.Context, ownerID, provider string, message Message, replaces string) (Created, error) {
	if _, known := g.Senders[provider]; !known {
		return Created{}, fmt.Errorf("%w: this kind of mailbox cannot send", errDraftInvalid)
	}
	message = message.canonical()
	if err := message.validate(); err != nil {
		return Created{}, err
	}
	id, err := newUUID()
	if err != nil {
		return Created{}, err
	}
	now := g.Now()
	draft := Draft{ID: id, Provider: provider, AccountID: message.AccountID, Message: message,
		StoredHash: message.ContentHash(), Status: StatusPending, CreatedAt: now, ExpiresAt: now.Add(DraftLifetime)}
	if err := g.Rows.Insert(ctx, ownerID, draft, replaces); err != nil {
		return Created{}, err
	}
	label := ""
	if account, err := g.Accounts.Describe(ctx, ownerID, provider, message.AccountID); err == nil {
		label = account.Label
	}
	g.Notify.DraftWaiting(ctx, Waiting{OwnerID: ownerID, DraftID: id, AccountLabel: label, Subject: message.Subject,
		Recipients: len(message.Recipients()), ExpiresAt: draft.ExpiresAt})
	return Created{ID: id, ExpiresAt: draft.ExpiresAt}, nil
}

// IsInvalid reports whether err is a draft refused for its content, whose
// text the model can act on.
func IsInvalid(err error) bool { return errors.Is(err, errDraftInvalid) }

// Confirm is the card's view of a draft and, while it can be sent, a fresh
// go-ahead for exactly the content in the view, with the unusual points
// the person must tick before it sends.
func (g *Guard) Confirm(ctx context.Context, ownerID, draftID string) (View, error) {
	draft, err := g.Rows.Draft(ctx, ownerID, draftID)
	if err != nil {
		return View{}, err
	}
	now := g.Now()
	view, err := g.view(ctx, ownerID, draft, now)
	if err != nil || !draft.sendable(now) {
		return view, err
	}
	// A draft whose file was removed or replaced since it was made is not
	// offered for sending: what it would carry is no longer what it says.
	if len(draft.Message.Attachments) > 0 {
		changed, err := g.Files.Changed(ctx, ownerID, draft.Message.Attachments)
		if err != nil {
			return View{}, err
		}
		if changed != "" {
			blocked := "The file " + changed + " was changed or removed after this draft was made, so it cannot be sent. Ask for a new draft."
			view.Draft.Blocked = &blocked
			return view, nil
		}
	}
	sent, err := g.Rows.SentSince(ctx, ownerID, now.Add(-time.Hour))
	if err != nil {
		return View{}, err
	}
	points := append(fromContent(draft.Message), fromPace(sent)...)
	points = append(points, g.history(ctx, ownerID, draft)...)
	token, digest, err := newToken()
	if err != nil {
		return View{}, err
	}
	// The hash is of the content this view was built from, computed now:
	// the go-ahead is for what the person is about to read, and for the
	// unusual points they are about to be asked about.
	contentHash := draft.Message.ContentHash()
	expires := now.Add(ConfirmationLifetime)
	if err := g.Rows.Issue(ctx, ownerID, draftID, digest, contentHash, codes(points), now, expires); err != nil {
		return View{}, err
	}
	if points == nil {
		points = []Unusual{}
	}
	view.Confirmation = &ConfirmationView{Token: token, ContentHash: contentHash, ExpiresAt: expires, Unusual: points}
	return view, nil
}

// history is the recipients point: those the mailbox has never written to.
// A mailbox that cannot be asked (unreachable, no Sent folder) makes every
// recipient checked an unchecked one, asked about in its own words: not
// knowing is never taken as knowing they are not new.
func (g *Guard) history(ctx context.Context, ownerID string, draft Draft) []Unusual {
	recipients := recipientsToCheck(draft.Message)
	if len(recipients) == 0 {
		return nil
	}
	never, err := g.History.NeverMailed(ctx, ownerID, draft.Provider, draft.AccountID, recipients)
	if err != nil {
		g.Logger.LogAttrs(ctx, slog.LevelWarn, "whether a draft's recipients are new could not be checked",
			slog.String("user_id", ownerID), slog.String("draft_id", draft.ID), slog.String("error", err.Error()))
		return fromHistory(nil, recipients)
	}
	return fromHistory(never, nil)
}

// Send sends a draft its owner confirmed. acknowledged is the unusual
// points the person ticked on the card. A *Refusal is a send the guard
// turned down (nothing was sent); a *SendFailure is one the mail server
// did not accept (the draft is 'failed' and the view says why).
func (g *Guard) Send(ctx context.Context, ownerID, draftID, token, presentedHash string, acknowledged []string) (View, error) {
	digest := sha256.Sum256([]byte(token))
	now := g.Now()
	draft, err := g.Rows.BeginSend(ctx, ownerID, draftID, digest[:], presentedHash, acknowledged, now)
	if err != nil {
		var refused *Refusal
		if errors.As(err, &refused) {
			g.Logger.LogAttrs(ctx, slog.LevelWarn, "a send was refused by the guard",
				slog.String("user_id", ownerID), slog.String("draft_id", draftID), slog.String("code", refused.Code))
		}
		return View{}, err
	}
	// From here the send is under way: it runs to its end and is recorded
	// even if the browser that asked goes away.
	settle := context.WithoutCancel(ctx)
	finished := Finished{Draft: draft}
	sender := g.Senders[draft.Provider]
	if sender == nil {
		finished.Failure = "This kind of mailbox cannot send"
	} else {
		finished.ProviderMessageID, err = sender.Send(settle, ownerID, draft)
		var failure *SendFailure
		switch {
		case errors.As(err, &failure):
			finished.Failure = failure.Message
		case err != nil:
			g.Logger.LogAttrs(ctx, slog.LevelError, "sending a confirmed draft failed",
				slog.String("user_id", ownerID), slog.String("draft_id", draftID), slog.String("error", err.Error()))
			finished.Failure = "The message could not be sent just now. Check your Sent folder before trying again."
		}
	}
	finished.At = g.Now()
	if err := g.Rows.FinishSend(settle, ownerID, finished); err != nil {
		g.Logger.LogAttrs(ctx, slog.LevelError, "the outcome of a send could not be recorded",
			slog.String("user_id", ownerID), slog.String("draft_id", draftID), slog.String("error", err.Error()))
		return View{}, err
	}
	stored, err := g.Rows.Draft(settle, ownerID, draftID)
	if err != nil {
		return View{}, err
	}
	view, err := g.view(settle, ownerID, stored, g.Now())
	if err != nil {
		return View{}, err
	}
	if finished.Failure != "" {
		return view, &SendFailure{Message: finished.Failure}
	}
	return view, nil
}

// Discard drops a waiting draft. Discarding one that is not waiting is not
// an error: the view says what it is.
func (g *Guard) Discard(ctx context.Context, ownerID, draftID string) (View, error) {
	if _, err := g.Rows.Discard(ctx, ownerID, draftID); err != nil {
		return View{}, err
	}
	draft, err := g.Rows.Draft(ctx, ownerID, draftID)
	if err != nil {
		return View{}, err
	}
	return g.view(ctx, ownerID, draft, g.Now())
}

// newToken is a go-ahead: 32 random bytes for the browser, and the hash
// that is all the database keeps of them.
func newToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

// newUUID is a random UUID (version 4), made here so the draft's id is
// known before it is written.
func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}
