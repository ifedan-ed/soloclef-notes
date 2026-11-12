// Package indextask is the indexing job as the queue sees it: a broker.Runner
// that runs a task in this process, as Go, instead of handing it to a Python
// task process the way internal/executor does.
//
// # The message
//
// The task is named "index_document" and carries one argument, the document's
// id, as the first positional argument or as the keyword `document_id`. That
// is all: which file, which parser and which account are read from the
// document's row when the job runs, so a message delivered twice, or
// delivered after the row changed, indexes what the row says now.
//
// # No result row
//
// The Python executor writes a Celery result row for every task, because
// something polls AsyncResult for it. Nothing polls for this one: the
// outcome of indexing is the document's own status, which the apps already
// read. A second record of it could only disagree with the first.
package indextask

import (
	"context"
	"errors"
	"fmt"
	"math"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
)

// Task is the task's name on the queue.
const Task = "index_document"

// Queue is the queue the task is published to, and the only queue an
// indexing worker should be given: a worker that also consumed a Python
// queue would refuse every message on it.
const Queue = "soloclef.index"

// Indexer is the job (indexing.Indexer).
type Indexer interface {
	Index(ctx context.Context, documentID int64) (indexing.Result, error)
}

// Ready is told of each document a job left ready (namerequest.Requester:
// the notebooks reading it may name themselves now). It cannot fail the
// job: the document is ready whatever it does.
type Ready interface {
	DocumentReady(ctx context.Context, documentID int64)
}

// Runner runs index_document messages.
type Runner struct {
	indexer Indexer
	// observe is told how each job ended, for the metrics.
	observe func(indexing.Result)
	ready   Ready
}

// New builds the runner. observe and ready may be nil.
func New(indexer Indexer, observe func(indexing.Result), ready Ready) (*Runner, error) {
	if indexer == nil {
		return nil, errors.New("indextask: no indexer")
	}
	if observe == nil {
		observe = func(indexing.Result) {}
	}
	return &Runner{indexer: indexer, observe: observe, ready: ready}, nil
}

// Run is broker.Runner. A nil return or a plain error acknowledges the
// message; broker.ErrRequeue puts it back.
func (r *Runner) Run(ctx context.Context, msg *celery.Message) error {
	if msg.Task != Task {
		// Acknowledged, not put back: nothing in this process will ever be
		// able to run it, and a message that is put back for ever starves
		// the queue. The error is logged by the consumer with the task name.
		return fmt.Errorf("indextask: this worker runs %s only, and was sent %q on queue %q", Task, msg.Task, msg.Queue)
	}
	documentID, err := documentID(msg)
	if err != nil {
		return err
	}
	result, err := r.indexer.Index(ctx, documentID)
	r.observe(result)
	if err == nil && result.Outcome == indexing.Indexed && r.ready != nil {
		r.ready.DocumentReady(ctx, documentID)
	}
	if errors.Is(err, indexing.ErrRetry) {
		return fmt.Errorf("%w: document %d: %w", broker.ErrRequeue, documentID, err)
	}
	return err
}

// documentID reads the one argument. JSON has one number type, so the id
// arrives as a float64 and is accepted only when it is a whole, positive
// number that an id could be.
func documentID(msg *celery.Message) (int64, error) {
	var raw any
	switch {
	case len(msg.Args) == 1 && len(msg.Kwargs) == 0:
		raw = msg.Args[0]
	case len(msg.Args) == 0 && len(msg.Kwargs) == 1:
		raw = msg.Kwargs["document_id"]
	default:
		return 0, fmt.Errorf("indextask: %s takes one argument, the document id; got %d positional and %d keyword",
			Task, len(msg.Args), len(msg.Kwargs))
	}
	number, isNumber := raw.(float64)
	if !isNumber || number < 1 || number > math.MaxInt32 || number != math.Trunc(number) {
		return 0, fmt.Errorf("indextask: %v is not a document id", raw)
	}
	return int64(number), nil
}
