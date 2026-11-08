package results

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"github.com/redis/go-redis/v9"
)

// stubClient never connects: these checks are about the configuration, not Redis.
func stubClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
}

func TestNewBackendRefusesWhatItCannotWriteWith(t *testing.T) {
	if _, err := NewBackend(nil, time.Hour); err == nil {
		t.Error("a backend with no client must be refused")
	}
	rdb := stubClient()
	if _, err := NewBackend(rdb, 0); err == nil {
		t.Error("an expiry of zero would leave rows on the broker forever")
	}
}

func TestARowIsReadableWhereThePythonBackendLooks(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	backend, err := NewBackend(rdb, CeleryResultExpiry)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	row := celery.Result{
		TaskID: "task-1",
		State:  celery.StateSuccess,
		Value:  map[string]any{"indexed": 3.0},
		Name:   "reindex_knowledge_store",
		Queue:  "probe.exec",
		Worker: "go-worker-test",
		DoneAt: time.Now(),
	}
	if err := backend.Write(ctx, row); err != nil {
		t.Fatalf("Write: %v", err)
	}

	raw, err := rdb.Get(ctx, celery.ResultKey("task-1")).Bytes()
	if err != nil {
		t.Fatalf("the row is not where AsyncResult looks: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("the stored row is not JSON: %v", err)
	}
	if stored["status"] != "SUCCESS" || stored["task_id"] != "task-1" {
		t.Errorf("stored row = %#v", stored)
	}

	ttl, err := rdb.TTL(ctx, celery.ResultKey("task-1")).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > CeleryResultExpiry {
		t.Errorf("TTL = %v, want result_expires or less", ttl)
	}
}

// AsyncResult.get() on the Redis backend waits on a subscription to the result
// key, so a row written without the publish is a row a caller waits out.
func TestWritingPublishesOnTheKeyAsCeleryDoes(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	backend, err := NewBackend(rdb, CeleryResultExpiry)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	sub := rdb.Subscribe(ctx, celery.ResultKey("task-2"))
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := backend.Write(ctx, celery.Result{TaskID: "task-2", State: celery.StateSuccess}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case got := <-sub.Channel():
		var stored map[string]any
		if err := json.Unmarshal([]byte(got.Payload), &stored); err != nil {
			t.Fatalf("the published payload is not JSON: %v", err)
		}
		if stored["status"] != "SUCCESS" {
			t.Errorf("published %#v", stored)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was published on the result key")
	}
}

func TestTheTerminalRowReplacesTheStartedOne(t *testing.T) {
	rdb := testRedis(t)
	ctx := context.Background()
	backend, err := NewBackend(rdb, CeleryResultExpiry)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}

	for _, state := range []celery.State{celery.StateStarted, celery.StateFailure} {
		if err := backend.Write(ctx, celery.Result{TaskID: "task-3", State: state}); err != nil {
			t.Fatalf("Write %s: %v", state, err)
		}
	}

	raw, err := rdb.Get(ctx, celery.ResultKey("task-3")).Bytes()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if stored["status"] != "FAILURE" {
		t.Errorf("status = %#v, want the terminal state to win", stored["status"])
	}
}
