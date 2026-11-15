package pushdelivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/delivery"
	"git.danvics.com/danvics/soloclef/soloclef_notify/expo"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
)

// How delivery paces itself. These are decisions about the jobs, made once
// here with the reason, not deployment settings.
const (
	// A push the service did not take is tried five times in all, 30 s,
	// 1, 2 and 4 minutes apart: long enough for a rate limit or a short
	// outage to pass, short enough that a push still means "just now".
	attempts = 5
	backoff  = 30 * time.Second
	// Expo asks that receipts be read about fifteen minutes after sending.
	receiptDelay = 15 * time.Minute
	// One request to Expo answers in well under a second; thirty seconds
	// is a service that is not going to answer.
	requestTimeout = 30 * time.Second

	// A delivery is a few statements and a request or two; a receipts
	// check twenty pages at most.
	deliverDeadline  = 2 * time.Minute
	receiptsDeadline = 5 * time.Minute
)

// Producer is the publisher the notifier and the jobs queue with, on the
// deployment's default queue.
func Producer(rdb redis.UniversalClient, defaultQueue string) (produce.Producer, error) {
	if rdb == nil || defaultQueue == "" {
		return produce.Producer{}, errors.New("pushdelivery: no Redis client or no default queue")
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return produce.Producer{}, err
	}
	return produce.Producer{
		Broker: Broker{Redis: rdb}, Routes: routing.Table{Default: defaultQueue},
		Origin: produce.Origin(), ReplyTo: replyTo,
	}, nil
}

// Notifier is the one notification step for this process: the worker's
// jobs and the indexer call it when work ends.
func Notifier(pool *pgxpool.Pool, publisher notify.Publisher, log *slog.Logger) (*notify.Notifier, error) {
	notifier := &notify.Notifier{Database: pool, Publisher: publisher, Log: log}
	return notifier, notifier.Check()
}

// Jobs is the two delivery jobs for the job table: the real ones, or, with
// push switched off, ones that acknowledge each message and say why nothing
// was sent.
func Jobs(config Config, pool *pgxpool.Pool, rdb redis.UniversalClient, publisher delivery.Publisher, log *slog.Logger) ([]jobs.Job, error) {
	if !config.Enabled {
		off := func(_ context.Context, msg *celery.Message) error {
			log.Debug("push is switched off; nothing sent", "task", msg.Task, "setting", Enabled)
			return nil
		}
		return []jobs.Job{
			{Task: notify.DeliverTask, Deadline: deliverDeadline, Run: off},
			{Task: notify.ReceiptsTask, Deadline: receiptsDeadline, Run: off},
		}, nil
	}
	sender := expo.Client{BaseURL: expo.ServiceURL, AccessToken: config.AccessToken, HTTP: &http.Client{Timeout: requestTimeout}}
	if err := sender.Check(); err != nil {
		return nil, err
	}
	sending := delivery.Sending{Expo: sender, Origin: config.Origin}
	if config.APNs.Set() {
		apple, err := APNsClient(config.APNs, log)
		if err != nil {
			return nil, err
		}
		sending.APNs, sending.APNsEnvironment = apple, config.APNs.Environment
		log.Info("iPhones are pushed through Apple directly", "environment", config.APNs.Environment, "topic", config.APNs.Topic)
	} else {
		log.Warn("no APNs key is set (SOLOCLEF_WORKER_APNS_*): iPhones registered with an Apple token will not be pushed; Android phones are, through Expo")
	}
	delivering := &delivery.Jobs{
		Database:  pool,
		Sending:   sending,
		Presence:  Presence{Redis: rdb},
		Fence:     Fence{Redis: rdb},
		Publisher: publisher,
		Timing:    delivery.Timing{Attempts: attempts, Backoff: backoff, ReceiptDelay: receiptDelay},
		Log:       log,
	}
	if err := delivering.Check(); err != nil {
		return nil, fmt.Errorf("pushdelivery: %w", err)
	}
	return []jobs.Job{
		{Task: notify.DeliverTask, Deadline: deliverDeadline, Run: delivering.Deliver},
		{Task: notify.ReceiptsTask, Deadline: receiptsDeadline, Run: delivering.Receipts},
	}, nil
}

// APNsClient is Apple's sender for the settings: the key read and checked
// now, so a missing file or a key that is not Apple's stops the worker at
// start rather than failing every iPhone's push later.
func APNsClient(config APNsConfig, log *slog.Logger) (*apns.Client, error) {
	key, err := os.ReadFile(config.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", APNsKeyFile, err)
	}
	signer, err := apns.NewSigner(key, config.KeyID, config.TeamID)
	if err != nil {
		return nil, fmt.Errorf("%s, %s, %s: %w", APNsKeyFile, APNsKeyID, APNsTeamID, err)
	}
	address, err := apns.URLFor(config.Environment)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", APNsEnvironment, err)
	}
	client := &apns.Client{BaseURL: address, Topic: config.Topic, Signer: signer, HTTP: apns.NewHTTPClient(nil), Log: log}
	return client, client.Check()
}
