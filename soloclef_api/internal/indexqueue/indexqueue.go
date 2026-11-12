// Package indexqueue asks the indexing worker to index a document.
//
// # The message
//
// One task, "index_document", on one queue, "soloclef.index", carrying one
// argument: the document's id. Which file, which parser and which account
// are read from the document's row when the job runs
// (soloclef_worker/internal/indextask says why), so a route that wants a
// document indexed writes the row first and sends the id after the commit.
// The two names are the worker's; they are repeated here, not imported,
// because the worker's packages are internal to its module, and a test in
// this package pins them to the envelope the worker's own reader accepts.
//
// # When the queue is down
//
// A document that is "pending" with no job behind it would wait for ever,
// and the person who uploaded it would watch a spinner that nothing can
// stop. So a failed enqueue marks the document failed, with a reason that
// says what to do, and reports the failure to the caller. The file is
// stored and the row is intact: re-indexing the document later needs no
// second upload.
package indexqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

const (
	// Task is the task's name on the queue (indextask.Task).
	Task = "index_document"
	// Queue is the only queue the indexing worker consumes
	// (indextask.Queue). It is named on every message, because the routing
	// table would otherwise put the task on the default queue, where a
	// Python worker would refuse it.
	Queue = "soloclef.index"
)

// QueueingFailed is the reason a document carries when its job could not be
// queued. It is written for the person looking at the document list.
const QueueingFailed = "Indexing could not be started because the job queue did not answer. Re-index the document to try again."

// Message is the job for one document.
func Message(documentID int64, context produce.LogContext) produce.Message {
	return produce.Message{
		Task:    Task,
		Args:    []any{documentID},
		Queue:   Queue,
		Context: context,
		// Nothing polls for the task's result: the outcome of indexing is
		// the document's own status (indextask, "No result row").
		IgnoreResult: true,
	}
}

// Marker records that a document's job could not be queued.
type Marker interface {
	// MarkQueueingFailed reports whether the document was still pending
	// and is now failed.
	MarkQueueingFailed(ctx context.Context, documentID int64, reason string) (bool, error)
}

// PostgresMarker is Marker over the pool.
type PostgresMarker struct {
	Pool *pgxpool.Pool
}

func (m PostgresMarker) MarkQueueingFailed(ctx context.Context, documentID int64, reason string) (bool, error) {
	changed, err := New(m.Pool).MarkQueueingFailed(ctx, MarkQueueingFailedParams{ID: documentID, Reason: reason})
	return changed == 1, err
}

// Indexing queues index jobs and owns what happens when it cannot.
type Indexing struct {
	Queue  celeryqueue.Enqueuer
	Marker Marker
	Logger *slog.Logger
}

// ErrNotQueued is returned by Enqueue when the job is not on the queue. The
// document has been marked failed unless the returned error also says that
// could not be done.
var ErrNotQueued = errors.New("indexqueue: the indexing job was not queued")

// Enqueue queues the document's job. It must be called after the
// transaction that wrote the document has committed: the worker reads the
// row the moment it takes the message.
//
// On failure the document is marked failed and ErrNotQueued is returned,
// wrapping the cause.
func (i Indexing) Enqueue(ctx context.Context, documentID int64, logContext produce.LogContext) error {
	return i.EnqueueAfter(ctx, documentID, 0, logContext)
}

// EnqueueAfter is Enqueue for a job that should not run before delay has
// passed. A note's autosave uses it: the job reads the row when it runs, so
// every save made while it waits is indexed by that one job, and typing
// does not embed the note once per save. A delay of zero is Enqueue.
//
// The delay travels as the message's eta, and a worker that takes the
// message early hands it to a sorted set in Redis until it is due
// (soloclef_worker/internal/broker, scheduled.go): a waiting job holds none
// of the indexing queue's slots, so notes being typed into never hold up an
// upload.
func (i Indexing) EnqueueAfter(ctx context.Context, documentID int64, delay time.Duration, logContext produce.LogContext) error {
	message := Message(documentID, logContext)
	message.Countdown = delay
	_, err := i.Queue.Enqueue(ctx, message)
	if err == nil {
		return nil
	}
	i.Logger.LogAttrs(ctx, slog.LevelError, "could not queue a document's indexing",
		slog.Int64("document_id", documentID), slog.String("request_id", logContext.RequestID),
		slog.String("error", err.Error()))
	// Not the request's context: a client that hung up must not leave the
	// document pending with nothing behind it.
	markCtx, done := context.WithTimeout(context.WithoutCancel(ctx), markTimeout)
	defer done()
	if _, markErr := i.Marker.MarkQueueingFailed(markCtx, documentID, QueueingFailed); markErr != nil {
		i.Logger.LogAttrs(ctx, slog.LevelError, "could not mark a document whose indexing was not queued; it is left pending",
			slog.Int64("document_id", documentID), slog.String("error", markErr.Error()))
		return fmt.Errorf("%w: %w; and the document could not be marked failed: %w", ErrNotQueued, err, markErr)
	}
	return fmt.Errorf("%w: %w", ErrNotQueued, err)
}
