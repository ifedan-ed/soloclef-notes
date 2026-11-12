package turns

import (
	"context"
	"sync"
	"time"
)

// Memory is a Coordinator inside one process, for tests and for nothing
// else: a server with two replicas needs Valkey. It keeps the same contract,
// including leases that expire, on a clock the test can move.
type Memory struct {
	// Now is the clock leases expire on.
	Now func() time.Time

	mu        sync.Mutex
	leases    map[string]lease
	stops     map[string]bool
	listeners map[int]func(string)
	nextID    int
}

type lease struct {
	owner   string
	expires time.Time
}

// NewMemory returns an empty coordinator on the given clock.
func NewMemory(now func() time.Time) *Memory {
	return &Memory{Now: now, leases: map[string]lease{}, stops: map[string]bool{}, listeners: map[int]func(string){}}
}

// held returns the live lease on thread, dropping one that has expired.
func (m *Memory) held(thread string) (lease, bool) {
	l, ok := m.leases[thread]
	if ok && !m.Now().Before(l.expires) {
		delete(m.leases, thread)
		delete(m.stops, thread)
		return lease{}, false
	}
	return l, ok
}

func (m *Memory) Claim(_ context.Context, thread, owner string, lifetime time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.held(thread); taken {
		return false, nil
	}
	m.leases[thread] = lease{owner: owner, expires: m.Now().Add(lifetime)}
	delete(m.stops, thread)
	return true, nil
}

func (m *Memory) Renew(_ context.Context, thread, owner string, lifetime time.Duration) (Standing, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.held(thread)
	if !ok || l.owner != owner {
		return Lost, nil
	}
	m.leases[thread] = lease{owner: owner, expires: m.Now().Add(lifetime)}
	if m.stops[thread] {
		return StopRequested, nil
	}
	return Held, nil
}

func (m *Memory) Release(_ context.Context, thread, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.held(thread); ok && l.owner == owner {
		delete(m.leases, thread)
		delete(m.stops, thread)
	}
	return nil
}

func (m *Memory) RequestStop(_ context.Context, thread string) (bool, error) {
	m.mu.Lock()
	if _, running := m.held(thread); !running {
		m.mu.Unlock()
		return false, nil
	}
	m.stops[thread] = true
	listeners := make([]func(string), 0, len(m.listeners))
	for _, deliver := range m.listeners {
		listeners = append(listeners, deliver)
	}
	m.mu.Unlock()
	// Delivered outside the lock: a listener cancels a turn, and that turn's
	// End takes the lock to release.
	for _, deliver := range listeners {
		deliver(thread)
	}
	return true, nil
}

func (m *Memory) Running(_ context.Context, thread string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, running := m.held(thread)
	return running, nil
}

func (m *Memory) Listen(ctx context.Context, deliver func(string)) error {
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.listeners[id] = deliver
	m.mu.Unlock()
	<-ctx.Done()
	m.mu.Lock()
	delete(m.listeners, id)
	m.mu.Unlock()
	return nil
}
