// Package queues names the Redis keys kombu's Redis transport holds a Celery
// queue's work in.
//
// One queue is not one list: kombu splits it by priority step, and the keys
// are what a producer writes and a consumer reads. Both Go tiers need them —
// the worker to take work off, the API tier to put work on — which is why they
// are here rather than in either one.
package queues

import (
	"sort"
	"strconv"
	"strings"
)

// prioritySeparator is kombu's Channel.sep: it joins a queue name to its
// priority step to make the Redis list key.
const prioritySeparator = "\x06\x16"

// Priorities is kombu's PRIORITY_STEPS, lowest number (highest priority)
// first. Soloclef publishes everything at 0 today; the other three exist so a
// message published at a priority is never left sitting unread.
var Priorities = []int{0, 3, 6, 9}

// Key names the Redis list holding a queue's work at one priority.
//
// The step is picked exactly as kombu's Channel.priority does, including its
// quirk that a negative priority indexes the step table from the end and so
// lands on the lowest priority rather than the highest.
func Key(queue string, priority int) string {
	step := priorityStep(priority)
	if step == 0 {
		return queue
	}
	return queue + prioritySeparator + strconv.Itoa(step)
}

// Keys names every Redis list a queue's work can be on, highest priority
// first.
func Keys(queue string) []string {
	keys := make([]string, 0, len(Priorities))
	for _, priority := range Priorities {
		keys = append(keys, Key(queue, priority))
	}
	return keys
}

// QueueOf recovers the queue name a Redis list key belongs to, so a message
// taken by a multi-key BRPOP can be attributed to its queue.
func QueueOf(key string) string {
	if cut := strings.Index(key, prioritySeparator); cut >= 0 {
		return key[:cut]
	}
	return key
}

func priorityStep(priority int) int {
	// sort.SearchInts is bisect_left; kombu uses bisect_right, hence +1.
	at := sort.SearchInts(Priorities, priority+1) - 1
	if at < 0 {
		// Python's steps[-1]: a negative priority is the lowest priority.
		return Priorities[len(Priorities)-1]
	}
	return Priorities[at]
}
