package broker

import (
	"reflect"
	"testing"
)

// A queue is not one Redis list. Kombu splits it into one list per priority
// step, the unsuffixed name being step 0, so a consumer that reads only the
// bare name would silently ignore anything published at a priority.
func TestQueueKeyMatchesKombusPriorityLists(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{0, "surfsense.heavy"},
		{3, "surfsense.heavy\x06\x163"},
		{6, "surfsense.heavy\x06\x166"},
		{9, "surfsense.heavy\x06\x169"},
		{4, "surfsense.heavy\x06\x163"},  // rounded down to its step
		{99, "surfsense.heavy\x06\x169"}, // clamped to the last step
		{-1, "surfsense.heavy\x06\x169"}, // kombu's negative index wraps to the last step
	}
	for _, c := range cases {
		if got := QueueKey("surfsense.heavy", c.priority); got != c.want {
			t.Errorf("QueueKey(priority %d) = %q, want %q", c.priority, got, c.want)
		}
	}
}

func TestQueueKeysCoverEveryPriorityHighestFirst(t *testing.T) {
	want := []string{"surfsense", "surfsense\x06\x163", "surfsense\x06\x166", "surfsense\x06\x169"}
	if got := QueueKeys("surfsense"); !reflect.DeepEqual(got, want) {
		t.Errorf("QueueKeys = %q", got)
	}
}

func TestQueueOfKeyStripsThePrioritySuffix(t *testing.T) {
	if got := QueueOfKey("surfsense.heavy\x06\x166"); got != "surfsense.heavy" {
		t.Errorf("QueueOfKey = %q", got)
	}
	if got := QueueOfKey("surfsense.heavy"); got != "surfsense.heavy" {
		t.Errorf("QueueOfKey = %q", got)
	}
}

// Kombu orders a multi-key BRPOP by priority step first and queue second, so
// every queue's priority-0 work is taken before any queue's priority-3 work.
func TestPopKeysPutEveryQueuesTopPriorityFirst(t *testing.T) {
	got := popKeys([]string{"surfsense", "surfsense.heavy"})
	want := []string{
		"surfsense", "surfsense.heavy",
		"surfsense\x06\x163", "surfsense.heavy\x06\x163",
		"surfsense\x06\x166", "surfsense.heavy\x06\x166",
		"surfsense\x06\x169", "surfsense.heavy\x06\x169",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("popKeys = %q", got)
	}
}

// BRPOP serves its keys in order, so a fixed order would let a busy queue
// starve a quiet one. Kombu rotates the queues between polls; so do we.
func TestQueueCycleRotatesSoNoQueueStarves(t *testing.T) {
	cycle := newQueueCycle([]string{"a", "b", "c"})
	if got := cycle.next(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("first = %q", got)
	}
	if got := cycle.next(); !reflect.DeepEqual(got, []string{"b", "c", "a"}) {
		t.Fatalf("second = %q", got)
	}
	if got := cycle.next(); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Fatalf("third = %q", got)
	}
	if got := cycle.next(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("wrapped = %q", got)
	}
}

// Only the queues with a free slot are polled, and the rotation must survive
// the subset so a queue that is full does not lose its turn.
func TestQueueCycleRotatesASubset(t *testing.T) {
	cycle := newQueueCycle([]string{"a", "b", "c"})
	cycle.next()
	if got := cycle.only([]string{"a", "c"}); !reflect.DeepEqual(got, []string{"c", "a"}) {
		t.Errorf("only = %q", got)
	}
}
