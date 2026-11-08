package executor

import (
	"fmt"
	"log/slog"
)

// wornOut is when a warm process stops being worth reusing, judged between
// jobs. Celery replaces a prefork child after worker_max_tasks_per_child
// bodies for the reason that applies here too: whatever a body leaks, or a
// library holds per call, then costs one process rather than the worker's
// whole uptime.
func wornOut(cfg Config, jobs, pid int, log *slog.Logger) (string, bool) {
	if cfg.MaxJobsPerChild > 0 && jobs >= cfg.MaxJobsPerChild {
		return fmt.Sprintf("it has run %d jobs", jobs), true
	}
	if cfg.MaxChildRSS == 0 {
		return "", false
	}
	resident, err := residentBytes(pid)
	if err != nil {
		// Nothing is retired on a reading that could not be taken: a process
		// that is working is worth more than a guess about its memory.
		log.Warn("could not read a task process's resident memory", "pid", pid, "error", err)
		return "", false
	}
	if resident <= cfg.MaxChildRSS {
		return "", false
	}
	return fmt.Sprintf("it holds %d MiB, past the %d MiB a healthy one should",
		resident>>20, cfg.MaxChildRSS>>20), true
}
