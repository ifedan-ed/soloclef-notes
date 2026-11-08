package executor

import (
	"context"
	"log/slog"
	"time"
)

// job is one task body in flight, however it is being run: in a process
// started for it, or by a warm process from the pool. Everything above this
// seam — the soft limit, the hard limit, the process-group kill and the
// shutdown — is the same for both, so the two shapes cannot drift apart on
// what happens to a job that will not stop.
type job interface {
	// pid is the leader of the process group to signal. Signalling the group
	// rather than the process is what reaches an ffmpeg the body started.
	pid() int

	// done closes when there is nothing left to wait for: the body reported,
	// or the process running it died.
	done() <-chan struct{}

	// stop asks the process group to end, and makes it end once grace is up.
	stop(grace time.Duration)

	// conclude decides what happened. fallback is what the worker decided — a
	// limit or a shutdown — and stands in when the body never said anything
	// usable.
	conclude(fallback *outcome) outcome

	// release gives up the process the job was running in: reaped, retired,
	// or handed back warm for the next job.
	release()
}

// jobStarter is how bodies are run: one process per job, or a pool of warm
// processes. The executor holds one and does not care which.
type jobStarter interface {
	start(ctx context.Context, task string, call Call, log *slog.Logger) (job, error)

	// warm readies processes before the first job arrives, so the import cost
	// is paid at startup rather than by whoever enqueues first.
	warm(ctx context.Context) error

	// close stops every process this starter still owns.
	close()
}

// oneShot runs every body in a process of its own: phase 3's shape, kept
// because it is the simplest thing to debug with and the baseline the warm
// pool's saving is measured against.
type oneShot struct{ cfg Config }

func (o oneShot) start(_ context.Context, task string, call Call, log *slog.Logger) (job, error) {
	process, err := startChild(o.cfg, task, call, log)
	if err != nil {
		return nil, err
	}
	return process, nil
}

func (oneShot) warm(context.Context) error { return nil }

func (oneShot) close() {}
