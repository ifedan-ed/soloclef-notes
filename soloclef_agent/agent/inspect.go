package agent

import (
	"context"
	"errors"
	"fmt"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// Load returns a thread's saved state and where it stands. It returns
// checkpoint.ErrNotFound for a thread that has never run.
func (a *Agent) Load(ctx context.Context, thread string) (State, checkpoint.Status, error) {
	cp, err := a.cfg.Store.Load(ctx, thread)
	if err != nil {
		if errors.Is(err, checkpoint.ErrNotFound) {
			return State{}, "", err
		}
		return State{}, "", fmt.Errorf("agent: load thread %q: %w", thread, err)
	}
	st, err := decodeState(cp.State)
	return st, cp.Status, err
}

// Pending lists the calls a paused thread is waiting on, across every nested
// agent, in the order their ApprovalNeeded events were sent. A thread that is
// not paused has none.
//
// This is how a client that reloaded the page rebuilds its approval cards:
// from the same saved state Resume will check the decisions against, through
// a public call.
func (a *Agent) Pending(ctx context.Context, thread string) ([]Approval, error) {
	st, status, err := a.Load(ctx, thread)
	if errors.Is(err, checkpoint.ErrNotFound) || (err == nil && status != checkpoint.Paused) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a.waiting(ctx, thread, &st)
}

// waiting collects the waiting calls of a state and of the delegates beneath
// it, giving nested calls the same prefixed ids their events carried.
func (a *Agent) waiting(ctx context.Context, thread string, st *State) ([]Approval, error) {
	var out []Approval
	for i := range st.Batch {
		c := &st.Batch[i]
		switch c.Status {
		case CallAwaitingApproval:
			out = append(out, Approval{CallID: c.ID, Tool: c.Name, Args: c.Args, AllowAlways: a.tools[c.Name] != nil && a.tools[c.Name].Approval == tool.Ask})
		case CallAwaitingAnswer:
			out = append(out, Approval{CallID: c.ID, Tool: c.Name, Args: c.Args, Question: c.Question})
		case CallDelegated:
			d := a.delegates[c.Name]
			if d == nil {
				return nil, fmt.Errorf("agent: thread %q is waiting inside delegate %q, which this agent no longer has", thread, c.Name)
			}
			nested, err := d.Agent.Pending(ctx, childThread(thread, c.ID))
			if err != nil {
				return nil, err
			}
			for _, n := range nested {
				n.CallID = c.ID + "/" + n.CallID
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// Edit changes a thread's saved state: fn is given the state, and what it
// leaves there is saved. It is how a caller rewinds a conversation to an
// earlier message, or replaces old messages with a summary.
//
// The state is the caller's to change, and keeping it valid is the caller's
// job too: a conversation must still answer every tool call it contains. The
// save follows the same sequence rule as a run, so an edit made while a turn
// is running on the thread is refused with checkpoint.ErrConflict rather
// than silently lost or silently winning.
func (a *Agent) Edit(ctx context.Context, thread string, fn func(*State) error) error {
	cp, err := a.cfg.Store.Load(ctx, thread)
	if err != nil {
		if errors.Is(err, checkpoint.ErrNotFound) {
			return err
		}
		return fmt.Errorf("agent: load thread %q: %w", thread, err)
	}
	st, err := decodeState(cp.State)
	if err != nil {
		return err
	}
	if err := fn(&st); err != nil {
		return err
	}
	raw, err := st.encode()
	if err != nil {
		return err
	}
	cp.Seq++
	cp.State = raw
	if err := a.cfg.Store.Save(ctx, cp); err != nil {
		return fmt.Errorf("agent: save thread %q: %w", thread, err)
	}
	return nil
}
