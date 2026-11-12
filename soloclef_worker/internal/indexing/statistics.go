package indexing

import (
	"sync"
	"time"
)

// Statistics adds up what the jobs of one process did, for its metrics.
type Statistics struct {
	mu       sync.Mutex
	snapshot Snapshot
}

// Snapshot is the totals so far.
type Snapshot struct {
	// Outcomes counts jobs by how they ended.
	Outcomes map[Outcome]int64
	// Chunks, Embedded and Bytes are summed over indexed documents.
	Chunks, Embedded, Bytes int64
	// Phases is the time indexed documents spent in each phase, by name.
	Phases map[string]time.Duration
}

// NewStatistics starts every count at zero, with every outcome and phase
// present, so a graph of failures has a zero to rise from.
func NewStatistics() *Statistics {
	statistics := &Statistics{snapshot: Snapshot{
		Outcomes: map[Outcome]int64{Indexed: 0, Skipped: 0, Failed: 0, Retry: 0},
		Phases:   map[string]time.Duration{},
	}}
	for phase := range (Timings{}).Phases() {
		statistics.snapshot.Phases[phase] = 0
	}
	return statistics
}

// Observe records one finished job.
func (s *Statistics) Observe(result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Outcomes[result.Outcome]++
	if result.Outcome != Indexed {
		// A job that did not finish spent its time waiting on whatever
		// failed; adding it in would make a phase look slow when it is down.
		return
	}
	s.snapshot.Chunks += int64(result.Chunks)
	s.snapshot.Embedded += int64(result.Embedded)
	s.snapshot.Bytes += result.Bytes
	for phase, spent := range result.Timings.Phases() {
		s.snapshot.Phases[phase] += spent
	}
}

// Snapshot is a copy of the totals.
func (s *Statistics) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := Snapshot{
		Outcomes: make(map[Outcome]int64, len(s.snapshot.Outcomes)),
		Chunks:   s.snapshot.Chunks, Embedded: s.snapshot.Embedded, Bytes: s.snapshot.Bytes,
		Phases: make(map[string]time.Duration, len(s.snapshot.Phases)),
	}
	for outcome, count := range s.snapshot.Outcomes {
		copied.Outcomes[outcome] = count
	}
	for phase, spent := range s.snapshot.Phases {
		copied.Phases[phase] = spent
	}
	return copied
}

// Phases is the timings by name, without the total: the names the metrics
// and the benchmarks report under.
func (t Timings) Phases() map[string]time.Duration {
	return map[string]time.Duration{
		"load": t.Load, "fetch": t.Fetch, "parse": t.Parse, "chunk": t.Chunk,
		"reconcile": t.Reconcile, "embed": t.Embed, "vectors": t.Vectors,
		"write": t.Write, "cleanup": t.Cleanup,
	}
}
