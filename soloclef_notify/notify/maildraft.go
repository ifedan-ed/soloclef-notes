package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// MailDraft is a mail the assistant drafted that waits for the person to
// confirm before it is sent. This is the hook for the mail connector
// (confirm-to-send): it calls MailDraftWaiting once per draft, when the
// draft starts waiting. Nothing of the mail itself is passed, so nothing of
// it can reach the inbox or a push.
type MailDraft struct {
	UserID string
	// DraftID is the connector's own id for the draft, kept on the row so
	// the inbox can open it.
	DraftID string
	// AccountLabel names the mailbox ("Work"), or "". It is shown in the
	// inbox only, never pushed.
	AccountLabel string
	// WebPath is where the web app opens the draft ("/dashboard/..."), or
	// "" when the inbox has nowhere better to send the person.
	WebPath string
}

// Drafts made together (a reply to each of several mails) are one row
// within ten minutes, and the push waits half a minute for the rest of them.
const (
	mailWindow = 10 * time.Minute
	mailDelay  = 30 * time.Second
)

const mailType = "mail_draft"

// phoneDraftsPath is the phone app's Drafts waiting screen
// (soloclef_mobile src/app/(app)/mail/drafts.tsx).
const phoneDraftsPath = "/mail/drafts"

// MailDraftWaiting notifies the owner that a draft waits for them.
func (n *Notifier) MailDraftWaiting(ctx context.Context, draft MailDraft) error {
	if draft.UserID == "" || draft.DraftID == "" {
		return errors.New("notify: a mail draft names its owner and itself")
	}
	render := func(count int, _ []string) rendered {
		if count == 1 {
			return rendered{
				title:     "A mail draft is waiting for you",
				message:   "Confirm it to send it, or discard it. Nothing is sent until you do.",
				pushTitle: "A mail draft is waiting for you",
			}
		}
		return rendered{
			title:     fmt.Sprintf("%d mail drafts are waiting for you", count),
			message:   "Confirm each to send it, or discard it. Nothing is sent until you do.",
			pushTitle: fmt.Sprintf("%d mail drafts are waiting for you", count),
		}
	}
	first := render(1, nil)
	_, err := n.record(ctx, event{
		userID: draft.UserID, kind: pushpolicy.Mail, typ: mailType,
		title: first.title, message: first.message,
		// The phone's Drafts waiting screen, where each draft can be sent
		// or discarded. A phone too old to know the route opens its Inbox
		// instead (its pushHref falls back), where the row is.
		push: push{Kind: pushpolicy.Mail, Title: first.pushTitle, Path: phoneDraftsPath},
		metadata: map[string]any{
			"status": "completed", "draft_id": draft.DraftID,
			"account_label": nonEmpty(draft.AccountLabel), "link": nonEmpty(draft.WebPath),
		},
		group: &group{key: "mail:draft", window: mailWindow, delay: mailDelay, item: draft.DraftID, render: render},
	})
	return err
}
