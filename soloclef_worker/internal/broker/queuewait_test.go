package broker

import (
	"errors"
	"testing"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

func stampedAt(monotonicNs int64) *celery.Message {
	return &celery.Message{EnqueuedAt: time.Unix(0, monotonicNs)}
}

// The only record of when a job was enqueued is this app's own header, and the
// producer stamps it with Python's time.monotonic_ns — seconds since the
// host's boot, not a wall clock. Wait time is that number subtracted from this
// process's own CLOCK_MONOTONIC, which is the same clock inside any container
// on the same host.
func TestQueueWaitSubtractsTheProducersMonotonicStamp(t *testing.T) {
	now := 500 * time.Second
	wait, err := QueueWait(stampedAt(int64(440*time.Second)), func() (time.Duration, error) { return now, nil })
	if err != nil {
		t.Fatalf("QueueWait: %v", err)
	}
	if wait != 60*time.Second {
		t.Errorf("wait = %v, want 1m", wait)
	}
}

func TestQueueWaitSaysNothingRatherThanGuessWithoutAStamp(t *testing.T) {
	_, err := QueueWait(&celery.Message{}, func() (time.Duration, error) { return time.Hour, nil })
	if !errors.Is(err, ErrNoEnqueueStamp) {
		t.Fatalf("err = %v, want ErrNoEnqueueStamp", err)
	}
}

// A stamp from a different boot clock — a producer on another host — reads as
// a negative or absurd wait. Reporting nothing is right; reporting a number
// that looks like a measurement is not.
func TestQueueWaitRefusesAStampFromAnotherClock(t *testing.T) {
	now := 500 * time.Second
	clock := func() (time.Duration, error) { return now, nil }

	if _, err := QueueWait(stampedAt(int64(600*time.Second)), clock); !errors.Is(err, ErrClockNotShared) {
		t.Error("a stamp from the future should be refused")
	}
	if _, err := QueueWait(stampedAt(1), clock); err != nil {
		t.Errorf("a credible wait should be reported: %v", err)
	}

	later := func() (time.Duration, error) { return MaxCredibleWait + 2*time.Hour, nil }
	if _, err := QueueWait(stampedAt(1), later); !errors.Is(err, ErrClockNotShared) {
		t.Error("a wait longer than any result survives should be refused")
	}
}

// The real clock must be readable and must move forward; everything above is
// meaningless if it is not.
func TestMonotonicClockIsReadableAndAdvances(t *testing.T) {
	first, err := ReadMonotonic()
	if err != nil {
		t.Fatalf("ReadMonotonic: %v", err)
	}
	if first <= 0 {
		t.Fatalf("ReadMonotonic = %v", first)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := ReadMonotonic()
	if err != nil {
		t.Fatalf("ReadMonotonic: %v", err)
	}
	if second <= first {
		t.Fatalf("the clock did not advance: %v then %v", first, second)
	}
}
