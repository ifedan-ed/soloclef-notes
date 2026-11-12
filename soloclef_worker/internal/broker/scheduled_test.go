package broker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// A claimed message that is not due moves, in one step, out of its claim and
// into the scheduled set: never in both, so neither a restore nor a
// promotion can run it twice.
func TestScheduleMovesAClaimIntoTheScheduledSetAndPromoteBringsItBackWhenDue(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), DefaultConfig().VisibilityWindow)
	now := time.Now()
	registry.now = func() time.Time { return now }

	eta := now.Add(15 * time.Second)
	msg := decodeFor(t, scheduledEnvelope("tag-later", testQueue, eta.UTC().Format(time.RFC3339Nano), time.Time{}))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if won, err := registry.Schedule(ctx, msg); err != nil || !won {
		t.Fatalf("Schedule = %v, %v", won, err)
	}
	if claims, _ := rdb.HLen(ctx, "unacked").Result(); claims != 0 {
		t.Errorf("%d claims left after scheduling", claims)
	}
	if scored, _ := rdb.ZCard(ctx, "unacked_index").Result(); scored != 0 {
		t.Errorf("%d claim scores left after scheduling", scored)
	}
	if count, err := registry.Scheduled(ctx); err != nil || count != 1 {
		t.Fatalf("Scheduled = %d, %v", count, err)
	}

	// Not due: nothing moves.
	if moved, err := registry.PromoteDue(ctx); err != nil || moved != 0 {
		t.Fatalf("before its eta, PromoteDue = %d, %v", moved, err)
	}
	if depth, _ := rdb.LLen(ctx, testQueue).Result(); depth != 0 {
		t.Fatalf("a message not due is on the queue")
	}

	// Due: it goes to the consume end, ahead of what was already queued,
	// because it has waited its delay already.
	publish(t, rdb, envelopeFor("tag-queued", testQueue, 0))
	now = eta.Add(time.Millisecond)
	if moved, err := registry.PromoteDue(ctx); err != nil || moved != 1 {
		t.Fatalf("after its eta, PromoteDue = %d, %v", moved, err)
	}
	next, err := rdb.RPop(ctx, testQueue).Result()
	if err != nil {
		t.Fatal(err)
	}
	if back := decodeFor(t, []byte(next)); back.DeliveryTag != "tag-later" || !back.ETA.Equal(msg.ETA) {
		t.Errorf("the next message out is %q (eta %v)", back.DeliveryTag, back.ETA)
	}
	if count, _ := registry.Scheduled(ctx); count != 0 {
		t.Errorf("%d left scheduled after the promotion", count)
	}
	if moved, _ := registry.PromoteDue(ctx); moved != 0 {
		t.Errorf("a second promotion moved %d", moved)
	}
}

// A claim that another worker restored first is that worker's message: it is
// not scheduled as well, which would run it twice.
func TestAClaimAlreadyRestoredIsNotScheduled(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), DefaultConfig().VisibilityWindow)
	msg := decodeFor(t, scheduledEnvelope("tag-taken", testQueue, time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), time.Time{}))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatal(err)
	}
	if err := registry.Ack(ctx, msg.DeliveryTag); err != nil {
		t.Fatal(err)
	}

	if won, err := registry.Schedule(ctx, msg); err != nil || won {
		t.Fatalf("Schedule = %v, %v; want false", won, err)
	}
	if count, _ := registry.Scheduled(ctx); count != 0 {
		t.Errorf("%d scheduled", count)
	}
}

// The point of the scheduled set: a delayed message holds no queue slot
// while it waits. With one slot, a message sent after a delayed one runs at
// once, and the delayed one still runs no earlier than its eta.
func TestADelayedMessageHoldsNoSlotWhileItWaits(t *testing.T) {
	rdb := testRedis(t)
	ran := make(chan string, 2)
	var running atomic.Int64
	consumer, _ := runConsumer(t, rdb, fastConfig(1), runnerFunc(func(_ context.Context, msg *celery.Message) error {
		running.Add(1)
		defer running.Add(-1)
		ran <- msg.DeliveryTag
		return nil
	}))

	due := time.Now().Add(3 * time.Second)
	publish(t, rdb, scheduledEnvelope("tag-delayed", testQueue, due.UTC().Format(time.RFC3339Nano), time.Time{}))
	waitFor(t, "the delayed message to be scheduled", func() bool {
		count, _ := consumer.registry.Scheduled(context.Background())
		return count == 1
	})
	snapshot, err := consumer.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.InFlightByQueue[testQueue] != 0 || snapshot.HeldClaims != 0 || snapshot.Scheduled != 1 || snapshot.Delayed != 1 {
		t.Fatalf("while it waits: %+v", snapshot)
	}

	publish(t, rdb, envelopeFor("tag-now", testQueue, 0))
	select {
	case tag := <-ran:
		if tag != "tag-now" || time.Now().After(due) {
			t.Fatalf("%q ran first, at %v before the delayed one's eta", tag, time.Until(due))
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the message behind the delayed one never ran")
	}
	select {
	case tag := <-ran:
		if tag != "tag-delayed" {
			t.Fatalf("then %q ran", tag)
		}
		if early := time.Until(due); early > 0 {
			t.Errorf("the delayed message ran %v early", early)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the delayed message never ran")
	}
	waitFor(t, "both claims to be acknowledged", func() bool {
		n, _ := rdb.HLen(context.Background(), "unacked").Result()
		return n == 0
	})
}
