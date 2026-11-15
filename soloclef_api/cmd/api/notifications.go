package main

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
)

// queuePublisher is notify.Publisher over this process's Celery queue: the
// notification step publishes a push's delivery the way every write here
// enqueues its background work, for the worker to run.
type queuePublisher struct{ queue celeryqueue.Enqueuer }

func (p queuePublisher) Send(ctx context.Context, message produce.Message) (string, error) {
	return p.queue.Enqueue(ctx, message)
}

// notifier is this process's one notification step (soloclef_notify/notify):
// what the Studio's artifact runner and the mail connector's waiting
// drafts report through. The push itself is the worker's.
func notifier(pool *pgxpool.Pool, queue celeryqueue.Enqueuer, changes livechange.Publisher, logger *slog.Logger) (*notify.Notifier, error) {
	n := &notify.Notifier{Database: pool, Publisher: queuePublisher{queue: queue}, Changes: changes, Log: logger}
	return n, n.Check()
}
