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

func TestCancellingMidStreamStopsTheRunAndLeavesTheThreadUsable(t *testing.T) {
	store := checkpoint.NewMemory()
	a := build(t, agent.Config{Store: store, Model: script(modeltest.Reply{Text: "Half an ans", Hang: true})})

	ctx, cancel := context.WithCancel(context.Background())
	var rec recorder
	_, err := a.Start(ctx, "t1", user("question"), func(ev event.Event) {
		rec.sink(ev)
		// Stop is pressed while the answer is arriving.
		if ev.Kind == event.TextDelta && ev.Text == "ans" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	equal(t, "events", rec.trace(), "step-started text-delta text-delta text-delta error")

	// The half answer was never stored, so the conversation has no assistant
	// message that the model did not finish. The user's message was: the
	// thread was claimed before the model was called.
	st, status, err := a.Load(context.Background(), "t1")
	if err != nil || status != checkpoint.Running {
		t.Fatalf("load: %v, status %q", err, status)
	}
	equal(t, "stored after cancel", roles(st.Messages), []string{"user:question"})

	m := script(modeltest.Reply{Text: "A whole answer."})
	b := build(t, agent.Config{Store: store, Model: m})
	if _, err := b.Start(context.Background(), "t1", user("try again"), event.Discard); err != nil {
		t.Fatal(err)
	}
	equal(t, "next turn", roles(m.Requests()[0].Messages), []string{"user:question", "user:try again"})
}

func TestCancellingMidToolsKeepsFinishedResultsAndClosesTheRest(t *testing.T) {
	store := checkpoint.NewMemory()
	var fastRuns atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())

	fast := countingTool("fast", tool.Auto, &fastRuns)
	fast.Parallel = true
	stuck := tool.Tool{Name: "stuck", Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: true,
		Run: func(ctx context.Context, _ tool.Call) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}
	a := build(t, agent.Config{Store: store, Tools: []tool.Tool{fast, stuck}, Model: script(
		modeltest.Reply{Calls: []model.ToolCall{call("f1", "fast", `{}`), call("s1", "stuck", `{}`)}},
	)})

	_, err := a.Start(ctx, "t1", user("go"), func(ev event.Event) {
		// Stop is pressed once the fast tool has finished and the other has not.
		if ev.Kind == event.ToolResult && ev.CallID == "f1" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// The next turn is shown the result that was produced, and an honest
	// "interrupted" for the call that was not; neither tool is run again.
	m := script(modeltest.Reply{Text: "ok"})
	b := build(t, agent.Config{Store: store, Tools: []tool.Tool{fast, stuck}, Model: m})
	if _, err := b.Start(context.Background(), "t1", user("and now?"), event.Discard); err != nil {
		t.Fatal(err)
	}
	equal(t, "next turn", roles(m.Requests()[0].Messages), []string{
		"user:go",
		"assistant:->f1,s1",
		"tool[f1]:fast got {}",
		"tool[s1]:error: this tool call was interrupted before it finished. Whether it took effect is unknown. Check before trying it again.",
		"user:and now?",
	})
	if fastRuns.Load() != 1 {
		t.Fatalf("fast ran %d times, want 1", fastRuns.Load())
	}
}

func TestACancelledContextIsRefusedBeforeAnythingRuns(t *testing.T) {
	m := script(modeltest.Reply{Text: "never"})
	a := build(t, agent.Config{Model: m})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Start(ctx, "t1", user("hi"), event.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(m.Requests()) != 0 {
		t.Fatal("the model was called on a cancelled context")
	}
}
