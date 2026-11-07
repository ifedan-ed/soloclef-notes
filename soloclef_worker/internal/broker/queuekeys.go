// Package broker consumes Soloclef's Celery queues off Redis.
//
// Everything here is written against the layout kombu's Redis transport
// already uses on this host, because the FastAPI app keeps producing through
// Celery's Python client: one Redis list per queue and priority step, and an
// `unacked` registry standing in for the broker ack Redis does not have. A
// Python Celery worker and this worker therefore read and write the same keys,
// which is what makes handing a queue over — or handing it straight back —
// a change of which process consumes.
package broker

import (
	"sort"
	"strconv"
	"strings"
)

// prioritySeparator is kombu's Channel.sep: it joins a queue name to its
// priority step to make the Redis list key.
const prioritySeparator = "\x06\x16"

// priorityLevels is kombu's PRIORITY_STEPS, lowest number (highest priority)
// first. Soloclef publishes everything at 0 today; the other three exist so a
// message published at a priority is never left sitting unread.
var priorityLevels = []int{0, 3, 6, 9}

// QueueKey names the Redis list holding a queue's work at one priority.
//
// The step is picked exactly as kombu's Channel.priority does, including its
// quirk that a negative priority indexes the step table from the end and so
// lands on the lowest priority rather than the highest.
func QueueKey(queue string, priority int) string {
	step := priorityStep(priority)
	if step == 0 {
		return queue
	}
	return queue + prioritySeparator + strconv.Itoa(step)
}

// QueueKeys names every Redis list a queue's work can be on, highest priority
// first.
func QueueKeys(queue string) []string {
	keys := make([]string, 0, len(priorityLevels))
	for _, priority := range priorityLevels {
		keys = append(keys, QueueKey(queue, priority))
	}
	return keys
}

// QueueOfKey recovers the queue name a Redis list key belongs to, so a message
// taken by a multi-key BRPOP can be attributed to its queue.
func QueueOfKey(key string) string {
	if cut := strings.Index(key, prioritySeparator); cut >= 0 {
		return key[:cut]
	}
	return key
}

// popKeys is the key list for one BRPOP: priority step outer, queue inner, so
// that every queue's top-priority work is taken before any queue's next step.
func popKeys(queues []string) []string {
	keys := make([]string, 0, len(queues)*len(priorityLevels))
	for _, priority := range priorityLevels {
		for _, queue := range queues {
			keys = append(keys, QueueKey(queue, priority))
		}
	}
	return keys
}

func priorityStep(priority int) int {
	// sort.SearchInts is bisect_left; kombu uses bisect_right, hence +1.
	at := sort.SearchInts(priorityLevels, priority+1) - 1
	if at < 0 {
		// Python's steps[-1]: a negative priority is the lowest priority.
		return priorityLevels[len(priorityLevels)-1]
	}
	return priorityLevels[at]
}

// queueCycle rotates the queue order between polls. BRPOP serves its keys in
// the order given, so a fixed order lets a busy queue starve a quiet one.
type queueCycle struct {
	queues []string
	at     int
}

func newQueueCycle(queues []string) *queueCycle {
	ordered := make([]string, len(queues))
	copy(ordered, queues)
	return &queueCycle{queues: ordered}
}

// next returns every queue, one further round the rotation each call.
func (c *queueCycle) next() []string {
	return c.only(c.queues)
}

// only returns the named subset in rotation order, so a queue skipped because
// it is full does not lose its turn.
func (c *queueCycle) only(wanted []string) []string {
	keep := make(map[string]bool, len(wanted))
	for _, queue := range wanted {
		keep[queue] = true
	}
	ordered := make([]string, 0, len(wanted))
	for i := range c.queues {
		queue := c.queues[(c.at+i)%len(c.queues)]
		if keep[queue] {
			ordered = append(ordered, queue)
		}
	}
	if len(c.queues) > 0 {
		c.at = (c.at + 1) % len(c.queues)
	}
	return ordered
}
