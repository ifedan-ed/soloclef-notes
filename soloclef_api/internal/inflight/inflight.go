// Package inflight counts the work a process has under way that outlives the
// request that started it -- a chat turn, a Studio artifact -- so that a
// stopping server can wait for that work to finish before it exits.
//
// # Why not a sync.WaitGroup
//
// The chat and the Studio runner each used one, and a WaitGroup answers only
// "wait until zero, for as long as that takes". A deploy needs two other
// answers, and a WaitGroup gives neither:
//
//   - "wait until zero or until the drain's bound passes", which is a select
//     between a channel and a timer, and a WaitGroup has no channel;
//   - "wait while new work may still begin". The server keeps serving while
//     it drains, so a turn can start after the wait has begun. A WaitGroup
//     forbids exactly that: an Add from zero while another goroutine is in
//     Wait is a misuse the race detector reports.
//
// A Counter is a count and a channel that is closed each time the count comes
// back to zero, so a wait is a receive that a select can bound, and work that
// begins mid-wait simply opens a new channel for the next wait. Nothing polls.
package inflight

import (
	"context"
	"sync"
)

// Counter is the number of pieces of work in flight. Its zero value is an idle
// counter, ready to use.
type Counter struct {
	mu sync.Mutex
	n  int
	// idle is closed when n comes back to zero. It is nil while n is zero:
	// Idle hands out the shared closed channel then.
	idle chan struct{}
}

// closed is the channel every idle Counter answers with.
var closed = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// Begin counts one more piece of work. Every Begin must be matched by an End.
func (c *Counter) Begin() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == 0 {
		c.idle = make(chan struct{})
	}
	c.n++
}

// End counts one piece of work as finished.
func (c *Counter) End() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == 0 {
		// A bug in the caller, and one that would otherwise surface as a
		// shutdown that never waits for anything: say so where it happens.
		panic("inflight: End without a matching Begin")
	}
	c.n--
	if c.n == 0 {
		close(c.idle)
		c.idle = nil
	}
}

// Idle is a channel that is closed once no work is in flight: already closed
// when there is none now.
func (c *Counter) Idle() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == 0 {
		return closed
	}
	return c.idle
}

// Wait blocks until no work is in flight.
func (c *Counter) Wait() { <-c.Idle() }

// Work is anything that can say when it has nothing in flight (a Counter, or
// a service that keeps one).
type Work interface {
	Idle() <-chan struct{}
}

// AllIdle waits until every piece of work is idle at the same moment, and
// reports true; or until ctx ends, and reports false.
//
// "At the same moment" is the point. Waiting for each in turn is not enough:
// while the second is being waited for, the first may have begun something
// new (a person sends a chat message during a deploy). So after each round
// every channel is read again without blocking, and only a round in which all
// of them are closed ends the wait. Each round blocks on a channel, never on
// a timer, so a busy server costs one wake-up per piece of work finished.
func AllIdle(ctx context.Context, works ...Work) bool {
	for {
		for _, work := range works {
			select {
			case <-work.Idle():
			case <-ctx.Done():
				return false
			}
		}
		if allClosed(works) {
			return true
		}
	}
}

func allClosed(works []Work) bool {
	for _, work := range works {
		select {
		case <-work.Idle():
		default:
			return false
		}
	}
	return true
}
