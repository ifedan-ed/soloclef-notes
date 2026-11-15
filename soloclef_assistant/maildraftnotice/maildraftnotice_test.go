package maildraftnotice

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

type recorded struct {
	drafts []notify.MailDraft
	err    error
}

func (r *recorded) MailDraftWaiting(_ context.Context, draft notify.MailDraft) error {
	r.drafts = append(r.drafts, draft)
	return r.err
}

// A waiting draft reaches the notification step as its owner, its id, its
// mailbox's label and the Drafts waiting page, and nothing of the mail: not the subject, not how
// many it goes to.
func TestAWaitingDraftIsReportedWithoutTheMail(t *testing.T) {
	drafts := &recorded{}
	n := Notifier{Drafts: drafts, Log: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}
	n.DraftWaiting(context.Background(), Waiting{
		OwnerID: "owner", DraftID: "draft-7", AccountLabel: "Work", Subject: "My private subject", Recipients: 3,
		ExpiresAt: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC),
	})
	if len(drafts.drafts) != 1 || drafts.drafts[0] != (notify.MailDraft{UserID: "owner", DraftID: "draft-7", AccountLabel: "Work", WebPath: "/dashboard/mail/drafts"}) {
		t.Fatalf("reported %+v", drafts.drafts)
	}
}

// A notification that cannot be written never fails the draft: it is
// logged, and DraftWaiting returns as always.
func TestAFailedNotificationIsLoggedNotRaised(t *testing.T) {
	var log bytes.Buffer
	n := Notifier{Drafts: &recorded{err: errors.New("database away")}, Log: slog.New(slog.NewTextHandler(&log, nil))}
	n.DraftWaiting(context.Background(), Waiting{OwnerID: "owner", DraftID: "draft-8"})
	if !strings.Contains(log.String(), "draft-8") || !strings.Contains(log.String(), "database away") {
		t.Errorf("logged %q", log.String())
	}
}
