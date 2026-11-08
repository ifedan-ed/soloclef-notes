package metrics

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// ConsumerSource is the consumer, as this package needs it.
type ConsumerSource interface {
	Snapshot(ctx context.Context) (broker.Snapshot, error)
}

// Consumer exposes a consumer's snapshot. Depth and wait come first because
// they are the two numbers the tier was built to be able to say: a queue with
// work on it and nobody taking it, and how long a message sat before it was.
func Consumer(source ConsumerSource) Collector {
	return collectorFunc(func(ctx context.Context, into *Writer) error {
		snapshot, err := source.Snapshot(ctx)
		if err != nil {
			return err
		}

		into.Declare("queue_depth", Gauge, "Messages waiting on a consumed queue, across its priority lists.")
		for queue, depth := range snapshot.QueueDepth {
			into.Sample("queue_depth", float64(depth), "queue", queue)
		}

		into.Declare("jobs_in_flight", Gauge, "Jobs running now, by queue.")
		for queue, count := range snapshot.InFlightByQueue {
			into.Sample("jobs_in_flight", float64(count), "queue", queue)
		}

		into.Declare("task_jobs_in_flight", Gauge, "Jobs running now, by task name: whether one class is taking a queue.")
		for task, count := range snapshot.InFlightByTask {
			into.Sample("task_jobs_in_flight", float64(count), "task", task)
		}

		into.Declare("held_claims", Gauge, "Messages this worker holds unacked: the jobs running plus anything waiting for its eta.")
		into.Sample("held_claims", float64(snapshot.HeldClaims))

		into.Declare("waiting_for_eta", Gauge, "Messages taken off a queue that are not due yet.")
		into.Sample("waiting_for_eta", float64(snapshot.WaitingForETA))

		// The wait is a sum and a count rather than a histogram: a mean is
		// what these two give, and the buckets a histogram would need cannot
		// be chosen before the distribution is known.
		into.Declare("queue_wait_seconds_total", Counter, "Total time messages sat on a queue before this worker took them, by task.")
		into.Declare("queue_wait_measurements_total", Counter, "Messages whose queue wait was measured, by task: the denominator of the mean.")
		into.Declare("queue_wait_seconds_max", Gauge, "The longest queue wait seen since this worker started, by task.")
		for task, wait := range snapshot.Wait {
			into.Sample("queue_wait_seconds_total", wait.Total.Seconds(), "task", task)
			into.Sample("queue_wait_measurements_total", float64(wait.Count), "task", task)
			into.Sample("queue_wait_seconds_max", wait.Max.Seconds(), "task", task)
		}

		counters := []struct {
			name  string
			help  string
			value uint64
		}{
			{"messages_taken_total", "Messages taken off a queue.", snapshot.Taken},
			{"messages_deferred_total", "Messages put back because their task class was full.", snapshot.Deferred},
			{"messages_expired_total", "Messages dropped unrun, past their expires.", snapshot.Expired},
			{"messages_unreadable_total", "Messages dropped unrun because they are not an envelope this worker can read.", snapshot.Unreadable},
			{"messages_requeued_total", "Messages put back on their queue for any reason.", snapshot.Requeued},
			{"messages_restored_total", "Messages made visible again after a claim went stale.", snapshot.Restored},
			{"queue_wait_unknown_total", "Messages whose queue wait could not be measured, so an absent measurement is never read as a fast queue.", snapshot.WaitUnknown},
		}
		for _, counter := range counters {
			into.Declare(counter.name, Counter, counter.help)
			into.Sample(counter.name, float64(counter.value))
		}
		return nil
	})
}
