package retrypolicy

import (
	"testing"
	"time"
)

func TestTheDelaysAreRetryPolicysOwn(t *testing.T) {
	want := []time.Duration{5e9, 15e9, 60e9, 180e9, 600e9, 1800e9}
	for i, d := range want {
		got, ok := Delay(i + 1)
		if !ok || got != d {
			t.Errorf("after %d failures: %v %v, want %v", i+1, got, ok, d)
		}
	}
}

func TestTheSeventhTryIsTheLast(t *testing.T) {
	if MaxAttempts != 7 {
		t.Fatalf("MAX_ATTEMPTS is 7, this says %d", MaxAttempts)
	}
	if _, ok := Delay(MaxAttempts); ok {
		t.Error("a seventh failure must be the end of it")
	}
	if _, ok := Delay(0); ok {
		t.Error("retry_delay returns None below one failure")
	}
}

func TestTheWholeRunIsAboutFortyFiveMinutes(t *testing.T) {
	var total time.Duration
	for _, d := range Delays {
		total += d
	}
	if total < 40*time.Minute || total > 50*time.Minute {
		t.Errorf("the docs promise about 45 minutes, these add to %v", total)
	}
}
