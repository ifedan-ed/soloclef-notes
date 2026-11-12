package podcasts

import (
	"context"
	"errors"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// StartDraft is the approve route's transition for a caller that is not a
// request for it. It must hold to the same gate: only a podcast of the
// caller's, waiting at its brief gate, with a brief; and a draft that
// cannot be queued must not leave the podcast drafting.
func TestStartDraftApprovesOnlyWhatTheApproveRouteWould(t *testing.T) {
	ctx := context.Background()
	logContext := produce.LogContext{RequestID: "req-1", UserID: callerID}

	rows := newFake(podcast(7, nil))
	if err := StartDraft(ctx, rows, rows, callerID, 7, logContext); err != nil {
		t.Fatalf("a podcast at its brief gate: %v", err)
	}
	if rows.podcasts[7].Status != statusDrafting || len(rows.enqueued) != 1 {
		t.Fatalf("status %q, %d messages queued", rows.podcasts[7].Status, len(rows.enqueued))
	}
	if queued := rows.enqueued[0]; queued.Task != DraftTask || queued.Context.RequestID != "req-1" {
		t.Errorf("queued %+v", queued)
	}

	refused := []struct {
		name string
		rows *fake
		user string
		id   int64
	}{
		{"already drafting", newFake(podcast(7, func(row *PodcastOfOwnerRow) { row.Status = statusDrafting })), callerID, 7},
		{"no brief", newFake(podcast(7, func(row *PodcastOfOwnerRow) { row.Spec = nil })), callerID, 7},
		{"another account's", newFake(podcast(7, nil)), strangerID, 7},
		{"absent", newFake(), callerID, 7},
	}
	for _, c := range refused {
		before := c.rows.podcasts[c.id].Status
		if err := StartDraft(ctx, c.rows, c.rows, c.user, c.id, logContext); !errors.Is(err, ErrNotAtBriefGate) {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.rows.podcasts[c.id].Status != before || len(c.rows.enqueued) != 0 {
			t.Errorf("%s: the podcast moved or a draft was queued", c.name)
		}
	}

	down := newFake(podcast(7, nil))
	down.queueFails = true
	if err := StartDraft(ctx, down, down, callerID, 7, logContext); err == nil || errors.Is(err, ErrNotAtBriefGate) {
		t.Errorf("a queue that is down should be the queue's error: %v", err)
	}
	if down.podcasts[7].Status != statusAwaitingBrief {
		t.Errorf("an unqueued draft left the podcast %q", down.podcasts[7].Status)
	}
}
