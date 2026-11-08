// Package dedupe is not sending the same email twice.
//
// Two failures, two guards, and they are not interchangeable:
//
//   - The same mail handed to the queue twice -- a request that was retried, a
//     task that ran again -- is refused at the push (QueuedOnce), against the
//     key its caller said identifies it.
//   - A message the mail server already took, whose sender died before it
//     could say so, is put back in line at the next start; HandoverGuard stops
//     that copy going out again.
//
// Neither can swallow a retry. A failed job comes back as the same mail id at
// the next attempt: it never passes the push again, and the handover key
// carries the attempt.
//
// This is app/mail_queue/dedupe, key for key. Nothing here speaks to Redis --
// the keyspace is an interface each half brings its own client for, which is
// what keeps go-redis out of a tier that only queues mail.
package dedupe

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
)

// queued records what a caller said identifies its mail, and which mail took
// that identity. handover records which attempt of which mail has been given
// to the mail server.
const (
	queued   = queuekeys.Prefix + ":queued"
	handover = queuekeys.Prefix + ":handover"
)

// Window is how long both are remembered, and it is WINDOW_SECONDS in
// app/mail_queue/dedupe/keys.py. A day is longer than the whole retry ladder
// (about 45 minutes) and than any work that could be repeated and mail the
// same thing again, and short enough that two small keys per mail cost
// nothing. Once it passes, an identical mail is queued and sent again, and a
// message recovered from a run that has been down longer than that goes out
// again.
//
// It is a constant and not a setting for the reason the queue's keys are: the
// two senders drain the same Redis, and two sides reading two different
// numbers is a duplicate nobody can see.
const Window = 24 * time.Hour

// Claimed means a sender has taken this attempt and is giving it to the mail
// server now. Sent means the mail server took it, and that mark is never
// undone inside the window.
const (
	Claimed = "claimed"
	Sent    = "sent"
)

// QueuedKey is where the mail holding idempotencyKey is recorded.
//
// Hashed, because a caller's key may be built out of a reset token and a Redis
// key name is not a place to keep one: key names show up in SCAN, in MONITOR
// and in anything that walks the keyspace.
func QueuedKey(idempotencyKey string) string {
	digest := sha256.Sum256([]byte(idempotencyKey))
	return queued + ":" + hex.EncodeToString(digest[:])
}

// HandoverKey is where one attempt of one mail is recorded.
//
// The attempt is in the key, which is what keeps a retry sending: a temporary
// failure re-queues the same mail id at the next attempt, so a guard keyed on
// the id alone would turn every transient SMTP failure into a mail that never
// arrives and never errors.
func HandoverKey(mailID string, attempt int) string {
	return handover + ":" + mailID + ":" + strconv.Itoa(attempt)
}
