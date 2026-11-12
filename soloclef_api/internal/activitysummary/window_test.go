package activitysummary

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestSinceTruncatesTheSecondAndSubtractsTheHours(t *testing.T) {
	now := time.Date(2026, 11, 7, 20, 45, 32, 269159000, time.UTC)

	since, err := Since(now, 24)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	want := time.Date(2026, 11, 6, 20, 45, 32, 0, time.UTC)
	if !since.Equal(want) {
		t.Errorf("got %s, want %s", since, want)
	}
}

func TestSinceAcceptsANegativeWindowAsThePythonRouteDoes(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	since, err := Since(now, -5)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if !since.After(now) {
		t.Errorf("a negative window must look into the future, got %s", since)
	}
}

func TestSinceReachesAsFarBackAsPythonsDatetimeDoes(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	// A thousand years is past a Go Duration but well inside Python's range.
	since, err := Since(now, 1000*365*24)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	// Python: datetime(2026, 11, 7) - timedelta(hours=1000*365*24)
	want := time.Date(1027, 7, 8, 0, 0, 0, 0, time.UTC)
	if !since.Equal(want) {
		t.Errorf("got %s, want %s", since, want)
	}
}

func TestSinceRefusesWindowsPythonAlsoRefuses(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)

	for name, hours := range map[string]int64{
		"before year 1":          3000 * 365 * 24,
		"after year 9999":        -9000 * 365 * 24,
		"past timedelta's range": math.MaxInt64,
	} {
		if _, err := Since(now, hours); !errors.Is(err, ErrWindowOutOfRange) {
			t.Errorf("%s: got %v, want ErrWindowOutOfRange", name, err)
		}
	}
}
