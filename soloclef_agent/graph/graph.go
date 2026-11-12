// Package graph runs a fixed pipeline of steps over a state type the caller
// defines: nodes are plain functions, edges say what follows what, and a
// branch picks the next node from the state.
//
// It is here for work that really is a pipeline with a shape known in
// advance (outline, then script, then render; retry the draft until it
// passes a check). The chat's tool-calling loop is not built on it and does
// not need it: that loop has two steps and lives in package agent. The two
// share the checkpoint store and the event stream, and nothing else.
//
// It is small on purpose. One node runs at a time, so a node returns the new
// state and there is nothing to merge: no reducers, no channels. There is no
// compile step beyond Build's checks. A graph that cannot reach its end is
// refused when it is built, and one that loops at run time is stopped by a
// step limit the caller must set.
package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
)

// End is the target that finishes a run. It is a name no node may have.
const End = "__end__"

// Step is what a node is told about the run it is part of.
type Step struct {
	// Thread is the thread being run, for a node that keeps things of its
	// own beside the state.
	Thread string

	// N counts the steps of this run from 1.
	N int

	// Emit reports progress on the run's event stream. It is never nil. A node
	// that starts goroutines must not call it from more than one at a time.
	Emit event.Sink
}

// Node is one step of work: it is given the state and returns the state that
// follows. An error stops the run; the state it returned alongside is
// discarded, so the thread stays at the last step that succeeded.
type Node[S any] func(ctx context.Context, step Step, state S) (S, error)

// Route is where a branch sends the run next.
type Route struct {
	node  string
	pause bool
}

// To continues with the named node, or finishes the run when given End.
func To(node string) Route { return Route{node: node} }

// PauseBefore saves the run and stops it, to be resumed at the named node.
//
// This is the whole of human-in-the-loop for a graph. The node before the
// branch puts its question in the state; the branch pauses; Resume is given a
// function that writes the answer into the state; the named node then runs
// and reads it. No node is run twice to deliver the answer.
func PauseBefore(node string) Route { return Route{node: node, pause: true} }

// Router picks the next node from the state.
type Router[S any] func(ctx context.Context, state S) (Route, error)

// Builder collects a graph's nodes and edges. Its methods record mistakes
// instead of returning them, so a graph reads as a list of declarations;
// Build reports everything that was wrong at once.
type Builder[S any] struct {
	nodes    map[string]Node[S]
	order    []string
	next     map[string]string
	routers  map[string]Router[S]
	targets  map[string][]string
	start    string
	mistakes []string
}

// New starts a graph over state type S.
func New[S any]() *Builder[S] {
	return &Builder[S]{
		nodes:   make(map[string]Node[S]),
		next:    make(map[string]string),
		routers: make(map[string]Router[S]),
		targets: make(map[string][]string),
	}
}

func (b *Builder[S]) mistake(format string, args ...any) {
	b.mistakes = append(b.mistakes, fmt.Sprintf(format, args...))
}

// Node adds a node.
func (b *Builder[S]) Node(name string, fn Node[S]) *Builder[S] {
	switch {
	case name == "" || name == End:
		b.mistake("%q cannot be a node's name", name)
	case fn == nil:
		b.mistake("node %q has no function", name)
	case b.nodes[name] != nil:
		b.mistake("node %q is added twice", name)
	default:
		b.nodes[name] = fn
		b.order = append(b.order, name)
	}
	return b
}

// Start names the node a run begins at.
func (b *Builder[S]) Start(name string) *Builder[S] {
	if b.start != "" {
		b.mistake("the start is set twice, to %q and %q", b.start, name)
	}
	b.start = name
	return b
}

// Edge says that to always follows from. to may be End.
func (b *Builder[S]) Edge(from, to string) *Builder[S] {
	b.claimExit(from)
	b.next[from] = to
	return b
}

// Branch says that router chooses what follows from. Every node the router
// can return is listed here. The list is what lets Build check the graph's
// shape without running it, and a router that returns a name outside it is
// stopped at run time as the bug it is.
func (b *Builder[S]) Branch(from string, router Router[S], targets ...string) *Builder[S] {
	b.claimExit(from)
	if router == nil {
		b.mistake("the branch from %q has no router", from)
	}
	if len(targets) == 0 {
		b.mistake("the branch from %q lists no targets", from)
	}
	b.routers[from] = router
	b.targets[from] = targets
	return b
}

// claimExit refuses a second way out of a node. One node, one exit: with two
// there would have to be a rule for which wins, and whichever rule that was,
// someone would be surprised by it.
func (b *Builder[S]) claimExit(from string) {
	if _, edge := b.next[from]; edge {
		b.mistake("node %q has two ways out", from)
	}
	if _, branch := b.targets[from]; branch {
		b.mistake("node %q has two ways out", from)
	}
}

// Build checks the graph and returns it ready to run.
func (b *Builder[S]) Build() (*Graph[S], error) {
	mistakes := append([]string(nil), b.mistakes...)
	exits := func(name string) []string {
		if to, ok := b.next[name]; ok {
			return []string{to}
		}
		return b.targets[name]
	}

	if b.nodes[b.start] == nil {
		mistakes = append(mistakes, fmt.Sprintf("the start %q is not a node", b.start))
	}
	froms := make([]string, 0, len(b.next)+len(b.targets))
	for from := range b.next {
		froms = append(froms, from)
	}
	for from := range b.targets {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	for _, from := range froms {
		if b.nodes[from] == nil {
			mistakes = append(mistakes, fmt.Sprintf("an edge leaves %q, which is not a node", from))
		}
		for _, to := range exits(from) {
			if to != End && b.nodes[to] == nil {
				mistakes = append(mistakes, fmt.Sprintf("an edge from %q goes to %q, which is not a node", from, to))
			}
		}
	}
	for _, name := range b.order {
		if len(exits(name)) == 0 {
			mistakes = append(mistakes, fmt.Sprintf("node %q has no way out; add an Edge or a Branch from it (to graph.End if it is last)", name))
		}
	}
	if len(mistakes) > 0 {
		return nil, errors.New("graph: " + strings.Join(mistakes, "; "))
	}

	// Two questions about shape, answered by walking the edges. Which nodes
	// can a run reach at all, and from which of those can it still finish?
	reachable := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		if name == End || reachable[name] {
			return
		}
		reachable[name] = true
		for _, to := range exits(name) {
			walk(to)
		}
	}
	walk(b.start)

	// finishes grows from End backwards until it stops growing.
	finishes := map[string]bool{End: true}
	for changed := true; changed; {
		changed = false
		for _, name := range b.order {
			if finishes[name] {
				continue
			}
			for _, to := range exits(name) {
				if finishes[to] {
					finishes[name], changed = true, true
					break
				}
			}
		}
	}
	for _, name := range b.order {
		switch {
		case !reachable[name]:
			mistakes = append(mistakes, fmt.Sprintf("node %q can never be reached from the start", name))
		case !finishes[name]:
			// A cycle with an exit is a loop, and the step limit guards it.
			// A cycle with no exit is a hang with extra steps, and there is
			// no reason to wait for run time to say so.
			mistakes = append(mistakes, fmt.Sprintf("node %q can never reach graph.End: a run that got there would loop until the step limit", name))
		}
	}
	if len(mistakes) > 0 {
		return nil, errors.New("graph: " + strings.Join(mistakes, "; "))
	}

	g := &Graph[S]{start: b.start, nodes: make(map[string]compiled[S], len(b.nodes))}
	for name, fn := range b.nodes {
		g.nodes[name] = compiled[S]{run: fn, next: b.next[name], router: b.routers[name], targets: b.targets[name]}
	}
	return g, nil
}
