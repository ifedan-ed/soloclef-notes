package agent_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func TestContinueCarriesOnAFailedTurnFromItsLastSavedStep(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var runs atomic.Int32
	tools := []tool.Tool{countingTool("search", tool.Auto, &runs)}

	// The model fails on its second call, after the tool has run.
	a := build(t, agent.Config{Store: store, Tools: tools, Model: script(
		modeltest.Reply{Calls: []model.ToolCall{call("c1", "search", `{}`)}},
		modeltest.Reply{Err: &model.Error{Status: 503, Retryable: true, Message: "overloaded"}},
	)})
	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err == nil {
		t.Fatal("the failing model call did not fail the run")
	}

	// A new process continues. The step that failed is run again; the steps
	// before it, the tool call among them, are not.
	m := script(modeltest.Reply{Text: "recovered"})
	b := build(t, agent.Config{Store: store, Tools: tools, Model: m})
	var rec recorder
	res, err := b.Continue(ctx, "t1", rec.sink)
	if err != nil || res.Answer != "recovered" || res.Steps != 3 {
		t.Fatalf("continue: %+v, %v", res, err)
	}
	if runs.Load() != 1 {
		t.Fatalf("tool ran %d times, want 1", runs.Load())
	}
	equal(t, "continued request", roles(m.Requests()[0].Messages), []string{"user:go", "assistant:->c1", "tool[c1]:search got {}"})
	equal(t, "continued events", rec.trace(), "step-started text-delta model-finished step-finished done")

	if _, err := b.Continue(ctx, "t1", event.Discard); !errors.Is(err, agent.ErrNothingToContinue) {
		t.Fatalf("continue of a finished thread: %v", err)
	}
	if _, err := b.Continue(ctx, "never-ran", event.Discard); !errors.Is(err, agent.ErrNothingToContinue) {
		t.Fatalf("continue of an unknown thread: %v", err)
	}
}

func TestTwoRunsOnOneThreadCannotInterleave(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()

	// The first run is held inside its model call while a second run takes
	// the thread and finishes a whole turn.
	release := make(chan struct{})
	entered := make(chan struct{})
	slow := modeltest.Func(func(context.Context, model.Request, func(model.Chunk)) (model.Response, error) {
		close(entered)
		<-release
		return model.Response{Message: model.Message{Content: "from the first run"}}, nil
	})
	first := build(t, agent.Config{Store: store, Model: slow})
	firstErr := make(chan error, 1)
	go func() {
		_, err := first.Start(ctx, "t1", user("one"), event.Discard)
		firstErr <- err
	}()
	<-entered

	second := build(t, agent.Config{Store: store, Model: script(modeltest.Reply{Text: "from the second run"})})
	if _, err := second.Start(ctx, "t1", user("two"), event.Discard); err != nil {
		t.Fatal(err)
	}

	close(release)
	if err := <-firstErr; !errors.Is(err, checkpoint.ErrConflict) {
		t.Fatalf("first run's err = %v, want checkpoint.ErrConflict", err)
	}
	// The thread is the second run's, whole, with nothing of the first run's
	// answer spliced into it.
	st, _, err := second.Load(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "thread", roles(st.Messages), []string{"user:one", "user:two", "assistant:from the second run"})
}

func TestEditRewindsAConversation(t *testing.T) {
	ctx := context.Background()
	m := script(modeltest.Reply{Text: "first answer"}, modeltest.Reply{Text: "second answer"}, modeltest.Reply{Text: "better answer"})
	a := build(t, agent.Config{Model: m})
	for _, q := range []string{"q1", "q2"} {
		if _, err := a.Start(ctx, "t1", user(q), event.Discard); err != nil {
			t.Fatal(err)
		}
	}

	// Regenerate the second answer: drop the last exchange, ask again.
	err := a.Edit(ctx, "t1", func(st *agent.State) error {
		st.Messages = st.Messages[:2]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(ctx, "t1", user("q2"), event.Discard); err != nil {
		t.Fatal(err)
	}
	st, _, _ := a.Load(ctx, "t1")
	equal(t, "after regenerate", roles(st.Messages), []string{"user:q1", "assistant:first answer", "user:q2", "assistant:better answer"})

	if err := a.Edit(ctx, "nope", func(*agent.State) error { return nil }); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("edit of an unknown thread: %v", err)
	}
}

func TestStepFinishedIsOnlySentOnceTheStepIsSaved(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var runs atomic.Int32
	a := build(t, agent.Config{Store: store, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}, Model: script(
		modeltest.Reply{Calls: []model.ToolCall{call("c1", "search", `{}`)}},
		modeltest.Reply{Text: "done"},
	)})
	// At each StepFinished, what is in the store must already include that
	// step: a process killed right after the event would resume past it.
	wantNode := map[int]string{1: "tools", 2: "model", 3: ""}
	_, err := a.Start(ctx, "t1", user("go"), func(ev event.Event) {
		if ev.Kind != event.StepFinished {
			return
		}
		cp, err := store.Load(ctx, "t1")
		if err != nil || cp.Node != wantNode[ev.Step] {
			t.Errorf("at step-finished %d the store's next step is %q (%v), want %q", ev.Step, cp.Node, err, wantNode[ev.Step])
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
