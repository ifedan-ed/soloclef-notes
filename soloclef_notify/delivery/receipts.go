package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// The receipts check's own limits.
const (
	// receiptsKept is how long Expo keeps a receipt: a ticket older than
	// this can no longer be asked about.
	receiptsKept = 24 * time.Hour
	// receiptPages bounds one run: a thousand tickets a page, twenty pages
	// is twenty thousand pushes, and anything left is the next run's.
	receiptPages = 20
	// receiptRetry is the wait before a check that the service did not
	// answer is tried again.
	receiptRetry = 5 * time.Minute
)

// Receipts is notifications.check_push_receipts: ask Expo what became of
// the pushes sent through it at least ReceiptDelay ago, forget each phone
// whose receipt says it is gone, and forget the tickets. (Apple answers
// each push at once, so APNs pushes leave no ticket here.) If pushes are
// still waiting to be due, it schedules itself again.
func (j *Jobs) Receipts(ctx context.Context, _ *celery.Message) error {
	now := j.now()
	q := New(j.Database)
	if dropped, err := q.DropUnanswerableTickets(ctx, now.Add(-receiptsKept)); err != nil {
		return fmt.Errorf("delivery: dropping old tickets: %w", err)
	} else if dropped > 0 {
		j.Log.Warn("tickets too old for a receipt were dropped", "tickets", dropped)
	}
	dueBefore := now.Add(-j.Timing.ReceiptDelay)
	for page := 0; page < receiptPages; page++ {
		due, err := q.DueTickets(ctx, DueTicketsParams{DueBefore: dueBefore, PageSize: pushsend.MaxReceiptBatch})
		if err != nil {
			return fmt.Errorf("delivery: reading due tickets: %w", err)
		}
		if len(due) == 0 {
			break
		}
		ids := make([]string, len(due))
		tokenOf := make(map[string]string, len(due))
		for i, ticket := range due {
			ids[i], tokenOf[ticket.TicketID] = ticket.TicketID, ticket.Token
		}
		receipts, err := j.Sending.Expo.Receipts(ctx, ids)
		if errors.Is(err, pushsend.ErrTransient) {
			j.Log.Warn("the push service did not answer for receipts; trying again later", "error", err.Error())
			_, sendErr := j.Publisher.Send(ctx, notify.ReceiptsMessage(receiptRetry))
			return sendErr
		}
		if err != nil {
			return fmt.Errorf("delivery: reading receipts: %w", err)
		}
		var gone, answered []string
		for _, id := range ids {
			receipt, has := receipts[id]
			if !has {
				// Not ready yet; asked again next time, until receiptsKept.
				continue
			}
			answered = append(answered, id)
			if receipt.Gone {
				gone = append(gone, tokenOf[id])
			} else if receipt.Error != "" {
				j.Log.Warn("a push did not reach its phone", "error", receipt.Error, "message", receipt.Message)
			}
		}
		if err := j.forget(ctx, gone); err != nil {
			return fmt.Errorf("delivery: forgetting phones that are gone: %w", err)
		}
		if err := q.ForgetTickets(ctx, answered); err != nil {
			return fmt.Errorf("delivery: forgetting answered tickets: %w", err)
		}
		if len(answered) < len(ids) || len(due) < pushsend.MaxReceiptBatch {
			// Either Expo has nothing more to say about this page yet, or
			// there is no next page: either way, done for now.
			break
		}
	}
	left, err := q.TicketsLeft(ctx)
	if err != nil {
		return fmt.Errorf("delivery: looking for tickets left: %w", err)
	}
	if left {
		j.scheduleReceipts(context.WithoutCancel(ctx))
	}
	return nil
}
