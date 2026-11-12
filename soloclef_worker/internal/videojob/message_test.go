package videojob

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

type capturedPush struct{ key, value string }

func (c *capturedPush) LPush(_ context.Context, key, value string) error {
	c.key, c.value = key, value
	return nil
}

// The message this tier publishes for a job is one its own Run accepts: on
// the heavy queue, under the row's task id, with the one keyword argument.
func TestTheMessageIsTheOneRunReads(t *testing.T) {
	var sent capturedPush
	producer := produce.Producer{Broker: &sent, Routes: routing.Table{Default: "soloclef"}, Origin: "gen1@test", ReplyTo: "reply"}
	if _, err := producer.Send(t.Context(), Message(42, "deliverable-job:42:attempt:3")); err != nil {
		t.Fatal(err)
	}
	if sent.key != "soloclef.heavy" {
		t.Fatalf("published to %q, want the heavy queue", sent.key)
	}
	message, err := celery.Decode([]byte(sent.value))
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := jobOf(message)
	if err != nil || jobID != 42 || message.Task != Task || message.ID != "deliverable-job:42:attempt:3" {
		t.Fatalf("Run would read job %d (%v) of task %q under id %q", jobID, err, message.Task, message.ID)
	}
}
