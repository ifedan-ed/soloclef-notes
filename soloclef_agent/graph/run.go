package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// Graph is a checked graph. It holds no run state, so one Graph may run any
// number of threads at once.
type Graph[S any] struct {
	start string
	nodes map[string]compiled[S]
}

// compiled is a node with its one way out.
type compiled[S any] struct {
	run     Node[S]
	next    string
	router  Router[S]
	targets []string
}

// Run says how one run is carried out. Every field must be set; there is no
// run without a step limit and no run whose state silently goes nowhere.
type Run struct {
	// Thread names the run's saved state.
	Thread string

	// MaxSteps is how many nodes this call may run. It is the guard against
	// a graph that loops; reaching it is ErrStepLimit, with the thread saved
	// at the last finished step.
	MaxSteps int

	// Store is where the state is saved after every step.
	Store checkpoint.Store

	// Sink receives the run's events; event.Discard for none.
	Sink event.Sink
}

// Outcome says how a run ended when it did not end in an error.
type Outcome uint8

const (
	// Finished means the run reached End.
	Finished Outcome = iota + 1
	// Paused means a branch paused the run; Resume continues it.
	Paused
)

// Result is what a run returns.
type Result[S any] struct {
	Outcome Outcome
	State   S

	// Steps is how many nodes this call ran.
	Steps int
}

var (
	// ErrStepLimit means the run used Run.MaxSteps without reaching End.
	ErrStepLimit = errors.New("graph: step limit reached")

	// ErrFinished means Resume was called on a thread that has reached End.
	ErrFinished = errors.New("graph: thread has finished")
)

// Start runs the graph from its start node with the given state. It begins a
// thread, or begins one again from the top if it exists.
func (g *Graph[S]) Start(ctx context.Context, run Run, state S) (Result[S], error) {
	if err := run.check(); err != nil {
		return Result[S]{}, err
	}
	cp, err := run.Store.Load(ctx, run.Thread)
	if err != nil && !errors.Is(err, checkpoint.ErrNotFound) {
		return Result[S]{}, fmt.Errorf("graph: load thread %q: %w", run.Thread, err)
	}
	return g.loop(ctx, run, cp.Seq, g.start, state)
}

// Resume continues a thread from its checkpoint: a paused run from the node
// it paused before, or a run that failed or was cut short from the step it
// had reached.
//
// update is applied to the saved state first. It is how an answer reaches a
// paused run: the node about to run reads what update wrote. Pass a function
// that returns the state unchanged to resume as it was.
func (g *Graph[S]) Resume(ctx context.Context, run Run, update func(S) (S, error)) (Result[S], error) {
	if err := run.check(); err != nil {
		return Result[S]{}, err
	}
	if update == nil {
		return Result[S]{}, errors.New("graph: Resume needs an update function; return the state unchanged to resume as it was")
	}
	cp, err := run.Store.Load(ctx, run.Thread)
	if err != nil {
		if errors.Is(err, checkpoint.ErrNotFound) {
			return Result[S]{}, fmt.Errorf("graph: thread %q has nothing to resume: %w", run.Thread, err)
		}
		return Result[S]{}, fmt.Errorf("graph: load thread %q: %w", run.Thread, err)
	}
	if cp.Status == checkpoint.Done {
		return Result[S]{}, fmt.Errorf("%w: %q", ErrFinished, run.Thread)
	}
	var state S
	if err := json.Unmarshal(cp.State, &state); err != nil {
		return Result[S]{}, fmt.Errorf("graph: stored state of thread %q is not readable: %w", run.Thread, err)
	}
	if state, err = update(state); err != nil {
		return Result[S]{}, err
	}
	return g.loop(ctx, run, cp.Seq, cp.Node, state)
}

func (r Run) check() error {
	switch {
	case r.Thread == "":
		return errors.New("graph: Run.Thread is empty")
	case r.MaxSteps < 1:
		return fmt.Errorf("graph: Run.MaxSteps must be at least 1, got %d", r.MaxSteps)
	case r.Store == nil:
		return errors.New("graph: Run.Store is not set; use checkpoint.NewMemory() for state that need not outlive the process")
	case r.Sink == nil:
		return errors.New("graph: Run.Sink is nil; pass event.Discard to run unobserved")
	}
	return nil
}

func (g *Graph[S]) loop(ctx context.Context, run Run, seq int64, at string, state S) (Result[S], error) {
	fail := func(n int, err error) (Result[S], error) {
		run.Sink(event.Event{Kind: event.Error, Step: n, Node: at, Err: err})
		return Result[S]{}, err
	}

	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return fail(n, err)
		}
		if n > run.MaxSteps {
			return fail(n, fmt.Errorf("%w: thread %q ran %d steps and was about to run %q", ErrStepLimit, run.Thread, run.MaxSteps, at))
		}
		node, ok := g.nodes[at]
		if !ok {
			return fail(n, fmt.Errorf("graph: thread %q is saved at %q, which is not a node of this graph", run.Thread, at))
		}

		run.Sink(event.Event{Kind: event.StepStarted, Step: n, Node: at})
		next, err := node.run(ctx, Step{Thread: run.Thread, N: n, Emit: run.Sink}, state)
		if err != nil {
			return fail(n, fmt.Errorf("graph: node %q: %w", at, err))
		}
		state = next

		route := Route{node: node.next}
		if node.router != nil {
			if route, err = node.router(ctx, state); err != nil {
				return fail(n, fmt.Errorf("graph: branch from %q: %w", at, err))
			}
			if !slices.Contains(node.targets, route.node) {
				return fail(n, fmt.Errorf("graph: the branch from %q chose %q, which it did not list as a target", at, route.node))
			}
			if route.pause && route.node == End {
				return fail(n, fmt.Errorf("graph: the branch from %q paused before graph.End; there is nothing after a pause there to resume", at))
			}
		}

		status := checkpoint.Running
		saved := route.node
		switch {
		case route.node == End:
			status, saved = checkpoint.Done, ""
		case route.pause:
			status = checkpoint.Paused
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return fail(n, fmt.Errorf("graph: encode state after %q: %w", at, err))
		}
		seq++
		if err := run.Store.Save(ctx, checkpoint.Checkpoint{Thread: run.Thread, Seq: seq, Status: status, Node: saved, State: raw}); err != nil {
			return fail(n, fmt.Errorf("graph: save thread %q: %w", run.Thread, err))
		}
		// Sent after the save: an observer that has seen it knows a restart
		// would not repeat this node.
		run.Sink(event.Event{Kind: event.StepFinished, Step: n, Node: at})

		switch status {
		case checkpoint.Done:
			run.Sink(event.Event{Kind: event.Done, Step: n})
			return Result[S]{Outcome: Finished, State: state, Steps: n}, nil
		case checkpoint.Paused:
			run.Sink(event.Event{Kind: event.Paused, Step: n, Node: route.node})
			return Result[S]{Outcome: Paused, State: state, Steps: n}, nil
		}
		at = route.node
	}
}
