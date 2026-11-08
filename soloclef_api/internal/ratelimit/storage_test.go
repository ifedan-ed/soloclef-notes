package ratelimit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type brokenCounter struct {
	reachable bool
	hits      int
}

func (c *brokenCounter) Hit(context.Context, string, time.Duration, int64) (int64, error) {
	c.hits++
	if !c.reachable {
		return 0, errors.New("connection refused")
	}
	return int64(c.hits), nil
}

func (c *brokenCounter) Reachable(context.Context) error {
	if !c.reachable {
		return errors.New("connection refused")
	}
	return nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// While the shared counter is unreachable the limit still applies, in memory, as
// SlowAPI's in_memory_fallback does — and the shared one is picked up again as
// soon as it answers.
func TestTheLimitSurvivesAnUnreachableSharedCounter(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	shared := &brokenCounter{}
	storage := &Storage{
		Shared:   shared,
		Fallback: &MemoryCounter{Now: func() time.Time { return now }},
		Logger:   quietLogger(),
		Now:      func() time.Time { return now },
	}

	for want := int64(1); want <= 3; want++ {
		count, err := storage.Hit(context.Background(), "key", time.Minute, 1)
		if err != nil {
			t.Fatalf("hit %d: %v", want, err)
		}
		if count != want {
			t.Fatalf("hit %d: counted %d", want, count)
		}
	}
	if shared.hits != 1 {
		t.Errorf("the shared counter was asked %d times, want once before it was found dead", shared.hits)
	}

	// It answers again. The probe is allowed once the backoff has passed, and
	// counting goes back to the shared key.
	shared.reachable = true
	now = now.Add(3 * time.Second)
	if _, err := storage.Hit(context.Background(), "key", time.Minute, 1); err != nil {
		t.Fatalf("after recovery: %v", err)
	}
	if shared.hits != 2 {
		t.Errorf("the shared counter was asked %d times, want it used again", shared.hits)
	}
}

func TestTheMemoryCounterWindowStartsAtTheFirstHit(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	counter := &MemoryCounter{Now: func() time.Time { return now }}

	for want := int64(1); want <= 3; want++ {
		count, _ := counter.Hit(context.Background(), "key", time.Minute, 1)
		if count != want {
			t.Fatalf("got %d, want %d", count, want)
		}
	}

	now = now.Add(30 * time.Second)
	if count, _ := counter.Hit(context.Background(), "key", time.Minute, 1); count != 4 {
		t.Errorf("within the window: got %d, want 4", count)
	}

	// A minute after the first hit, not after the last.
	now = now.Add(31 * time.Second)
	if count, _ := counter.Hit(context.Background(), "key", time.Minute, 1); count != 1 {
		t.Errorf("after the window: got %d, want the count to start again", count)
	}
}
