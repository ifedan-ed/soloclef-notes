package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"sync"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// toolStep moves the batch forward as far as it can go. It reports paused
// when a person is needed, and an error only when the run itself must stop.
//
// It has two phases, and the order between them is the point.
//
// First every new call is decided: run, or ask. If any call must ask, the
// step pauses there with nothing run. All the questions for one batch reach
// the person together, and no call runs ahead of an earlier one that is
// still waiting.
//
// Then the calls run, in the model's order. Neighbouring calls that are safe
// to run together do; every other call runs alone. A call can itself come to
// need a person here (a tool asks a question, a delegate's agent pauses), and
// then the step pauses after the group it was in, keeping every result
// already produced. On resume the finished calls are skipped, never rerun.
func (t *turn) toolStep(ctx context.Context) (paused bool, err error) {
	batch := t.state.Batch

	asked := false
	for i := range batch {
		if batch[i].Status == CallRejected {
			t.finish(&batch[i], batch[i].Result, false)
		}
		if batch[i].Status != CallPending {
			continue
		}
		if err := t.decide(ctx, &batch[i]); err != nil {
			return false, err
		}
		asked = asked || batch[i].Status == CallAwaitingApproval
	}
	if asked {
		// The waiting calls are announced by the outermost run once the
		// pause is saved; see announceWaiting.
		return true, nil
	}

	for i := 0; i < len(batch); {
		if batch[i].Status == CallDone {
			i++
			continue
		}
		// A group is one call, or a run of neighbours that may all run
		// together. Finished calls inside the run do not break it.
		j := i + 1
		if t.agent.parallel(batch[i].Name) {
			for j < len(batch) && (batch[j].Status == CallDone || t.agent.parallel(batch[j].Name)) {
				j++
			}
		}
		if err := t.runGroup(ctx, batch[i:j]); err != nil {
			return false, err
		}
		if slices.ContainsFunc(batch[i:j], func(c Call) bool { return c.Status != CallDone }) {
			return true, nil
		}
		i = j
	}

	t.state.flushBatch()
	return false, nil
}

func (a *Agent) parallel(name string) bool {
	if d := a.delegates[name]; d != nil {
		return d.Parallel
	}
	if tl := a.tools[name]; tl != nil {
		return tl.Parallel
	}
	return false
}

// decide settles whether a new call runs, asks, or is answered at once with
// an error. The order of the checks is fixed and each is final:
//
//  1. A name that is no tool, or arguments that are not an object, is
//     answered with an error the model can act on. Nothing runs.
//  2. A delegate runs. Handing over a task is not itself an action in the
//     world; the delegate's own tools carry their own policies.
//  3. A tool.Auto tool runs.
//  4. A tool.AlwaysAsk tool asks. Nothing below is consulted.
//  5. A tool.Ask tool runs if the user said "always allow" for it earlier in
//     this turn, or if Config.Trusted says so now; otherwise it asks.
func (t *turn) decide(ctx context.Context, c *Call) error {
	a := t.agent
	if a.delegates[c.Name] != nil {
		if !isJSONObject([]byte(c.Args)) {
			t.finish(c, badArgsText, true)
			return nil
		}
		c.Status = CallApproved
		return nil
	}
	tl := a.tools[c.Name]
	if tl == nil {
		t.finish(c, unknownToolText(c.Name, a.names), true)
		return nil
	}
	if !isJSONObject([]byte(c.Args)) {
		t.finish(c, badArgsText, true)
		return nil
	}

	switch tl.Approval {
	case tool.Auto:
		c.Status = CallApproved
	case tool.AlwaysAsk:
		c.Status = CallAwaitingApproval
	case tool.Ask:
		trusted := slices.Contains(t.state.TurnTrust, c.Name)
		if !trusted && a.cfg.Trusted != nil {
			var err error
			if trusted, err = a.cfg.Trusted(ctx, t.toolCall(c)); err != nil {
				// No answer is not "no" and not "yes". Running would be a
				// guess and so would asking, when the caller's own source of
				// truth has just failed; the turn stops and says why.
				return fmt.Errorf("agent: ask whether %s is trusted: %w", c.Name, err)
			}
		}
		if trusted {
			c.Status = CallApproved
		} else {
			c.Status = CallAwaitingApproval
		}
	}
	return nil
}

func (t *turn) toolCall(c *Call) tool.Call {
	// The notice carries the call's id and name as they are now, copied:
	// the batch entry c is rewritten when the call finishes, and a tool's
	// own goroutine may still be reporting at that moment.
	id, name, step := c.ID, c.Name, t.state.Steps
	return tool.Call{
		ID: id, Name: name, Args: []byte(c.Args), Thread: t.thread, Answer: c.Answer, Citations: t.cites,
		Notify: func(kind string, payload any) error {
			raw, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("agent: notice %q from %s cannot be encoded: %w", kind, name, err)
			}
			t.emit(event.Event{Kind: event.Notice, Step: step, CallID: id, Tool: name, Name: kind, Payload: raw})
			return nil
		},
	}
}

// finish records a call's result and announces it.
func (t *turn) finish(c *Call, result string, isError bool) {
	*c = Call{ID: c.ID, Name: c.Name, Args: c.Args, Status: CallDone, Result: result, IsError: isError}
	t.emit(event.Event{Kind: event.ToolResult, Step: t.state.Steps, CallID: c.ID, Tool: c.Name, Text: result, IsError: isError})
}

// runGroup runs the calls of one group that are able to run. With one such
// call it runs on the calling goroutine: the common batch is a single call,
// and that case should cost a function call, not a goroutine and a wait.
func (t *turn) runGroup(ctx context.Context, group []Call) error {
	runnable := 0
	last := -1
	for i := range group {
		if canRun(group[i].Status) {
			runnable++
			last = i
		}
	}
	switch runnable {
	case 0:
		return nil
	case 1:
		return t.execute(ctx, &group[last])
	}

	// If one call stops the run (a panic, say), its siblings are cancelled
	// rather than left to finish work nobody will read.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Each goroutine writes only its own element of group and of errs, so the
	// results need no lock; the semaphore is the caller's limit on how many
	// run at once.
	errs := make([]error, len(group))
	slots := make(chan struct{}, t.agent.cfg.MaxParallelTools)
	var wg sync.WaitGroup
	for i := range group {
		if !canRun(group[i].Status) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				errs[i] = ctx.Err()
				return
			}
			if errs[i] = t.execute(ctx, &group[i]); errs[i] != nil {
				cancel()
			}
		}()
	}
	wg.Wait()

	// Report the error that caused the stop, not the cancellations it set
	// off in the siblings.
	var first error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if !errors.Is(err, context.Canceled) {
			return err
		}
		if first == nil {
			first = err
		}
	}
	return first
}

func canRun(s CallStatus) bool {
	return s == CallApproved || s == CallAnswered || s == CallDelegated
}

// execute runs one call to whatever end it reaches: a result, a question, a
// delegate's pause. It returns an error only when the run must stop.
//
// A tool and a delegate go through the same path: the call's own work at the
// bottom, the agent's tool hooks over it, and one reading of what came back.
func (t *turn) execute(ctx context.Context, c *Call) error {
	var base tool.Func
	if d := t.agent.delegates[c.Name]; d != nil {
		base = func(ctx context.Context, call tool.Call) (string, error) {
			return t.runDelegate(ctx, d, c, call)
		}
	} else if tl := t.agent.tools[c.Name]; tl != nil {
		base = tl.Run
	} else {
		// The call was accepted by an agent that had this tool, and is being
		// run by one that does not: agents are built per turn, and a tool
		// can be withdrawn while its call waits for approval.
		t.finish(c, unknownToolText(c.Name, t.agent.names), true)
		return nil
	}

	out, err := guard(ctx, c.Name, chainTool(base, t.agent.cfg.ToolHooks), t.toolCall(c))
	var (
		panicked *PanicError
		stop     *stopError
	)
	switch {
	case err == nil:
		// A tool that returned a result has done its work, even if the run
		// was cancelled while it did. The result is kept.
		t.finish(c, out, false)
		return nil
	case errors.As(err, &panicked):
		return err
	case errors.As(err, &stop):
		return stop.err
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, errDelegateWaiting):
		c.Status = CallDelegated
		return nil
	}
	if q, ok := tool.AsQuestion(err); ok {
		c.Status, c.Question, c.Answer = CallAwaitingAnswer, q.Payload, nil
		return nil
	}
	t.finish(c, toolErrorText(err), true)
	return nil
}

// stopError is an error from a call's own machinery, not from its work: the
// store could not be read, a finished delegate's thread could not be removed.
// It stops the run instead of being shown to the model, which can do nothing
// about the database.
type stopError struct{ err error }

func (e *stopError) Error() string { return e.err.Error() }
func (e *stopError) Unwrap() error { return e.err }

// PanicError is a tool that panicked. It stops the run instead of becoming a
// tool result: a panic is a bug in the program, not something for the model
// to work around, and it must not take the process (and every other user's
// turn) down with it either.
type PanicError struct {
	Tool  string
	Value any
	Stack []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("agent: tool %s panicked: %v", e.Tool, e.Value)
}

// guard runs a call's stack of hooks and work, turning a panic anywhere in
// it into a PanicError.
func guard(ctx context.Context, name string, run tool.Func, call tool.Call) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Tool: name, Value: r, Stack: debug.Stack()}
		}
	}()
	return run(ctx, call)
}
