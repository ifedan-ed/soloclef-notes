package delivery_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/apns/apnstest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/delivery"
	"git.danvics.com/danvics/soloclef/soloclef_notify/expo"
	"git.danvics.com/danvics/soloclef/soloclef_notify/expo/expotest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/internal/notifytest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
)

// looking is a Presence of the accounts named in it.
type looking struct {
	mu    sync.Mutex
	users map[string]bool
	err   error
}

func (l *looking) Active(_ context.Context, userIDs []string, _ time.Time) (map[string]bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	out := map[string]bool{}
	for _, id := range userIDs {
		out[id] = l.users[id]
	}
	return out, nil
}

// fence is an in-memory Fence.
type fence struct {
	mu   sync.Mutex
	held map[string]bool
}

func (f *fence) Take(_ context.Context, key string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held[key] {
		return false, nil
	}
	f.held[key] = true
	return true, nil
}

type harness struct {
	pool      *pgxpool.Pool
	expo      *expotest.Server
	apple     *apnstest.Server
	presence  *looking
	published *notifytest.Publisher
	jobs      *delivery.Jobs
	notifier  *notify.Notifier
	now       time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := notifytest.Open(t)
	fake := expotest.New()
	t.Cleanup(fake.Close)
	apple := apnstest.New(t)
	h := &harness{pool: pool, expo: fake, apple: apple, presence: &looking{users: map[string]bool{}}, published: &notifytest.Publisher{},
		now: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.jobs = &delivery.Jobs{
		Database: pool,
		Sending: delivery.Sending{
			Expo: expo.Client{BaseURL: fake.URL, HTTP: &http.Client{Timeout: 5 * time.Second}},
			APNs: apple.Client(t, log), APNsEnvironment: apns.Production,
			Origin: "https://notes.example.com",
		},
		Presence:  h.presence,
		Fence:     &fence{held: map[string]bool{}},
		Publisher: h.published,
		Timing:    delivery.Timing{Attempts: 3, Backoff: 30 * time.Second, ReceiptDelay: 15 * time.Minute},
		Log:       log,
		Now:       func() time.Time { return h.now },
	}
	if err := h.jobs.Check(); err != nil {
		t.Fatal(err)
	}
	h.notifier = &notify.Notifier{Database: pool, Publisher: h.published, Log: log, Now: func() time.Time { return h.now }}
	return h
}

// phone registers an Expo token for the account under a sign-in that is
// live, or one that was signed out.
func (h *harness) phone(t *testing.T, userID, token string, live bool) {
	t.Helper()
	h.device(t, userID, token, "expo", "", live)
}

// iphone registers an APNs token of the environment for the account under
// a live sign-in, and answers the device's id.
func (h *harness) iphone(t *testing.T, userID, token, environment string) int64 {
	t.Helper()
	h.device(t, userID, token, "apns", environment, true)
	return notifytest.Int64(t, h.pool, `SELECT id FROM push_devices WHERE token = $1`, token)
}

func (h *harness) device(t *testing.T, userID, token, kind, environment string, live bool) {
	t.Helper()
	revoked := "NULL"
	if !live {
		revoked = "now()"
	}
	family := fmt.Sprintf("%08x-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffff, time.Now().UnixNano()&0xffffffffffff)
	notifytest.Exec(t, h.pool, fmt.Sprintf(`
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at, family_id)
		VALUES ($1::uuid, md5(random()::text), now() + interval '30 days', %s, $2::uuid)`, revoked), userID, family)
	notifytest.Exec(t, h.pool, `
		INSERT INTO push_devices (user_id, token, kind, apns_environment, platform, session_family_id)
		VALUES ($1::uuid, $2, $3, nullif($4, ''), 'ios', $5::uuid)`, userID, token, kind, environment, family)
}

// studioRow writes a ready Studio output's row through the notifier, as
// the API's runner does, and answers its id.
func (h *harness) studioRow(t *testing.T, userID string) int64 {
	t.Helper()
	job := notifytest.Int64(t, h.pool, `
		INSERT INTO deliverable_jobs (user_id, kind, title, tool_call_id, status)
		VALUES ($1::uuid, 'video', 'Lecture', md5(random()::text), 'ready') RETURNING id`, userID)
	if err := h.notifier.StudioFinished(context.Background(), notify.StudioRun{UserID: userID, JobID: job}); err != nil {
		t.Fatal(err)
	}
	return notifytest.Int64(t, h.pool, `SELECT max(id) FROM notifications WHERE user_id = $1::uuid`, userID)
}

func deliverMessage(ids []int64, attempt int) *celery.Message {
	list := make([]any, len(ids))
	for i, id := range ids {
		list[i] = float64(id)
	}
	return &celery.Message{Task: notify.DeliverTask, Args: []any{list}, Retries: attempt}
}

// A row's push goes to every live phone of its owner and to no signed-out
// one, says only the generic title, the notebook at most and the path, and
// carries the unread count as the badge. Its ticket is kept and one
// receipts check is scheduled.
func TestARowIsPushedToTheOwnersLivePhones(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	h.phone(t, user, "ExponentPushToken[live-1]", true)
	h.phone(t, user, "ExponentPushToken[live-2]", true)
	h.phone(t, user, "ExponentPushToken[signed-out]", false)
	id := h.studioRow(t, user)

	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
		t.Fatal(err)
	}
	sent := h.expo.Messages()
	if len(sent) != 2 {
		t.Fatalf("sent %d messages: %+v", len(sent), sent)
	}
	for _, message := range sent {
		if message.To == "ExponentPushToken[signed-out]" {
			t.Error("a signed-out phone was pushed")
		}
		if message.Title != "Video ready" || message.Body != "" || message.ChannelID != "soloclef" || message.Badge == nil || *message.Badge != 1 {
			t.Errorf("message = %+v", message)
		}
		if message.Data["path"] != "/inbox" || message.Data["kind"] != "output" || message.Data["v"] != float64(1) || message.Data["server"] != "https://notes.example.com" {
			t.Errorf("data = %v", message.Data)
		}
	}
	if tickets := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_tickets WHERE token LIKE 'ExponentPushToken[live-%'`); tickets != 2 {
		t.Errorf("%d tickets kept", tickets)
	}
	receipts := 0
	for _, m := range h.published.Messages() {
		if m.Task == notify.ReceiptsTask {
			receipts++
			if m.Countdown != 15*time.Minute {
				t.Errorf("the receipts check waits %v", m.Countdown)
			}
		}
	}
	// A second delivery in the same span does not schedule another check.
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{h.studioRow(t, user)}, 0)); err != nil {
		t.Fatal(err)
	}
	for _, m := range h.published.Messages() {
		if m.Task == notify.ReceiptsTask {
			receipts--
		}
	}
	if receipts != 0 {
		t.Errorf("the receipts check was scheduled more than once")
	}
	notifytest.Exec(t, h.pool, `DELETE FROM push_tickets WHERE token LIKE 'ExponentPushToken[live-%'`)
}

// Each reason a push is held back holds it: a kind switched off, quiet
// hours, the person looking at Soloclef, and a row already read. The row
// stays in the inbox in every case.
func TestEachReasonToStaySilentIsObeyed(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, h *harness, user string, id int64)
	}{
		{"switched off", func(t *testing.T, h *harness, user string, _ int64) {
			notifytest.Exec(t, h.pool, `INSERT INTO push_preferences (user_id, choices) VALUES ($1::uuid, '{"output": false}')`, user)
		}},
		{"quiet hours", func(t *testing.T, h *harness, user string, _ int64) {
			// 12:00 UTC is 13:00 in Lagos, inside 12:30-14:00.
			notifytest.Exec(t, h.pool, `
				INSERT INTO push_preferences (user_id, choices, quiet_start_minute, quiet_end_minute, time_zone)
				VALUES ($1::uuid, '{}', 750, 840, 'Africa/Lagos')`, user)
		}},
		{"looking at Soloclef", func(_ *testing.T, h *harness, user string, _ int64) { h.presence.users[user] = true }},
		{"already read", func(t *testing.T, h *harness, _ string, id int64) {
			notifytest.Exec(t, h.pool, `UPDATE notifications SET read = true WHERE id = $1`, id)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			user := notifytest.Account(t, h.pool)
			h.phone(t, user, "ExponentPushToken[p-"+user[:8]+"]", true)
			id := h.studioRow(t, user)
			c.setup(t, h, user, id)
			if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
				t.Fatal(err)
			}
			if sent := h.expo.Messages(); len(sent) != 0 {
				t.Errorf("pushed: %+v", sent)
			}
		})
	}
}

// Outside quiet hours, with another kind switched off, the push goes.
func TestQuietHoursOutsideTheirSpanLetThePushThrough(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	h.phone(t, user, "ExponentPushToken[q-"+user[:8]+"]", true)
	notifytest.Exec(t, h.pool, `
		INSERT INTO push_preferences (user_id, choices, quiet_start_minute, quiet_end_minute, time_zone)
		VALUES ($1::uuid, '{"source": false}', 1320, 420, 'Africa/Lagos')`, user)
	id := h.studioRow(t, user)
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
		t.Fatal(err)
	}
	if sent := h.expo.Messages(); len(sent) != 1 {
		t.Errorf("sent %d", len(sent))
	}
	notifytest.Exec(t, h.pool, `DELETE FROM push_tickets WHERE token = $1`, "ExponentPushToken[q-"+user[:8]+"]")
}

// A ticket that says DeviceNotRegistered deletes that phone at once.
func TestAPhoneThatIsGoneIsForgotten(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	gone := "ExponentPushToken[gone-" + user[:8] + "]"
	h.phone(t, user, gone, true)
	h.expo.Gone(gone)
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{h.studioRow(t, user)}, 0)); err != nil {
		t.Fatal(err)
	}
	if left := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token = $1`, gone); left != 0 {
		t.Error("the gone phone is still registered")
	}
}

// A request the service did not take is retried from the queue with a
// doubling wait, and given up on after the last attempt.
func TestATransientFailureIsRetriedFromTheQueue(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	h.phone(t, user, "ExponentPushToken[r-"+user[:8]+"]", true)
	id := h.studioRow(t, user)
	before := len(h.published.Messages())

	h.expo.FailNext(http.StatusServiceUnavailable)
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 1)); err != nil {
		t.Fatal(err)
	}
	retries := h.published.Messages()[before:]
	if len(retries) != 1 || retries[0].Task != notify.DeliverTask || retries[0].Retries != 2 || retries[0].Countdown != time.Minute {
		t.Fatalf("retry = %+v", retries)
	}
	h.expo.FailNext(http.StatusServiceUnavailable)
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 2)); err != nil {
		t.Fatal(err)
	}
	if len(h.published.Messages()) != before+1 {
		t.Error("a push was retried after its last attempt")
	}
	// Presence that cannot be read is retried too, not guessed.
	h.presence.err = errors.New("valkey down")
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{id}, 0)); err != nil {
		t.Fatal(err)
	}
	if got := h.published.Messages(); len(got) != before+2 || got[len(got)-1].Retries != 1 || len(h.expo.Messages()) != 0 {
		t.Errorf("presence failure: %+v", got[before:])
	}
}

// Receipts that say a phone is gone delete it; answered tickets are
// forgotten and unanswered ones kept for the next check.
func TestReceiptsForgetThePhonesThatWentAway(t *testing.T) {
	h := newHarness(t)
	user := notifytest.Account(t, h.pool)
	stays, leaves := "ExponentPushToken[stays-"+user[:8]+"]", "ExponentPushToken[leaves-"+user[:8]+"]"
	h.phone(t, user, stays, true)
	h.phone(t, user, leaves, true)
	h.expo.GoneLater(leaves)
	if err := h.jobs.Deliver(context.Background(), deliverMessage([]int64{h.studioRow(t, user)}, 0)); err != nil {
		t.Fatal(err)
	}
	notifytest.Exec(t, h.pool, `INSERT INTO push_tickets (ticket_id, token, created_at) VALUES ('ticket-unknown-'||$1, $2, now())`, user, stays)
	// Twenty minutes later the two real tickets are due.
	h.now = h.now.Add(20 * time.Minute)
	notifytest.Exec(t, h.pool, `UPDATE push_tickets SET created_at = $1 WHERE token IN ($2, $3) AND ticket_id NOT LIKE 'ticket-unknown-%'`,
		h.now.Add(-20*time.Minute), stays, leaves)
	if err := h.jobs.Receipts(context.Background(), &celery.Message{Task: notify.ReceiptsTask}); err != nil {
		t.Fatal(err)
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token = $1`, leaves); n != 0 {
		t.Error("the gone phone is still registered")
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_devices WHERE token = $1`, stays); n != 1 {
		t.Error("a working phone was forgotten")
	}
	if n := notifytest.Int64(t, h.pool, `SELECT count(*) FROM push_tickets WHERE token IN ($1, $2) AND ticket_id NOT LIKE 'ticket-unknown-%'`, stays, leaves); n != 0 {
		t.Errorf("%d answered tickets kept", n)
	}
	notifytest.Exec(t, h.pool, `DELETE FROM push_tickets WHERE token = $1`, stays)
}
