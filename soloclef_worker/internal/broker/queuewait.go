package broker

import (
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

// MaxCredibleWait bounds a reported wait. A result row expires after 24h
// (result_expires in app/celery_app.py) and no job may run longer than 8h, so
// a wait beyond this is a clock that is not the producer's, not a slow queue.
const MaxCredibleWait = 48 * time.Hour

var (
	// ErrNoEnqueueStamp means the message carries no
	// surfsense.enqueued_at_ns header, so its wait is unknowable: the Redis
	// broker records nothing about when a message arrived.
	ErrNoEnqueueStamp = errors.New("broker: message has no enqueue stamp")

	// ErrClockNotShared means the stamp and this process's monotonic clock
	// did not come from the same boot, which happens when the producer runs
	// on another host. The wait is then unknown rather than wrong.
	ErrClockNotShared = errors.New("broker: enqueue stamp is not from this host's monotonic clock")
)

// Clock reads a monotonic clock, in the same terms as the producer's stamp.
type Clock func() (time.Duration, error)

// QueueWait is how long a message sat on the queue before this worker took it.
//
// The producer stamps surfsense.enqueued_at_ns with Python's
// time.monotonic_ns (app/celery_app.py, _stamp_enqueue_time), which on Linux
// is clock_gettime(CLOCK_MONOTONIC): nanoseconds since the host booted, shared
// by every container on that host and unrelated to any wall clock. So the wait
// is this process's own CLOCK_MONOTONIC minus the stamp — and the stamp cannot
// be compared with time.Now() at all.
func QueueWait(msg *celery.Message, clock Clock) (time.Duration, error) {
	if msg.EnqueuedAt.IsZero() {
		return 0, ErrNoEnqueueStamp
	}
	now, err := clock()
	if err != nil {
		return 0, fmt.Errorf("broker: reading the monotonic clock: %w", err)
	}
	wait := now - time.Duration(msg.EnqueuedAt.UnixNano())
	if wait < 0 || wait > MaxCredibleWait {
		return 0, fmt.Errorf("%w: stamp %dns against clock %dns", ErrClockNotShared,
			msg.EnqueuedAt.UnixNano(), now.Nanoseconds())
	}
	return wait, nil
}
