package pushdelivery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
	"git.danvics.com/danvics/soloclef/soloclef_notify/delivery"
	"git.danvics.com/danvics/soloclef/soloclef_notify/expo"
	"git.danvics.com/danvics/soloclef/soloclef_notify/expo/expotest"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_notify/presence"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/jobs"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testredis"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/videojob"
)

// stack is the worker as wired for push, against a throwaway Postgres and
// Valkey and a fake Expo: the real consumer takes the messages off a queue
// and the job table runs them.
type stack struct {
	pool     *pgxpool.Pool
	rdb      *redis.Client
	expo     *expotest.Server
	producer produce.Producer
	notifier *notify.Notifier
}

func startStack(t *testing.T, ctx context.Context) *stack {
	t.Helper()
	rdb := testredis.Open(t, testredis.PushDelivery)
	pool := testdatabase.Open(t)
	fake := expotest.New()
	t.Cleanup(fake.Close)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	producer, err := Producer(rdb, "probe")
	if err != nil {
		t.Fatal(err)
	}
	notifier, err := Notifier(pool, producer, log)
	if err != nil {
		t.Fatal(err)
	}
	// The real delivery jobs with Expo replaced by the fake, and a short
	// back-off so a retry is seen within the test.
	delivering := &delivery.Jobs{
		Database:  pool,
		Sending:   delivery.Sending{Expo: expo.Client{BaseURL: fake.URL, HTTP: &http.Client{Timeout: 5 * time.Second}}, Origin: "https://notes.example.com"},
		Presence:  Presence{Redis: rdb},
		Fence:     Fence{Redis: rdb},
		Publisher: producer,
		Timing:    delivery.Timing{Attempts: 3, Backoff: time.Second, ReceiptDelay: time.Hour},
		Log:       log,
	}
	if err := delivering.Check(); err != nil {
		t.Fatal(err)
	}
	table, err := jobs.New(log,
		jobs.Job{Task: notify.DeliverTask, Deadline: time.Minute, Run: delivering.Deliver},
		jobs.Job{Task: notify.ReceiptsTask, Deadline: time.Minute, Run: delivering.Receipts},
		// A Studio job that ends: the video render switched off, which
		// fails the queued video at once, as a deployment without a sandbox
		// does.
		jobs.Job{Task: videojob.Task, Deadline: time.Minute, Run: videojob.Refusal{Pool: pool, Notifier: notifier}.Run},
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg := broker.DefaultConfig()
	cfg.Name = "push-probe"
	cfg.Queues = routing.Table{Default: "probe"}.Queues()
	cfg.QueueConcurrency = map[string]int{"probe": 2, "probe.heavy": 1}
	consumer, err := broker.NewConsumer(rdb, cfg, table)
	if err != nil {
		t.Fatal(err)
	}
	running, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- consumer.Run(running) }()
	t.Cleanup(func() {
		stop()
		<-done
	})
	return &stack{pool: pool, rdb: rdb, expo: fake, producer: producer, notifier: notifier}
}

// phone registers a token for the account under a live sign-in.
func (s *stack) phone(t *testing.T, ctx context.Context, userID, token string) {
	t.Helper()
	family := fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1_000_000_000_000)
	testdatabase.Exec(t, s.pool, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family_id)
		VALUES ($1::uuid, md5(random()::text), now() + interval '30 days', $2::uuid)`, userID, family)
	testdatabase.Exec(t, s.pool, `
		INSERT INTO push_devices (user_id, token, kind, platform, session_family_id) VALUES ($1::uuid, $2, 'expo', 'android', $3::uuid)`, userID, token, family)
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DELETE FROM push_tickets WHERE token = $1`, token) })
}

func (s *stack) waitFor(t *testing.T, ctx context.Context, what string, done func() bool) {
	t.Helper()
	for !done() {
		if ctx.Err() != nil {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// A Studio job that finishes, run off the queue by the real consumer,
// writes the owner's inbox row and pushes it through the sender: the
// notifier publishes the delivery, the consumer runs it, and the fake Expo
// is called with the row's push.
func TestAFinishedStudioJobWritesTheInboxRowAndCallsTheSender(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := startStack(t, ctx)
	owner := testdatabase.Account(t, s.pool)
	token := "ExponentPushToken[e2e-" + owner[:8] + "]"
	s.phone(t, ctx, owner, token)
	var notebook, job int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO notebooks (user_id, title) VALUES ($1::uuid, 'Cardiology') RETURNING id`, owner).Scan(&notebook); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO deliverable_jobs (user_id, kind, title, tool_call_id, status) VALUES ($1::uuid, 'video', 'The heart', 'e2e', 'queued') RETURNING id`,
		owner).Scan(&job); err != nil {
		t.Fatal(err)
	}
	testdatabase.Exec(t, s.pool, `
		INSERT INTO notebook_outputs (notebook_id, user_id, kind, title, deliverable_job_id) VALUES ($1, $2::uuid, 'video', 'The heart', $3)`,
		notebook, owner, job)

	// The message the Studio's generate publishes for a video.
	if _, err := s.producer.Send(ctx, produce.Message{Task: videojob.Task, Kwargs: map[string]any{"job_id": job}}); err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, ctx, "the push", func() bool { return len(s.expo.Messages()) > 0 })

	var title, kind string
	var retry bool
	if err := s.pool.QueryRow(ctx, `
		SELECT title, type, (metadata ->> 'retry')::boolean FROM notifications WHERE user_id = $1::uuid`, owner).Scan(&title, &kind, &retry); err != nil {
		t.Fatal(err)
	}
	if title != "Video could not be made" || kind != "studio_output" || !retry {
		t.Errorf("the inbox row is %q (%s), retry %v", title, kind, retry)
	}
	sent := s.expo.Messages()
	if len(sent) != 1 || sent[0].To != token || sent[0].Title != "Video could not be made" || sent[0].Body != "Cardiology" ||
		sent[0].Data["path"] != fmt.Sprintf("/notebook/%d?pane=studio", notebook) || sent[0].Data["server"] != "https://notes.example.com" {
		t.Errorf("sent %+v", sent)
	}
	// The receipts check is scheduled once, in the broker's own delay.
	if held, _ := s.rdb.Get(ctx, "soloclef:notify:receipts-scheduled").Result(); held != "1" {
		t.Error("the receipts check was not fenced")
	}
}

// While the person has a Soloclef tab open (a live presence mark), the row
// is written and nothing is pushed; once the mark is gone the next row is
// pushed.
func TestNothingIsPushedWhileThePersonIsLooking(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := startStack(t, ctx)
	owner := testdatabase.Account(t, s.pool)
	token := "ExponentPushToken[look-" + owner[:8] + "]"
	s.phone(t, ctx, owner, token)
	now := time.Now()
	if err := s.rdb.ZAdd(ctx, presence.Key(owner), redis.Z{Score: presence.Expiry(now), Member: "tab-12345678"}).Err(); err != nil {
		t.Fatal(err)
	}
	podcast := func() int64 {
		var id int64
		if err := s.pool.QueryRow(ctx, `INSERT INTO podcast_runs (user_id, title, status) VALUES ($1::uuid, 'Ep', 'ready') RETURNING id`, owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if err := s.notifier.StudioFinished(ctx, notify.StudioRun{UserID: owner, PodcastID: podcast()}); err != nil {
		t.Fatal(err)
	}
	// The delivery ran (the queue is empty and nothing in flight) and sent
	// nothing.
	s.waitFor(t, ctx, "the delivery to run", func() bool {
		n, _ := s.rdb.LLen(ctx, "probe").Result()
		return n == 0
	})
	time.Sleep(300 * time.Millisecond)
	if sent := s.expo.Messages(); len(sent) != 0 {
		t.Fatalf("pushed while looking: %+v", sent)
	}

	// The tab is hidden: its mark is removed, and the next output pushes.
	if err := s.rdb.ZRem(ctx, presence.Key(owner), "tab-12345678").Err(); err != nil {
		t.Fatal(err)
	}
	if err := s.notifier.StudioFinished(ctx, notify.StudioRun{UserID: owner, PodcastID: podcast()}); err != nil {
		t.Fatal(err)
	}
	s.waitFor(t, ctx, "the push", func() bool { return len(s.expo.Messages()) > 0 })
	if sent := s.expo.Messages(); len(sent) != 1 || sent[0].Title != "Audio overview ready" || *sent[0].Badge != 2 {
		t.Errorf("sent %+v", sent)
	}
	var rows int64
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id = $1::uuid`, owner).Scan(&rows)
	if rows != 2 {
		t.Errorf("%d inbox rows, want both", rows)
	}
}

// A push the service did not take is retried from the queue, through the
// broker's own countdown, and arrives.
func TestAPushTheServiceRefusedArrivesOnItsRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := startStack(t, ctx)
	owner := testdatabase.Account(t, s.pool)
	s.phone(t, ctx, owner, "ExponentPushToken[retry-"+owner[:8]+"]")
	s.expo.FailNext(http.StatusBadGateway)
	var id int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO podcast_runs (user_id, title, status) VALUES ($1::uuid, 'Ep', 'ready') RETURNING id`, owner).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.notifier.StudioFinished(ctx, notify.StudioRun{UserID: owner, PodcastID: id}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	s.waitFor(t, ctx, "the retried push", func() bool { return len(s.expo.Messages()) > 0 })
	if waited := time.Since(started); waited < 900*time.Millisecond {
		t.Errorf("the retry came after %v, before its back-off", waited)
	}
}

func TestTheSettingsSayExactlyWhatIsMeant(t *testing.T) {
	env := func(values map[string]string) func(string) string { return func(k string) string { return values[k] } }
	if _, err := LoadConfig(env(nil)); err == nil {
		t.Error("an unset switch was accepted")
	}
	if c, err := LoadConfig(env(map[string]string{Enabled: "false"})); err != nil || c.Enabled {
		t.Errorf("off: %+v %v", c, err)
	}
	if _, err := LoadConfig(env(map[string]string{Enabled: "true"})); err == nil {
		t.Error("push on with no public address was accepted")
	}
	c, err := LoadConfig(env(map[string]string{Enabled: "true", PublicURL: "https://Notes.Example.com/"}))
	if err != nil || c.Origin != "https://notes.example.com" || c.AccessToken != "" {
		t.Errorf("on, no token: %+v %v", c, err)
	}
	c, err = LoadConfig(env(map[string]string{Enabled: "true", PublicURL: "https://n.example", ExpoAccessToken: " tok "}))
	if err != nil || c.AccessToken != "tok" {
		t.Errorf("on, a token: %+v %v", c, err)
	}
	if _, err := LoadConfig(env(map[string]string{Enabled: "yes"})); err == nil {
		t.Error("a switch that is not true or false was accepted")
	}
}
