// Package service runs the mail senders and the retry clock: the process
// app/mail_queue/service.py is.
//
// Exactly one of these runs per deployment, as exactly one Python mail_sender
// did. At start it puts back whatever the previous run's senders were holding,
// which is only safe when no other copy is sending -- and during a cutover
// that means one of the two senders, not both.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/drain"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuestore"
)

// retryClock is RETRY_CLOCK_SECONDS: how often the due retries are put back in
// line.
const retryClock = time.Second

// Settings is what the process needs to know. BuildMailer is called once per
// sender, so each gets its own connection.
type Settings struct {
	Senders     int
	Transport   string
	BuildMailer func() (mailer.Mailer, error)
	Store       queuestore.Store
	Log         *slog.Logger
}

// Run starts the senders and the retry clock, and returns when ctx is done and
// every sender has finished the message it was on.
func Run(ctx context.Context, settings Settings) error {
	if settings.Senders < 1 {
		return errors.New("service: MAIL_SENDERS must be a whole number of at least 1")
	}
	log := settings.Log
	if log == nil {
		log = slog.Default()
	}

	held, err := settings.Store.RequeueHeld(ctx)
	if err != nil {
		return err
	}
	if held > 0 {
		log.Warn("mail from a stopped run put back in line", "messages", held)
	}

	// Every mailer is built before any sender starts, so bad settings stop the
	// service at once instead of failing each message.
	senders := make([]drain.Sender, 0, settings.Senders)
	for i := range settings.Senders {
		built, err := settings.BuildMailer()
		if err != nil {
			for _, started := range senders {
				_ = started.Mailer.Close()
			}
			return err
		}
		senders = append(senders, drain.Sender{
			Index:  i,
			Store:  settings.Store,
			Mailer: built,
			Log:    log,
		})
	}

	depth, err := settings.Store.Depth(ctx)
	if err != nil {
		return err
	}
	log.Info("mail sender started", "senders", settings.Senders, "transport", settings.Transport,
		"ready", depth.Ready, "retry", depth.Retry)

	done := make(chan error, len(senders)+1)
	for _, sender := range senders {
		go func(s drain.Sender) { done <- s.Run(ctx) }(sender)
	}
	go func() { done <- runRetryClock(ctx, settings.Store, log) }()

	var failure error
	for range len(senders) + 1 {
		if err := <-done; err != nil && failure == nil {
			failure = err
		}
	}
	log.Info("mail sender stopped")
	return failure
}

func runRetryClock(ctx context.Context, store queuestore.Store, log *slog.Logger) error {
	ticker := time.NewTicker(retryClock)
	defer ticker.Stop()
	for {
		moved, err := store.PromoteDue(ctx, float64(time.Now().UnixNano())/1e9)
		if err != nil && ctx.Err() == nil {
			log.Error("the mail retry set could not be read", "error", err)
		}
		if moved > 0 {
			log.Info("mail retries due, back in line", "messages", moved)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
