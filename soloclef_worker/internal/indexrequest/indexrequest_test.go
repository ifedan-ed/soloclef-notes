package indexrequest

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indextask"
)

func TestTheNamesAreTheIndexersOwn(t *testing.T) {
	if Task != indextask.Task || Queue != indextask.Queue {
		t.Fatalf("this package publishes %s on %s; the indexer runs %s from %s", Task, Queue, indextask.Task, indextask.Queue)
	}
}

type brokerFunc func(key, value string)

func (f brokerFunc) LPush(_ context.Context, key, value string) error {
	f(key, value)
	return nil
}

// The message this worker publishes for a podcast's document is one the
// indexer's own reader accepts, on the indexer's queue.
func TestTheMessageIsTheOneTheIndexerReads(t *testing.T) {
	var sent struct{ key, value string }
	producer := produce.Producer{
		Broker: brokerFunc(func(key, value string) { sent.key, sent.value = key, value }),
		Origin: "gen1@test", ReplyTo: "reply",
	}
	if _, err := producer.Send(t.Context(), Message(42)); err != nil {
		t.Fatal(err)
	}
	if sent.key != "soloclef.index" {
		t.Fatalf("published to %q", sent.key)
	}
	message, err := celery.Decode([]byte(sent.value))
	if err != nil {
		t.Fatal(err)
	}
	if message.Task != "index_document" || len(message.Args) != 1 || message.Args[0] != float64(42) || !message.IgnoreResult {
		t.Fatalf("the indexer would read %+v", message)
	}
}
