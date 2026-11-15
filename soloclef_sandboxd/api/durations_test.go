package api

import (
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_sandboxd/sandboxes"
)

// A body's durations are checked as integers before they become
// time.Durations: a number that would wrap around (to a negative or a small
// positive duration) is refused, and never reaches the manager.
func TestDurationsThatWouldOverflowAreRefused(t *testing.T) {
	acquire := func(idle, wait int64) string {
		return fmt.Sprintf(`{"kind":"thread","key":"7","account":"acct","cpu":"1","memory":"1Gi","idleTtlSeconds":%d,"maxPerAccount":2,"waitSeconds":%d}`, idle, wait)
	}
	mostWait := int64(sandboxes.MaximumWait / time.Second)
	for name, body := range map[string]string{
		// 2^63/10^9 + 1 seconds wraps to a negative duration; 18446744074
		// (2^64 ns / 10^9, rounded up) wraps back round to a few seconds.
		"a wait one past the bound":        acquire(600, mostWait+1),
		"a wait that wraps negative":       acquire(600, math.MaxInt64/int64(time.Second)+1),
		"a wait that wraps to a small one": acquire(600, 18446744074),
		"the largest wait":                 acquire(600, math.MaxInt64),
		"a negative wait":                  acquire(600, -1),
		"an idle time that wraps":          acquire(18446744074, 0),
		"an idle time past what fits":      acquire(math.MaxInt64, 0),
		"a negative idle time":             acquire(-1, 0),
		"a command limit that wraps":       `{"command":"true","timeoutMs":18446744073710}`,
		"a command limit past what fits":   `{"command":"true","timeoutMs":9223372036854775807}`,
	} {
		manager := &fakeManager{}
		target := "/v1/sandboxes"
		if body[2] == 'c' { // {"command":...
			target = "/v1/sandboxes/box-1/commands"
		}
		got := call(t, newHandler(manager, pinger{}), http.MethodPost, target, body, true)
		if got.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, got.Code, got.Body.String())
		}
		if manager.acquired != (sandboxes.Request{}) || manager.command != "" {
			t.Errorf("%s reached the manager: %+v %q", name, manager.acquired, manager.command)
		}
	}

	// The bound itself is accepted, as the exact duration.
	manager := &fakeManager{}
	got := call(t, newHandler(manager, pinger{}), http.MethodPost, "/v1/sandboxes", acquire(600, mostWait), true)
	if got.Code != http.StatusOK || manager.acquired.MaxWait != sandboxes.MaximumWait {
		t.Errorf("a wait of exactly the bound: %d, asked %s", got.Code, manager.acquired.MaxWait)
	}
}
