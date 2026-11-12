package checkpoint

import (
	"context"
	"strings"
	"sync"
)

// Memory is a Store that lives in the process. It is for tests and for runs
// whose state is not worth keeping past the process (a one-shot script). It
// applies the same Seq rule as the Postgres store, so a test that passes
// against it is testing the real concurrency contract.
type Memory struct {
	mu      sync.Mutex
	threads map[string]Checkpoint
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{threads: make(map[string]Checkpoint)}
}

func (m *Memory) Load(ctx context.Context, thread string) (Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.threads[thread]
	if !ok {
		return Checkpoint{}, ErrNotFound
	}
	// Hand out a copy of the bytes: the caller will decode them and may keep
	// the slice, and a store that shares its buffer is a store whose saved
	// state can be edited from outside.
	cp.State = append([]byte(nil), cp.State...)
	return cp, nil
}

func (m *Memory) Save(ctx context.Context, cp Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if cp.Seq != m.threads[cp.Thread].Seq+1 {
		return ErrConflict
	}
	cp.State = append([]byte(nil), cp.State...)
	m.threads[cp.Thread] = cp
	return nil
}

func (m *Memory) Delete(ctx context.Context, thread string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.threads, thread)
	return nil
}

func (m *Memory) DeleteTree(ctx context.Context, thread string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	nested := thread + "/"
	for name := range m.threads {
		if name == thread || strings.HasPrefix(name, nested) {
			delete(m.threads, name)
		}
	}
	return nil
}

// Len reports how many threads are stored. Tests use it to prove that a
// finished nested agent leaves no checkpoint behind.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.threads)
}
