// Package enqueue hands an email to the queue; the caller goes on without
// waiting for it to be sent.
//
// This is app/mail_queue/enqueue.py's half of the port, and the reversible
// one: mail queued here is drained by whichever mail sender is running, Python
// or Go, because what goes on the list is the same bytes either way
// (mailjob.Job.Encode).
//
// A caller that says what identifies its mail (Message.IdempotencyKey) gets
// one push per identity: the second is refused and told which mail already
// holds it. A caller that says nothing is queued every time. A retry is not a
// second push -- a failed job is written straight onto the retry set by the
// sender -- so it never comes back through here.
package enqueue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
)

// ErrQueueUnavailable is MailQueueUnavailableError: Redis did not take the
// message, so nothing will be sent. Every Python call site turns this into
// "the email is not on its way" for the person who asked, and a Go call site
// owes the same answer -- it must not report a send that did not happen.
var ErrQueueUnavailable = errors.New("mail queue unavailable")

// Broker is the Redis calls a producer makes. A tier satisfies it with its
// own client, for the reason soloclef_celery's produce.Broker exists: the two
// Go tiers reach Redis differently and the bytes must not depend on which.
//
// It was one method until mail began to dedupe. The dedupe half is the four
// commands of dedupe.Keys, and they are here rather than behind a second,
// optional seam on purpose: a producer that could be built without them would
// be a producer that silently queues duplicates.
type Broker interface {
	// LPush is the producer's end of the list; the senders take from the other.
	LPush(ctx context.Context, key, value string) error
	dedupe.Keys
}

// Producer queues mail. Clock and NewID are seams for the tests; a zero
// Producer with a broker uses the wall clock and uuid4 hex, as Python does.
type Producer struct {
	Broker Broker
	Clock  func() time.Time
	NewID  func() string
}

// Request is one email to queue, in the terms the Python call sites pass.
type Request struct {
	Message mailjob.Message
	// Kind is what the mail is for ("password_reset", "invite",
	// "automation_result"); it is logged.
	Kind string
	// Ref is the record the mail belongs to, never an address.
	Ref string
	// NotAfter is when the link or code in the message stops working; the
	// senders drop it unsent after that. Zero means it never goes stale.
	NotAfter time.Time
}

// Queue writes the message to Redis and returns the id of the mail that will
// be sent: the id every log line about it carries, from here to delivery.
//
// The id is this mail's own, unless Message.IdempotencyKey is already held by
// one queued within dedupe.Window -- then nothing is pushed and that mail's id
// comes back, because the email is on its way either way.
//
// A push that fails is ErrQueueUnavailable and is not retried here, because a
// second push of the same mail is a second email; the identity it took is
// given back so the caller can queue it again.
func (p Producer) Queue(ctx context.Context, r Request) (string, error) {
	if p.Broker == nil {
		return "", errors.New("enqueue: no broker to queue mail on")
	}
	if r.Kind == "" {
		return "", errors.New("enqueue: queued mail needs a kind")
	}
	job := mailjob.Job{
		Message:    r.Message,
		Kind:       r.Kind,
		Ref:        r.Ref,
		ID:         p.newID(),
		EnqueuedAt: unixFloat(p.now()),
	}
	if !r.NotAfter.IsZero() {
		deadline := unixFloat(r.NotAfter)
		job.NotAfter = &deadline
	}
	raw, err := job.Encode()
	if err != nil {
		return "", err
	}

	key := r.Message.IdempotencyKey
	if key == "" {
		if err := p.Broker.LPush(ctx, queuekeys.Ready, raw); err != nil {
			return "", fmt.Errorf("%w: %w", ErrQueueUnavailable, err)
		}
		return job.ID, nil
	}

	ledger := dedupe.QueuedOnce{Keys: p.Broker}
	heldBy, err := ledger.Claim(ctx, key, job.ID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrQueueUnavailable, err)
	}
	if heldBy != "" {
		return heldBy, nil
	}
	if err := p.Broker.LPush(ctx, queuekeys.Ready, raw); err != nil {
		// Nothing was queued, so nothing may keep this identity: otherwise
		// the caller could not queue the same mail again for a whole day.
		if release := ledger.Release(ctx, key, job.ID); release != nil {
			return "", fmt.Errorf("%w: %w", ErrQueueUnavailable, errors.Join(err, release))
		}
		return "", fmt.Errorf("%w: %w", ErrQueueUnavailable, err)
	}
	return job.ID, nil
}

func (p Producer) now() time.Time {
	if p.Clock != nil {
		return p.Clock()
	}
	return time.Now()
}

// newID is uuid4().hex: 32 hex characters, no dashes, which is what the senders
// and both sides' logs expect to see.
func (p Producer) newID() string {
	if p.NewID != nil {
		return p.NewID()
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("enqueue: the system has no randomness for a mail id: " + err.Error())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[:])
}

// unixFloat is datetime.timestamp() and time.time(): seconds since the epoch
// as a float.
//
// The seconds and the fraction are added separately rather than dividing a
// count of nanoseconds, because a not_after far enough in the future overflows
// int64 nanoseconds and a timestamp that wrapped negative is a message dropped
// as expired the moment it is queued. The byte comparison against Python
// caught exactly that.
func unixFloat(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}
