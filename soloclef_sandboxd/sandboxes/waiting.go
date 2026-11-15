package sandboxes

import (
	"context"
	"fmt"
	"time"
)

// # Waiting for a place in the account's limit
//
// A request may say how long it is willing to wait (Request.MaxWait) when
// its account already has as many sandboxes as it may. A Studio output or an
// automation's step says so, because nobody is watching it and a place
// frees itself soon: another output finishing releases its sandbox, and an
// idle one expires at its time to live. A chat turn says nothing and is
// refused at once (ErrAccountFull), because a person is watching it.
//
// The waiting is done here, in the one service that knows the moment a
// place frees, and not by the clients asking again: a waiting request is a
// parked goroutine and an open connection, woken by the removal that frees
// the place, with no timer that polls. Waiters are served in the order they
// asked, per account. A place freed while somebody waits is promised to the
// first of them under the registry's lock, so a request that arrives a
// moment later cannot take it first.
//
// Nothing here outlives a process. A caller that goes away (its context
// ends, or its connection closes) leaves the queue at once; if this service
// restarts, every waiting call fails and its caller decides what to do.
// What a crashed caller still holds is a sandbox, which the sweep removes at
// its idle time to live.

// MaximumWait is the longest a request may ask to wait for a place. It is a
// bound on what a caller may ask, not a value anyone is given: a request
// waiting longer than this is one whose caller has a bug, and a connection
// held open for an hour is one nobody is reading.
const MaximumWait = 30 * time.Minute

// MaximumWaiting is how many requests one account may have waiting at once.
// Each waiter is a parked goroutine and an open connection, so without a
// bound one account (or a caller with a retry bug) could pin as many of
// both as it liked. Only work nobody watches waits, and how much of it
// runs at once is set by the worker's and the API's job concurrency
// (single figures in docker/.env.go.example); 32 is several times what one
// account can have in flight, so only a runaway caller reaches it.
const MaximumWaiting = 32

// ErrTooManyWaiting is a request refused because its account already has
// MaximumWaiting requests waiting. It is an ErrAccountFull (the account has
// no place, and none is promised soon), so callers that act on that need
// nothing new; the message says which of the two it was.
var ErrTooManyWaiting = fmt.Errorf("%w, and %d requests are already waiting for a place", ErrAccountFull, MaximumWaiting)

// waiter is one request waiting for a place in its account's limit.
type waiter struct {
	// max is the limit this request counts against: requests carry their
	// own, so the place a waiter needs is judged against its own number.
	max int
	// granted is closed when a place is promised to this waiter, and handed
	// says so for a waiter that also gave up at that instant.
	granted chan struct{}
	handed  bool
}

// places is one account's waiters, oldest first, and how many places have
// been promised to waiters that have not yet taken them up. A promised
// place counts against the limit exactly like a sandbox, so nobody else can
// take it in between.
type places struct {
	queue    []*waiter
	promised int
}

// held is how many places an account holds: its thread sandboxes, those
// still starting included, and the places promised to its waiters. m.mu is
// held.
func (m *Manager) held(account string) int {
	held := m.threadsOf(account)
	if p := m.places[account]; p != nil {
		held += p.promised
	}
	return held
}

// mustWait says whether a request for a new sandbox cannot be given one
// now. A request that waits also queues behind anyone already waiting, so
// that order holds even at the instant a place frees. m.mu is held.
func (m *Manager) mustWait(request Request) bool {
	if m.held(request.Account) >= request.MaxPerAccount {
		return true
	}
	p := m.places[request.Account]
	return request.MaxWait > 0 && p != nil && len(p.queue) > 0
}

// enqueue puts a request at the back of its account's queue, or refuses it
// with ErrTooManyWaiting when the queue is at MaximumWaiting. m.mu is held.
func (m *Manager) enqueue(request Request) (*waiter, error) {
	p := m.places[request.Account]
	if p == nil {
		p = &places{}
		m.places[request.Account] = p
	}
	if len(p.queue) >= MaximumWaiting {
		return nil, ErrTooManyWaiting
	}
	w := &waiter{max: request.MaxPerAccount, granted: make(chan struct{})}
	p.queue = append(p.queue, w)
	return w, nil
}

// handOut promises every place the account has free to its waiters, oldest
// first. It is called whenever one of the account's sandboxes leaves the
// registry, and when a waiter gives a promised place back. m.mu is held.
func (m *Manager) handOut(account string) {
	p := m.places[account]
	for p != nil && len(p.queue) > 0 {
		head := p.queue[0]
		if m.held(account) >= head.max {
			break
		}
		p.queue = p.queue[1:]
		p.promised++
		head.handed = true
		close(head.granted)
	}
	m.tidy(account)
}

// takeUp turns a promised place into the sandbox about to be reserved: the
// caller inserts its registry entry under the same lock. m.mu is held.
func (m *Manager) takeUp(account string) {
	if p := m.places[account]; p != nil && p.promised > 0 {
		p.promised--
	}
	m.tidy(account)
}

// leave takes a waiter that gave up out of its queue. One that was promised
// a place at the same instant gives the place back. Either way the queue is
// then handed out again: the waiter leaving may have been the head that
// the others queued behind (a head whose own limit was lower than theirs
// blocks them while the account has places they could take), so a place
// that is free now must go to the next waiter rather than wait for the
// next removal. m.mu is held.
func (m *Manager) leave(account string, w *waiter) {
	if w.handed {
		m.takeUp(account)
	} else if p := m.places[account]; p != nil {
		for i, queued := range p.queue {
			if queued == w {
				p.queue = append(p.queue[:i], p.queue[i+1:]...)
				break
			}
		}
	}
	m.handOut(account)
}

// tidy forgets an account with nobody waiting and nothing promised, so the
// map holds only accounts that are waiting now. m.mu is held.
func (m *Manager) tidy(account string) {
	if p := m.places[account]; p != nil && len(p.queue) == 0 && p.promised == 0 {
		delete(m.places, account)
	}
}

// await parks until the waiter is promised a place, the request's wait runs
// out or its caller goes away. It returns with m.mu held and the place still
// promised (the caller takes it up), or with m.mu released and the reason
// it gave up: ErrAccountFull for a wait that ran out, the context's error for
// a caller that left. m.mu is not held on entry.
func (m *Manager) await(ctx context.Context, request Request, w *waiter) error {
	started := m.now()
	timer := time.NewTimer(request.MaxWait)
	defer timer.Stop()
	select {
	case <-w.granted:
	case <-timer.C:
	case <-ctx.Done():
	}
	m.mu.Lock()
	waited := m.now().Sub(started)
	if w.handed && ctx.Err() == nil {
		// Promised a place, even if the wait ran out at the same instant:
		// it is taken rather than handed on, since it is already this
		// request's.
		m.log.Info("sandbox: a request waited for a place in its account's limit",
			"account", request.Account, "waited_ms", waited.Milliseconds())
		return nil
	}
	m.leave(request.Account, w)
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	m.log.Info("sandbox: a request gave up waiting for a place in its account's limit",
		"account", request.Account, "waited_ms", waited.Milliseconds())
	return fmt.Errorf("%w (waited %s)", ErrAccountFull, request.MaxWait)
}
