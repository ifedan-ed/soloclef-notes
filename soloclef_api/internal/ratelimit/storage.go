package ratelimit

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// maxBackendChecks is SlowAPI's MAX_BACKEND_CHECKS.
const maxBackendChecks = 5

// Counter adds a request to a window and reports the count after it.
type Counter interface {
	Hit(ctx context.Context, key string, window time.Duration, cost int64) (int64, error)
}

// SharedCounter is a counter both tiers use, which can therefore be found
// unreachable and later found well again.
type SharedCounter interface {
	Counter
	Reachable(ctx context.Context) error
}

// Storage is SlowAPI's pair of counters: the shared one, and the in-process one
// it falls back to while the shared one cannot be reached. The fall back and the
// recovery are mirrored because they decide whether a client's budget is one
// budget or one per process, and that is the whole point of sharing the keys.
type Storage struct {
	Shared   SharedCounter
	Fallback Counter
	Logger   *slog.Logger
	// Now is overridable for tests only.
	Now func() time.Time

	mutex           sync.Mutex
	dead            bool
	lastBackendAt   time.Time
	backendChecks   float64
	recoveredLogged bool
}

func (s *Storage) Hit(ctx context.Context, key string, window time.Duration, cost int64) (int64, error) {
	if s.deadNow() {
		if s.shouldCheckBackend() && s.Shared.Reachable(ctx) == nil {
			s.revive()
			s.Logger.Info("the shared rate limit counter is reachable again")
		} else {
			return s.Fallback.Hit(ctx, key, window, cost)
		}
	}

	count, err := s.Shared.Hit(ctx, key, window, cost)
	if err != nil {
		s.markDead()
		s.Logger.Warn("the shared rate limit counter is unreachable; counting in memory",
			slog.String("error", err.Error()))
		return s.Fallback.Hit(ctx, key, window, cost)
	}
	return count, nil
}

func (s *Storage) deadNow() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.dead
}

func (s *Storage) markDead() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.dead = true
}

func (s *Storage) revive() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.dead, s.backendChecks = false, 0
}

// shouldCheckBackend is SlowAPI's __should_check_backend: probe the shared
// counter at most once every 2^n seconds, with n climbing to five and then
// starting again, so a dead Redis is not hammered and a recovered one is noticed.
func (s *Storage) shouldCheckBackend() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.backendChecks > maxBackendChecks {
		s.backendChecks = 0
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if now().Sub(s.lastBackendAt).Seconds() > math.Pow(2, s.backendChecks) {
		s.lastBackendAt = now()
		s.backendChecks++
		return true
	}
	return false
}
