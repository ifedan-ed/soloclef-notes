package dedupe

import "context"

// QueuedOnce is one push per mail: the ledger of which mail holds a caller's
// key. app/mail_queue/dedupe/queued_once.py.
type QueuedOnce struct {
	Keys Keys
}

// Claim takes idempotencyKey for mailID, and returns the id of the mail
// already holding it -- "" when this mail took it.
func (q QueuedOnce) Claim(ctx context.Context, idempotencyKey, mailID string) (string, error) {
	return q.Keys.SetIfAbsent(ctx, QueuedKey(idempotencyKey), mailID, Window)
}

// Release gives the key back after a push that never landed. Without it a mail
// Redis refused would hold its own identity for a day and the caller could
// never queue it again.
func (q QueuedOnce) Release(ctx context.Context, idempotencyKey, mailID string) error {
	key := QueuedKey(idempotencyKey)
	held, err := q.Keys.Get(ctx, key)
	if err != nil || held != mailID {
		return err
	}
	return q.Keys.Delete(ctx, key)
}
