package graph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/graph"
)

// draft is the state of a small write-and-review pipeline.
type draft struct {
	Text     string `json:"text"`
	Attempts int    `json:"attempts"`
	Approved bool   `json:"approved"`
	Trail    string `json:"trail"`
}

func visit(name string, change func(*draft)) graph.Node[draft] {
	return func(_ context.Context, _ graph.Step, d draft) (draft, error) {
		d.Trail += name + " "
		if change != nil {
			change(&d)
		}
		return d, nil
	}
}

func run(store checkpoint.Store, steps int, sink event.Sink) graph.Run {
	return graph.Run{Thread: "t1", MaxSteps: steps, Store: store, Sink: sink}
}

// reviewLoop is write -> review, back to write until three attempts, then
// publish.
func reviewLoop(t *testing.T) *graph.Graph[draft] {
	t.Helper()
	g, err := graph.New[draft]().
		Node("write", visit("write", func(d *draft) { d.Attempts++ })).
		Node("review", visit("review", nil)).
		Node("publish", visit("publish", nil)).
		Start("write").
		Edge("write", "review").
		Branch("review", func(_ context.Context, d draft) (graph.Route, error) {
			if d.Attempts < 3 {
				return graph.To("write"), nil
			}
			return graph.To("publish"), nil
		}, "write", "publish").
		Edge("publish", graph.End).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestNodesEdgesAndABranchRunToTheEnd(t *testing.T) {
	var kinds []string
	res, err := reviewLoop(t).Start(context.Background(), run(checkpoint.NewMemory(), 20, func(ev event.Event) {
		if ev.Kind == event.StepStarted || ev.Kind == event.Done {
			kinds = append(kinds, ev.Kind.String()+":"+ev.Node)
		}
	}), draft{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != graph.Finished || res.Steps != 7 || res.State.Trail != "write review write review write review publish " {
		t.Fatalf("result = %+v", res)
	}
	want := "step-started:write step-started:review step-started:write step-started:review step-started:write step-started:review step-started:publish done:"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("events = %s", got)
	}
}

func TestALoopThatDoesNotEndIsStoppedByTheStepLimitWithAClearError(t *testing.T) {
	g, err := graph.New[draft]().
		Node("spin", visit("spin", nil)).
		Start("spin").
		// The exit exists, so the shape is legal; this router never takes it.
		Branch("spin", func(context.Context, draft) (graph.Route, error) { return graph.To("spin"), nil }, "spin", graph.End).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	store := checkpoint.NewMemory()
	_, err = g.Start(context.Background(), run(store, 4, event.Discard), draft{})
	if !errors.Is(err, graph.ErrStepLimit) || !strings.Contains(err.Error(), `ran 4 steps and was about to run "spin"`) {
		t.Fatalf("err = %v", err)
	}
	// The work done is saved, not lost with the error.
	if cp, _ := store.Load(context.Background(), "t1"); cp.Seq != 4 || cp.Status != checkpoint.Running {
		t.Fatalf("checkpoint after the limit = %+v", cp)
	}
}

func TestBuildRefusesAGraphThatCannotWork(t *testing.T) {
	noop := visit("x", nil)
	never := func(context.Context, draft) (graph.Route, error) { return graph.To("a"), nil }
	cases := map[string]*graph.Builder[draft]{
		`the start "" is not a node`:                  graph.New[draft]().Node("a", noop).Edge("a", graph.End),
		`node "a" has no way out`:                     graph.New[draft]().Node("a", noop).Start("a"),
		`goes to "b", which is not a node`:            graph.New[draft]().Node("a", noop).Start("a").Edge("a", "b"),
		`node "b" can never be reached`:               graph.New[draft]().Node("a", noop).Node("b", noop).Start("a").Edge("a", graph.End).Edge("b", graph.End),
		`node "a" can never reach graph.End`:          graph.New[draft]().Node("a", noop).Node("b", noop).Start("a").Edge("a", "b").Edge("b", "a"),
		`node "a" has two ways out`:                   graph.New[draft]().Node("a", noop).Start("a").Edge("a", graph.End).Branch("a", never, "a"),
		`node "a" is added twice`:                     graph.New[draft]().Node("a", noop).Node("a", noop).Start("a").Edge("a", graph.End),
		`"__end__" cannot be a node's name`:           graph.New[draft]().Node(graph.End, noop),
		`the branch from "a" lists no targets`:        graph.New[draft]().Node("a", noop).Start("a").Branch("a", never),
		`an edge leaves "ghost", which is not a node`: graph.New[draft]().Node("a", noop).Start("a").Edge("a", graph.End).Edge("ghost", "a"),
	}
	for want, b := range cases {
		if _, err := b.Build(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Build error = %v, want one containing %q", err, want)
		}
	}
}

func TestARouterThatLeavesItsDeclaredTargetsIsStopped(t *testing.T) {
	g, err := graph.New[draft]().
		Node("a", visit("a", nil)).Node("b", visit("b", nil)).
		Start("a").
		Branch("a", func(context.Context, draft) (graph.Route, error) { return graph.To("b"), nil }, graph.End).
		Edge("b", graph.End).
		Build()
	// b is unreachable through the declared targets, so this is caught at
	// build time already; a graph where it is reachable another way is
	// caught at run time below.
	if err == nil || g != nil {
		t.Fatalf("Build = %v, %v", g, err)
	}

	g, err = graph.New[draft]().
		Node("a", visit("a", nil)).Node("b", visit("b", nil)).
		Start("a").
		Branch("a", func(context.Context, draft) (graph.Route, error) { return graph.To("nowhere"), nil }, "b", graph.End).
		Edge("b", graph.End).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.Start(context.Background(), run(checkpoint.NewMemory(), 5, event.Discard), draft{})
	if err == nil || !strings.Contains(err.Error(), `chose "nowhere", which it did not list`) {
		t.Fatalf("err = %v", err)
	}
}

// approvalGraph writes a draft, pauses for a person, then publishes or
// rewrites depending on what the person said.
func approvalGraph(t *testing.T, publishes *int) *graph.Graph[draft] {
	t.Helper()
	g, err := graph.New[draft]().
		Node("write", visit("write", func(d *draft) { d.Attempts++; d.Text = "draft" })).
		Node("decide", visit("decide", nil)).
		Node("publish", visit("publish", func(*draft) { *publishes++ })).
		Start("write").
		Branch("write", func(context.Context, draft) (graph.Route, error) { return graph.PauseBefore("decide"), nil }, "decide").
		Branch("decide", func(_ context.Context, d draft) (graph.Route, error) {
			if d.Approved {
				return graph.To("publish"), nil
			}
			return graph.To("write"), nil
		}, "publish", "write").
		Edge("publish", graph.End).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAPausedRunResumesWithTheAnswerAfterARestart(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	publishes := 0

	res, err := approvalGraph(t, &publishes).Start(ctx, run(store, 10, event.Discard), draft{})
	if err != nil || res.Outcome != graph.Paused || res.State.Trail != "write " {
		t.Fatalf("start: %+v, %v", res, err)
	}

	// A new Graph value, as after a restart; only the store is shared. The
	// answer is delivered by writing it into the state.
	res, err = approvalGraph(t, &publishes).Resume(ctx, run(store, 10, event.Discard), func(d draft) (draft, error) {
		d.Approved = true
		return d, nil
	})
	if err != nil || res.Outcome != graph.Finished {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	// "write" ran once: nothing was repeated to deliver the answer.
	if res.State.Trail != "write decide publish " || res.State.Attempts != 1 || publishes != 1 {
		t.Fatalf("after resume: %+v, publishes %d", res.State, publishes)
	}
	if _, err := approvalGraph(t, &publishes).Resume(ctx, run(store, 10, event.Discard), func(d draft) (draft, error) { return d, nil }); !errors.Is(err, graph.ErrFinished) {
		t.Fatalf("resume of a finished thread: %v", err)
	}
}

func TestAFailedNodeLeavesTheThreadAtTheLastGoodStepAndResumeRetriesIt(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	fail := true
	g, err := graph.New[draft]().
		Node("a", visit("a", nil)).
		Node("b", func(_ context.Context, _ graph.Step, d draft) (draft, error) {
			if fail {
				d.Trail += "half-done "
				return d, errors.New("renderer crashed")
			}
			d.Trail += "b "
			return d, nil
		}).
		Start("a").Edge("a", "b").Edge("b", graph.End).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	var last event.Event
	_, err = g.Start(ctx, run(store, 10, func(ev event.Event) { last = ev }), draft{})
	if err == nil || !strings.Contains(err.Error(), `node "b": renderer crashed`) || last.Kind != event.Error {
		t.Fatalf("err = %v, last event = %+v", err, last)
	}

	fail = false
	res, err := g.Resume(ctx, run(store, 10, event.Discard), func(d draft) (draft, error) { return d, nil })
	// "a" was not run again, and what "b" wrote before failing was dropped.
	if err != nil || res.State.Trail != "a b " {
		t.Fatalf("resume: %+v, %v", res, err)
	}
}

func TestCancellationStopsBetweenNodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g, err := graph.New[draft]().
		Node("a", visit("a", func(*draft) { cancel() })).
		Node("b", visit("b", nil)).
		Start("a").Edge("a", "b").Edge("b", graph.End).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	store := checkpoint.NewMemory()
	_, err = g.Start(ctx, run(store, 10, event.Discard), draft{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestARunWithoutALimitAStoreOrASinkIsRefused(t *testing.T) {
	g := reviewLoop(t)
	store := checkpoint.NewMemory()
	for want, r := range map[string]graph.Run{
		"Thread is empty":       {MaxSteps: 1, Store: store, Sink: event.Discard},
		"MaxSteps must be":      {Thread: "t", Store: store, Sink: event.Discard},
		"Store is not set":      {Thread: "t", MaxSteps: 1, Sink: event.Discard},
		"pass event.Discard to": {Thread: "t", MaxSteps: 1, Store: store},
	} {
		if _, err := g.Start(context.Background(), r, draft{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Start error = %v, want one containing %q", err, want)
		}
	}
}
