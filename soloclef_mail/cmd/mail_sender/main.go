// Command mail_sender delivers the queued email: MAIL_SENDERS senders over
// MAIL_TRANSPORT, and the clock that puts due retries back in line.
//
// It is the Go half of app/mail_queue's sender, and it drains the same Redis
// keys the Python mail_sender drains, including the dedupe keys. Run one
// sender process, not both: at start each puts back whatever a stopped run was
// holding and drops its claims, and two of them doing that at once is how a
// message in flight goes out twice.
//
// Restarting it touches nothing that is generating, so it needs no deploy
// gate. See custom/docs/go-mail.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupestore"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuestore"
	"git.danvics.com/danvics/soloclef/soloclef_mail/service"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("the mail sender stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := load()
	if err != nil {
		return err
	}
	if !cfg.enabled {
		// is_mail_enabled: with mail off there is nothing to deliver, and a
		// sender that started anyway would hold a queue nobody is filling.
		return errors.New("SMTP_ENABLED is off; the mail sender has nothing to do")
	}

	url := os.Getenv("REDIS_APP_URL")
	if url == "" {
		return errors.New("REDIS_APP_URL is required: it names the Redis the mail queue lives in")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		return err
	}
	client := redis.NewClient(options)
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	return service.Run(ctx, service.Settings{
		Senders:     cfg.senders,
		Transport:   cfg.transport,
		BuildMailer: cfg.buildMailer,
		Store:       queuestore.Store{Redis: client},
		Guard:       dedupe.HandoverGuard{Keys: dedupestore.Keys{Redis: client}},
	})
}

func warn(message string) {
	slog.Warn(message)
}
