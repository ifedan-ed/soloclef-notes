package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestARegisteredTaskRunsUnderItsOwnDeadline(t *testing.T) {
	var sawDeadline time.Duration
	table, err := New(quiet(), Job{Task: "podcast.draft_transcript", Deadline: time.Hour,
		Run: func(ctx context.Context, msg *celery.Message) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("the job ran with no deadline")
			}
			sawDeadline = time.Until(deadline)
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Run(context.Background(), &celery.Message{Task: "podcast.draft_transcript"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sawDeadline <= 59*time.Minute || sawDeadline > time.Hour {
		t.Fatalf("the job saw a deadline %v away, want about an hour", sawDeadline)
	}
}

// A message nothing here can run is acknowledged with an error, never
// handed back: handing it back would put it at the tail of its queue for
// ever.
func TestAnUnknownTaskIsRefusedAndNotRequeued(t *testing.T) {
	table, err := New(quiet())
	if err != nil {
		t.Fatal(err)
	}
	err = table.Run(context.Background(), &celery.Message{Task: "billing.roll_periods", Queue: "soloclef"})
	if !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("got %v, want ErrUnknownTask", err)
	}
	if errors.Is(err, broker.ErrRequeue) {
		t.Fatal("an unknown task was handed back to its queue")
	}
}

func TestARequeueIsPassedThroughToTheConsumer(t *testing.T) {
	table, err := New(quiet(), Job{Task: "x", Deadline: time.Second,
		Run: func(context.Context, *celery.Message) error {
			return errors.Join(broker.ErrRequeue, errors.New("the database did not answer"))
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Run(context.Background(), &celery.Message{Task: "x"}); !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("got %v, want a requeue", err)
	}
}

func TestARegistrationMissingAPartIsRefused(t *testing.T) {
	run := func(context.Context, *celery.Message) error { return nil }
	cases := map[string][]Job{
		"no name":     {{Deadline: time.Second, Run: run}},
		"no handler":  {{Task: "x", Deadline: time.Second}},
		"no deadline": {{Task: "x", Run: run}},
		"twice":       {{Task: "x", Deadline: time.Second, Run: run}, {Task: "x", Deadline: time.Second, Run: run}},
	}
	for name, registered := range cases {
		if _, err := New(quiet(), registered...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
