package beat

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Beat is the scheduler. It publishes the same tasks, on the same queues, at
// the same cadence as Celery Beat, and refuses to publish anything at all
// unless it holds the lease.
type Beat struct {
	cfg       Config
	entries   []compiledEntry
	lease     *lease
	fence     fence
	store     store
	publisher publisher
	log       *slog.Logger

	mu     sync.Mutex
	counts map[string]entryCounts
	held   bool
	caught uint64
}

type compiledEntry struct {
	Entry
	cron Cron
}

type entryCounts struct {
	Published uint64
	Fenced    uint64
	Failed    uint64
	LastTick  time.Time
}

// New refuses a schedule it could not run as written.
func New(rdb redis.UniversalClient, cfg Config) (*Beat, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	entries := make([]compiledEntry, 0, len(cfg.Entries))
	for _, entry := range cfg.Entries {
		cron, err := entry.Cron.Compile()
		if err != nil {
			return nil, err
		}
		entries = append(entries, compiledEntry{Entry: entry, cron: cron})
	}
	return &Beat{
		cfg:     cfg,
		entries: entries,
		lease: &lease{
			rdb:   rdb,
			key:   cfg.KeyPrefix + ":lease",
			ttl:   cfg.LeaseTTL,
			owner: cfg.Name,
		},
		fence:     fence{rdb: rdb, prefix: cfg.KeyPrefix, ttl: cfg.FenceTTL},
		store:     store{rdb: rdb, key: cfg.KeyPrefix + ":last-run"},
		publisher: newPublisher(rdb, cfg.Origin, cfg.ReplyTo),
		counts:    map[string]entryCounts{},
		log:       slog.Default().With("beat", cfg.Name),
	}, nil
}

// Run holds the lease and keeps the schedule until ctx is cancelled. Without
// the lease it waits: a second beat is not an error, it is the spare.
func (b *Beat) Run(ctx context.Context) error {
	announced := false
	for ctx.Err() == nil {
		held, err := b.lease.take(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			b.log.Error("asking for the schedule", "error", err)
			b.pause(ctx, b.cfg.LeaseRetry)
			continue
		}
		if !held {
			if !announced {
				b.log.Info("another beat holds the schedule; waiting", "lease", b.lease.key)
				announced = true
			}
			b.pause(ctx, b.cfg.LeaseRetry)
			continue
		}

		announced = false
		b.setHeld(true)
		b.log.Info("this beat holds the schedule", "entries", len(b.entries), "lease", b.lease.key)
		b.keep(ctx)
		b.setHeld(false)

		// The lease is released on the way out even when ctx is done, so a
		// planned restart costs no silence: the successor starts at once
		// instead of waiting out the TTL.
		if err := b.lease.release(context.WithoutCancel(ctx)); err != nil {
			b.log.Error("releasing the schedule", "error", err)
		}
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	return ctx.Err()
}

// keep ticks while the lease is this beat's, and returns as soon as it is not.
func (b *Beat) keep(ctx context.Context) {
	renewed := time.Now()
	for ctx.Err() == nil {
		if time.Since(renewed) >= b.cfg.LeaseRenew {
			kept, err := b.lease.renew(ctx)
			if err != nil {
				b.log.Error("renewing the schedule", "error", err)
			}
			if !kept {
				b.log.Warn("the schedule is no longer this beat's; standing down")
				return
			}
			renewed = time.Now()
		}
		if err := b.tick(ctx, b.cfg.Clock()); err != nil && ctx.Err() == nil {
			b.log.Error("a tick failed", "error", err)
		}
		b.pause(ctx, b.cfg.PollInterval)
	}
}

// tick publishes everything due at now, once.
//
// Due means what Celery's own scheduler means by it: the next firing after
// where the entry got to has arrived. So a window missed while this process
// was down is caught up **once**, not backfilled — the position moves to now,
// and the windows in between are gone, which is exactly what Celery Beat's
// PersistentScheduler does with a last_run_at from before a restart.
//
// An entry that has never fired is put at now and fires nothing, as Celery's
// Entry starts at its own default_now().
func (b *Beat) tick(ctx context.Context, now time.Time) error {
	at := now.UTC()
	positions, err := b.store.load(ctx)
	if err != nil {
		return err
	}

	var failures []error
	for _, entry := range b.entries {
		position, known := positions[entry.Name]
		if !known {
			if err := b.store.record(ctx, entry.Name, at); err != nil {
				failures = append(failures, err)
			}
			continue
		}

		window := entry.cron.Next(position)
		// A clock set back puts the next window in the future, so nothing
		// fires until the wall clock has caught up — which is Celery's
		// behaviour too, and the safe direction: a window run twice runs the
		// work twice.
		if window.IsZero() || window.After(at) {
			continue
		}
		late := at.Sub(window)
		if err := b.fire(ctx, entry, window, at); err != nil {
			failures = append(failures, err)
			continue
		}
		if late >= time.Minute {
			b.countCaughtUp()
			b.log.Warn("caught up a window missed while nothing was scheduling",
				"entry", entry.Name, "window", window, "late", late)
		}
	}
	return errors.Join(failures...)
}

// fire claims the window and publishes it, then moves the entry on.
func (b *Beat) fire(ctx context.Context, entry compiledEntry, window, now time.Time) error {
	mine, err := b.fence.claim(ctx, entry.Name, window)
	if err != nil {
		b.count(entry.Name, func(c *entryCounts) { c.Failed++ })
		return err
	}
	if !mine {
		// Another scheduler has this window. Its position still has to move,
		// or this beat would ask about the same window on every poll.
		b.count(entry.Name, func(c *entryCounts) { c.Fenced++ })
		b.log.Warn("another scheduler already fired this window",
			"entry", entry.Name, "window", window)
		return b.store.record(ctx, entry.Name, now)
	}

	id, err := b.publisher.publish(ctx, entry.Entry, now)
	if err != nil {
		b.count(entry.Name, func(c *entryCounts) { c.Failed++ })
		return err
	}
	b.count(entry.Name, func(c *entryCounts) {
		c.Published++
		c.LastTick = now
	})
	b.log.Info("published a scheduled task",
		"entry", entry.Name, "task", entry.Task, "queue", entry.Queue,
		"id", id, "window", window)
	return b.store.record(ctx, entry.Name, now)
}

func (b *Beat) pause(ctx context.Context, how time.Duration) {
	timer := time.NewTimer(how)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (b *Beat) setHeld(held bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = held
}

func (b *Beat) count(entry string, change func(*entryCounts)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	counts := b.counts[entry]
	change(&counts)
	b.counts[entry] = counts
}

func (b *Beat) countCaughtUp() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.caught++
}
