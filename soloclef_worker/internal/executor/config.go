package executor

import (
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

// Config is how this worker runs a task body. Nothing here is discovered at
// runtime: the command, the limits and the worker's own name are given, so a
// process that cannot run a body says so at startup rather than per message.
type Config struct {
	// Command runs one task body, with the task name appended as its last
	// argument: `python -m app.tasks.run` in surfsense_backend.
	Command []string

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
