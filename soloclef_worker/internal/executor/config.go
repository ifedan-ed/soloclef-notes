package executor

import (
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// Config is how this worker runs a task body. Nothing here is discovered at
// runtime: the command, the limits and the worker's own name are given, so a
// process that cannot run a body says so at startup rather than per message.
type Config struct {
	// Command runs task bodies. With PoolSize set it is the warm server,
	// which takes job after job on stdin and names no task of its own
	// (`python -m app.tasks.serve`); with no pool it is the one-shot
	// entrypoint, which has the task name appended per job
	// (`python -m app.tasks.run`). A command that does not match the mode
	// never says it is ready, and the worker says so rather than running
	// anything.
	Command []string

	// PoolSize is how many warm task processes may exist at once. Nothing
	// means a process per job, which is phase 3's shape: correct, simple to
	// debug, and 27-33s of this app's imports before every body starts. The
	// deployed worker sets this (custom/docs/go-worker.md).
	PoolSize int

	// MaxJobsPerChild retires a warm process after that many bodies, as
	// Celery's worker_max_tasks_per_child does for a prefork child: a leak in
	// a body, or a library that holds something per call, then costs one
	// process rather than the worker's whole uptime. Nothing means never.
	MaxJobsPerChild int

	// MaxChildRSS retires a warm process whose resident memory is past this
	// between jobs. A healthy child is roughly what a Celery prefork child is
	// on this host, so the ceiling catches a leak rather than a load. Nothing
	// means no ceiling. The figure that used to be quoted here included torch,
	// which this image no longer carries, so re-measure before trusting a
	// number rather than the shape.
	MaxChildRSS uint64

	// ChildReadyTimeout is how long a warm process has to import this app and
	// say it is ready for work.
	ChildReadyTimeout time.Duration

	// Dir is where the command runs; empty means this process's directory.
	Dir string

	// Env is the child's environment, or nil to inherit this process's.
	Env []string

	// Worker names this worker in the rows it writes, as Celery's hostname
	// does. broker.Config.Name is where it comes from.
	Worker string

	// SoftTimeLimit and HardTimeLimit are the fallbacks for a message that
	// carries no timelimit header of its own, which is every message this app
	// sends today: the limits come from app/celery_app.py, not the producer.
	SoftTimeLimit time.Duration
	HardTimeLimit time.Duration

	// KillGrace is how long a process group has between the SIGTERM that asks
	// it to stop and the SIGKILL that makes it.
	KillGrace time.Duration

	// StartFailureDelay is how long to wait before handing a message back
	// when the process could not be started at all, so a worker that cannot
	// run anything does not spin through the queue.
	StartFailureDelay time.Duration
}

// CeleryMaxJobsPerChild is worker_max_tasks_per_child in app/celery_app.py:
// how many bodies a prefork child runs before Celery replaces it. The same
// number here for the same reason.
const CeleryMaxJobsPerChild = 1000

// DefaultConfig is every limit Celery already enforces on these tasks, and
// nothing that names a command: a config with no command runs nothing.
func DefaultConfig() Config {
	return Config{
		SoftTimeLimit: broker.CelerySoftTimeLimit,
		HardTimeLimit: broker.CeleryHardTimeLimit,
		// Long enough for a task's own except/finally to release a billing
		// hold or mark a row failed, short enough that a shutdown ends.
		KillGrace:         20 * time.Second,
		StartFailureDelay: 5 * time.Second,

		// A process per job unless a pool is asked for: the one-shot shape is
		// the one that needs nothing of the backend but app/tasks/run.py.
		PoolSize:        0,
		MaxJobsPerChild: CeleryMaxJobsPerChild,
		MaxChildRSS:     2 << 30,
		// Two minutes for an import graph measured at 27-33s, so a host under
		// load does not lose a warm process it was about to get.
		ChildReadyTimeout: 2 * time.Minute,
	}
}

// Validate refuses a configuration that could not run a body, or could not
// say what happened to one.
func (c Config) Validate() error {
	if len(c.Command) == 0 {
		return errors.New("executor: no command to run a task body with")
	}
	if c.Worker == "" {
		return errors.New("executor: no worker name, so no row could say which worker ran the task")
	}
	if c.HardTimeLimit <= 0 {
		return errors.New("executor: hard time limit must be positive")
	}
	if c.SoftTimeLimit < 0 {
		return errors.New("executor: soft time limit must not be negative")
	}
	if c.SoftTimeLimit > 0 && c.SoftTimeLimit >= c.HardTimeLimit {
		return fmt.Errorf("executor: soft time limit %v leaves no room before the hard limit %v",
			c.SoftTimeLimit, c.HardTimeLimit)
	}
	if c.KillGrace <= 0 {
		return errors.New("executor: kill grace must be positive")
	}
	if c.StartFailureDelay < 0 {
		return errors.New("executor: start failure delay must not be negative")
	}
	if c.PoolSize < 0 {
		return fmt.Errorf("executor: pool size %d is not a number of processes", c.PoolSize)
	}
	if c.MaxJobsPerChild < 0 {
		return fmt.Errorf("executor: max jobs per child %d is not a number of jobs", c.MaxJobsPerChild)
	}
	if c.PoolSize > 0 && c.ChildReadyTimeout <= 0 {
		return errors.New("executor: a warm process needs a ready timeout, or a failed start would never be noticed")
	}
	return nil
}

// limits are the deadlines one message runs under. The message's own
// timelimit header wins, because a producer that bothered to set one meant it.
type limits struct {
	soft time.Duration
	hard time.Duration
}

func (c Config) limitsFor(msg *celery.Message) limits {
	lim := limits{soft: c.SoftTimeLimit, hard: c.HardTimeLimit}
	if msg.SoftTimeLimit > 0 {
		lim.soft = msg.SoftTimeLimit
	}
	if msg.HardTimeLimit > 0 {
		lim.hard = msg.HardTimeLimit
	}
	if lim.soft >= lim.hard {
		// A soft limit at or past the hard one could never fire first, and
		// pretending otherwise would hide which limit ended the job.
		lim.soft = 0
	}
	return lim
}
