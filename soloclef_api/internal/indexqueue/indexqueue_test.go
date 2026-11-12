package indexqueue

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/queues"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// list is a broker that keeps what was pushed, by Redis key.
type list struct {
	pushed map[string][]string
}

func (l *list) LPush(_ context.Context, key, value string) error {
	if l.pushed == nil {
		l.pushed = map[string][]string{}
	}
	l.pushed[key] = append(l.pushed[key], value)
	return nil
}

// producer is the real producer over the stand-in broker, with a default
// queue that is NOT the indexing queue, as in a deployment.
type producer struct {
	real produce.Producer
}

func (p producer) Enqueue(ctx context.Context, message produce.Message) (string, error) {
	return p.real.Send(ctx, message)
}

// The job must arrive where the indexing worker listens, in the shape its
// reader takes: one positional argument, the document's id. This goes
// through the real encoder and the real decoder (soloclef_celery), which
// are the two ends the worker and this tier share.
func TestTheJobIsTheOneTheIndexingWorkerReads(t *testing.T) {
	broker := &list{}
	queue := producer{real: produce.Producer{
		Broker: broker, Routes: routing.Table{Default: "soloclef"},
		Origin: "test@host", ReplyTo: "00000000-0000-0000-0000-000000000000",
	}}
	indexing := Indexing{Queue: queue, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if err := indexing.Enqueue(context.Background(), 4211,
		produce.LogContext{RequestID: "req-1", UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}

	// On the indexing queue's list and on no other: the default queue is
	// consumed by Python workers, which have no such task.
	key := queues.Key(Queue, 0)
	if len(broker.pushed) != 1 || len(broker.pushed[key]) != 1 {
		t.Fatalf("pushed to %v, want one message on %q", keys(broker.pushed), key)
	}
	message, err := celery.Decode([]byte(broker.pushed[key][0]))
	if err != nil {
		t.Fatalf("the worker's reader refuses the message: %v", err)
	}
	if message.Task != "index_document" || message.Queue != "soloclef.index" {
		t.Errorf("task %q on queue %q", message.Task, message.Queue)
	}
	// JSON has one number type; the worker's reader takes a whole float64.
	if len(message.Args) != 1 || message.Args[0] != float64(4211) || len(message.Kwargs) != 0 {
		t.Errorf("arguments %v %v, want the document id alone", message.Args, message.Kwargs)
	}
}

// A delayed job carries its delay as an eta, which is what the indexing
// worker schedules by: a job taken before its eta waits in Redis, not in a
// worker slot. The eta has to be the delay from now, on the same queue.
func TestADelayedJobCarriesItsDelayAsAnETA(t *testing.T) {
	broker := &list{}
	queue := producer{real: produce.Producer{
		Broker: broker, Routes: routing.Table{Default: "soloclef"},
		Origin: "test@host", ReplyTo: "00000000-0000-0000-0000-000000000000",
	}}
	indexing := Indexing{Queue: queue, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	before := time.Now()
	if err := indexing.EnqueueAfter(context.Background(), 4212, 15*time.Second, produce.LogContext{RequestID: "req-2"}); err != nil {
		t.Fatal(err)
	}

	pushed := broker.pushed[queues.Key(Queue, 0)]
	if len(pushed) != 1 {
		t.Fatalf("pushed to %v, want one message on the indexing queue", keys(broker.pushed))
	}
	message, err := celery.Decode([]byte(pushed[0]))
	if err != nil {
		t.Fatal(err)
	}
	if earliest, latest := before.Add(15*time.Second), time.Now().Add(15*time.Second); message.ETA.Before(earliest.Add(-time.Second)) || message.ETA.After(latest.Add(time.Second)) {
		t.Errorf("eta %v, want 15s from now (between %v and %v)", message.ETA, earliest, latest)
	}
	if due, _ := message.Due(time.Now()); due {
		t.Error("a job delayed by 15s is due at once")
	}
}

// marker is the document's status for one test.
type marker struct {
	marked map[int64]string
	err    error
}

func (m *marker) MarkQueueingFailed(_ context.Context, documentID int64, reason string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	if m.marked == nil {
		m.marked = map[int64]string{}
	}
	m.marked[documentID] = reason
	return true, nil
}

// down is a broker that does not answer.
type down struct{}

func (down) Enqueue(context.Context, produce.Message) (string, error) {
	return "", errors.New("dial tcp: connection refused")
}

func TestADocumentWhoseJobCannotBeQueuedIsMarkedFailed(t *testing.T) {
	status := &marker{}
	indexing := Indexing{Queue: down{}, Marker: status, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// A request whose client has already hung up: the mark must still be
	// written, or the document waits for ever.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	err := indexing.Enqueue(gone, 4211, produce.LogContext{})

	if !errors.Is(err, ErrNotQueued) {
		t.Fatalf("error %v, want ErrNotQueued", err)
	}
	reason, marked := status.marked[4211]
	if !marked {
		t.Fatal("the document was left pending with no job behind it")
	}
	// The reason is read by a person: it says what happened and what to do,
	// and carries nothing of the broker's address or error.
	if !strings.Contains(reason, "queue did not answer") || !strings.Contains(reason, "Re-index the document") || strings.Contains(reason, "tcp") {
		t.Errorf("reason %q", reason)
	}
}

func TestAMarkThatFailsIsReportedBesideTheQueueFailure(t *testing.T) {
	indexing := Indexing{Queue: down{}, Marker: &marker{err: errors.New("the database went away")},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err := indexing.Enqueue(context.Background(), 4211, produce.LogContext{})
	if !errors.Is(err, ErrNotQueued) || !strings.Contains(err.Error(), "could not be marked failed") {
		t.Fatalf("error %v, want both failures named", err)
	}
}

func keys(pushed map[string][]string) []string {
	var listed []string
	for key := range pushed {
		listed = append(listed, key)
	}
	return listed
}
