package beat

import (
	"fmt"
	"os"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// Environment variables the scheduler reads.
//
// The first is the one that decides whether this process schedules anything at
// all, and it is never defaulted on: two schedulers run every scheduled task
// twice, so a worker nobody asked to beat does not beat.
//
// The next two are **Celery's own**, not this worker's. The schedule is built
// from the same settings app/celery_app.py builds it from, so the Go beat and
// the Python beat cannot disagree about the cadence of the connector checker
// or about which queue a tick goes to.
const (
	EnvEnabled         = "SOLOCLEF_WORKER_BEAT"
	EnvDefaultQueue    = "CELERY_TASK_DEFAULT_QUEUE"
	EnvCheckerInterval = "SCHEDULE_CHECKER_INTERVAL"

	EnvKeyPrefix    = "SOLOCLEF_WORKER_BEAT_KEY_PREFIX"
	EnvLeaseTTL     = "SOLOCLEF_WORKER_BEAT_LEASE_TTL"
	EnvLeaseRenew   = "SOLOCLEF_WORKER_BEAT_LEASE_RENEW"
	EnvLeaseRetry   = "SOLOCLEF_WORKER_BEAT_LEASE_RETRY"
	EnvFenceTTL     = "SOLOCLEF_WORKER_BEAT_FENCE_TTL"
	EnvPollInterval = "SOLOCLEF_WORKER_BEAT_POLL_INTERVAL"
	EnvName         = "SOLOCLEF_WORKER_BEAT_NAME"
)

// Defaults app/config.py gives the two Celery settings, repeated here so an
// unset variable means the same thing on both sides.
const (
	defaultQueue           = "surfsense"
	defaultCheckerInterval = "2m"
)

// Enabled is whether this process should schedule. Anything but an explicit
// yes is no.
func Enabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvEnabled))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// LoadConfig reads the configuration from an environment. The schedule itself
// is not configurable: it is app/celery_app.py's, built from the two settings
// above.
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()

	queue := or(getenv(EnvDefaultQueue), defaultQueue)
	entries, err := SoloclefSchedule(queue, or(getenv(EnvCheckerInterval), defaultCheckerInterval))
	if err != nil {
		return Config{}, err
	}
	cfg.Entries = entries

	if prefix := strings.TrimSpace(getenv(EnvKeyPrefix)); prefix != "" {
		cfg.KeyPrefix = prefix
	}
	durations := []struct {
		name string
		into *time.Duration
	}{
		{EnvLeaseTTL, &cfg.LeaseTTL},
		{EnvLeaseRenew, &cfg.LeaseRenew},
		{EnvLeaseRetry, &cfg.LeaseRetry},
		{EnvFenceTTL, &cfg.FenceTTL},
		{EnvPollInterval, &cfg.PollInterval},
	}
	for _, duration := range durations {
		raw := strings.TrimSpace(getenv(duration.name))
		if raw == "" {
			continue
		}
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return Config{}, fmt.Errorf("%s: %w", duration.name, parseErr)
		}
		*duration.into = parsed
	}

	cfg.Name = or(strings.TrimSpace(getenv(EnvName)), "go-beat@"+hostname())
	cfg.Origin = produce.Origin()
	replyTo, err := produce.NewID()
	if err != nil {
		return Config{}, err
	}
	cfg.ReplyTo = replyTo
	return cfg, nil
}

func or(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

// hostname is what Celery names a publisher after, and is unique per replica
// without needing configuration.
func hostname() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}
