package beat

import "time"

// Snapshot is what the scheduler can say about itself, and the reason it can
// be watched at all: Celery Beat reports nothing but its log.
type Snapshot struct {
	// LeaseHeld is whether this process is the one keeping the schedule.
	// Exactly one beat should report true, and a dashboard with none is a
	// schedule nobody is running.
	LeaseHeld bool

	// Entries is each entry's own counts, by beat_schedule name.
	Entries map[string]EntrySnapshot

	// CaughtUp counts windows fired a minute or more late, which is a window
	// that was missed while nothing was scheduling.
	CaughtUp uint64
}

// EntrySnapshot is one schedule entry's history in this process.
type EntrySnapshot struct {
	Published uint64
	Fenced    uint64
	Failed    uint64
	LastTick  time.Time
}

// Snapshot reads the counters without stopping the schedule.
func (b *Beat) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	entries := make(map[string]EntrySnapshot, len(b.entries))
	for _, entry := range b.entries {
		counts := b.counts[entry.Name]
		entries[entry.Name] = EntrySnapshot{
			Published: counts.Published,
			Fenced:    counts.Fenced,
			Failed:    counts.Failed,
			LastTick:  counts.LastTick,
		}
	}
	return Snapshot{LeaseHeld: b.held, Entries: entries, CaughtUp: b.caught}
}
