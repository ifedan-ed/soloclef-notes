// Package recovery puts back what a stopped run's senders were holding.
//
// A message is moved onto its sender's held list in the same operation that
// takes it, so nothing is ever on no list and a sender that dies mid-send
// loses nothing. The price was that a message already handed to the mail
// server went out again on the next start; the send guard is what stops that
// now, and this is where the two meet.
//
// A claim with no "sent" mark means a sender was giving the message to the
// mail server -- or was, when it died. From here the two cannot be told apart,
// and the answer to the ambiguity is to send again: never-sent is worse than
// twice-sent. So every claim belonging to recovered mail is dropped, and the
// mark that the mail server took it is the one thing recovery leaves alone.
//
// This is app/mail_queue/recovery.py.
package recovery

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
)

// Queue is the moves recovery makes, as queuestore.Store makes them.
type Queue interface {
	// HeldKeys is every sender's held list, this run's and a stopped run's alike.
	HeldKeys(ctx context.Context) ([]string, error)
	// NewestHeld is the entry PutBack would move next, without moving it:
	// what recovery has to do about a message depends on what the message is.
	NewestHeld(ctx context.Context, key string) (raw string, ok bool, err error)
	// PutBack moves one held message to the end of the line.
	PutBack(ctx context.Context, key string) (moved bool, err error)
}

// Guard is the one thing recovery asks of the send guard.
type Guard interface {
	Release(ctx context.Context, mailID string, attempt int) error
}

// RequeueHeld offers every held message again, and returns how many went back
// in line.
func RequeueHeld(ctx context.Context, queue Queue, guard Guard) (int, error) {
	keys, err := queue.HeldKeys(ctx)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, key := range keys {
		for {
			raw, ok, err := queue.NewestHeld(ctx, key)
			if err != nil {
				return moved, err
			}
			if !ok {
				break
			}
			if err := dropStaleClaim(ctx, guard, raw); err != nil {
				return moved, err
			}
			put, err := queue.PutBack(ctx, key)
			if err != nil {
				return moved, err
			}
			if !put {
				break
			}
			moved++
		}
	}
	return moved, nil
}

func dropStaleClaim(ctx context.Context, guard Guard, raw string) error {
	job, err := mailjob.Decode(raw)
	if err != nil {
		// Unreadable, so it holds no claim worth dropping. It goes back in
		// line and a sender logs and drops it, as it always did.
		return nil
	}
	return guard.Release(ctx, job.ID, job.Attempt)
}
