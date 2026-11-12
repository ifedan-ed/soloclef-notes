package jobreconcile

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
)

// outbox is Rows: what it hands over, and what it was asked.
type outbox struct {
	stale      []Queued
	err        error
	staleAfter time.Duration
	limit      int
}

func (o *outbox) TakeStale(_ context.Context, staleAfter time.Duration, limit int) ([]Queued, error) {
	o.staleAfter, o.limit = staleAfter, limit
	return o.stale, o.err
}

// queue is Sender: what was published, and which task ids it refuses.
type queue struct {
	sent    []produce.Message
	refuses map[string]bool
}

func (q *queue) Send(_ context.Context, message produce.Message) (string, error) {
	if q.refuses[message.Task] || q.refuses[message.ID] {
		return "", errors.New("the broker is down")
	}
	q.sent = append(q.sent, message)
	return message.ID, nil
}

func (q *queue) of(task string) []produce.Message {
	var found []produce.Message
	for _, message := range q.sent {
		if message.Task == task {
			found = append(found, message)
		}
	}
	return found
}

// fences is Fence, in memory.
type fences struct {
	taken map[int64]time.Duration
	err   error
}

func (f *fences) Take(_ context.Context, moment time.Time, hold time.Duration) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if _, held := f.taken[moment.Unix()]; held {
		return false, nil
	}
	f.taken[moment.Unix()] = hold
	return true, nil
}

func (f *fences) Release(_ context.Context, moment time.Time) error {
	delete(f.taken, moment.Unix())
	return nil
}

type world struct {
	job    *Job
	rows   *outbox
	queue  *queue
	fences *fences
	now    time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		rows: &outbox{}, queue: &queue{refuses: map[string]bool{}}, fences: &fences{taken: map[int64]time.Duration{}},
		// Twenty seconds into a minute.
		now: time.Date(2026, 11, 12, 9, 30, 20, 0, time.UTC),
	}
	w.job = &Job{
		Rows: w.rows, Publisher: w.queue, Fence: w.fences,
		Timing: Timing{Every: time.Minute, StaleAfter: 2 * time.Minute, Batch: 100},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return w.now },
	}
	if err := w.job.Check(); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestAnIncompleteReconcilerIsRefused(t *testing.T) {
	w := newWorld(t)
	w.job.Timing.StaleAfter = 30 * time.Second
	if w.job.Check() == nil {
		t.Error("a job would be republished before one interval had passed, and that was accepted")
	}
	w.job.Timing = Timing{Every: time.Minute, StaleAfter: time.Minute}
	if w.job.Check() == nil {
		t.Error("a batch of nothing was accepted")
	}
	w = newWorld(t)
	w.job.Fence = nil
	if w.job.Check() == nil {
		t.Error("a reconciler with no fence was accepted")
	}
}

// One tick a moment: the first to ask publishes it, for the next whole
// interval, and everyone else asking for that moment publishes nothing.
func TestOneTickIsPublishedForAMoment(t *testing.T) {
	w := newWorld(t)
	for range 3 { // three workers starting within the same minute
		if err := w.job.Seed(t.Context()); err != nil {
			t.Fatal(err)
		}
		w.now = w.now.Add(10 * time.Second)
	}
	ticks := w.queue.of(Task)
	if len(ticks) != 1 {
		t.Fatalf("%d ticks were published for one moment", len(ticks))
	}
	tick := ticks[0]
	due := time.Date(2026, 11, 12, 9, 31, 0, 0, time.UTC)
	if tick.Countdown != 40*time.Second || tick.ID != Task+":"+strconv.FormatInt(due.Unix(), 10) || tick.Queue != "" || !tick.IgnoreResult || tick.ExpiresIn != 0 {
		t.Errorf("the tick is %+v; want it due at the next whole minute, routed by the table, never expiring", tick)
	}
	// The next minute is another moment.
	w.now = time.Date(2026, 11, 12, 9, 31, 0, 0, time.UTC)
	if err := w.job.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ticks := w.queue.of(Task); len(ticks) != 2 || ticks[1].Countdown != time.Minute {
		t.Errorf("after the minute turned: %+v", ticks)
	}
}

// A tick that could not be published gives its moment back, so the next to
// ask can publish it; otherwise a broker's hiccup would end the chain for
// as long as the fence is held.
func TestATickThatWasNotPublishedGivesItsMomentBack(t *testing.T) {
	w := newWorld(t)
	w.queue.refuses[Task] = true
	if err := w.job.Seed(t.Context()); err == nil {
		t.Fatal("a tick that was not published reported success")
	}
	w.queue.refuses[Task] = false
	if err := w.job.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(w.queue.of(Task)) != 1 {
		t.Error("the moment was not given back")
	}
}

// Each stale job is published as the message its worker claims: the video
// task, its id, under the row's own task id.
func TestStaleJobsArePublishedUnderTheirOwnTaskIDs(t *testing.T) {
	w := newWorld(t)
	w.rows.stale = []Queued{
		{ID: 7, TaskID: "deliverable-job:7:attempt:1"},
		{ID: 8, TaskID: ""}, // nothing could claim it
		{ID: 9, TaskID: "deliverable-job:9:attempt:4"},
		{ID: 10, TaskID: "deliverable-job:10:attempt:2"},
	}
	w.queue.refuses["deliverable-job:9:attempt:4"] = true
	published, err := w.job.Republish(t.Context())
	if published != 2 || err == nil {
		t.Fatalf("Republish = %d, %v; want the two that could be published, and the failure said", published, err)
	}
	if w.rows.staleAfter != 2*time.Minute || w.rows.limit != 100 {
		t.Errorf("the outbox was asked for %s and %d", w.rows.staleAfter, w.rows.limit)
	}
	sent := w.queue.of(videojob.Task)
	if len(sent) != 2 || sent[0].ID != "deliverable-job:7:attempt:1" || sent[1].ID != "deliverable-job:10:attempt:2" ||
		sent[0].Kwargs["job_id"] != int64(7) || sent[1].Kwargs["job_id"] != int64(10) {
		t.Errorf("published %+v", sent)
	}
}

// A run schedules its successor before it does anything else, so a run
// whose work fails still has one.
func TestARunSchedulesTheNextEvenWhenItsWorkFails(t *testing.T) {
	w := newWorld(t)
	w.rows.err = errors.New("the database is down")
	err := w.job.Run(t.Context(), nil)
	if err == nil || errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("Run = %v; want the failure, acknowledged: the next tick tries again", err)
	}
	if len(w.queue.of(Task)) != 1 {
		t.Error("the run left no successor")
	}
}

// A run that could not schedule its successor goes back on the queue: it is
// the only message that would have continued the chain.
func TestARunThatCouldNotScheduleTheNextIsPutBack(t *testing.T) {
	w := newWorld(t)
	w.rows.stale = []Queued{{ID: 7, TaskID: "deliverable-job:7:attempt:1"}}
	w.fences.err = errors.New("redis is down")
	err := w.job.Run(t.Context(), nil)
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("Run = %v; want the message put back", err)
	}
	// Its work was still done.
	if len(w.queue.of(videojob.Task)) != 1 {
		t.Error("the stale job was not published")
	}
}
