package beat

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// store is where the schedule has got to: the last window each entry fired
// in, which is what makes a window missed while this process was down
// recoverable at all.
//
// Celery Beat keeps the same state in a shelve file beside the process
// (PersistentScheduler). On this host that file is inside the celery_beat
// container and survives nothing, so every restart of the Python beat starts
// the schedule at "now" and silently skips whatever was due while it was
// down. Keeping it in Redis is the one deliberate improvement here, and it is
// what makes the catch-up in beat.go real rather than decorative.
type store struct {
	rdb redis.UniversalClient
	key string
}

// load reads every entry's last window. An entry absent from it has never
// fired, which is not the same as being overdue.
func (s store) load(ctx context.Context) (map[string]time.Time, error) {
	raw, err := s.rdb.HGetAll(ctx, s.key).Result()
	if err != nil {
		return nil, fmt.Errorf("beat: reading %s: %w", s.key, err)
	}
	positions := make(map[string]time.Time, len(raw))
	for entry, value := range raw {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			// A field nothing can read is treated as never having fired,
			// which costs one skipped window rather than a beat that will not
			// start.
			continue
		}
		positions[entry] = time.Unix(seconds, 0).UTC()
	}
	return positions, nil
}

// record moves an entry on. It is written after the tick is published, so a
// crash between the two costs a repeated window rather than a lost one — the
// same at-least-once bargain the rest of this tier makes.
func (s store) record(ctx context.Context, entry string, window time.Time) error {
	if err := s.rdb.HSet(ctx, s.key, entry, window.UTC().Unix()).Err(); err != nil {
		return fmt.Errorf("beat: recording %s in %s: %w", entry, s.key, err)
	}
	return nil
}
