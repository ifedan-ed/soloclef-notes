package maildrafts

import (
	"context"
	"time"
)

// MostWaitingListed is how many waiting drafts the Drafts waiting page is
// given at once. A draft waits a day at most (DraftLifetime), so a person
// with more than fifty at one time has something drafting for them in a
// loop; the page says there are more rather than drawing a hundred cards,
// each of which asks the server for a go-ahead of its own.
const MostWaitingListed = 50

// WaitingDraft is one line of the Drafts waiting page: enough to order the
// list and name each draft. The card the page draws for it fetches the
// draft itself (Confirm), so the recipients and the body are never in this
// list, and the go-ahead is issued only for a card that is on screen.
type WaitingDraft struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Subject string `json:"subject"`
	// AccountLabel is the mailbox the draft would be sent from, as its
	// owner named it.
	AccountLabel string    `json:"account_label"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// WaitingList is the page's list, newest first. More says there were more
// than MostWaitingListed.
type WaitingList struct {
	Drafts []WaitingDraft `json:"drafts"`
	More   bool           `json:"more"`
}

// Waiting is the owner's drafts that still wait for them: the stable home
// of a draft whose chat is gone (an automation's run deletes its
// conversation when it ends), and of every other one too, so there is one
// place to find them all.
func (g *Guard) Waiting(ctx context.Context, ownerID string) (WaitingList, error) {
	// One more than is shown is read, which is how "more" is known without
	// a second count.
	drafts, err := g.Rows.Waiting(ctx, ownerID, g.Now(), MostWaitingListed+1)
	if err != nil {
		return WaitingList{}, err
	}
	list := WaitingList{Drafts: drafts}
	if len(drafts) > MostWaitingListed {
		list.Drafts, list.More = drafts[:MostWaitingListed], true
	}
	if list.Drafts == nil {
		// An empty list encodes as [], which the page reads as "none".
		list.Drafts = []WaitingDraft{}
	}
	return list, nil
}

func (s Store) Waiting(ctx context.Context, ownerID string, now time.Time, most int) ([]WaitingDraft, error) {
	rows, err := New(s.Pool).WaitingDraftsOfOwner(ctx, WaitingDraftsOfOwnerParams{UserID: ownerID, Now: now, Most: int32(most)})
	if err != nil {
		return nil, err
	}
	drafts := make([]WaitingDraft, 0, len(rows))
	for _, row := range rows {
		drafts = append(drafts, WaitingDraft{ID: row.ID, Status: row.Status, Subject: row.Subject, AccountLabel: row.AccountLabel, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt})
	}
	return drafts, nil
}
