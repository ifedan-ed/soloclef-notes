package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// A delegate is another agent run as a tool call. Nothing about it is
// special-cased beyond this file: the nested agent runs the same loop, on a
// thread of its own, saving its own checkpoints, and its last message is the
// call's result.
//
// The two things that are hard to do when the nested agent is a graph inside
// a graph are plain here because there is no state shared between the two
// runs to reconcile:
//
//   - A pause inside the nested agent is its Run returning Paused. The call
//     is marked delegated, the parent pauses like for any waiting call, and a
//     resume hands the nested agent the decisions addressed to it. No
//     exception crosses the boundary and nothing is rerun to deliver them.
//   - Anything the two runs must agree on (a citation numbering, an audit
//     log) is one Go value both sets of tools were built with. It is shared
//     by being the same object, so there is no second copy to merge when the
//     nested agent returns; see package citation.

// childThread names a delegate's thread after its parent's thread and the
// call that started it. The name is all that links them: given a parent's
// state, every nested thread can be found, and none needs a registry.
func childThread(parent, callID string) string {
	return parent + "/" + callID
}

// runDelegate runs or continues the nested agent for one call, and reports
// the outcome as a tool would: the agent's answer, errDelegateWaiting when it
// paused, a *stopError when the run must stop, or any other error for a
// delegate that failed, which the parent's model is shown. It is the bottom
// of the call's stack of tool hooks, so call is the call as the hooks passed
// it down, and c is the batch entry it belongs to.
func (t *turn) runDelegate(ctx context.Context, d *Delegate, c *Call, call tool.Call) (string, error) {
	thread := childThread(t.thread, c.ID)
	sink := t.nestedSink(c.ID)

	// The delegate's own time limit is a second context inside the run's.
	// The run's context is kept as it is in outer, because afterwards the
	// two must be told apart: the run being stopped ends everything, the
	// limit passing ends only this call.
	outer := ctx
	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}

	// What to do is decided by where the nested thread actually stands, not
	// by what the parent last recorded about it. The two are saved separately
	// and a crash can fall between the saves; the nested thread's own
	// checkpoint is the one that is true.
	var (
		res Result
		err error
	)
	cp, loadErr := d.Agent.cfg.Store.Load(ctx, thread)
	switch {
	case errors.Is(loadErr, checkpoint.ErrNotFound):
		var args struct {
			Task string `json:"task"`
		}
		if json.Unmarshal(call.Args, &args) != nil || args.Task == "" {
			return "", errors.New(`the arguments need a "task" string saying what to do.`)
		}
		res, err = d.Agent.start(ctx, thread, []model.Message{model.User(args.Task)}, sink, t)
	case loadErr != nil:
		if outer.Err() != nil {
			return "", outer.Err()
		}
		return "", &stopError{fmt.Errorf("agent: load delegate thread %q: %w", thread, loadErr)}
	case cp.Status == checkpoint.Paused:
		if len(c.Forward) == 0 {
			// Still waiting, and nobody has answered yet.
			return "", errDelegateWaiting
		}
		res, err = d.Agent.resume(ctx, thread, c.Forward, sink, t)
	case cp.Status == checkpoint.Running:
		res, err = d.Agent.cont(ctx, thread, sink, t)
	default:
		// The nested turn finished and the process stopped before the parent
		// recorded it. Its answer is in its state.
		var st State
		if st, err = decodeState(cp.State); err == nil {
			res = Result{Outcome: Finished, Answer: st.answer()}
		}
	}
	// Decisions are handed over once. Whatever happened, the nested thread
	// now holds them or has no use for them.
	c.Forward = nil

	// A panic inside the nested agent is a bug in the program. It stops the
	// run whatever else is true, and is checked first so that no other
	// reading of the error (a limit that also passed, say) can turn it into
	// a tool result.
	var panicked *PanicError
	if errors.As(err, &panicked) {
		return "", err
	}

	switch {
	case err == nil && res.Outcome == Paused:
		return "", errDelegateWaiting
	case err == nil:
		// On the run's context, not the limited one: an answer that arrived
		// just inside the limit must not be lost to it while tidying up.
		if err := d.Agent.cfg.Store.Delete(outer, thread); err != nil {
			return "", &stopError{fmt.Errorf("agent: remove finished delegate thread %q: %w", thread, err)}
		}
		return res.Answer, nil
	case outer.Err() != nil:
		// The run is being stopped. The nested thread stays as it was last
		// saved: Continue picks it up, and a new Start removes it.
		return "", outer.Err()
	case ctx.Err() != nil:
		// Only the delegate's own limit has passed. Its thread is removed,
		// so that the work it abandoned is not picked up later as work in
		// progress, and the calling model is told what happened.
		if delErr := d.Agent.discard(outer, thread); delErr != nil {
			return "", &stopError{delErr}
		}
		return "", fmt.Errorf("%s did not finish within %s", d.Name, d.Timeout)
	}

	// The nested agent failed on its own account: its model call failed, it
	// ran out of steps. To the parent that is a tool that failed, and the
	// parent's model decides what to do about it. The nested thread is
	// removed so that the failure is not picked up again as work in
	// progress.
	if delErr := d.Agent.discard(outer, thread); delErr != nil {
		return "", &stopError{delErr}
	}
	return "", err
}

// nestedSink returns the sink a nested agent reports to. It stamps each event
// with the delegating call, which does two jobs: Parent tells a consumer the
// event is a specialist's working and not the answer, and the prefix on
// CallID makes a nested call's id unique in the tree and routable, since a
// decision for "a/b" is recognisably for call b inside delegate call a.
func (t *turn) nestedSink(callID string) event.Sink {
	return func(ev event.Event) {
		if ev.CallID != "" {
			ev.CallID = callID + "/" + ev.CallID
		}
		if ev.Parent == "" {
			ev.Parent = callID
		} else {
			ev.Parent = callID + "/" + ev.Parent
		}
		t.emit(ev)
	}
}

// discard removes a thread and every delegate thread beneath it.
func (a *Agent) discard(ctx context.Context, thread string) error {
	cp, err := a.cfg.Store.Load(ctx, thread)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent: load thread %q to remove it: %w", thread, err)
	}
	st, err := decodeState(cp.State)
	if err != nil {
		return err
	}
	for i := range st.Batch {
		if d := a.delegates[st.Batch[i].Name]; d != nil {
			if err := d.Agent.discard(ctx, childThread(thread, st.Batch[i].ID)); err != nil {
				return err
			}
		}
	}
	if err := a.cfg.Store.Delete(ctx, thread); err != nil {
		return fmt.Errorf("agent: remove thread %q: %w", thread, err)
	}
	return nil
}
