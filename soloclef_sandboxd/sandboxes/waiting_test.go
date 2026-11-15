package sandboxes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waiting is a thread request that will wait up to a minute for a place.
func waiting(key, account string, max int) Request {
	request := thread(key, account, max)
	request.MaxWait = time.Minute
	return request
}

// acquired is one Acquire's answer, delivered on a channel.
type acquired struct {
	id  string
	err error
}

// acquireLater starts an Acquire and returns where its answer arrives.
func acquireLater(ctx context.Context, m *Manager, request Request) <-chan acquired {
	answer := make(chan acquired, 1)
	go func() {
		id, _, err := m.Acquire(ctx, request)
		answer <- acquired{id, err}
	}()
	return answer
}

// queued is how many requests of the account are waiting.
func queued(m *Manager, account string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.places[account]; p != nil {
		return len(p.queue)
	}
	return 0
}

// untilQueued waits until n of the account's requests are waiting, so the
// order a test starts them in is the order they queued in.
func untilQueued(t *testing.T, m *Manager, account string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for queued(m, account) != n {
		if time.Now().After(deadline) {
			t.Fatalf("%d requests waiting, want %d", queued(m, account), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// pending says whether an answer has not arrived yet.
func pending(answer <-chan acquired) bool {
	select {
	case got := <-answer:
		panic("answered early: " + got.id)
	default:
		return true
	}
}

// Requests that wait are given places in the order they asked, each the
// moment one of the account's sandboxes is removed, and a request that does
// not wait is refused at once while they queue.
func TestWaitersAreServedInOrderAsPlacesFree(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	second := acquireLater(ctx, m, waiting("2", "acct", 1))
	untilQueued(t, m, "acct", 1)
	third := acquireLater(ctx, m, waiting("3", "acct", 1))
	untilQueued(t, m, "acct", 2)

	if _, _, err := m.Acquire(ctx, thread("4", "acct", 1)); !errors.Is(err, ErrAccountFull) {
		t.Fatalf("a request that does not wait: %v, want ErrAccountFull at once", err)
	}
	if !pending(second) || !pending(third) {
		t.Fatal("unreachable")
	}

	if err := m.Release(ctx, Thread, "1"); err != nil {
		t.Fatal(err)
	}
	got := <-second
	if got.err != nil {
		t.Fatalf("the first waiter: %v", got.err)
	}
	if !pending(third) {
		t.Fatal("unreachable")
	}
	if err := m.Release(ctx, Thread, "2"); err != nil {
		t.Fatal(err)
	}
	if got := <-third; got.err != nil {
		t.Fatalf("the second waiter: %v", got.err)
	}
	if engine.count() != 1 || queued(m, "acct") != 0 {
		t.Errorf("%d containers and %d waiting; want 1 and 0", engine.count(), queued(m, "acct"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.places) != 0 {
		t.Errorf("the queue bookkeeping outlived its waiters: %+v", m.places)
	}
}

// A wait that runs out is ErrAccountFull, and leaves the queue.
func TestAWaitThatRunsOutIsRefused(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	request := thread("2", "acct", 1)
	request.MaxWait = 30 * time.Millisecond
	started := time.Now()
	_, _, err := m.Acquire(ctx, request)
	if !errors.Is(err, ErrAccountFull) {
		t.Fatalf("err = %v, want ErrAccountFull", err)
	}
	if waited := time.Since(started); waited < 30*time.Millisecond {
		t.Errorf("gave up after %s, before its wait", waited)
	}
	if queued(m, "acct") != 0 {
		t.Error("a request that gave up is still queued")
	}
}

// A caller that goes away leaves the queue, and the place it would have had
// goes to the one behind it.
func TestACallerThatLeavesGivesUpItsTurn(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	leaving, leave := context.WithCancel(ctx)
	first := acquireLater(leaving, m, waiting("2", "acct", 1))
	untilQueued(t, m, "acct", 1)
	second := acquireLater(ctx, m, waiting("3", "acct", 1))
	untilQueued(t, m, "acct", 2)
	leave()
	if got := <-first; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the caller that left: %v", got.err)
	}
	untilQueued(t, m, "acct", 1)
	if err := m.Release(ctx, Thread, "1"); err != nil {
		t.Fatal(err)
	}
	if got := <-second; got.err != nil {
		t.Fatalf("the one behind it: %v", got.err)
	}
}

// What a crashed caller left behind is freed by the sweep at its idle time
// to live, and that wakes the waiter like any removal.
func TestTheSweepFreesAPlaceForAWaiter(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	start := time.Now()
	var elapsed atomic.Int64
	m.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	answer := acquireLater(ctx, m, waiting("2", "acct", 1))
	untilQueued(t, m, "acct", 1)
	elapsed.Store(int64(11 * time.Minute))
	m.Sweep(ctx)
	if got := <-answer; got.err != nil {
		t.Fatalf("the waiter: %v", got.err)
	}
}

// Two waiters for one thread end with one sandbox, and the second place
// goes on to the next waiter rather than being kept by nobody.
func TestWaitersForOneThreadShareItsSandbox(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	ctx := context.Background()
	for _, key := range []string{"1", "2"} {
		if _, _, err := m.Acquire(ctx, thread(key, "acct", 2)); err != nil {
			t.Fatal(err)
		}
	}
	a := acquireLater(ctx, m, waiting("7", "acct", 2))
	untilQueued(t, m, "acct", 1)
	b := acquireLater(ctx, m, waiting("7", "acct", 2))
	untilQueued(t, m, "acct", 2)
	other := acquireLater(ctx, m, waiting("8", "acct", 2))
	untilQueued(t, m, "acct", 3)

	// Both places free at once: the two requests for thread 7 are promised
	// one each; the second finds the first's sandbox and hands its place on.
	for _, key := range []string{"1", "2"} {
		if err := m.Release(ctx, Thread, key); err != nil {
			t.Fatal(err)
		}
	}
	first, second, third := <-a, <-b, <-other
	if first.err != nil || second.err != nil || third.err != nil {
		t.Fatalf("errors: %v %v %v", first.err, second.err, third.err)
	}
	if first.id != second.id {
		t.Errorf("thread 7 got two sandboxes: %s and %s", first.id, second.id)
	}
	if third.id == first.id || engine.count() != 2 {
		t.Errorf("thread 8 got %s beside %s, %d containers; want its own, and 2", third.id, first.id, engine.count())
	}
}

// failsFirstStart is Docker whose first start fails.
type failsFirstStart struct {
	*fakeEngine
	started atomic.Bool
}

func (f *failsFirstStart) Start(ctx context.Context, id string) error {
	if f.started.CompareAndSwap(false, true) {
		return errors.New("no such image")
	}
	return f.fakeEngine.Start(ctx, id)
}

// A sandbox that failed to start frees its place for the next waiter.
func TestAFailedStartFreesAPlaceForAWaiter(t *testing.T) {
	engine := &failsFirstStart{fakeEngine: newFakeEngine()}
	engine.createDelay = 30 * time.Millisecond
	m := newTestManager(t, engine)
	ctx := context.Background()
	failing := acquireLater(ctx, m, thread("1", "acct", 1))
	// Queued behind the start that is still being tried, which holds the
	// place until it fails.
	time.Sleep(10 * time.Millisecond)
	answer := acquireLater(ctx, m, waiting("2", "acct", 1))
	untilQueued(t, m, "acct", 1)
	if got := <-failing; got.err == nil {
		t.Fatal("the failing start succeeded")
	}
	if got := <-answer; got.err != nil {
		t.Fatalf("the waiter: %v", got.err)
	}
}

func TestAWaitPastTheBoundIsRefused(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	for _, wait := range []time.Duration{-time.Second, MaximumWait + time.Second} {
		request := thread("1", "acct", 1)
		request.MaxWait = wait
		if _, _, err := m.Acquire(context.Background(), request); !errors.Is(err, ErrBadRequest) {
			t.Errorf("a wait of %s: %v", wait, err)
		}
	}
}

// An account may have at most MaximumWaiting requests waiting; the next is
// refused at once with ErrTooManyWaiting, which is an ErrAccountFull, and
// once a waiter leaves there is room again.
func TestTheQueueIsBounded(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	answers := make([]<-chan acquired, MaximumWaiting)
	for i := range answers {
		answers[i] = acquireLater(ctx, m, waiting(fmt.Sprint(100+i), "acct", 1))
		untilQueued(t, m, "acct", i+1)
	}
	_, _, err := m.Acquire(ctx, waiting("2", "acct", 1))
	if !errors.Is(err, ErrTooManyWaiting) || !errors.Is(err, ErrAccountFull) {
		t.Fatalf("one past the bound: %v, want ErrTooManyWaiting (an ErrAccountFull)", err)
	}
	if queued(m, "acct") != MaximumWaiting {
		t.Errorf("%d waiting after the refusal, want %d", queued(m, "acct"), MaximumWaiting)
	}
	// Another account's queue is its own.
	if _, _, err := m.Acquire(ctx, thread("3", "other", 1)); err != nil {
		t.Fatalf("another account: %v", err)
	}
	cancel()
	for _, answer := range answers {
		if got := <-answer; !errors.Is(got.err, context.Canceled) {
			t.Errorf("a waiter whose caller left: %v", got.err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.places) != 0 {
		t.Errorf("the queue bookkeeping outlived its waiters: %+v", m.places)
	}
}

// A head waiter whose own limit is lower than the account holds blocks the
// ones behind it, who have places free under theirs. When it leaves, the
// next one is served at once, not at the next removal.
func TestAWaiterLeavingTheHeadLetsTheNextIn(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	leaving, leave := context.WithCancel(ctx)
	head := acquireLater(leaving, m, waiting("2", "acct", 1))
	untilQueued(t, m, "acct", 1)
	// Its limit is 2 and the account holds 1, but it queues behind the head.
	next := acquireLater(ctx, m, waiting("3", "acct", 2))
	untilQueued(t, m, "acct", 2)
	leave()
	if got := <-head; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the head that left: %v", got.err)
	}
	select {
	case got := <-next:
		if got.err != nil {
			t.Fatalf("the next waiter: %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the next waiter was not served when the head left, with a place free")
	}
}

// A waiter promised a place at the very moment its caller left gives the
// place to the next waiter. The promise is made before await runs, so the
// two happen together whichever the select sees first.
func TestAPlacePromisedToAWaiterThatLeftGoesToTheNext(t *testing.T) {
	m := newTestManager(t, newFakeEngine())
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, thread("1", "acct", 1)); err != nil {
		t.Fatal(err)
	}
	request := waiting("2", "acct", 1)
	m.mu.Lock()
	first, err := m.enqueue(request)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := acquireLater(ctx, m, waiting("3", "acct", 1))
	untilQueued(t, m, "acct", 2)
	// The removal promises the freed place to the first waiter...
	if err := m.Release(ctx, Thread, "1"); err != nil {
		t.Fatal(err)
	}
	// ...whose caller has gone at the same instant.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.await(gone, request, first); !errors.Is(err, context.Canceled) {
		t.Fatalf("the waiter that left: %v", err)
	}
	select {
	case got := <-next:
		if got.err != nil {
			t.Fatalf("the next waiter: %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the place promised to the waiter that left was lost")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.places) != 0 {
		t.Errorf("a promise outlived its waiter: %+v", m.places["acct"])
	}
}

// Many waiters leaving while places free, under -race: no place is lost or
// promised twice. The account never holds more than its limit, every
// waiter that stayed is served (each releasing its sandbox at once, so the
// next can be), and afterwards nothing is promised or queued.
func TestWaitersLeavingWhilePlacesFreeLoseNoPlace(t *testing.T) {
	engine := newFakeEngine()
	m := newTestManager(t, engine)
	ctx := context.Background()
	const limit, waiters = 2, MaximumWaiting
	for _, key := range []string{"1", "2"} {
		if _, _, err := m.Acquire(ctx, thread(key, "acct", limit)); err != nil {
			t.Fatal(err)
		}
	}
	type outcome struct {
		key    string
		leaves bool
		err    error
	}
	answers := make(chan outcome, waiters)
	cancels := make([]context.CancelFunc, waiters)
	for i := range waiters {
		waiterCtx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		key, leaves := fmt.Sprint(100+i), i%2 == 0
		go func() {
			_, _, err := m.Acquire(waiterCtx, waiting(key, "acct", limit))
			if err == nil {
				m.mu.Lock()
				held := m.held("acct")
				m.mu.Unlock()
				if held > limit {
					t.Errorf("the account holds %d places, past its limit of %d", held, limit)
				}
				err = m.Release(ctx, Thread, key)
			}
			answers <- outcome{key, leaves, err}
		}()
	}
	untilQueued(t, m, "acct", waiters)

	// Half the callers hang up while the two sandboxes are released, so
	// promises and departures cross.
	var wg sync.WaitGroup
	for i := range waiters {
		if i%2 == 0 {
			wg.Add(1)
			go func() { defer wg.Done(); cancels[i]() }()
		}
	}
	for _, key := range []string{"1", "2"} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = m.Release(ctx, Thread, key) }()
	}
	wg.Wait()

	// A lost place stalls the waiters behind it, which the deadline
	// catches. One that left may still have been served first (it was
	// promised a place before it saw its caller go); either is right.
	deadline := time.After(10 * time.Second)
	for range waiters {
		select {
		case got := <-answers:
			if got.err != nil && !(got.leaves && errors.Is(got.err, context.Canceled)) {
				t.Errorf("waiter %s (leaves: %v): %v", got.key, got.leaves, got.err)
			}
		case <-deadline:
			t.Fatal("a waiter was never answered: a place was lost")
		}
	}
	for _, cancel := range cancels {
		cancel()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.places) != 0 {
		t.Errorf("a promise or a waiter outlived the run: %+v", m.places["acct"])
	}
	if engine.count() != 0 {
		t.Errorf("%d containers left", engine.count())
	}
}
