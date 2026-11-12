package broker

import (
	"sync"
	"time"
)

// Snapshot is what the tier was built to be able to say. `celery_heavy` could
// report none of it: a fifth podcast request waited with nothing recording
// that it waited, and the only concurrency number was one per container.
type Snapshot struct {
	// QueueDepth is how many messages are waiting on each consumed queue.
	QueueDepth map[string]int64

	// InFlightByQueue and InFlightByTask are the jobs running now. The task
	// breakdown is the one that says whether one class is taking a queue.
	InFlightByQueue map[string]int
	InFlightByTask  map[string]int

	// HeldClaims is how many messages this worker holds unacked: the jobs
	// it is running, and any waiting for a task class to have room.
	HeldClaims int

	// Scheduled is how many messages wait in the scheduled set for their
	// eta, for every queue and every worker (scheduled.go).
	Scheduled int64

	// Wait is true queue wait by task name — how long a message sat before
	// this worker took it, from this app's own enqueue stamp.
	Wait map[string]WaitStats

	Taken      uint64 // messages taken off a queue
	Deferred   uint64 // put back because the task class was full
	Expired    uint64 // dropped unrun, past their expires
	Unreadable uint64 // dropped unrun, not an envelope this worker can read
	Requeued   uint64 // put back for any reason
	Delayed    uint64 // taken before their eta and handed to the scheduled set
	Restored   uint64 // made visible again after a claim went stale
	// WaitUnknown counts messages whose wait could not be measured, so an
	// absent measurement is never mistaken for a fast queue.
	WaitUnknown uint64
}

// WaitStats summarises the queue wait seen for one task name.
type WaitStats struct {
	Count uint64
	Total time.Duration
	Max   time.Duration
}

// Mean is the average wait, or zero when nothing has been measured.
func (w WaitStats) Mean() time.Duration {
	if w.Count == 0 {
		return 0
	}
	return w.Total / time.Duration(w.Count)
}

type metrics struct {
	mu   sync.Mutex
	wait map[string]WaitStats

	taken       uint64
	deferred    uint64
	expired     uint64
	unreadable  uint64
	requeued    uint64
	delayed     uint64
	restored    uint64
	waitUnknown uint64
}

func newMetrics() *metrics {
	return &metrics{wait: map[string]WaitStats{}}
}

func (m *metrics) observeWait(task string, waited time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := m.wait[task]
	stats.Count++
	stats.Total += waited
	if waited > stats.Max {
		stats.Max = waited
	}
	m.wait[task] = stats
}

func (m *metrics) count(of *uint64, by uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	*of += by
}

func (m *metrics) fill(into *Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	into.Wait = make(map[string]WaitStats, len(m.wait))
	for task, stats := range m.wait {
		into.Wait[task] = stats
	}
	into.Taken = m.taken
	into.Deferred = m.deferred
	into.Expired = m.expired
	into.Unreadable = m.unreadable
	into.Requeued = m.requeued
	into.Delayed = m.delayed
	into.Restored = m.restored
	into.WaitUnknown = m.waitUnknown
}
