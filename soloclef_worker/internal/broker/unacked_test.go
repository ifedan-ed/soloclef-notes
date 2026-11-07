package broker

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

func envelopeFor(tag, queue string, priority int) []byte {
	return []byte(`{"body":"W1tdLHt9LHt9XQ==","content-encoding":"utf-8",` +
		`"content-type":"application/json","headers":{"lang":"py","task":"probe.task",` +
		`"id":"` + tag + `","retries":0,"eta":null,"expires":null,"timelimit":[null,null]},` +
		`"properties":{"body_encoding":"base64","priority":` + itoa(priority) +
		`,"delivery_tag":"` + tag + `","delivery_info":{"exchange":"` + queue +
		`","routing_key":"` + queue + `"}}}`)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func decodeFor(t *testing.T, raw []byte) *celery.Message {
	t.Helper()
	msg, err := celery.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return msg
}

// Celery's Redis transport has no broker ack. It emulates one with two keys:
// a hash of in-flight payloads by delivery tag, and a sorted set scoring each
// tag by when it was taken. Writing anything else would make a Python worker
// blind to this worker's in-flight work.
func TestClaimWritesTheSameTwoKeysCeleryDoes(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), DefaultConfig().VisibilityWindow)

	raw := envelopeFor("tag-a", "probe.queue", 0)
	if err := registry.Claim(ctx, decodeFor(t, raw)); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	stored, err := rdb.HGet(ctx, "unacked", "tag-a").Result()
	if err != nil {
		t.Fatalf("the payload should be in the unacked hash: %v", err)
	}
	var entry []json.RawMessage
	if err := json.Unmarshal([]byte(stored), &entry); err != nil || len(entry) != 3 {
		t.Fatalf("the entry must be [payload, exchange, routing_key], got %s", stored)
	}
	var exchange, routingKey string
	if err := json.Unmarshal(entry[1], &exchange); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if err := json.Unmarshal(entry[2], &routingKey); err != nil {
		t.Fatalf("routing key: %v", err)
	}
	if exchange != "probe.queue" || routingKey != "probe.queue" {
		t.Errorf("entry named %q / %q", exchange, routingKey)
	}

	score, err := rdb.ZScore(ctx, "unacked_index", "tag-a").Result()
	if err != nil {
		t.Fatalf("the tag should be scored in unacked_index: %v", err)
	}
	if drift := time.Since(time.Unix(0, int64(score*float64(time.Second)))); drift > time.Minute || drift < -time.Minute {
		t.Errorf("the score should be a unix timestamp in seconds, got %v (%v off)", score, drift)
	}
}

func TestAckLeavesNoTraceOfTheClaim(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), DefaultConfig().VisibilityWindow)

	msg := decodeFor(t, envelopeFor("tag-b", "probe.queue", 0))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := registry.Ack(ctx, msg.DeliveryTag); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if n, _ := rdb.HLen(ctx, "unacked").Result(); n != 0 {
		t.Errorf("unacked still holds %d entries", n)
	}
	if n, _ := rdb.ZCard(ctx, "unacked_index").Result(); n != 0 {
		t.Errorf("unacked_index still holds %d entries", n)
	}
}

// Requeue is what a worker does with work it decided not to run. Celery pushes
// it to the producer end of the list, so it waits behind everything already
// queued rather than coming straight back.
func TestRequeuePutsTheMessageBehindWorkAlreadyQueued(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), DefaultConfig().VisibilityWindow)

	if err := rdb.LPush(ctx, "probe.queue", "already-here").Err(); err != nil {
		t.Fatalf("seeding the queue: %v", err)
	}
	msg := decodeFor(t, envelopeFor("tag-c", "probe.queue", 0))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	restored, err := registry.Requeue(ctx, msg)
	if err != nil || !restored {
		t.Fatalf("Requeue = %v, %v", restored, err)
	}

	next, err := rdb.RPop(ctx, "probe.queue").Result()
	if err != nil {
		t.Fatalf("RPop: %v", err)
	}
	if next != "already-here" {
		t.Errorf("the requeued message jumped the queue: %q came out first", next)
	}
	back, err := rdb.RPop(ctx, "probe.queue").Result()
	if err != nil {
		t.Fatalf("the requeued message should be behind it: %v", err)
	}
	if !decodeFor(t, []byte(back)).Redelivered {
		t.Error("a requeued message must be stamped redelivered")
	}
}

// This is the acks_late contract on Redis: a message a dead worker took stops
// having its claim refreshed, and after the visibility window any worker makes
// it visible again — at the consume end, so it is taken next.
func TestRestoreVisibleBringsBackAStaleClaimAndLeavesFreshOnesAlone(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), KombuVisibilityWindow)

	stale := decodeFor(t, envelopeFor("tag-d", "probe.queue", 0))
	fresh := decodeFor(t, envelopeFor("tag-e", "probe.queue", 0))
	for _, msg := range []*celery.Message{stale, fresh} {
		if err := registry.Claim(ctx, msg); err != nil {
			t.Fatalf("Claim: %v", err)
		}
	}
	// Age one claim past the window, exactly as a dead worker's claim ages.
	aged := float64(time.Now().Add(-2*KombuVisibilityWindow).UnixNano()) / float64(time.Second)
	if err := rdb.ZAdd(ctx, "unacked_index", redisMember("tag-d", aged)).Err(); err != nil {
		t.Fatalf("ageing the claim: %v", err)
	}

	restored, err := registry.RestoreVisible(ctx)
	if err != nil {
		t.Fatalf("RestoreVisible: %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored %d messages, want 1", restored)
	}
	if n, _ := rdb.LLen(ctx, "probe.queue").Result(); n != 1 {
		t.Fatalf("the queue holds %d messages", n)
	}
	if held, _ := rdb.HKeys(ctx, "unacked").Result(); len(held) != 1 || held[0] != "tag-e" {
		t.Errorf("the fresh claim should still be held, got %q", held)
	}
}

// A restored message is taken next rather than joining the back of the queue:
// it was already waiting before anything behind it.
func TestARestoredMessageIsTakenNext(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), KombuVisibilityWindow)

	if err := rdb.LPush(ctx, "probe.queue", "queued-after").Err(); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	msg := decodeFor(t, envelopeFor("tag-f", "probe.queue", 0))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	aged := float64(time.Now().Add(-2*KombuVisibilityWindow).UnixNano()) / float64(time.Second)
	if err := rdb.ZAdd(ctx, "unacked_index", redisMember("tag-f", aged)).Err(); err != nil {
		t.Fatalf("ageing: %v", err)
	}
	if _, err := registry.RestoreVisible(ctx); err != nil {
		t.Fatalf("RestoreVisible: %v", err)
	}
	next, err := rdb.RPop(ctx, "probe.queue").Result()
	if err != nil {
		t.Fatalf("RPop: %v", err)
	}
	if decodeFor(t, []byte(next)).DeliveryTag != "tag-f" {
		t.Errorf("the restored message should come out first, got %q", next)
	}
}

// Renewing is what lets a job run for hours on a transport whose visibility
// window is one hour. Without it a live worker's own message is restored under
// it and runs twice.
func TestRenewKeepsALongJobsClaimOutOfTheRestoreWindow(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), KombuVisibilityWindow)

	msg := decodeFor(t, envelopeFor("tag-g", "probe.queue", 0))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	aged := float64(time.Now().Add(-2*KombuVisibilityWindow).UnixNano()) / float64(time.Second)
	if err := rdb.ZAdd(ctx, "unacked_index", redisMember("tag-g", aged)).Err(); err != nil {
		t.Fatalf("ageing: %v", err)
	}
	if err := registry.Renew(ctx, []string{"tag-g"}); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	restored, err := registry.RestoreVisible(ctx)
	if err != nil {
		t.Fatalf("RestoreVisible: %v", err)
	}
	if restored != 0 {
		t.Errorf("a renewed claim was restored under its owner")
	}
}

// Two workers can scan for stale claims at the same moment. Exactly one may
// restore a given message, or the queue grows a duplicate.
func TestOnlyOneRestorerCanWinAMessage(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	first := NewRegistry(rdb, CeleryRegistryKeys(), KombuVisibilityWindow)
	second := NewRegistry(rdb, CeleryRegistryKeys(), KombuVisibilityWindow)

	msg := decodeFor(t, envelopeFor("tag-h", "probe.queue", 0))
	if err := first.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	wonFirst, err := first.Requeue(ctx, msg)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	wonSecond, err := second.Requeue(ctx, msg)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if !wonFirst || wonSecond {
		t.Errorf("wins = %v / %v; exactly the first should win", wonFirst, wonSecond)
	}
	if n, _ := rdb.LLen(ctx, "probe.queue").Result(); n != 1 {
		t.Errorf("the queue holds %d copies of one message", n)
	}
}

// Kombu takes a lock before scanning for stale claims so a fleet of workers
// does not all scan at once. We take the same lock, by the same name.
func TestTheRestoreScanIsTakenUnderCelerysOwnLock(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)
	registry := NewRegistry(rdb, CeleryRegistryKeys(), KombuVisibilityWindow)

	if err := rdb.Set(ctx, "unacked_mutex", "another-worker", time.Minute).Err(); err != nil {
		t.Fatalf("seeding the lock: %v", err)
	}
	msg := decodeFor(t, envelopeFor("tag-i", "probe.queue", 0))
	if err := registry.Claim(ctx, msg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	aged := float64(time.Now().Add(-2*KombuVisibilityWindow).UnixNano()) / float64(time.Second)
	if err := rdb.ZAdd(ctx, "unacked_index", redisMember("tag-i", aged)).Err(); err != nil {
		t.Fatalf("ageing: %v", err)
	}

	restored, err := registry.RestoreVisible(ctx)
	if err != nil {
		t.Fatalf("RestoreVisible: %v", err)
	}
	if restored != 0 {
		t.Error("the scan ran while another worker held the lock")
	}
	if err := rdb.Del(ctx, "unacked_mutex").Err(); err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if restored, err = registry.RestoreVisible(ctx); err != nil || restored != 1 {
		t.Errorf("after the lock was released: %d, %v", restored, err)
	}
	if held, _ := rdb.Exists(ctx, "unacked_mutex").Result(); held != 0 {
		t.Error("the scan must give the lock back")
	}
}

// Queue depth is a per-queue number, but a queue is several Redis lists.
func TestDepthsSumEveryPriorityListOfAQueue(t *testing.T) {
	rdb := testRedis(t)
	ctx := testContext(t)

	if err := rdb.LPush(ctx, "probe.queue", "a", "b").Err(); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := rdb.LPush(ctx, QueueKey("probe.queue", 6), "c").Err(); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if err := rdb.LPush(ctx, "probe.other", "d").Err(); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	depths, err := Depths(ctx, rdb, []string{"probe.queue", "probe.other", "probe.empty"})
	if err != nil {
		t.Fatalf("Depths: %v", err)
	}
	if depths["probe.queue"] != 3 || depths["probe.other"] != 1 || depths["probe.empty"] != 0 {
		t.Errorf("Depths = %v", depths)
	}
}
