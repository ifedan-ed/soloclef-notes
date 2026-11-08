package enqueue_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe/dedupetest"
	"git.danvics.com/danvics/soloclef/soloclef_mail/enqueue"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
)

// testBroker is a Broker over an in-memory keyspace: the push goes where the
// test says, the dedupe keys stay here.
type testBroker struct {
	*dedupetest.Memory
	push func(ctx context.Context, key, value string) error
}

func newTestBroker(push func(ctx context.Context, key, value string) error) testBroker {
	return testBroker{Memory: dedupetest.NewMemory(), push: push}
}

func (b testBroker) LPush(ctx context.Context, key, value string) error {
	return b.push(ctx, key, value)
}

func aMessage() mailjob.Message {
	return mailjob.Message{To: "person@queue.test", Subject: "s", TextBody: "t"}
}

func TestARefusedPushIsNotQueuedAndNotRetried(t *testing.T) {
	refused := errors.New("connection refused")
	pushes := 0
	producer := enqueue.Producer{Broker: newTestBroker(func(context.Context, string, string) error {
		pushes++
		return refused
	})}

	id, err := producer.Queue(context.Background(), enqueue.Request{Message: aMessage(), Kind: "invite"})

	if !errors.Is(err, enqueue.ErrQueueUnavailable) {
		t.Errorf("a refused push must read as the queue being unavailable, got %v", err)
	}
	if !errors.Is(err, refused) {
		t.Error("the reason Redis gave is lost")
	}
	if id != "" {
		t.Errorf("a mail id was handed back for mail that is not queued: %q", id)
	}
	if pushes != 1 {
		t.Errorf("a failed push was tried %d times; a second push is a second email", pushes)
	}
}

func TestOneSendIsOnePushOntoReady(t *testing.T) {
	var keys, values []string
	producer := enqueue.Producer{Broker: newTestBroker(func(_ context.Context, key, value string) error {
		keys = append(keys, key)
		values = append(values, value)
		return nil
	})}

	id, err := producer.Queue(context.Background(), enqueue.Request{Message: aMessage(), Kind: "invite", Ref: "7"})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != queuekeys.Ready {
		t.Fatalf("pushed %v", keys)
	}
	if len(id) != 32 || strings.ContainsAny(id, "-ghijklmnopqrstuvwxyz") {
		t.Errorf("the id is not uuid4().hex: %q", id)
	}
	if !strings.Contains(values[0], `"id":"`+id+`"`) {
		t.Error("the id handed back is not the id on the queue")
	}
	if !strings.Contains(values[0], `"attempt":0`) {
		t.Error("newly queued mail has had no tries")
	}
}

func TestAnExpiryTravelsWithTheMessageAndAnAbsentOneIsNull(t *testing.T) {
	var queued []string
	producer := enqueue.Producer{Broker: newTestBroker(func(_ context.Context, _, value string) error {
		queued = append(queued, value)
		return nil
	})}
	ctx := context.Background()

	if _, err := producer.Queue(ctx, enqueue.Request{Message: aMessage(), Kind: "k"}); err != nil {
		t.Fatal(err)
	}
	expires := time.Unix(1762608600, 250000000)
	if _, err := producer.Queue(ctx, enqueue.Request{Message: aMessage(), Kind: "k", NotAfter: expires}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(queued[0], `"not_after":null`) {
		t.Error("mail with no deadline must carry none, or the senders drop it")
	}
	if !strings.Contains(queued[1], `"not_after":1762608600.25`) {
		t.Errorf("the deadline is not the instant given: %s", queued[1])
	}
}

func TestMailWithNoKindIsRefusedBeforeItReachesRedis(t *testing.T) {
	pushes := 0
	producer := enqueue.Producer{Broker: newTestBroker(func(context.Context, string, string) error {
		pushes++
		return nil
	})}

	// Every log line about a message names its kind, and the senders report
	// outcomes by kind; an unnamed one is invisible in both logs.
	if _, err := producer.Queue(context.Background(), enqueue.Request{Message: aMessage()}); err == nil {
		t.Error("mail with no kind was accepted")
	}
	if pushes != 0 {
		t.Error("it reached Redis anyway")
	}
}

func TestADifferentIdEveryTime(t *testing.T) {
	producer := enqueue.Producer{Broker: newTestBroker(func(context.Context, string, string) error { return nil })}
	seen := map[string]bool{}
	for range 200 {
		id, err := producer.Queue(context.Background(), enqueue.Request{Message: aMessage(), Kind: "k"})
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("id %s was handed out twice", id)
		}
		seen[id] = true
	}
}

func TestTheSameMailQueuedTwiceIsQueuedOnce(t *testing.T) {
	var pushed []string
	broker := newTestBroker(func(_ context.Context, _, value string) error {
		pushed = append(pushed, value)
		return nil
	})
	producer := enqueue.Producer{Broker: broker}
	ctx := context.Background()

	request := enqueue.Request{Message: aMessage(), Kind: "password_reset"}
	request.Message.IdempotencyKey = "password_reset:abc"

	first, err := producer.Queue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := producer.Queue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}

	if len(pushed) != 1 {
		t.Errorf("the same mail was queued %d times", len(pushed))
	}
	if second != first {
		t.Errorf("the second caller was told %q, not the mail that is on its way (%q)", second, first)
	}
}

func TestACallerWithNoKeyIsQueuedEveryTime(t *testing.T) {
	var pushed []string
	producer := enqueue.Producer{Broker: newTestBroker(func(_ context.Context, _, value string) error {
		pushed = append(pushed, value)
		return nil
	})}
	ctx := context.Background()

	request := enqueue.Request{Message: aMessage(), Kind: "automation_result"}
	first, err := producer.Queue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := producer.Queue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed) != 2 || first == second {
		t.Errorf("a caller that named no identity was deduped anyway: %d pushes", len(pushed))
	}
}

func TestAMailWithItsOwnKeyIsQueued(t *testing.T) {
	pushes := 0
	producer := enqueue.Producer{Broker: newTestBroker(func(context.Context, string, string) error {
		pushes++
		return nil
	})}
	ctx := context.Background()
	for _, key := range []string{"invite:7:1.0", "invite:7:2.0"} {
		request := enqueue.Request{Message: aMessage(), Kind: "invite"}
		request.Message.IdempotencyKey = key
		if _, err := producer.Queue(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if pushes != 2 {
		t.Errorf("two send occasions made %d pushes", pushes)
	}
}

func TestAPushThatNeverLandedDoesNotBlockTheNextOne(t *testing.T) {
	fails := true
	pushes := 0
	producer := enqueue.Producer{Broker: newTestBroker(func(context.Context, string, string) error {
		if fails {
			return errors.New("connection refused")
		}
		pushes++
		return nil
	})}
	ctx := context.Background()
	request := enqueue.Request{Message: aMessage(), Kind: "invite"}
	request.Message.IdempotencyKey = "invite:7:1.0"

	if _, err := producer.Queue(ctx, request); !errors.Is(err, enqueue.ErrQueueUnavailable) {
		t.Fatalf("got %v", err)
	}
	fails = false
	if _, err := producer.Queue(ctx, request); err != nil {
		t.Fatal(err)
	}
	if pushes != 1 {
		t.Errorf("a mail Redis refused kept its identity and could not be queued again")
	}
}
