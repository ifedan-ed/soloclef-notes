package namerequest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/notebookname"
)

type notebooks struct {
	ids []int64
	err error
}

func (n notebooks) ToName(context.Context, int64) ([]int64, error) { return n.ids, n.err }

type sender struct {
	sent []produce.Message
	err  error
}

func (s *sender) Send(_ context.Context, message produce.Message) (string, error) {
	s.sent = append(s.sent, message)
	return "id", s.err
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// The message is the one the worker's job reads, and the one the API
// publishes for a chat's mention: (notebook, "source", null), a moment late.
func TestTheMessageIsTheNamingJobsOwn(t *testing.T) {
	if Task != notebookname.Task {
		t.Fatalf("Task = %q, the job is %q", Task, notebookname.Task)
	}
	message := Message(12)
	if message.Task != Task || !reflect.DeepEqual(message.Args, []any{int64(12), "source", nil}) || message.Countdown != 4*time.Second {
		t.Fatalf("message = %+v", message)
	}
	if message.Queue != "" {
		t.Errorf("the queue is the routing table's to choose, not %q", message.Queue)
	}
}

func TestEachNotebookThatMayNameItselfIsAsked(t *testing.T) {
	out := &sender{}
	(&Requester{Notebooks: notebooks{ids: []int64{3, 9}}, Sender: out, Log: quiet}).DocumentReady(context.Background(), 41)
	if len(out.sent) != 2 || out.sent[0].Args[0] != int64(3) || out.sent[1].Args[0] != int64(9) {
		t.Fatalf("sent %+v", out.sent)
	}
}

// A failure is logged and goes no further: the document is ready either way.
func TestAFailureNeverReachesTheIndexer(t *testing.T) {
	out := &sender{}
	(&Requester{Notebooks: notebooks{err: errors.New("down")}, Sender: out, Log: quiet}).DocumentReady(context.Background(), 41)
	if len(out.sent) != 0 {
		t.Fatalf("sent %+v after the lookup failed", out.sent)
	}
	out = &sender{err: errors.New("broker down")}
	(&Requester{Notebooks: notebooks{ids: []int64{3, 9}}, Sender: out, Log: quiet}).DocumentReady(context.Background(), 41)
	if len(out.sent) != 2 {
		t.Fatalf("one failed publish stopped the others: %+v", out.sent)
	}
}
