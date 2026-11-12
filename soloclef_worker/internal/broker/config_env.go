package broker

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Environment variables the consumer reads. Queues is the one that decides
// whether this process touches live work at all, so it is never defaulted.
const (
	EnvQueues             = "SOLOCLEF_WORKER_QUEUES"
	EnvQueueConcurrency   = "SOLOCLEF_WORKER_QUEUE_CONCURRENCY"
	EnvTaskConcurrency    = "SOLOCLEF_WORKER_TASK_CONCURRENCY"
	EnvTaskQueue          = "SOLOCLEF_WORKER_TASK_QUEUE"
	EnvVisibilityWindow   = "SOLOCLEF_WORKER_VISIBILITY_WINDOW"
	EnvRenewInterval      = "SOLOCLEF_WORKER_RENEW_INTERVAL"
	EnvRestoreInterval    = "SOLOCLEF_WORKER_RESTORE_INTERVAL"
	EnvPopTimeout         = "SOLOCLEF_WORKER_POP_TIMEOUT"
	EnvShutdownGrace      = "SOLOCLEF_WORKER_SHUTDOWN_GRACE"
	EnvDeferredRetryDelay = "SOLOCLEF_WORKER_DEFERRED_RETRY_DELAY"
	EnvName               = "SOLOCLEF_WORKER_NAME"
)

// LoadConfig reads the configuration from an environment. An unset variable
// leaves DefaultConfig's value, and an unset queue list leaves no queues, so
// a process started with no configuration consumes nothing.
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()

	cfg.Queues = splitList(getenv(EnvQueues))

	var err error
	if cfg.QueueConcurrency, err = parseLimits(getenv(EnvQueueConcurrency), EnvQueueConcurrency); err != nil {
		return Config{}, err
	}
	if cfg.TaskConcurrency, err = parseLimits(getenv(EnvTaskConcurrency), EnvTaskConcurrency); err != nil {
		return Config{}, err
	}
	if cfg.TaskQueue, err = parseRouting(getenv(EnvTaskQueue), EnvTaskQueue); err != nil {
		return Config{}, err
	}

	durations := []struct {
		name string
		into *time.Duration
	}{
		{EnvVisibilityWindow, &cfg.VisibilityWindow},
		{EnvRenewInterval, &cfg.RenewInterval},
		{EnvRestoreInterval, &cfg.RestoreInterval},
		{EnvPopTimeout, &cfg.PopTimeout},
		{EnvShutdownGrace, &cfg.ShutdownGrace},
		{EnvDeferredRetryDelay, &cfg.DeferredRetryDelay},
	}
	for _, d := range durations {
		raw := strings.TrimSpace(getenv(d.name))
		if raw == "" {
			continue
		}
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return Config{}, fmt.Errorf("%s: %w", d.name, parseErr)
		}
		*d.into = parsed
	}

	cfg.Name = strings.TrimSpace(getenv(EnvName))
	return cfg, nil
}

func splitList(raw string) []string {
	var items []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func parseLimits(raw, name string) (map[string]int, error) {
	limits := map[string]int{}
	for _, pair := range splitList(raw) {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("%s: %q is not name=limit", name, pair)
		}
		limit, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("%s: limit for %s: %w", name, strings.TrimSpace(key), err)
		}
		limits[strings.TrimSpace(key)] = limit
	}
	return limits, nil
}

// parseRouting reads "task=queue" pairs: which queue carries each limited
// class, mirroring task_routes.
func parseRouting(raw, name string) (map[string]string, error) {
	routing := map[string]string{}
	for _, pair := range splitList(raw) {
		task, queue, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("%s: %q is not task=queue", name, pair)
		}
		routing[strings.TrimSpace(task)] = strings.TrimSpace(queue)
	}
	return routing, nil
}
