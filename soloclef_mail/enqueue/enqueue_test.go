package enqueue_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/enqueue"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/queuekeys"
)

func aMessage() mailjob.Message {
	return mailjob.Message{To: "person@queue.test", Subject: "s", TextBody: "t"}
}

func TestARefusedPushIsNotQueuedAndNotRetried(t *testing.T) {
	refused := errors.New("connection refused")
	pushes := 0
	producer := enqueue.Producer{Broker: brokerFunc(func(context.Context, string, string) error {
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
	producer := enqueue.Producer{Broker: brokerFunc(func(_ context.Context, key, value string) error {
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
	producer := enqueue.Producer{Broker: brokerFunc(func(_ context.Context, _, value string) error {
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
	producer := enqueue.Producer{Broker: brokerFunc(func(context.Context, string, string) error {
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
	producer := enqueue.Producer{Broker: brokerFunc(func(context.Context, string, string) error { return nil })}
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
