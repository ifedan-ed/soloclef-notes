// Package broker consumes Soloclef's Celery queues off Redis.
//
// Everything here is written against the layout kombu's Redis transport
// already uses on this host: one Redis list per queue and priority step
// (soloclef_celery/queues), and an `unacked` registry standing in for the
// broker ack Redis does not have. A Python Celery worker and this worker
// therefore read and write the same keys, which is what makes handing a queue
// over — or handing it straight back — a change of which process consumes.
package broker

import (
	"git.danvics.com/danvics/soloclef/soloclef_celery/queues"
)

// popKeys is the key list for one BRPOP: priority step outer, queue inner, so
// that every queue's top-priority work is taken before any queue's next step.
func popKeys(wanted []string) []string {
	keys := make([]string, 0, len(wanted)*len(queues.Priorities))
	for _, priority := range queues.Priorities {
		for _, queue := range wanted {
			keys = append(keys, queues.Key(queue, priority))
		}
	}
	return keys
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
