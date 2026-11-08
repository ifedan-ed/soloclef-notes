package metrics

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/beat"
)

// BeatSource is the scheduler, as this package needs it.
type BeatSource interface {
	Snapshot() beat.Snapshot
}

// Beat exposes the scheduler. The first metric is the one that matters most:
// exactly one process should report holding the lease, and a tier where none
// does is a schedule nobody is running — which is the failure Celery Beat
// reported only by being quiet.
func Beat(source BeatSource) Collector {
	return collectorFunc(func(_ context.Context, into *Writer) error {
		snapshot := source.Snapshot()

		into.Declare("beat_lease_held", Gauge,
			"1 on the process holding the schedule. Across the tier this should sum to exactly 1.")
		into.Sample("beat_lease_held", boolean(snapshot.LeaseHeld))

		into.Declare("beat_ticks_published_total", Counter, "Scheduled tasks published, by beat_schedule entry.")
		into.Declare("beat_ticks_fenced_total", Counter, "Windows another scheduler had already fired, by entry: this should stay at zero.")
		into.Declare("beat_ticks_failed_total", Counter, "Windows that could not be published, by entry.")
		into.Declare("beat_last_tick_timestamp_seconds", Gauge, "When this process last published each entry, as a unix timestamp.")
		for name, entry := range snapshot.Entries {
			into.Sample("beat_ticks_published_total", float64(entry.Published), "entry", name)
			into.Sample("beat_ticks_fenced_total", float64(entry.Fenced), "entry", name)
			into.Sample("beat_ticks_failed_total", float64(entry.Failed), "entry", name)
			if !entry.LastTick.IsZero() {
				into.Sample("beat_last_tick_timestamp_seconds", float64(entry.LastTick.Unix()), "entry", name)
			}
		}

		into.Declare("beat_windows_caught_up_total", Counter,
			"Windows fired a minute or more late: ones missed while nothing was scheduling.")
		into.Sample("beat_windows_caught_up_total", float64(snapshot.CaughtUp))
		return nil
	})
}

func boolean(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
