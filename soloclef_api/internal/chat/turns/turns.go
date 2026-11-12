// Package turns answers three questions about a chat thread from any process
// of the API tier: is a turn running on it, may this request start one, and
// has somebody pressed Stop.
//
// The Python chat kept all three in the memory of its one worker (a mutex, a
// task registry and a cancel flag), which is why Stop and "thread busy" only
// worked while there was exactly one process. Here the facts live in Valkey,
// so a Stop that reaches replica B ends a turn that replica A is running.
//
// A running turn holds a lease: a key that names its owner and expires by
// itself. The process running the turn renews it on a heartbeat; a process
// that dies stops renewing, and the thread frees itself after one lifetime
// with nobody having to notice. Stop is delivered twice over, on purpose:
//
//   - published on a channel every process listens to, which ends the turn
//     within a network round trip;
//   - written as a flag beside the lease, which the heartbeat reads. A
//     published message is lost if the listener's connection was down at
//     that instant; the flag is still there at the next heartbeat.
//
// The first is speed and the second is the guarantee.
package turns

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Standing is what a heartbeat learns about its own lease.
type Standing uint8

const (
	// Held: the lease is still this owner's and nobody asked it to stop.
	Held Standing = iota + 1
	// StopRequested: the lease is this owner's and Stop was pressed.
	StopRequested
	// Lost: the lease expired or belongs to another owner. The turn must
	// end: another process may already be answering on this thread.
	Lost
)

// Coordinator is the shared store. Valkey is the production one; Memory is
// for tests and holds the same contract (contract_test.go runs both).
type Coordinator interface {
	// Claim takes the thread's lease for owner if nobody holds it, and
	// clears any stop flag left by an earlier turn so that a Stop pressed
	// at the end of the last answer cannot end this one.
	Claim(ctx context.Context, thread, owner string, lifetime time.Duration) (bool, error)

	// Renew extends owner's lease and reports where it stands.
	Renew(ctx context.Context, thread, owner string, lifetime time.Duration) (Standing, error)

	// Release gives the lease up, only if owner still holds it: a turn that
	// overran its lease must not free the thread from under its successor.
	Release(ctx context.Context, thread, owner string) error

	// RequestStop asks the turn running on thread to stop and reports
	// whether there was one. With none running it does nothing, so a late
	// click cannot arm a stop for a turn that has not started.
	RequestStop(ctx context.Context, thread string) (running bool, err error)

	// Running reports whether a lease is held on thread.
	Running(ctx context.Context, thread string) (bool, error)

	// Listen calls deliver with the thread of every stop request, from any
	// process, until ctx ends. It blocks.
	Listen(ctx context.Context, deliver func(thread string)) error
}

// ErrBusy is returned by Begin when another turn holds the thread.
var ErrBusy = errors.New("turns: a turn is already running on this thread")

// ErrStopped is the cause a turn's context is cancelled with when a person
// pressed Stop, so the route can tell that apart from a client that went
// away, a deadline or a shutdown.
var ErrStopped = errors.New("turns: stopped by request")

// ErrLeaseLost is the cause when the lease could not be kept.
var ErrLeaseLost = errors.New("turns: the thread's lease was lost")

// Config is what a Registry needs. Every field is required: how long a dead
// process may block a thread, and how often a live one says it is alive, are
// decisions about a deployment and have no value that is right everywhere.
type Config struct {
	Store Coordinator

	// Lifetime is how long a lease lasts without renewal: the longest a
	// thread stays "busy" after the process running its turn has died.
	Lifetime time.Duration

	// Heartbeat is how often a running turn renews. It must be well under
	// Lifetime, or one slow round trip loses the lease of a healthy turn.
	Heartbeat time.Duration

	// NewOwner names a turn's hold on the lease; it must not repeat.
	NewOwner func() string
}

// Registry is one process's view: the turns this process is running, and the
// shared store behind them.
type Registry struct {
	cfg Config

	mu      sync.Mutex
	running map[string]*Turn
}

// New checks cfg and returns a registry.
func New(cfg Config) (*Registry, error) {
	switch {
	case cfg.Store == nil:
		return nil, errors.New("turns: Store is not set")
	case cfg.Lifetime <= 0:
		return nil, fmt.Errorf("turns: Lifetime must be positive, got %s", cfg.Lifetime)
	case cfg.Heartbeat <= 0 || cfg.Heartbeat*2 > cfg.Lifetime:
		return nil, fmt.Errorf("turns: Heartbeat must be positive and at most half of Lifetime (%s), got %s", cfg.Lifetime, cfg.Heartbeat)
	case cfg.NewOwner == nil:
		return nil, errors.New("turns: NewOwner is not set")
	}
	return &Registry{cfg: cfg, running: make(map[string]*Turn)}, nil
}

// Turn is one running turn's hold on its thread.
type Turn struct {
	registry *Registry
	thread   string
	owner    string
	cancel   context.CancelCauseFunc
	done     chan struct{}
	once     sync.Once
}

// Begin claims thread and returns a context that is cancelled, with a cause,
// when the turn must end: ErrStopped for a Stop from any process, ErrLeaseLost
// if the lease cannot be kept. parent's own cancellation passes through.
//
// The caller must call End exactly when the turn is over, whatever way it
// ended; until then the thread is busy for everyone.
func (r *Registry) Begin(parent context.Context, thread string) (context.Context, *Turn, error) {
	owner := r.cfg.NewOwner()
	claimed, err := r.cfg.Store.Claim(parent, thread, owner, r.cfg.Lifetime)
	if err != nil {
		return nil, nil, fmt.Errorf("turns: claim thread %s: %w", thread, err)
	}
	if !claimed {
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithCancelCause(parent)
	t := &Turn{registry: r, thread: thread, owner: owner, cancel: cancel, done: make(chan struct{})}
	r.mu.Lock()
	r.running[thread] = t
	r.mu.Unlock()
	go t.heartbeat(ctx)
	return ctx, t, nil
}

// heartbeat renews the lease until the turn ends, and is where a stop that
// the channel did not deliver is noticed.
func (t *Turn) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(t.registry.cfg.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		standing, err := t.registry.cfg.Store.Renew(ctx, t.thread, t.owner, t.registry.cfg.Lifetime)
		switch {
		case err != nil:
			// One failed round trip is not a lost lease: the key outlives
			// two heartbeats by construction. If the store stays away the
			// key expires and the next successful renewal reports Lost.
			continue
		case standing == StopRequested:
			t.cancel(ErrStopped)
			return
		case standing == Lost:
			t.cancel(ErrLeaseLost)
			return
		}
	}
}

// End releases the thread. It is safe to call more than once. The release
// runs on a context of its own because the turn's context is usually
// cancelled by the time a stopped turn gets here, and a lease that is not
// released keeps the thread busy for a whole Lifetime.
func (t *Turn) End() {
	t.once.Do(func() {
		close(t.done)
		t.cancel(nil)
		t.registry.mu.Lock()
		if t.registry.running[t.thread] == t {
			delete(t.registry.running, t.thread)
		}
		t.registry.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
		defer cancel()
		// A failed release costs one Lifetime of "busy" and nothing else;
		// the lease expires by itself.
		_ = t.registry.cfg.Store.Release(ctx, t.thread, t.owner)
	})
}

// releaseTimeout bounds the one store call End makes.
const releaseTimeout = 5 * time.Second

// Stop asks for the turn on thread to stop, wherever it runs, and reports
// whether one was running. A turn in this process is cancelled at once,
// without waiting for the message to come back round.
func (r *Registry) Stop(ctx context.Context, thread string) (bool, error) {
	running, err := r.cfg.Store.RequestStop(ctx, thread)
	if err != nil {
		return false, fmt.Errorf("turns: stop thread %s: %w", thread, err)
	}
	r.stopLocal(thread)
	return running, nil
}

// Running reports whether any process is running a turn on thread.
func (r *Registry) Running(ctx context.Context, thread string) (bool, error) {
	return r.cfg.Store.Running(ctx, thread)
}

// Listen delivers other processes' stop requests to this process's turns
// until ctx ends. Run it in a goroutine for the life of the server. An error
// means the subscription broke; the caller decides whether to call it again,
// and turns are still stopped by their heartbeats in the meantime.
func (r *Registry) Listen(ctx context.Context) error {
	return r.cfg.Store.Listen(ctx, r.stopLocal)
}

func (r *Registry) stopLocal(thread string) {
	r.mu.Lock()
	t := r.running[thread]
	r.mu.Unlock()
	if t != nil {
		t.cancel(ErrStopped)
	}
}
