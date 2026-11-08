package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupestore"
	"git.danvics.com/danvics/soloclef/soloclef_mail/enqueue"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuestore"
	"git.danvics.com/danvics/soloclef/soloclef_mail/service"
	"github.com/redis/go-redis/v9"
)

// Can either sender drain what either producer queued?
//
// That is the whole point of porting the producer and the sender separately:
// during a cutover one Python mail_sender or one Go mail_sender is running, and
// both tiers may be queueing. So this queues with each and drains with each,
// against a throwaway Redis and a throwaway Mailpit.
//
// Nothing is defaulted, and the Redis it is given is emptied. Never point it at
// soloclef-redis-1. This package and mailer/smtp share one Mailpit and each
// clears it, so they are run with -p 1; the commands are in
// soloclef_mail/AGENTS.md.

type stackEnv struct {
	redisURL        string
	redisFromImage  string
	mailpitAPI      string
	smtpHost        string
	smtpPort        int
	smtpFromImage   string
	image           string
	network         string
	pythonSenderRun string
}

func stackSettings(t *testing.T) stackEnv {
	t.Helper()
	port, _ := strconv.Atoi(os.Getenv("SOLOCLEF_MAIL_TEST_SMTP_PORT"))
	env := stackEnv{
		redisURL:       os.Getenv("SOLOCLEF_MAIL_TEST_REDIS"),
		redisFromImage: os.Getenv("SOLOCLEF_MAIL_TEST_REDIS_FROM_CONTAINER"),
		mailpitAPI:     os.Getenv("SOLOCLEF_MAIL_TEST_MAILPIT_API"),
		smtpHost:       os.Getenv("SOLOCLEF_MAIL_TEST_SMTP_HOST"),
		smtpPort:       port,
		smtpFromImage:  os.Getenv("SOLOCLEF_MAIL_TEST_SMTP_FROM_CONTAINER"),
		image:          os.Getenv("SOLOCLEF_MAIL_TEST_IMAGE"),
		network:        os.Getenv("SOLOCLEF_MAIL_TEST_NETWORK"),
	}
	if env.redisURL == "" || env.mailpitAPI == "" || env.smtpHost == "" || port == 0 {
		t.Skip("set SOLOCLEF_MAIL_TEST_REDIS, _MAILPIT_API, _SMTP_HOST and _SMTP_PORT to run the queue end to end")
	}
	if strings.Contains(env.redisURL, "soloclef-redis") {
		t.Fatal("that is the live Redis; this test empties the one it is given")
	}
	return env
}

const defaultSender = "notes@queue.test"

func newStore(t *testing.T, env stackEnv) (queuestore.Store, *redis.Client) {
	t.Helper()
	options, err := redis.ParseURL(env.redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { client.Close() })
	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	return queuestore.Store{Redis: client}, client
}

func goSettings(t *testing.T, env stackEnv, store queuestore.Store, senders int) service.Settings {
	t.Helper()
	return service.Settings{
		Senders:   senders,
		Transport: "smtp",
		BuildMailer: func() (mailer.Mailer, error) {
			m, _, err := smtp.New(smtp.Settings{
				Host: env.smtpHost, Port: env.smtpPort, Security: smtp.Insecure,
				From: defaultSender, Timeout: 10 * time.Second, HelloName: "queue.test",
			})
			return m, err
		},
		Store: store,
		Guard: dedupe.HandoverGuard{Keys: dedupestore.Keys{Redis: store.Redis}},
	}
}

func TestAGoSenderDeliversABurstQueuedByTheGoProducer(t *testing.T) {
	env := stackSettings(t)
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	const burst = 60
	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	queueingStarted := time.Now()
	for n := range burst {
		if _, err := producer.Queue(ctx, enqueue.Request{
			Message: mailjob.Message{
				To:       fmt.Sprintf("person%d@queue.test", n),
				Subject:  fmt.Sprintf("burst %04d", n),
				TextBody: fmt.Sprintf("body %d\n", n),
			},
			Kind: "burst",
			Ref:  strconv.Itoa(n),
		}); err != nil {
			t.Fatal(err)
		}
	}
	queueing := time.Since(queueingStarted)
	if depth, err := client.LLen(ctx, queuekeys.Ready).Result(); err != nil || depth != burst {
		t.Fatalf("%d on %s, %v", depth, queuekeys.Ready, err)
	}

	subjects := drainWithGo(t, env, store, 4, burst)

	want := make([]string, 0, burst)
	for n := range burst {
		want = append(want, fmt.Sprintf("burst %04d", n))
	}
	sort.Strings(want)
	if strings.Join(subjects, "|") != strings.Join(want, "|") {
		t.Errorf("delivered %d of %d", len(subjects), burst)
	}
	if left, _ := client.LLen(ctx, queuekeys.Ready).Result(); left != 0 {
		t.Errorf("%d left ready", left)
	}
	for i := range 4 {
		if held, _ := client.LLen(ctx, queuekeys.Sending(i)).Result(); held != 0 {
			t.Errorf("sender %d still holds %d", i, held)
		}
	}
	t.Logf("queued %d in %v", burst, queueing)
}

func TestAGoSenderDeliversWhatPythonsEnqueueQueued(t *testing.T) {
	env := stackSettings(t)
	if env.image == "" || env.network == "" || env.redisFromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _NETWORK and _REDIS_FROM_CONTAINER for the Python half")
	}
	clearSink(t, env)
	store, _ := newStore(t, env)

	pythonEnqueue(t, env, "Résumé — queued by python")

	subjects := drainWithGo(t, env, store, 2, 1)
	if len(subjects) != 1 || subjects[0] != "Résumé — queued by python" {
		t.Errorf("got %q", subjects)
	}
}

func TestThePythonSenderDeliversWhatTheGoProducerQueued(t *testing.T) {
	env := stackSettings(t)
	if env.image == "" || env.network == "" || env.redisFromImage == "" || env.smtpFromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _NETWORK, _REDIS_FROM_CONTAINER and _SMTP_FROM_CONTAINER for the Python half")
	}
	clearSink(t, env)
	store, _ := newStore(t, env)
	ctx := context.Background()

	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	if _, err := producer.Queue(ctx, enqueue.Request{
		Message: mailjob.Message{
			To:       "person@queue.test",
			Subject:  "Grüße — queued by go",
			TextBody: "Grüße\n",
			HTMLBody: "<p>Grüße &amp; —</p>",
			Sender:   "Soloclef Nötes <notes@queue.test>",
		},
		Kind: "invite",
		Ref:  "7",
	}); err != nil {
		t.Fatal(err)
	}

	pythonDrain(t, env, 2, 8)

	subjects := sinkSubjects(t, env)
	if len(subjects) != 1 || subjects[0] != "Grüße — queued by go" {
		t.Errorf("the Python sender delivered %q", subjects)
	}
}

func TestMailHeldByAStoppedRunGoesOutOnRestart(t *testing.T) {
	env := stackSettings(t)
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	held, err := mailjob.Job{
		Message:    mailjob.Message{To: "person@queue.test", Subject: "held", TextBody: "x\n"},
		ID:         "aaaabbbbccccddddeeeeffff00001111",
		Kind:       "held",
		EnqueuedAt: 1762608000,
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.LPush(ctx, queuekeys.Sending(2), held).Err(); err != nil {
		t.Fatal(err)
	}

	subjects := drainWithGo(t, env, store, 4, 1)
	if len(subjects) != 1 || subjects[0] != "held" {
		t.Errorf("got %q", subjects)
	}
	if left, _ := client.LLen(ctx, queuekeys.Sending(2)).Result(); left != 0 {
		t.Errorf("%d still held", left)
	}
}

func TestAnExpiredCodeEmailIsNeverSent(t *testing.T) {
	env := stackSettings(t)
	clearSink(t, env)
	store, _ := newStore(t, env)
	ctx := context.Background()

	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	for _, c := range []struct {
		subject  string
		notAfter time.Time
	}{
		{"expired", time.Now().Add(-time.Second)},
		{"still good", time.Now().Add(10 * time.Minute)},
	} {
		if _, err := producer.Queue(ctx, enqueue.Request{
			Message:  mailjob.Message{To: "person@queue.test", Subject: c.subject, TextBody: "x\n"},
			Kind:     "code",
			NotAfter: c.notAfter,
		}); err != nil {
			t.Fatal(err)
		}
	}

	subjects := drainWithGo(t, env, store, 2, 1)
	time.Sleep(time.Second) // time for the expired one, had it been sent
	subjects = sinkSubjects(t, env)
	if len(subjects) != 1 || subjects[0] != "still good" {
		t.Errorf("delivered %q", subjects)
	}
}

func TestATemporaryFailureComesBackAndIsDelivered(t *testing.T) {
	env := stackSettings(t)
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	if _, err := producer.Queue(ctx, enqueue.Request{
		Message: mailjob.Message{To: "person@queue.test", Subject: "retried", TextBody: "x\n"},
		Kind:    "retry",
	}); err != nil {
		t.Fatal(err)
	}

	settings := goSettings(t, env, store, 1)
	build := settings.BuildMailer
	refusals := 1
	settings.BuildMailer = func() (mailer.Mailer, error) {
		inner, err := build()
		if err != nil {
			return nil, err
		}
		return &flakyOnce{inner: inner, left: &refusals}, nil
	}

	// The first delay is five seconds, so the retry clock has to put it back
	// and a sender pick it up again inside the window below.
	ctx, cancel := context.WithCancel(ctx)
	go service.Run(ctx, settings)
	subjects := awaitSink(t, env, 1, 25*time.Second)
	cancel()

	if len(subjects) != 1 || subjects[0] != "retried" {
		t.Fatalf("got %q", subjects)
	}
	if refusals != 0 {
		t.Error("the mailer was never made to fail")
	}
	if left, _ := client.ZCard(ctx, queuekeys.Retry).Result(); left != 0 {
		t.Errorf("%d left on the retry set", left)
	}
}

type flakyOnce struct {
	inner mailer.Mailer
	left  *int
}

func (f *flakyOnce) Send(ctx context.Context, message mailer.Message) error {
	if *f.left > 0 {
		*f.left--
		return fmt.Errorf("%w: 451 try later", mailer.ErrUnavailable)
	}
	return f.inner.Send(ctx, message)
}

func (f *flakyOnce) Close() error { return f.inner.Close() }

// pushBroker is the Broker a producer in this test uses: the push goes onto
// the real queue and the dedupe keys into the real Redis, so what the Go
// producer refuses is what the Python producer would have refused.
type pushBroker struct {
	store queuestore.Store
	dedupestore.Keys
}

func (b pushBroker) LPush(ctx context.Context, key, value string) error {
	if key != queuekeys.Ready {
		return fmt.Errorf("the producer pushed onto %q", key)
	}
	return b.store.Push(ctx, value)
}

func drainWithGo(t *testing.T, env stackEnv, store queuestore.Store, senders, expect int) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, goSettings(t, env, store, senders)) }()
	subjects := awaitSink(t, env, expect, 60*time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return subjects
}

func pythonEnqueue(t *testing.T, env stackEnv, subject string) {
	t.Helper()
	pythonEnqueueKeyed(t, env, subject, "ffffffffffffffffffffffffffffffff", "")
}

// pythonEnqueueKeyed queues through the app's own enqueue_email, with the id
// pinned and the caller's identity given: the half of the dedupe that lives on
// the producer side.
func pythonEnqueueKeyed(t *testing.T, env stackEnv, subject, id, idempotencyKey string) {
	t.Helper()
	message := map[string]any{
		"to":          "person@queue.test",
		"subject":     subject,
		"text_body":   "queued by python\n",
		"attachments": []any{},
	}
	if idempotencyKey != "" {
		message["idempotency_key"] = idempotencyKey
	}
	runInImage(t, env, "python_enqueue_push.py", map[string]any{
		"redis_url":   env.redisFromImage,
		"id":          id,
		"enqueued_at": 1762608000.123456,
		"kind":        "invite",
		"ref":         "7",
		"not_after":   nil,
		"message":     message,
	})
}

func pythonDrain(t *testing.T, env stackEnv, senders, seconds int) {
	t.Helper()
	host, port, _ := strings.Cut(env.smtpFromImage, ":")
	portNumber, _ := strconv.Atoi(port)
	runInImage(t, env, "python_mail_sender_run.py", map[string]any{
		"redis_url":      env.redisFromImage,
		"smtp_host":      host,
		"smtp_port":      portNumber,
		"default_sender": defaultSender,
		"senders":        senders,
		"seconds":        seconds,
	})
}

func runInImage(t *testing.T, env stackEnv, script string, payload map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "run", "--rm", "-i",
		"--network", env.network, "-e", "PYTHONPATH=/app", "-w", "/app",
		"-v", here+"/../scripts/"+script+":/tmp/run.py:ro",
		env.image, "python", "/tmp/run.py")
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not run: %v\n%s", script, err, out)
	}
}

func clearSink(t *testing.T, env stackEnv) {
	t.Helper()
	request, err := http.NewRequest(http.MethodDelete, env.mailpitAPI+"/api/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
}

func sinkSubjects(t *testing.T, env stackEnv) []string {
	t.Helper()
	response, err := http.Get(env.mailpitAPI + "/api/v1/messages?limit=1000")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var answer struct {
		Messages []struct {
			Subject string `json:"Subject"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	subjects := make([]string, 0, len(answer.Messages))
	for _, m := range answer.Messages {
		subjects = append(subjects, m.Subject)
	}
	sort.Strings(subjects)
	return subjects
}

func awaitSink(t *testing.T, env stackEnv, expect int, within time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		subjects := sinkSubjects(t, env)
		if len(subjects) >= expect || time.Now().After(deadline) {
			return subjects
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The dedupe across the two languages.
//
// During a cutover one sender is running and both tiers may be queueing, so a
// key one side writes has to be the key the other side reads -- otherwise the
// dedupe quietly stops working exactly when the two are mixed, which is the
// one moment it matters most.

func TestAMailPythonQueuedTwiceIsSentOnceByTheGoSender(t *testing.T) {
	env := stackSettings(t)
	if env.image == "" || env.network == "" || env.redisFromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _NETWORK and _REDIS_FROM_CONTAINER for the Python half")
	}
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	const key = "invite:7:1762608000.0"
	pythonEnqueueKeyed(t, env, "python once", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", key)
	pythonEnqueueKeyed(t, env, "python once", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", key)

	if depth, err := client.LLen(ctx, queuekeys.Ready).Result(); err != nil || depth != 1 {
		t.Fatalf("Python queued %d copies of one mail, %v", depth, err)
	}
	if subjects := drainWithGo(t, env, store, 2, 1); len(subjects) != 1 {
		t.Errorf("delivered %q", subjects)
	}
}

func TestTheGoProducerRefusesAMailPythonAlreadyQueued(t *testing.T) {
	env := stackSettings(t)
	if env.image == "" || env.network == "" || env.redisFromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _NETWORK and _REDIS_FROM_CONTAINER for the Python half")
	}
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	const key = "automation_result:42:succeeded"
	pythonEnqueueKeyed(t, env, "queued by python", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", key)

	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	message := mailjob.Message{To: "person@queue.test", Subject: "queued by go", TextBody: "x\n"}
	message.IdempotencyKey = key
	if _, err := producer.Queue(ctx, enqueue.Request{Message: message, Kind: "automation_result"}); err != nil {
		t.Fatal(err)
	}

	if depth, err := client.LLen(ctx, queuekeys.Ready).Result(); err != nil || depth != 1 {
		t.Fatalf("%d on the queue, %v: the two producers do not share one key", depth, err)
	}
	subjects := drainWithGo(t, env, store, 2, 1)
	if len(subjects) != 1 || subjects[0] != "queued by python" {
		t.Errorf("delivered %q; the Go push should have been refused", subjects)
	}
}

func TestThePythonProducerRefusesAMailTheGoProducerAlreadyQueued(t *testing.T) {
	env := stackSettings(t)
	if env.image == "" || env.network == "" || env.redisFromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _NETWORK and _REDIS_FROM_CONTAINER for the Python half")
	}
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	const key = "password_reset:9f86d081884c7d659a2feaa0c55ad015"
	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	message := mailjob.Message{To: "person@queue.test", Subject: "queued by go", TextBody: "x\n"}
	message.IdempotencyKey = key
	if _, err := producer.Queue(ctx, enqueue.Request{Message: message, Kind: "password_reset"}); err != nil {
		t.Fatal(err)
	}

	pythonEnqueueKeyed(t, env, "queued by python", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", key)

	if depth, err := client.LLen(ctx, queuekeys.Ready).Result(); err != nil || depth != 1 {
		t.Fatalf("%d on the queue, %v: the two producers do not share one key", depth, err)
	}
	subjects := drainWithGo(t, env, store, 2, 1)
	if len(subjects) != 1 || subjects[0] != "queued by go" {
		t.Errorf("delivered %q; the Python push should have been refused", subjects)
	}
}

func TestTheGoSenderDoesNotSendAgainWhatThePythonSenderMarkedSent(t *testing.T) {
	env := stackSettings(t)
	if env.image == "" || env.network == "" || env.redisFromImage == "" || env.smtpFromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_IMAGE, _NETWORK, _REDIS_FROM_CONTAINER and _SMTP_FROM_CONTAINER for the Python half")
	}
	clearSink(t, env)
	store, client := newStore(t, env)
	ctx := context.Background()

	producer := enqueue.Producer{Broker: pushBroker{store, dedupestore.Keys{Redis: store.Redis}}}
	id, err := producer.Queue(ctx, enqueue.Request{
		Message: mailjob.Message{To: "person@queue.test", Subject: "sent by python", TextBody: "x\n"},
		Kind:    "invite",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.LIndex(ctx, queuekeys.Ready, 0).Result()
	if err != nil {
		t.Fatal(err)
	}

	// The Python sender delivers it and records the handover.
	pythonDrain(t, env, 1, 6)
	if subjects := sinkSubjects(t, env); len(subjects) != 1 {
		t.Fatalf("the Python sender delivered %q", subjects)
	}
	if mark, err := client.Get(ctx, dedupe.HandoverKey(id, 0)).Result(); err != nil || mark != dedupe.Sent {
		t.Fatalf("Python recorded %q %v, not %q", mark, err, dedupe.Sent)
	}

	// Now the same entry is held by a run that stopped before it could drop
	// it -- the failure the queue's own notes admit. A Go sender recovers it.
	if err := client.LPush(ctx, queuekeys.Sending(3), raw).Err(); err != nil {
		t.Fatal(err)
	}
	goCtx, cancel := context.WithCancel(ctx)
	settings := goSettings(t, env, store, 1)
	done := make(chan error, 1)
	go func() { done <- service.Run(goCtx, settings) }()
	time.Sleep(4 * time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if subjects := sinkSubjects(t, env); len(subjects) != 1 {
		t.Errorf("the Go sender sent it again: %q", subjects)
	}
	if held, _ := client.LLen(ctx, queuekeys.Sending(3)).Result(); held != 0 {
		t.Errorf("%d still held", held)
	}
}
