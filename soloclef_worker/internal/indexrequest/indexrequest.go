// Package indexrequest asks the indexing worker (cmd/indexer) to index a
// document this worker wrote: a podcast's transcript, kept as its
// artifact's document.
//
// It publishes exactly the message the API tier's internal/indexqueue
// publishes, through the one encoder both tiers share
// (soloclef_celery/produce): `index_document`, the document's id, on
// `soloclef.index`, with no result row. The two names are the indexer's own
// (internal/indextask). They are repeated here rather than imported, because
// importing that package would link the whole indexer (its parsers, its
// embedding client) into the worker binary; the test pins them to it.
package indexrequest

import (
	"context"
	"errors"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// The indexer's task and queue (indextask.Task, indextask.Queue).
const (
	Task  = "index_document"
	Queue = "soloclef.index"
)

// Publisher puts index_document messages on the indexer's queue.
type Publisher struct {
	producer produce.Producer
}

// New publishes through the worker's own Redis client.
func New(rdb redis.UniversalClient) (*Publisher, error) {
	if rdb == nil {
		return nil, errors.New("indexrequest: no Redis client")
	}
	replyTo, err := produce.NewID()
	if err != nil {
		return nil, err
	}
	return &Publisher{producer: produce.Producer{
		Broker: redisBroker{rdb: rdb}, Origin: produce.Origin(), ReplyTo: replyTo,
	}}, nil
}

// Message is the job for one document.
func Message(documentID int64) produce.Message {
	return produce.Message{
		Task: Task, Args: []any{documentID}, Queue: Queue,
		// The outcome of indexing is the document's own status.
		IgnoreResult: true,
	}
}

// Index publishes the document's job. It is called after the transaction
// that wrote the document has committed: the indexer reads the row the
// moment it takes the message.
func (p *Publisher) Index(ctx context.Context, documentID int64) error {
	_, err := p.producer.Send(ctx, Message(documentID))
	return err
}

type redisBroker struct{ rdb redis.UniversalClient }

func (b redisBroker) LPush(ctx context.Context, key, value string) error {
	return b.rdb.LPush(ctx, key, value).Err()
}
