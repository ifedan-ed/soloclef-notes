// Package celeryqueue enqueues the API's background work for the Go worker
// (soloclef_worker), on Valkey, in the Celery wire format both speak.
//
// The envelope, the routing and the enqueue stamp are soloclef_celery's,
// shared with the worker so that the bytes cannot drift apart; this package is
// the deployment's half of it — which Valkey, which default queue, and the
// client the API already uses.
//
// An enqueue that fails is returned as an error rather than hidden. The Python
// call sites divide into two, and both need to be able to tell: a route that
// reverts its own row and answers 503 (app/routes/documents_routes.py), and a
// reporting path that logs and carries on (app/push/events.py).
package celeryqueue

import (
	"context"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// Enqueuer is what a route holds: everything it needs to put one task on a
// queue, and nothing it needs to know about Redis.
type Enqueuer interface {
	Enqueue(ctx context.Context, message produce.Message) (string, error)
}

// Queue is this process's producer.
type Queue struct {
	broker   *ValkeyBroker
	producer produce.Producer
}

// New reads the broker's address and the default queue's name. Both are
// required settings with no default (internal/config): a producer that guessed
// either would put work where no worker is looking.
func New(brokerURL, defaultQueue string) (*Queue, error) {
	if defaultQueue == "" {
		return nil, fmt.Errorf("celery queue: no default queue name")
	}
	broker, err := NewValkeyBroker(brokerURL)
	if err != nil {
		return nil, err
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return nil, err
	}
	return &Queue{
		broker: broker,
		producer: produce.Producer{
			Broker:  broker,
			Routes:  routing.Table{Default: defaultQueue},
			Origin:  produce.Origin(),
			ReplyTo: replyTo,
		},
	}, nil
}

// Enqueue publishes one task and returns the id .delay() would have returned.
func (q *Queue) Enqueue(ctx context.Context, message produce.Message) (string, error) {
	return q.producer.Send(ctx, message)
}

// Close releases the connections.
func (q *Queue) Close() { q.broker.Close() }
