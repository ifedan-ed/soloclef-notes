// Package beat is the scheduler half of the worker tier: the Celery Beat
// replacement. Celery Beat is the one part of the tier that *publishes*, so it
// is the one part where "only the consumer moves" does not apply — which is
// why everything it writes is compared against what Celery's own client
// writes (internal/celery/publish_test.go) and why a Python Celery worker can
// still consume every tick.
//
// Two things about beat are different in kind from consuming. A tick that
// fires twice runs the work twice, and Celery Beat holds no lock, so the
// locking is this package's own (lease.go, fence.go). And a beat bug is
// invisible until it has run for a day, so where the schedule has got to is
// kept in Redis rather than in memory (store.go) and the clock is injectable,
// so a test can be at any minute of any day.
package beat

import (
	"errors"
	"fmt"
	"time"
)

// Config is everything the scheduler needs. The schedule itself is Entries,
// which defaults to none: a beat that was not given a schedule publishes
// nothing, the same safe default the consumer has for queues.
type Config struct {
	Entries []Entry

	// KeyPrefix namespaces the lease, the fences and the schedule state. Two
	// beats sharing a prefix are two beats sharing one schedule, which is the
	// point: the lease is what stops them both running it.
	KeyPrefix string

	// LeaseTTL is how long the lease outlives the process holding it. It is
	// how long a killed beat keeps the schedule to itself, so it bounds how
	// late a successor's first tick is — and a missed window is caught up,
	// so the cost of a long TTL is lateness rather than a lost run.
	LeaseTTL time.Duration

	// LeaseRenew is how often the holder extends it. It must be comfortably
	// shorter than LeaseTTL, or a slow Redis round trip loses the schedule.
	LeaseRenew time.Duration

	// LeaseRetry is how often a beat without the lease asks for it again.
	LeaseRetry time.Duration

	// FenceTTL is how long the record of a fired window is kept. It has only
	// to outlive the gap between two schedulers reaching the same window —
	// seconds — because a window already behind the recorded schedule
	// position is never revisited.
	FenceTTL time.Duration

	// PollInterval is how often the loop asks whether anything is due, and so
	// how late a tick can be. A crontab's resolution is the minute, so
	// anything well under a minute fires on time.
	PollInterval time.Duration

	// Name identifies this beat in the lease and in its logs.
	Name string

	// Origin is what the published envelope says published it, in the shape
	// Celery writes: gen<pid>@<hostname>.
	Origin string

	// ReplyTo is the publisher's oid, which the RPC result backend would
	// reply to. This app's backend is Redis, so it is written and never read.
	ReplyTo string

	// Clock is the wall clock the schedule is read against, in UTC —
	// app/celery_app.py sets enable_utc=True and timezone="UTC", so there is
	// no local time to be moved by a daylight-saving change.
	Clock func() time.Time
}

// ErrNoSchedule is the safe default made loud: a beat with no entries
// publishes nothing.
var ErrNoSchedule = errors.New("beat: no schedule configured")

// DefaultConfig is every setting but the schedule, which defaults to none.
func DefaultConfig() Config {
	return Config{
		KeyPrefix: "soloclef-beat",
		// Ninety seconds of TTL renewed every thirty: three renewals may
		// fail before the schedule moves on, and a killed beat's successor
		// starts within a minute and a half.
		LeaseTTL:     90 * time.Second,
		LeaseRenew:   30 * time.Second,
		LeaseRetry:   5 * time.Second,
		FenceTTL:     time.Hour,
		PollInterval: time.Second,
		Clock:        time.Now,
	}
}

// Validate refuses a schedule that could not be run as written.
func (c Config) Validate() error {
	if len(c.Entries) == 0 {
		return ErrNoSchedule
	}
	seen := map[string]bool{}
	for _, entry := range c.Entries {
		switch {
		case entry.Name == "":
			return errors.New("beat: an entry with no name has no fence either")
		case entry.Task == "":
			return fmt.Errorf("beat: entry %s publishes no task", entry.Name)
		case entry.Queue == "":
			return fmt.Errorf("beat: entry %s has no queue to publish to", entry.Name)
		case entry.Expires <= 0:
			return fmt.Errorf("beat: entry %s has no expiry", entry.Name)
		case seen[entry.Name]:
			return fmt.Errorf("beat: two entries named %s", entry.Name)
		}
		seen[entry.Name] = true
		if _, err := entry.Cron.Compile(); err != nil {
			return fmt.Errorf("beat: entry %s: %w", entry.Name, err)
		}
	}
	if c.KeyPrefix == "" {
		return errors.New("beat: no key prefix, so the lease has no name")
	}
	if c.LeaseTTL <= 0 || c.LeaseRenew <= 0 || c.LeaseRenew >= c.LeaseTTL {
		return fmt.Errorf("beat: renewing every %v does not keep a %v lease", c.LeaseRenew, c.LeaseTTL)
	}
	if c.LeaseRetry <= 0 {
		return errors.New("beat: lease retry must be positive")
	}
	if c.FenceTTL <= 0 {
		return errors.New("beat: fence ttl must be positive")
	}
	if c.PollInterval <= 0 || c.PollInterval > time.Minute {
		return fmt.Errorf("beat: polling every %v cannot keep a schedule whose resolution is the minute", c.PollInterval)
	}
	if c.Clock == nil {
		return errors.New("beat: no clock")
	}
	// Both are written into every envelope and read by nothing, and both are
	// required for that reason: an envelope with an empty origin or reply_to
	// is not the envelope Celery's client writes, and the comparison that
	// proves this scheduler right is a comparison of bytes.
	if c.Origin == "" {
		return errors.New("beat: no origin to publish under")
	}
	if c.ReplyTo == "" {
		return errors.New("beat: no reply_to")
	}
	return nil
}
