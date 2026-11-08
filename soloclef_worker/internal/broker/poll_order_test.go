package broker

import (
	"reflect"
	"testing"
)

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
