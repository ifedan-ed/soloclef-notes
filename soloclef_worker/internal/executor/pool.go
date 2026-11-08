package executor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// pool keeps the task processes warm. Phase 3 measured 27-33s of this app's
// imports before every body started, which is noise against a podcast and the
// whole cost of a job on the fast queue, so the processes outlive the jobs
// they run: a job is handed to a process that has already imported everything.
//
// What the pool does not do is reuse a process it has any reason to doubt. A
// process that was stopped, that reported a job nobody was waiting for, that
// has run its share of bodies or that is holding more memory than a healthy
// one does is retired and replaced, which is the same bargain Celery's
// worker_max_tasks_per_child makes.
type pool struct {
	cfg Config
	log *slog.Logger

	// idle holds the processes ready for work. Its capacity is the pool size,
	// so a process can always be handed back.
	idle chan *warmChild

	// spawning is the replacements started in the background, so closing the
	// pool does not leave a half-imported Python process behind.
	spawning sync.WaitGroup

	mu     sync.Mutex
	live   int
	closed bool
}

// poolWaitRecheck is how often a job waiting for a warm process looks again
// at whether it could start one itself.
const poolWaitRecheck = time.Second

func newPool(cfg Config, log *slog.Logger) *pool {
	return &pool{
		cfg:  cfg,
		log:  log,
		idle: make(chan *warmChild, cfg.PoolSize),
	}
}

// start hands one job to a warm process. An error means nothing ran, which is
// what makes the message still work: the executor hands it back.
func (p *pool) start(ctx context.Context, task string, call Call, log *slog.Logger) (job, error) {
	// Twice, because a process that was idle may have died between being
	// handed back and being handed a job, and the next process is the answer
	// to that rather than failing the job.
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		var child *warmChild
		if child, err = p.acquire(ctx); err != nil {
			return nil, err
		}
		leased := child.take(call.ID, log)
		if handErr := child.hand(task, call); handErr != nil {
			child.endJob(leased)
			p.retire(child, "it would not take a job")
			err = handErr
			continue
		}
		leased.pool = p
		return leased, nil
	}
	return nil, err
}

// warm readies the processes before the first job arrives, so the imports are
// paid at startup rather than by whoever enqueues first.
func (p *pool) warm(ctx context.Context) error {
	var wait sync.WaitGroup
	errs := make([]error, p.cfg.PoolSize)
	for i := range p.cfg.PoolSize {
		if !p.claim() {
			break
		}
		wait.Add(1)
		go func(slot int) {
			defer wait.Done()
			child, err := p.spawn(ctx)
			if err != nil {
				p.release()
				errs[slot] = err
				return
			}
			p.handBack(child)
		}(i)
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// close stops every process the pool still owns. Without it a worker that has
// stopped consuming would leave warm Python processes behind.
func (p *pool) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	p.spawning.Wait()
	for {
		select {
		case child := <-p.idle:
			child.dismiss(p.cfg.KillGrace, "the worker is shutting down")
			p.release()
		default:
			return
		}
	}
}

// acquire finds a process to run a job in: one that is already warm, a new one
// while the pool has room, or the next one handed back.
func (p *pool) acquire(ctx context.Context) (*warmChild, error) {
	for {
		if child, taken := p.takeIdle(); taken {
			return child, nil
		}
		if p.claim() {
			child, err := p.spawn(ctx)
			if err != nil {
				p.release()
				return nil, err
			}
			return child, nil
		}
		// Every process is busy. Waiting is right — the message has been
		// taken and its claim is being renewed — but a wait that happens often
		// means the pool is smaller than the queue's concurrency.
		p.log.Info("waiting for a warm task process", "pool_size", p.cfg.PoolSize)
		select {
		case child := <-p.idle:
			if why, ok := child.usable(); ok {
				return child, nil
			} else {
				p.retire(child, why)
			}
		case <-time.After(poolWaitRecheck):
			// Never only on a process being handed back: a process can be
			// retired while nothing is able to replace it, and this job would
			// then be waiting for one that is not coming. Looping lets it
			// take the pool's free place itself, and fail for the real
			// reason — so the message goes back as work instead of sitting on
			// a claim until the worker stops.
		case <-ctx.Done():
			return nil, fmt.Errorf("executor: no warm task process became free: %w", ctx.Err())
		}
	}
}

func (p *pool) takeIdle() (*warmChild, bool) {
	for {
		select {
		case child := <-p.idle:
			if why, ok := child.usable(); ok {
				return child, true
			} else {
				p.retire(child, why)
			}
		default:
			return nil, false
		}
	}
}

// put takes a process back after a job. This is where a process is judged:
// between jobs, when its memory is its own and nothing is waiting on it.
func (p *pool) put(child *warmChild) {
	if why, ok := child.usable(); !ok {
		p.retire(child, why)
		return
	}
	if why, worn := wornOut(p.cfg, child.jobsRun(), child.pid(), p.log); worn {
		p.retire(child, why)
		return
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		p.retire(child, "the worker is shutting down")
		return
	}
	p.handBack(child)
}

func (p *pool) handBack(child *warmChild) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		child.dismiss(p.cfg.KillGrace, "the worker is shutting down")
		p.release()
		return
	}
	select {
	case p.idle <- child:
	default:
		// The pool has no room, which can only mean its size shrank under it.
		child.dismiss(p.cfg.KillGrace, "the pool had no room to keep it")
		p.release()
	}
}

// retire ends a process and starts its replacement, so the next job finds a
// warm one rather than paying the imports again.
func (p *pool) retire(child *warmChild, why string) {
	p.log.Info("retiring a warm task process",
		"pid", child.pid(), "jobs", child.jobsRun(), "reason", why)
	child.dismiss(p.cfg.KillGrace, why)
	p.release()
	p.replace()
}

// replace fills the pool back up in the background. Its own deadline, not a
// job's: the job that caused the retirement is finished, and the next one
// should not wait for this.
func (p *pool) replace() {
	if !p.claim() {
		return
	}
	p.spawning.Add(1)
	go func() {
		defer p.spawning.Done()
		ctx, done := context.WithTimeout(context.Background(), p.cfg.ChildReadyTimeout+time.Minute)
		defer done()
		child, err := p.spawn(ctx)
		if err != nil {
			p.log.Error("could not start a replacement task process", "error", err)
			p.release()
			return
		}
		p.handBack(child)
	}()
}

func (p *pool) spawn(ctx context.Context) (*warmChild, error) {
	started := time.Now()
	child, err := startWarmChild(p.cfg, p.log)
	if err != nil {
		return nil, err
	}
	ready, done := context.WithTimeout(ctx, p.cfg.ChildReadyTimeout)
	defer done()
	if err := child.awaitReady(ready); err != nil {
		child.cancel(p.cfg.KillGrace, "it never said it was ready")
		return nil, err
	}
	p.log.Info("a warm task process is ready",
		"pid", child.pid(), "imports", time.Since(started).Round(time.Millisecond))
	return child, nil
}

// claim takes one of the pool's places, which is what bounds how many
// processes exist at once.
func (p *pool) claim() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.live >= p.cfg.PoolSize {
		return false
	}
	p.live++
	return true
}

func (p *pool) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live > 0 {
		p.live--
	}
}
