package automationtriggers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_automations/eventwire"
	"git.danvics.com/danvics/soloclef/soloclef_automations/runqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
)

const digest = `{"name":"Digest","plan":[{"step_id":"a","action":"agent_task","params":{"query":"q"}}],"execution":{"concurrency":"always"}}`

// outbox records what the workers published.
type outbox struct {
	mu       sync.Mutex
	messages []produce.Message
}

func (o *outbox) Send(_ context.Context, message produce.Message) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, message)
	return message.ID, nil
}

func (o *outbox) of(task string) (matched []produce.Message) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, message := range o.messages {
		if message.Task == task {
			matched = append(matched, message)
		}
	}
	return matched
}

// throwawayRedis is this package's own database on the test Redis,
// emptied (internal/testredis).
func throwawayRedis(t *testing.T) *redis.Client {
	t.Helper()
	return testredis.Open(t, testredis.AutomationTriggers)
}

func automation(t *testing.T, pool *pgxpool.Pool, owner, definition string) (id int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `INSERT INTO automations (user_id, name, definition) VALUES ($1::uuid, 'Digest', $2) RETURNING id`, owner, definition).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func trigger(t *testing.T, pool *pgxpool.Pool, automationID int64, kind, params string, next *time.Time) (id int64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO automation_triggers (automation_id, type, params, static_inputs, next_fire_at)
		VALUES ($1, $2::automation_trigger_type, $3, '{"audience":"team"}', $4) RETURNING id`, automationID, kind, params, next).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func runsOf(t *testing.T, pool *pgxpool.Pool, automationID int64) (inputs []string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT inputs::text FROM automation_runs WHERE automation_id = $1 ORDER BY id`, automationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, text)
	}
	return inputs
}

// Two workers, one schedule: both seed the tick as they start and both do a
// tick's work at the same moment, and the schedule still fires once. The
// fence allows one tick message a moment, and the trigger's row lock allows
// one fire of a due trigger whoever asks.
func TestAScheduleFiresOnceAcrossTwoWorkers(t *testing.T) {
	rdb := throwawayRedis(t)
	pool := testdatabase.Open(t)
	ctx := context.Background()
	owner := testdatabase.Account(t, pool)
	now := time.Date(2026, 11, 12, 9, 0, 30, 0, time.UTC)
	due := now.Add(-30 * time.Second)

	active := automation(t, pool, owner, digest)
	fired := trigger(t, pool, active, "schedule", `{"cron":"0 9 * * *","timezone":"UTC"}`, &due)
	// A paused automation's trigger is moved on without firing; a trigger
	// that has no next fire yet is given one and does not fire; one that is
	// not due is left alone.
	paused := automation(t, pool, owner, digest)
	testdatabase.Exec(t, pool, `UPDATE automations SET status = 'paused' WHERE id = $1`, paused)
	quiet := trigger(t, pool, paused, "schedule", `{"cron":"0 9 * * *","timezone":"UTC"}`, &due)
	unset := trigger(t, pool, active, "schedule", `{"cron":"0 12 * * *","timezone":"UTC"}`, nil)
	later := now.Add(time.Hour)
	notDue := trigger(t, pool, active, "schedule", `{"cron":"0 10 * * *","timezone":"UTC"}`, &later)
	broken := trigger(t, pool, active, "schedule", `{"cron":"nonsense","timezone":"UTC"}`, &due)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	published := &outbox{}
	worker := func() *Tick {
		tick := &Tick{
			Starter: Starter{Rows: Store{Pool: pool, Log: log}, Publisher: published, Log: log},
			Fence:   RedisFence{Redis: rdb},
			Timing:  Timing{Every: time.Minute, Batch: 50, RepublishAfter: 2 * time.Minute, OverdueGrace: 5 * time.Minute},
			Now:     func() time.Time { return now },
		}
		if err := tick.Check(); err != nil {
			t.Fatal(err)
		}
		return tick
	}
	one, two := worker(), worker()

	// Both start: one tick is on its way, for the next whole minute.
	var starting sync.WaitGroup
	for _, tick := range []*Tick{one, two} {
		starting.Add(1)
		go func() {
			defer starting.Done()
			if err := tick.Seed(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	starting.Wait()
	ticks := published.of(Task)
	if len(ticks) != 1 || ticks[0].Countdown != 30*time.Second || ticks[0].ID != Task+":"+strconv.FormatInt(now.Add(30*time.Second).Unix(), 10) {
		t.Fatalf("published %d ticks (%+v), want one for the next minute", len(ticks), ticks)
	}

	// Both do the tick's work at once.
	var working sync.WaitGroup
	for _, tick := range []*Tick{one, two} {
		working.Add(1)
		go func() {
			defer working.Done()
			if err := tick.Work(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	working.Wait()

	runs := runsOf(t, pool, active)
	if len(runs) != 1 {
		t.Fatalf("the schedule fired %d times across two workers, want once: %v", len(runs), runs)
	}
	var inputs map[string]any
	if err := json.Unmarshal([]byte(runs[0]), &inputs); err != nil || inputs["scheduled_for"] != "2026-11-12T09:00:00Z" ||
		inputs["fired_at"] != "2026-11-12T09:00:30Z" || inputs["last_fired_at"] != nil || inputs["audience"] != "team" {
		t.Errorf("the run was told %v", inputs)
	}
	if executes := published.of(runqueue.ExecuteTask); len(executes) != 1 {
		t.Errorf("published %d run messages, want one", len(executes))
	}
	if len(runsOf(t, pool, paused)) != 0 {
		t.Error("a paused automation's schedule fired")
	}
	next := func(id int64) (at *time.Time, enabled bool, last *time.Time) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT next_fire_at, enabled, last_fired_at FROM automation_triggers WHERE id = $1`, id).Scan(&at, &enabled, &last); err != nil {
			t.Fatal(err)
		}
		return at, enabled, last
	}
	tomorrow := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	if at, _, last := next(fired); at == nil || !at.Equal(tomorrow) || last == nil || !last.Equal(now) {
		t.Errorf("the fired trigger waits for %v (last %v), want tomorrow at nine", at, last)
	}
	if at, _, last := next(quiet); at == nil || !at.Equal(tomorrow) || last != nil {
		t.Errorf("the paused automation's trigger waits for %v (last %v)", at, last)
	}
	if at, _, _ := next(unset); at == nil || !at.Equal(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("the trigger with no next fire was given %v", at)
	}
	if at, _, _ := next(notDue); at == nil || !at.Equal(later) {
		t.Errorf("the trigger that was not due was moved to %v", at)
	}
	if at, enabled, _ := next(broken); enabled || at != nil {
		t.Errorf("the unreadable trigger is still on (next %v)", at)
	}

	// A second tick a moment later fires nothing more.
	if err := one.Work(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(runsOf(t, pool, active)); got != 1 {
		t.Errorf("%d runs after a second tick, want still one", got)
	}
}

// A tick also publishes a run whose message was lost, and ends a run whose
// worker died past its time limit.
func TestATickRepublishesALostRunAndEndsAnOverdueOne(t *testing.T) {
	pool := testdatabase.Open(t)
	ctx := context.Background()
	owner := testdatabase.Account(t, pool)
	id := automation(t, pool, owner, digest)
	var lost, fresh, overdue, healthy int64
	insert := func(status, created, started string) (run int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `
			INSERT INTO automation_runs (automation_id, status, definition_snapshot, created_at, started_at)
			VALUES ($1, $2::automation_run_status, '{"execution":{"timeout_seconds":60}}', now() - $3::interval, now() - $4::interval) RETURNING id`,
			id, status, created, started).Scan(&run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	lost, fresh = insert("pending", "10 minutes", "0"), insert("pending", "5 seconds", "0")
	overdue, healthy = insert("running", "1 hour", "30 minutes"), insert("running", "1 minute", "30 seconds")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	published := &outbox{}
	tick := &Tick{
		Starter: Starter{Rows: Store{Pool: pool, Log: log}, Publisher: published, Log: log},
		Timing:  Timing{Every: time.Minute, Batch: 50, RepublishAfter: 2 * time.Minute, OverdueGrace: 5 * time.Minute},
		Now:     time.Now,
	}
	if err := tick.Work(ctx); err != nil {
		t.Fatal(err)
	}
	republished := map[any]bool{}
	for _, message := range published.of(runqueue.ExecuteTask) {
		republished[message.Kwargs[runqueue.RunKey]] = true
	}
	if !republished[lost] || republished[fresh] {
		t.Errorf("republished %v, want the lost run %d and not the fresh one %d", republished, lost, fresh)
	}
	status := func(run int64) (got string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status::text FROM automation_runs WHERE id = $1`, run).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if status(overdue) != "timed_out" || status(healthy) != "running" {
		t.Errorf("the overdue run is %s and the healthy one %s", status(overdue), status(healthy))
	}
}

// An event fires the triggers of its own account that its payload matches,
// once each, and no other account's.
func TestAnEventFiresItsOwnAccountsMatchingTriggersOnce(t *testing.T) {
	rdb := throwawayRedis(t)
	pool := testdatabase.Open(t)
	owner, other := testdatabase.Account(t, pool), testdatabase.Account(t, pool)
	arrivals := `{"event_type":"document.entered_folder","filter":{"folder_id":7}}`
	mine := automation(t, pool, owner, digest)
	matching := trigger(t, pool, mine, "event", arrivals, nil)
	elsewhere := automation(t, pool, owner, digest)
	trigger(t, pool, elsewhere, "event", `{"event_type":"document.entered_folder","filter":{"folder_id":8}}`, nil)
	// The other account waits for the very same folder id. It is not its
	// folder, and the event is not its event.
	theirs := automation(t, pool, other, digest)
	trigger(t, pool, theirs, "event", arrivals, nil)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	published := &outbox{}
	reader := func(name string) *Events {
		events := &Events{
			Redis: rdb, Starter: Starter{Rows: Store{Pool: pool, Log: log}, Publisher: published, Log: log},
			Consumer: name, Block: 50 * time.Millisecond, ClaimIdle: time.Hour, Batch: 10, Now: time.Now,
		}
		if err := events.Check(); err != nil {
			t.Fatal(err)
		}
		return events
	}
	one, two := reader("worker-one"), reader("worker-two")
	ctx := context.Background()
	if err := one.ensureGroup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := two.ensureGroup(ctx); err != nil {
		t.Fatalf("a group that already exists: %v", err)
	}
	publish := func(event eventwire.Event) {
		t.Helper()
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: eventwire.Stream, Values: map[string]any{eventwire.Field: string(body)}}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	arrival := eventwire.Event{EventType: "document.entered_folder", UserID: owner, EventID: "e1", OccurredAt: "2026-11-12T09:00:00Z",
		Payload: json.RawMessage(`{"document_id":42,"folder_id":7,"previous_folder_id":null,"document_type":"FILE","title":"Tides"}`)}
	publish(arrival)
	// An entry that is not an event is skipped and does not hold the stream.
	if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: eventwire.Stream, Values: map[string]any{eventwire.Field: "not json"}}).Err(); err != nil {
		t.Fatal(err)
	}

	// Two readers read at once: the group hands each entry to one of them.
	var reading sync.WaitGroup
	for _, events := range []*Events{one, two} {
		reading.Add(1)
		go func() {
			defer reading.Done()
			if err := events.Once(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	reading.Wait()

	runs := runsOf(t, pool, mine)
	if len(runs) != 1 {
		t.Fatalf("the matching trigger fired %d times, want once: %v", len(runs), runs)
	}
	var inputs map[string]any
	if err := json.Unmarshal([]byte(runs[0]), &inputs); err != nil || inputs["event_id"] != "e1" || inputs["title"] != "Tides" ||
		inputs["folder_id"] != float64(7) || inputs["audience"] != "team" || inputs["event_type"] != "document.entered_folder" {
		t.Errorf("the run was told %v", inputs)
	}
	if got := len(runsOf(t, pool, elsewhere)); got != 0 {
		t.Errorf("a trigger for another folder fired %d times", got)
	}
	if got := len(runsOf(t, pool, theirs)); got != 0 {
		t.Errorf("another account's trigger fired %d times on this account's event", got)
	}
	if pending, err := rdb.XPending(ctx, eventwire.Stream, eventwire.Group).Result(); err != nil || pending.Count != 0 {
		t.Errorf("%d entries were left unacknowledged (%v)", pending.Count, err)
	}
	var last *time.Time
	if err := pool.QueryRow(ctx, `SELECT last_fired_at FROM automation_triggers WHERE id = $1`, matching).Scan(&last); err != nil || last == nil {
		t.Errorf("the trigger does not say it fired (%v)", err)
	}

	// The same event handed over again (its first reader died before
	// acknowledging it) starts nothing more.
	if err := one.Fire(ctx, arrival); err != nil {
		t.Fatal(err)
	}
	if got := len(runsOf(t, pool, mine)); got != 1 {
		t.Errorf("%d runs after the event was read twice, want one", got)
	}
	if executes := published.of(runqueue.ExecuteTask); len(executes) != 1 {
		t.Errorf("published %d run messages, want one", len(executes))
	}
}
