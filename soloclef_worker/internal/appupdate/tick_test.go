package appupdate

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// memoryFence is Fence in memory.
type memoryFence struct {
	mu   sync.Mutex
	held map[int64]bool
}

func (f *memoryFence) Take(_ context.Context, moment time.Time, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[moment.Unix()] {
		return false, nil
	}
	f.held[moment.Unix()] = true
	return true, nil
}

func (f *memoryFence) Release(_ context.Context, moment time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, moment.Unix())
	return nil
}

// recorder is Sender, keeping what it was given.
type recorder struct{ sent []produce.Message }

func (r *recorder) Send(_ context.Context, message produce.Message) (string, error) {
	r.sent = append(r.sent, message)
	return message.ID, nil
}

// The next tick is published once for the next whole hour, however many
// workers seed it, and a pass schedules the one after before it works.
func TestTheTickIsPublishedOnceAnHour(t *testing.T) {
	sent := &recorder{}
	fence := &memoryFence{held: map[int64]bool{}}
	at := time.Date(2026, 11, 13, 12, 20, 0, 0, time.UTC)
	job := &Job{
		BundleID: bundle, Store: &fakeStore{}, Rows: &fakeRows{}, Notifier: &fakeNotifier{},
		Publisher: sent, Fence: fence,
		Timing: Timing{Every: time.Hour, LookupEvery: 24 * time.Hour, Accounts: 10},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return at },
	}
	if err := job.Check(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := job.Seed(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent.sent) != 1 {
		t.Fatalf("%d ticks published for one moment", len(sent.sent))
	}
	if tick := sent.sent[0]; tick.Task != TickTask || tick.Countdown != 40*time.Minute {
		t.Errorf("tick = %+v", tick)
	}

	// The tick itself, at 13:00, schedules 14:00 first. Nothing is kept
	// and Apple names nothing, so the pass says so; the chain goes on.
	at = time.Date(2026, 11, 13, 13, 0, 0, 0, time.UTC)
	job.Store = &fakeStore{err: ErrNotListed}
	if err := job.Run(context.Background(), nil); err == nil {
		t.Error("a pass with no version said nothing")
	}
	if len(sent.sent) != 2 || sent.sent[1].Countdown != time.Hour {
		t.Errorf("the next tick: %+v", sent.sent)
	}
}
