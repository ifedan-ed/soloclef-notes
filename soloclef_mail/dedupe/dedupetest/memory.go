// Package dedupetest is the in-memory dedupe.Keys the tests of every package
// above dedupe use.
package dedupetest

import (
	"context"
	"sync"
	"time"
)

// Memory is dedupe.Keys in a map: the four commands, with a clock the test
// moves so the window can be stepped over without waiting a day. The Python
// unit tests have the same fake, for the same reason -- a fake that offers
// nothing else cannot hide a command the real store does not make.
//
// It is a package of its own rather than a _test.go file because enqueue,
// drain, recovery and service each need it, and Go does not share a test file
// across packages.
type Memory struct {
	mu       sync.Mutex
	now      time.Duration
	values   map[string]string
	expiries map[string]time.Duration
}

func NewMemory() *Memory {
	return &Memory{values: map[string]string{}, expiries: map[string]time.Duration{}}
}

// Advance moves the fake's clock on, which expires what the window has passed.
func (m *Memory) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now += d
}

func (m *Memory) evict(key string) {
	deadline, ok := m.expiries[key]
	if ok && m.now >= deadline {
		delete(m.values, key)
		delete(m.expiries, key)
	}
}

func (m *Memory) SetIfAbsent(_ context.Context, key, value string, ttl time.Duration) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evict(key)
	if held, ok := m.values[key]; ok {
		return held, nil
	}
	m.values[key] = value
	m.expiries[key] = m.now + ttl
	return "", nil
}

func (m *Memory) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	m.expiries[key] = m.now + ttl
	return nil
}

func (m *Memory) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evict(key)
	return m.values[key], nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	delete(m.expiries, key)
	return nil
}
