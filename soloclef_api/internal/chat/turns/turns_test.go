package turns

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// stores is every Coordinator the contract is held to. Valkey joins only when
// SOLOCLEF_API_TEST_VALKEY_URL names a throwaway server: the tests write and
// delete keys under soloclef:chat:, so it must never be a live one.
func stores(t *testing.T) map[string]Coordinator {
	t.Helper()
	out := map[string]Coordinator{"memory": NewMemory(time.Now)}
	if url := os.Getenv("SOLOCLEF_API_TEST_VALKEY_URL"); url != "" {
		v, err := NewValkey(url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(v.Close)
		out["valkey"] = v
	} else {
		t.Log("valkey: skipped, SOLOCLEF_API_TEST_VALKEY_URL is not set")
	}
	return out
}

var threadSeq atomic.Int64

// thread returns a name no other test or earlier run has used, so a test
// against a shared Valkey never meets another's lease.
func thread() string {
	return "test-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(threadSeq.Add(1), 10)
}

func TestALeaseIsHeldByOneOwnerAtATime(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx, th := context.Background(), thread()
			if ok, err := store.Claim(ctx, th, "a", time.Minute); err != nil || !ok {
				t.Fatalf("first claim: %v, %v", ok, err)
			}
			if ok, err := store.Claim(ctx, th, "b", time.Minute); err != nil || ok {
				t.Fatalf("a second owner claimed a held thread: %v, %v", ok, err)
			}
			if running, _ := store.Running(ctx, th); !running {
				t.Fatal("a held thread is not reported running")
			}
			// Somebody else's release changes nothing.
			if err := store.Release(ctx, th, "b"); err != nil {
				t.Fatal(err)
			}
			if st, err := store.Renew(ctx, th, "a", time.Minute); err != nil || st != Held {
				t.Fatalf("renew after a stranger's release: %v, %v", st, err)
			}
			if st, err := store.Renew(ctx, th, "b", time.Minute); err != nil || st != Lost {
				t.Fatalf("a stranger renewed: %v, %v", st, err)
			}
			if err := store.Release(ctx, th, "a"); err != nil {
				t.Fatal(err)
			}
			if running, _ := store.Running(ctx, th); running {
				t.Fatal("a released thread is still running")
			}
			if ok, _ := store.Claim(ctx, th, "b", time.Minute); !ok {
				t.Fatal("a released thread cannot be claimed")
			}
			_ = store.Release(ctx, th, "b")
		})
	}
}

func TestALeaseExpiresWhenItsOwnerStopsRenewing(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx, th := context.Background(), thread()
			if ok, _ := store.Claim(ctx, th, "dead", 60*time.Millisecond); !ok {
				t.Fatal("claim")
			}
			time.Sleep(120 * time.Millisecond)
			if st, _ := store.Renew(ctx, th, "dead", time.Minute); st != Lost {
				t.Fatalf("an expired lease was renewed: %v", st)
			}
			if ok, _ := store.Claim(ctx, th, "next", time.Minute); !ok {
				t.Fatal("a thread whose owner died stayed busy")
			}
			_ = store.Release(ctx, th, "next")
		})
	}
}

func TestAStopIsSeenByTheHeartbeatAndNotByTheNextTurn(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			ctx, th := context.Background(), thread()
			if running, err := store.RequestStop(ctx, th); err != nil || running {
				t.Fatalf("a stop with nothing running: %v, %v", running, err)
			}
			_, _ = store.Claim(ctx, th, "a", time.Minute)
			if running, err := store.RequestStop(ctx, th); err != nil || !running {
				t.Fatalf("stop: %v, %v", running, err)
			}
			if st, _ := store.Renew(ctx, th, "a", time.Minute); st != StopRequested {
				t.Fatalf("the heartbeat did not see the stop: %v", st)
			}
			_ = store.Release(ctx, th, "a")
			// The flag went with the turn it was for.
			_, _ = store.Claim(ctx, th, "b", time.Minute)
			if st, _ := store.Renew(ctx, th, "b", time.Minute); st != Held {
				t.Fatalf("the next turn inherited a stop: %v", st)
			}
			_ = store.Release(ctx, th, "b")
		})
	}
}

// registry builds one process's registry over store.
func registry(t *testing.T, store Coordinator, heartbeat time.Duration) *Registry {
	t.Helper()
	var n atomic.Int64
	prefix := thread()
	r, err := New(Config{Store: store, Lifetime: 20 * heartbeat, Heartbeat: heartbeat,
		NewOwner: func() string { return prefix + strconv.FormatInt(n.Add(1), 10) }})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Two registries over one store are two replicas. Stop pressed on the second
// ends the turn the first is running, through the channel.
func TestStopReachesATurnRunningInAnotherProcess(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			// A heartbeat far longer than the test, so only the channel can
			// have delivered the stop.
			a, b := registry(t, store, time.Hour), registry(t, store, time.Hour)
			listening, stopListening := context.WithCancel(context.Background())
			defer stopListening()
			go func() { _ = a.Listen(listening) }()

			th := thread()
			ctx, turn, err := a.Begin(context.Background(), th)
			if err != nil {
				t.Fatal(err)
			}
			defer turn.End()
			if _, _, err := b.Begin(context.Background(), th); !errors.Is(err, ErrBusy) {
				t.Fatalf("the other process started a second turn: %v", err)
			}
			// The subscription is made asynchronously; press Stop until it
			// lands rather than guess how long subscribing takes.
			deadline := time.After(5 * time.Second)
			for stopped := false; !stopped; {
				if running, err := b.Stop(context.Background(), th); err != nil || !running {
					t.Fatalf("stop: %v, %v", running, err)
				}
				select {
				case <-ctx.Done():
					stopped = true
				case <-time.After(20 * time.Millisecond):
				case <-deadline:
					t.Fatal("the turn was not stopped")
				}
			}
			if cause := context.Cause(ctx); !errors.Is(cause, ErrStopped) {
				t.Fatalf("cause = %v, want ErrStopped", cause)
			}
			turn.End()
			if running, _ := b.Running(context.Background(), th); running {
				t.Fatal("the thread is still busy after the turn ended")
			}
		})
	}
}

// With nobody listening (the subscriber's connection is down), the stop is
// still delivered, by the heartbeat reading the flag.
func TestStopIsDeliveredByTheHeartbeatWhenTheChannelIsDown(t *testing.T) {
	for name, store := range stores(t) {
		t.Run(name, func(t *testing.T) {
			a, b := registry(t, store, 20*time.Millisecond), registry(t, store, 20*time.Millisecond)
			th := thread()
			ctx, turn, err := a.Begin(context.Background(), th)
			if err != nil {
				t.Fatal(err)
			}
			defer turn.End()
			if _, err := b.Stop(context.Background(), th); err != nil {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("the heartbeat did not deliver the stop")
			}
			if cause := context.Cause(ctx); !errors.Is(cause, ErrStopped) {
				t.Fatalf("cause = %v, want ErrStopped", cause)
			}
		})
	}
}

// A turn whose lease was taken over (its process stalled past the lifetime)
// is ended, and its End does not free the thread from under the new owner.
func TestATurnThatLostItsLeaseEndsAndLeavesTheNewOwnerAlone(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	store := NewMemory(func() time.Time { return clock() })
	a := registry(t, store, 10*time.Millisecond)
	th := thread()
	ctx, turn, err := a.Begin(context.Background(), th)
	if err != nil {
		t.Fatal(err)
	}
	// Force the takeover directly: the lease now names somebody else.
	store.mu.Lock()
	store.leases[th] = lease{owner: "successor", expires: now.Add(time.Hour)}
	store.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the turn went on without its lease")
	}
	if cause := context.Cause(ctx); !errors.Is(cause, ErrLeaseLost) {
		t.Fatalf("cause = %v, want ErrLeaseLost", cause)
	}
	turn.End()
	if st, _ := store.Renew(context.Background(), th, "successor", time.Hour); st != Held {
		t.Fatalf("the old turn's End took the successor's lease: %v", st)
	}
}

func TestNewRefusesAConfigThatCannotKeepALease(t *testing.T) {
	store := NewMemory(time.Now)
	owner := func() string { return "x" }
	for name, cfg := range map[string]Config{
		"no store":               {Lifetime: time.Minute, Heartbeat: time.Second, NewOwner: owner},
		"no lifetime":            {Store: store, Heartbeat: time.Second, NewOwner: owner},
		"heartbeat over half":    {Store: store, Lifetime: time.Minute, Heartbeat: 40 * time.Second, NewOwner: owner},
		"no owner names":         {Store: store, Lifetime: time.Minute, Heartbeat: time.Second},
		"heartbeat not positive": {Store: store, Lifetime: time.Minute, NewOwner: owner},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
