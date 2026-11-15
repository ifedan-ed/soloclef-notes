// Package maildraftnotice reports a mail draft that waits for its owner's
// confirmation through the one notification step (soloclef_notify/notify):
// an inbox row, and a push saying only "A mail draft is waiting for you".
//
// It is the maildrafts.Notifier of the mail connector (confirm-to-send),
// which calls DraftWaiting once a draft is stored and waiting. The draft's
// subject, body and recipients are never passed on: the subject is the
// person's own words and Expo relays whatever a push carries, so the inbox
// row and the push both say only that a draft waits, from which mailbox.
package maildraftnotice

import (
	"context"
	"log/slog"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

// Waiting is maildrafts.Waiting: what may be said about a waiting draft.
type Waiting = maildrafts.Waiting

// Drafts is the notification step's mail-draft method (notify.Notifier).
type Drafts interface {
	MailDraftWaiting(ctx context.Context, draft notify.MailDraft) error
}

// WaitingPage is the web app's Drafts waiting page, where the inbox row
// opens. Not the chat that made the draft: an automation's run deletes its
// conversation when it ends, so that chat may be gone, and several drafts
// made together are one row (notify groups them), which only a page of all
// of them can stand for. The page is soloclef_web/app/dashboard/mail/drafts.
const WaitingPage = "/dashboard/mail/drafts"

// Notifier is maildrafts.Notifier over the notification step.
type Notifier struct {
	Drafts Drafts
	Log    *slog.Logger
}

// DraftWaiting is maildrafts.Notifier. The interface returns nothing,
// because a notification must never fail the draft it reports on, so a
// failure is logged here and the draft stands.
func (n Notifier) DraftWaiting(ctx context.Context, waiting Waiting) {
	err := n.Drafts.MailDraftWaiting(ctx, notify.MailDraft{
		UserID: waiting.OwnerID, DraftID: waiting.DraftID, AccountLabel: waiting.AccountLabel,
		WebPath: WaitingPage,
	})
	if err != nil {
		n.Log.ErrorContext(ctx, "a waiting mail draft's notification could not be written",
			slog.String("draft_id", waiting.DraftID), slog.String("error", err.Error()))
	}
}
