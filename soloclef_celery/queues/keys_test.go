package queues

import (
	"reflect"
	"testing"
)

// A queue is not one Redis list. Kombu splits it into one list per priority
// step, the unsuffixed name being step 0, so a consumer that reads only the
// bare name would silently ignore anything published at a priority — and a
// producer that wrote only the bare name would be invisible to a consumer
// reading a priority list.
func TestKeyMatchesKombusPriorityLists(t *testing.T) {
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
		if got := Key("surfsense.heavy", c.priority); got != c.want {
			t.Errorf("Key(priority %d) = %q, want %q", c.priority, got, c.want)
		}
	}
}

func TestKeysCoverEveryPriorityHighestFirst(t *testing.T) {
	want := []string{"surfsense", "surfsense\x06\x163", "surfsense\x06\x166", "surfsense\x06\x169"}
	if got := Keys("surfsense"); !reflect.DeepEqual(got, want) {
		t.Errorf("Keys = %q", got)
	}
}

func TestQueueOfStripsThePrioritySuffix(t *testing.T) {
	if got := QueueOf("surfsense.heavy\x06\x166"); got != "surfsense.heavy" {
		t.Errorf("QueueOf = %q", got)
	}
	if got := QueueOf("surfsense.heavy"); got != "surfsense.heavy" {
		t.Errorf("QueueOf = %q", got)
	}
}
