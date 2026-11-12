package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
)

// The two kinds of step. They are the values of checkpoint.Checkpoint.Node
// and of event.Event.Node for an agent's thread.
const (
	nodeModel = "model"
	nodeTools = "tools"
)

// Outcome says how a run ended when it did not end in an error.
type Outcome uint8

const (
	// Finished means the model answered with no tool call.
	Finished Outcome = iota + 1

	// Paused means the turn is waiting for decisions; Pending lists them and
	// Resume continues it.
	Paused
)

// Result is what a run returns.
type Result struct {
	Outcome Outcome

	// Answer is the model's final text when Outcome is Finished.
	Answer string

	// FinishReason is the provider's reason the last model call of this run
	// stopped, untranslated. "length" means the answer was cut off at the
	// output cap; whether to ask the model to go on is the caller's call.
	FinishReason string

	// Steps is how many steps the turn has used so far.
	Steps int

	// Citations are the thread's numbered sources after the run, in number
	// order: the source a label "[n]" in the answer refers to is
	// Citations[n-1]. A label with no entry was invented by the model.
	Citations []citation.Source
}

var (
	// ErrStepLimit means the turn used Config.MaxSteps steps without
	// finishing. The thread is saved as it stood; Continue would go on, and
	// would be refused again at once, so a caller that wants to allow more
	// builds the agent with a higher limit. A new Start begins a new count.
	ErrStepLimit = errors.New("agent: step limit reached")

	// ErrAwaitingApproval means the thread is paused for decisions and the
	// call was not Resume. Starting a new turn over a paused one would
	// orphan the calls a person is being asked about.
	ErrAwaitingApproval = errors.New("agent: thread is waiting for approval")

	// ErrNotPaused means Resume was called on a thread that is not waiting.
	ErrNotPaused = errors.New("agent: thread is not waiting for approval")

	// ErrNothingToContinue means Continue was called on a thread whose last
	// turn finished, or that has never run.
	ErrNothingToContinue = errors.New("agent: thread has no unfinished turn")
)

// turn is one run in progress. It exists for the length of one call to
// Start, Resume or Continue and holds what must not be saved: the sink, the
// lock that serialises it, and the bookkeeping of ids.
type turn struct {
	agent  *Agent
	thread string
	seq    int64
	state  State
	next   string

	// emitMu makes the sink single-threaded. Tools of one batch run side by
	// side and a nested agent emits from inside one of them, so events can
	// arrive from several goroutines; the lock is what lets a Sink be written
	// without one.
	emitMu sync.Mutex
	sink   event.Sink

	// finishReason is the last model call's, for Result.
	finishReason string

	// usedIDs and stepIDs belong to the model step; see claimID.
	usedIDs map[string]struct{}
	stepIDs []string

	// cites is the thread's citation registry, handed to every tool call. A
	// delegate's run is given its parent's, so the whole tree of a turn
	// numbers sources from one sequence.
	cites *citation.Registry

	// nested is set for a delegate's run, made from inside its parent's tool
	// step. A nested run does not announce the approvals it is waiting for:
	// the outermost run announces every waiting call of the tree at once,
	// when it pauses, so that the announcements come in one order, the
	// order Pending lists them in and Resume expects them answered.
	nested bool
}

func (t *turn) emit(ev event.Event) {
	t.emitMu.Lock()
	t.sink(ev)
	t.emitMu.Unlock()
}

// Start begins a new turn on the thread with the given messages, normally one
// user message, and runs it until it finishes, pauses or fails.
//
// The thread need not exist. If its last turn was cut short (cancelled,
// crashed, out of steps), any tool call left without a result is answered
// "interrupted" so the conversation is valid again, and the new turn goes on
// from there; Continue is the other choice, which carries on the old turn.
func (a *Agent) Start(ctx context.Context, thread string, input []model.Message, sink event.Sink) (Result, error) {
	return a.start(ctx, thread, input, sink, nil)
}

// start is Start for a run the caller made (parent nil) or a delegate's run
// made from a parent's tool step.
func (a *Agent) start(ctx context.Context, thread string, input []model.Message, sink event.Sink, parent *turn) (Result, error) {
	t, cp, err := a.open(ctx, thread, sink, parent)
	switch {
	case errors.Is(err, checkpoint.ErrNotFound):
	case err != nil:
		return Result{}, err
	case cp.Status == checkpoint.Paused:
		return Result{}, fmt.Errorf("%w: thread %q", ErrAwaitingApproval, thread)
	}
	if err := t.closeInterruptedBatch(ctx); err != nil {
		return Result{}, err
	}
	t.state.Messages = append(t.state.Messages, input...)
	t.state.Steps = 0
	t.state.TurnTrust = nil
	t.next = nodeModel
	return t.claimAndRun(ctx)
}

// Resume continues a paused turn with a decision for every waiting call.
//
// The decisions are checked against the waiting calls before anything
// changes. A set that is short, long, or names a call that is not waiting is
// refused and the thread stays paused exactly as it was.
func (a *Agent) Resume(ctx context.Context, thread string, decisions []Decision, sink event.Sink) (Result, error) {
	return a.resume(ctx, thread, decisions, sink, nil)
}

func (a *Agent) resume(ctx context.Context, thread string, decisions []Decision, sink event.Sink, parent *turn) (Result, error) {
	t, cp, err := a.open(ctx, thread, sink, parent)
	if errors.Is(err, checkpoint.ErrNotFound) || (err == nil && cp.Status != checkpoint.Paused) {
		return Result{}, fmt.Errorf("%w: thread %q", ErrNotPaused, thread)
	}
	if err != nil {
		return Result{}, err
	}
	waiting, err := a.waiting(ctx, thread, &t.state)
	if err != nil {
		return Result{}, err
	}
	if err := checkDecisions(waiting, decisions); err != nil {
		return Result{}, err
	}
	t.state.applyDecisions(decisions)
	t.next = nodeTools
	return t.claimAndRun(ctx)
}

// Continue carries on a turn that stopped without finishing: after a crash, a
// cancellation, or a failed model call the caller wants to try again. It runs
// the step that was due next.
//
// Tool calls of that step which never produced a result are run. A call that
// was in flight when the process died is therefore run a second time, so
// Continue suits tools that tolerate that; Start, which answers such calls
// "interrupted" instead, suits the rest.
func (a *Agent) Continue(ctx context.Context, thread string, sink event.Sink) (Result, error) {
	return a.cont(ctx, thread, sink, nil)
}

func (a *Agent) cont(ctx context.Context, thread string, sink event.Sink, parent *turn) (Result, error) {
	t, cp, err := a.open(ctx, thread, sink, parent)
	switch {
	case errors.Is(err, checkpoint.ErrNotFound) || (err == nil && cp.Status == checkpoint.Done):
		return Result{}, fmt.Errorf("%w: thread %q", ErrNothingToContinue, thread)
	case err != nil:
		return Result{}, err
	case cp.Status == checkpoint.Paused:
		return Result{}, fmt.Errorf("%w: thread %q", ErrAwaitingApproval, thread)
	}
	if parent == nil {
		if err := a.restoreCitations(ctx, thread, &t.state, t.cites); err != nil {
			return Result{}, err
		}
	}
	t.next = cp.Node
	return t.claimAndRun(ctx)
}

// restoreCitations brings the registry up to every list saved by the
// unfinished delegates beneath a thread, before any of them runs again.
//
// It matters after a crash. Each delegate saves the registry as it stood at
// its own last save, and the parent's save can be the oldest of them all. If
// the delegates were then simply run again, each restoring its own list as
// it opened, a delegate that opened first could give out, as new, a number a
// sibling had already used in prose it saved before the crash, and the
// sibling's list would no longer fit. Reading every list first, in one walk
// of the tree before any work, means every number ever saved is taken before
// a new one is given out.
//
// Only Continue needs it. A paused tree was saved from the top after every
// delegate had stopped, so its top list is already the longest; and a new
// Start discards the unfinished delegates together with their prose.
func (a *Agent) restoreCitations(ctx context.Context, thread string, st *State, reg *citation.Registry) error {
	for i := range st.Batch {
		c := &st.Batch[i]
		d := a.delegates[c.Name]
		if d == nil || c.Status == CallDone {
			continue
		}
		child := childThread(thread, c.ID)
		cp, err := d.Agent.cfg.Store.Load(ctx, child)
		if errors.Is(err, checkpoint.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("agent: load delegate thread %q: %w", child, err)
		}
		childState, err := decodeState(cp.State)
		if err != nil {
			return err
		}
		if err := reg.Extend(childState.Citations); err != nil {
			return fmt.Errorf("agent: thread %q: %w", child, err)
		}
		if err := d.Agent.restoreCitations(ctx, child, &childState, reg); err != nil {
			return err
		}
	}
	return nil
}

// open loads the thread into a new turn. When the thread does not exist it
// returns the empty turn together with checkpoint.ErrNotFound, because for
// Start that is a normal beginning and for the others it is the caller's
// mistake, and only they know which.
//
// The turn's citation registry is the parent's for a delegate, and a new one
// for a run the caller made. Either way it is then brought up to the list the
// thread saved, which for a delegate can be longer than its parent's after a
// crash (see State.Citations).
func (a *Agent) open(ctx context.Context, thread string, sink event.Sink, parent *turn) (*turn, checkpoint.Checkpoint, error) {
	if thread == "" {
		return nil, checkpoint.Checkpoint{}, errors.New("agent: thread id is empty")
	}
	if sink == nil {
		return nil, checkpoint.Checkpoint{}, errors.New("agent: sink is nil; pass event.Discard to run unobserved")
	}
	t := &turn{agent: a, thread: thread, sink: sink, nested: parent != nil}
	if parent != nil {
		t.cites = parent.cites
	} else {
		t.cites = citation.NewRegistry()
	}
	cp, err := a.cfg.Store.Load(ctx, thread)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return t, cp, err
	}
	if err != nil {
		return nil, cp, fmt.Errorf("agent: load thread %q: %w", thread, err)
	}
	if t.state, err = decodeState(cp.State); err != nil {
		return nil, cp, err
	}
	if err := t.cites.Extend(t.state.Citations); err != nil {
		return nil, cp, fmt.Errorf("agent: thread %q: %w", thread, err)
	}
	t.seq = cp.Seq
	return t, cp, nil
}

// claimAndRun saves the thread once before the first step, then runs.
//
// The early save is the claim on the thread. Every save must carry the next
// sequence number, so once this one lands, any other run holding the thread's
// old number can never save again: a thread's history is written by one run
// at a time and is never an interleaving of two. If two requests begin at the
// same moment, one of them is refused right here, before it has called a
// model or written a word to its reader.
//
// What the rule does not do is refuse a new run because an older one is
// still going: from a saved thread, a turn in flight and a turn that crashed
// look alike, and refusing both would leave a crashed thread stuck. The newer
// run takes the thread and the older one fails at its next save. Whoever
// tracks live turns (a row with a heartbeat, say) is the one who can turn a
// second request away while the first is healthy.
func (t *turn) claimAndRun(ctx context.Context) (Result, error) {
	if err := t.save(ctx, checkpoint.Running); err != nil {
		return t.fail(err)
	}
	return t.run(ctx)
}

// run is the loop.
func (t *turn) run(ctx context.Context) (Result, error) {
	limit := t.agent.cfg.MaxSteps
	for {
		if err := ctx.Err(); err != nil {
			return t.fail(err)
		}
		if t.state.Steps >= limit {
			return t.fail(fmt.Errorf("%w: thread %q used all %d steps of this turn", ErrStepLimit, t.thread, limit))
		}
		t.state.Steps++
		step, node := t.state.Steps, t.next
		t.emit(event.Event{Kind: event.StepStarted, Step: step, Node: node})

		status := checkpoint.Running
		switch node {
		case nodeModel:
			if err := t.modelStep(ctx); err != nil {
				return t.fail(err)
			}
			if len(t.state.Batch) > 0 {
				t.next = nodeTools
			} else {
				t.next, status = "", checkpoint.Done
			}
		case nodeTools:
			paused, err := t.toolStep(ctx)
			if err != nil {
				t.keepProgress(ctx)
				return t.fail(err)
			}
			if paused {
				status = checkpoint.Paused
			} else {
				t.next = nodeModel
			}
		default:
			return t.fail(fmt.Errorf("agent: thread %q is saved at an unknown step %q", t.thread, node))
		}

		if err := t.save(ctx, status); err != nil {
			return t.fail(err)
		}
		// StepFinished is sent after the save, so an observer that has seen
		// it knows the step is durable.
		t.emit(event.Event{Kind: event.StepFinished, Step: step, Node: node})

		switch status {
		case checkpoint.Done:
			t.emit(event.Event{Kind: event.Done, Step: step})
			return Result{Outcome: Finished, Answer: t.state.answer(), FinishReason: t.finishReason, Steps: step, Citations: t.cites.Sources()}, nil
		case checkpoint.Paused:
			if !t.nested {
				if err := t.announceWaiting(ctx); err != nil {
					return t.fail(err)
				}
			}
			t.emit(event.Event{Kind: event.Paused, Step: step})
			return Result{Outcome: Paused, FinishReason: t.finishReason, Steps: step, Citations: t.cites.Sources()}, nil
		}
	}
}

// fail ends the run with an Error event, so that every stream ends in exactly
// one terminal event whatever went wrong.
func (t *turn) fail(err error) (Result, error) {
	t.emit(event.Event{Kind: event.Error, Step: t.state.Steps, Err: err})
	return Result{}, err
}

// announceWaiting sends one ApprovalNeeded event for every call the paused
// tree is waiting on, its own and its delegates', from the same saved state
// and the same function that Pending reads and Resume checks against. The
// three can therefore never disagree about which calls are waiting or in
// what order, which is what a client that answers by position relies on.
//
// It runs once the pause is saved. If it fails, the thread is paused and
// whole, and Pending will list the same calls.
func (t *turn) announceWaiting(ctx context.Context) error {
	waiting, err := t.agent.waiting(ctx, t.thread, &t.state)
	if err != nil {
		return err
	}
	for _, w := range waiting {
		// A nested call's id is its delegate's path, a slash and its own id;
		// the path is its Parent, as on every other event from inside a
		// delegate.
		parent := ""
		if i := strings.LastIndexByte(w.CallID, '/'); i >= 0 {
			parent = w.CallID[:i]
		}
		t.emit(event.Event{
			Kind: event.ApprovalNeeded, Parent: parent, Step: t.state.Steps,
			CallID: w.CallID, Tool: w.Tool, Args: w.Args, AllowAlways: w.AllowAlways, Payload: w.Question,
		})
	}
	return nil
}

func (t *turn) save(ctx context.Context, status checkpoint.Status) error {
	// The registry is shared by the whole tree and may have grown since the
	// last save; taking its list does not copy it.
	t.state.Citations = t.cites.Sources()
	raw, err := t.state.encode()
	if err != nil {
		return err
	}
	cp := checkpoint.Checkpoint{Thread: t.thread, Seq: t.seq + 1, Status: status, Node: t.next, State: raw}
	if err := t.agent.cfg.Store.Save(ctx, cp); err != nil {
		return fmt.Errorf("agent: save thread %q: %w", t.thread, err)
	}
	t.seq++
	return nil
}

// progressSaveTimeout bounds the one save made after a run's context has
// ended. It is short because the caller has already been told to stop.
const progressSaveTimeout = 5 * time.Second

// keepProgress saves the batch as it stands when a tool step is cut short.
//
// A tool step can be cancelled with some of its calls finished, their effects
// already in the world. Dropping those results would make the thread say
// "interrupted, outcome unknown" about work whose outcome is known, so they
// are saved, on a context that outlives the cancellation by a few seconds.
// If this save fails too there is nothing further to try: the error that
// stopped the step is already on its way to the caller, and the thread is
// left at the step before, which is a valid place to resume from.
func (t *turn) keepProgress(ctx context.Context) {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), progressSaveTimeout)
	defer cancel()
	_ = t.save(saveCtx, checkpoint.Running)
}

// closeInterruptedBatch makes a thread whose last turn was cut short valid
// for a new turn. Calls that have results keep them; the rest are answered
// "interrupted"; a delegate's half-finished thread is removed, since nothing
// will ever resume it.
func (t *turn) closeInterruptedBatch(ctx context.Context) error {
	if len(t.state.Batch) == 0 {
		return nil
	}
	for i := range t.state.Batch {
		c := &t.state.Batch[i]
		if c.Status == CallDone {
			continue
		}
		if d := t.agent.delegates[c.Name]; d != nil {
			if err := d.Agent.discard(ctx, childThread(t.thread, c.ID)); err != nil {
				return err
			}
		}
		*c = Call{ID: c.ID, Name: c.Name, Args: c.Args, Status: CallDone, Result: interruptedText, IsError: true}
	}
	t.state.flushBatch()
	return nil
}
