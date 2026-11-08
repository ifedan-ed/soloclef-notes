package executor

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Environment variables this executor reads, alongside internal/broker's. The
// command is the one that decides what runs at all, so it is never defaulted:
// a worker with no command runs nothing and says so at startup.
const (
	// EnvTaskCommand is the command, split on spaces. In a pool it is the warm
	// server (`python -m app.tasks.serve`); with no pool it is the one-shot
	// entrypoint (`python -m app.tasks.run`), which has the task name appended
	// per job.
	EnvTaskCommand = "SOLOCLEF_WORKER_TASK_COMMAND"

	// EnvTaskDir is where that command runs: /app in the backend image.
	EnvTaskDir = "SOLOCLEF_WORKER_TASK_DIR"

	// EnvPoolSize is how many warm task processes to keep. Unset is a process
	// per job, which pays this app's imports every time.
	EnvPoolSize = "SOLOCLEF_WORKER_TASK_POOL_SIZE"

	// EnvMaxJobsPerChild is how many bodies one warm process runs before it is
	// replaced, as Celery's worker_max_tasks_per_child does.
	EnvMaxJobsPerChild = "SOLOCLEF_WORKER_TASK_MAX_JOBS_PER_CHILD"

	// EnvMaxChildRSS is the resident memory, in MiB, past which a warm process
	// is replaced between jobs.
	EnvMaxChildRSS = "SOLOCLEF_WORKER_TASK_MAX_CHILD_RSS_MIB"

	// EnvChildReadyTimeout is how long a warm process has to import this app
	// and say it is ready.
	EnvChildReadyTimeout = "SOLOCLEF_WORKER_TASK_READY_TIMEOUT"

	// EnvTaskSoftTimeLimit and EnvTaskHardTimeLimit are the fallbacks for a
	// message carrying no timelimit header, which is every message this app
	// sends: they are task_soft_time_limit and task_time_limit.
	EnvTaskSoftTimeLimit = "SOLOCLEF_WORKER_TASK_SOFT_TIME_LIMIT"
	EnvTaskHardTimeLimit = "SOLOCLEF_WORKER_TASK_HARD_TIME_LIMIT"

	// EnvKillGrace is what a process group has between the SIGTERM that asks
	// it to stop and the SIGKILL that makes it.
	EnvKillGrace = "SOLOCLEF_WORKER_TASK_KILL_GRACE"

	// EnvStartFailureDelay is how long to wait before handing a message back
	// when no process could be started for it.
	EnvStartFailureDelay = "SOLOCLEF_WORKER_TASK_START_FAILURE_DELAY"
)

// LoadConfig reads the configuration from an environment. An unset variable
// leaves DefaultConfig's value; Worker is not read here, because a row must
// name the same worker the broker's claims do (broker.Config.Name).
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()

	cfg.Command = strings.Fields(getenv(EnvTaskCommand))
	cfg.Dir = strings.TrimSpace(getenv(EnvTaskDir))

	counts := []struct {
		name string
		into *int
	}{
		{EnvPoolSize, &cfg.PoolSize},
		{EnvMaxJobsPerChild, &cfg.MaxJobsPerChild},
	}
	for _, count := range counts {
		raw := strings.TrimSpace(getenv(count.name))
		if raw == "" {
			continue
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", count.name, err)
		}
		*count.into = parsed
	}

	if raw := strings.TrimSpace(getenv(EnvMaxChildRSS)); raw != "" {
		mib, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("%s: want a number of MiB: %w", EnvMaxChildRSS, err)
		}
		cfg.MaxChildRSS = mib << 20
	}

	durations := []struct {
		name string
		into *time.Duration
	}{
		{EnvChildReadyTimeout, &cfg.ChildReadyTimeout},
		{EnvTaskSoftTimeLimit, &cfg.SoftTimeLimit},
		{EnvTaskHardTimeLimit, &cfg.HardTimeLimit},
		{EnvKillGrace, &cfg.KillGrace},
		{EnvStartFailureDelay, &cfg.StartFailureDelay},
	}
	for _, d := range durations {
		raw := strings.TrimSpace(getenv(d.name))
		if raw == "" {
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", d.name, err)
		}
		*d.into = parsed
	}

	return cfg, nil
}
