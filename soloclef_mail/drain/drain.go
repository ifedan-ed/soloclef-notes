// Package drain takes a message off the queue, delivers it, and records what
// happened.
//
// app/mail_queue/sender.py's loop, with its order of decisions kept: a message
// whose link has expired is dropped before a connection is touched, a
// permanent refusal is not retried, a transient one is, and anything else is
// treated as a bug in the sender rather than trouble at the mail server --
// because retrying the same input would only repeat it.
//
// One sender owns one mailer, so its session stays open from one message to
// the next.
package drain

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/retrypolicy"
)

// takeWait is TAKE_WAIT_SECONDS: how long one wait for new mail blocks, so a
// stop is seen promptly.
const takeWait = time.Second

// Store is the queue as one sender uses it. The moves are queue_store.py's and
// each is one Redis operation, or one transaction, for the reason the Python
// ones are: a message is never on no list.
type Store interface {
	// Take moves the oldest ready message onto this sender's held list and
	// returns it. ok is false when the wait passed with nothing to take.
	Take(ctx context.Context, sender int, wait time.Duration) (raw string, ok bool, err error)
	// Done drops the message from this sender's held list: it is finished with,
	// whether it was sent, dropped or given up on.
	Done(ctx context.Context, sender int, raw string) error
	// RetryLater puts the next try on the retry set and drops the held one, in
	// one transaction.
	RetryLater(ctx context.Context, sender int, raw, next string, due float64) error
}

// Outcome is what was decided about one message. The words are the ones
// sender.py returns, so the two senders' logs read the same.
type Outcome string

const (
	Sent       Outcome = "sent"
	Retry      Outcome = "retry"
	Failed     Outcome = "failed"
	GaveUp     Outcome = "gave_up"
	Expired    Outcome = "expired"
	Unreadable Outcome = "unreadable"
)

// Sender is one mail sender.
type Sender struct {
	Index  int
	Store  Store
	Mailer mailer.Mailer
	// Clock is time.time(): the wall clock the expiry and the retry time are
	// in. The monotonic clock times the send; these two are not the same clock
	// and a retry due time written in the monotonic one is a message that never
	// comes back.
	Clock func() float64
	Log   *slog.Logger
}

// Run takes messages until ctx is done, then lets its mailer go.
func (s Sender) Run(ctx context.Context) error {
	defer func() {
		if err := s.Mailer.Close(); err != nil {
			s.log().Warn("the mail sender could not close its connection", "sender", s.Index, "error", err)
		}
	}()
	for ctx.Err() == nil {
		raw, ok, err := s.Store.Take(ctx, s.Index, takeWait)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// The queue itself is unreachable. Nothing is lost -- an
			// untaken message stays ready -- so this waits and tries again
			// rather than ending the process.
			s.log().Error("the mail queue could not be read", "sender", s.Index, "error", err)
			if !sleep(ctx, takeWait) {
				return nil
			}
			continue
		}
		if !ok {
			continue
		}
		if _, err := s.Handle(ctx, raw); err != nil {
			s.log().Error("the mail queue could not be written", "sender", s.Index, "error", err)
		}
	}
	return nil
}

// Handle delivers one queued message and returns the outcome that was logged.
// The returned error is the queue's, never the message's: a message that could
// not be sent is an outcome, not a failure of this call.
func (s Sender) Handle(ctx context.Context, raw string) (Outcome, error) {
	job, err := mailjob.Decode(raw)
	if err != nil {
		s.log().Error("a mail sender dropped an unreadable queue entry", "sender", s.Index)
		return Unreadable, s.Store.Done(ctx, s.Index, raw)
	}

	// Everything logged about a message: its id, what it is for, which record
	// it belongs to and which try this is. Never its address, link or code.
	log := s.log().With("mail", job.ID, "kind", job.Kind, "ref", job.Ref, "try", job.Attempt+1)

	if job.Expired(s.now()) {
		log.Warn("mail dropped unsent: its link or code has expired")
		return Expired, s.Store.Done(ctx, s.Index, raw)
	}

	started := time.Now()
	err = s.Mailer.Send(ctx, message(job))
	switch {
	case err == nil:
		if err := s.Store.Done(ctx, s.Index, raw); err != nil {
			return Sent, err
		}
		log.Info("mail sent", "ms", time.Since(started).Milliseconds())
		return Sent, nil
	case errors.Is(err, mailer.ErrRejected), errors.Is(err, mailer.ErrMisconfigured):
		// Checked before ErrUnavailable, because a misconfiguration is a kind
		// of unavailable and sending the same message again cannot succeed
		// until someone changes a setting.
		log.Error("mail failed permanently, not retried", "error", err)
		return Failed, s.Store.Done(ctx, s.Index, raw)
	case errors.Is(err, mailer.ErrUnavailable):
		return s.retryOrGiveUp(ctx, job, raw, log, err)
	default:
		// A bug, not the mail server: a message that cannot even be built
		// lands here. The error names no address or content.
		log.Error("mail failed unexpectedly, not retried", "error", err)
		return Failed, s.Store.Done(ctx, s.Index, raw)
	}
}

func (s Sender) retryOrGiveUp(ctx context.Context, job mailjob.Job, raw string, log *slog.Logger, cause error) (Outcome, error) {
	delay, ok := retrypolicy.Delay(job.Attempt + 1)
	if !ok {
		log.Error("mail failed temporarily and is out of tries", "error", cause)
		return GaveUp, s.Store.Done(ctx, s.Index, raw)
	}
	due := s.now() + delay.Seconds()
	if job.NotAfter != nil && due >= *job.NotAfter {
		log.Warn("mail failed temporarily and was dropped: it expires before the retry", "error", cause)
		return Expired, s.Store.Done(ctx, s.Index, raw)
	}
	next, err := job.NextAttempt().Encode()
	if err != nil {
		log.Error("mail could not be written back for a retry", "error", err)
		return Failed, s.Store.Done(ctx, s.Index, raw)
	}
	log.Warn("mail failed temporarily, retrying", "in", delay, "error", cause)
	return Retry, s.Store.RetryLater(ctx, s.Index, raw, next, due)
}

func message(job mailjob.Job) mailer.Message {
	m := job.Message
	attachments := make([]mailer.Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		attachments = append(attachments, mailer.Attachment{
			Filename:    a.Filename,
			Content:     a.Content,
			ContentType: a.ContentType,
		})
	}
	if len(attachments) == 0 {
		attachments = nil
	}
	return mailer.Message{
		To:             m.To,
		Subject:        m.Subject,
		TextBody:       m.TextBody,
		HTMLBody:       m.HTMLBody,
		Sender:         m.Sender,
		ReplyTo:        m.ReplyTo,
		Attachments:    attachments,
		IdempotencyKey: m.IdempotencyKey,
	}
}

func (s Sender) now() float64 {
	if s.Clock != nil {
		return s.Clock()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

func (s Sender) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
