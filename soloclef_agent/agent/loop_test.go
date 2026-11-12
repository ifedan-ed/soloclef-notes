package agent_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func TestToolResultReentersConversationAndLoopStopsOnAnswer(t *testing.T) {
	var runs atomic.Int32
	m := script(
		modeltest.Reply{Text: "Let me look.", Calls: []model.ToolCall{call("c1", "search", `{"q":"go"}`)}},
		modeltest.Reply{Text: "Go is a language.", Usage: model.Usage{PromptTokens: 9, CompletionTokens: 4, TotalTokens: 13}},
	)
	a := build(t, agent.Config{Model: m, System: "Be brief.", Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}})

	var rec recorder
	res, err := a.Start(context.Background(), "t1", user("What is Go?"), rec.sink)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != agent.Finished || res.Answer != "Go is a language." || res.Steps != 3 || res.FinishReason != "stop" {
		t.Fatalf("result = %+v", res)
	}
	if runs.Load() != 1 {
		t.Fatalf("tool ran %d times, want 1", runs.Load())
	}

	// The second model call is shown the tool's result, after the assistant
	// message that asked for it, under the system prompt.
	reqs := m.Requests()
	equal(t, "second request", roles(reqs[1].Messages), []string{
		"system:Be brief.",
		"user:What is Go?",
		"assistant:Let me look.->c1",
		`tool[c1]:search got {"q":"go"}`,
	})
	if len(reqs[1].Tools) != 1 || reqs[1].Tools[0].Name != "search" {
		t.Fatalf("tools sent = %+v", reqs[1].Tools)
	}

	equal(t, "event order", rec.trace(), strings.Join([]string{
		"step-started", "text-delta", "text-delta", "text-delta",
		"tool-call-started(c1)", "tool-call-delta(c1)", "tool-call-delta(c1)", "tool-call-ready(c1)",
		"model-finished", "step-finished",
		"step-started", "tool-result(c1)", "step-finished",
		"step-started", "text-delta", "text-delta", "text-delta", "text-delta",
		"model-finished", "step-finished", "done",
	}, " "))
	equal(t, "streamed text", rec.text(), "Let me look.Go is a language.")
	if got := rec.of(event.ModelFinished)[1].Usage.TotalTokens; got != 13 {
		t.Fatalf("usage total = %d, want 13", got)
	}
}

func TestSystemPromptAndPreparedMessagesAreSentButNeverStored(t *testing.T) {
	m := script(modeltest.Reply{Text: "one"}, modeltest.Reply{Text: "two"})
	a := build(t, agent.Config{
		Model:  m,
		System: "SYSTEM",
		Prepare: func(_ context.Context, stored []model.Message) ([]model.Message, error) {
			// What a memory block does: ride along on every call.
			return append([]model.Message{model.User("MEMORY")}, stored...), nil
		},
	})
	ctx := context.Background()
	for _, q := range []string{"first", "second"} {
		if _, err := a.Start(ctx, "t1", user(q), event.Discard); err != nil {
			t.Fatal(err)
		}
	}

	equal(t, "second request", roles(m.Requests()[1].Messages), []string{
		"system:SYSTEM", "user:MEMORY", "user:first", "assistant:one", "user:second",
	})
	st, status, err := a.Load(ctx, "t1")
	if err != nil || status != checkpoint.Done {
		t.Fatalf("load: %v, status %q", err, status)
	}
	// Two turns, one copy of nothing: the thread holds only what was said.
	equal(t, "stored", roles(st.Messages), []string{"user:first", "assistant:one", "user:second", "assistant:two"})
}

func TestStepLimitIsAClearErrorCountedPerTurn(t *testing.T) {
	var runs atomic.Int32
	// A model that never stops asking for the tool.
	forever := modeltest.Func(func(_ context.Context, req model.Request, _ func(model.Chunk)) (model.Response, error) {
		return model.Response{Message: model.Message{ToolCalls: []model.ToolCall{call("", "search", `{}`)}}}, nil
	})
	store := checkpoint.NewMemory()
	a := build(t, agent.Config{Model: forever, MaxSteps: 5, Store: store, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}})
	ctx := context.Background()

	var rec recorder
	_, err := a.Start(ctx, "t1", user("go"), rec.sink)
	if !errors.Is(err, agent.ErrStepLimit) {
		t.Fatalf("err = %v, want ErrStepLimit", err)
	}
	// Five steps: model, tools, model, tools, model.
	if runs.Load() != 2 {
		t.Fatalf("tool ran %d times in 5 steps, want 2", runs.Load())
	}
	events := rec.all()
	if last := events[len(events)-1]; last.Kind != event.Error || !errors.Is(last.Err, agent.ErrStepLimit) {
		t.Fatalf("last event = %+v, want the step-limit error", last)
	}

	// The thread is not worn out: a new turn gets a full count of its own,
	// and the call the old turn left unanswered is closed, so the
	// conversation the model is shown is still valid.
	answering := script(modeltest.Reply{Text: "ok"})
	b := build(t, agent.Config{Model: answering, MaxSteps: 5, Store: store, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}})
	res, err := b.Start(ctx, "t1", user("again"), event.Discard)
	if err != nil || res.Steps != 1 {
		t.Fatalf("new turn: %+v, %v", res, err)
	}
	sent := roles(answering.Requests()[0].Messages)
	if got := sent[len(sent)-2]; !strings.HasPrefix(got, "tool[call_5_0]:error: this tool call was interrupted") {
		t.Fatalf("unanswered call was not closed: %q", got)
	}
}

func TestModelErrorEndsTheRunAndIsReturnedIntact(t *testing.T) {
	refusal := &model.Error{Status: 400, Message: "context too long"}
	a := build(t, agent.Config{Model: script(modeltest.Reply{Text: "partial", Err: refusal})})

	var rec recorder
	_, err := a.Start(context.Background(), "t1", user("hi"), rec.sink)
	var got *model.Error
	if !errors.As(err, &got) || got.Status != 400 {
		t.Fatalf("err = %v, want the model's own error", err)
	}
	equal(t, "events", rec.trace(), "step-started text-delta error")
}

func TestUnknownToolAndBrokenArgumentsAreAnsweredNotFatal(t *testing.T) {
	var runs atomic.Int32
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{
			call("c1", "serach", `{}`),
			call("c2", "search", `{"q": `),
			call("c3", "search", ``),
		}},
		modeltest.Reply{Text: "done"},
	)
	a := build(t, agent.Config{Model: m, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}})
	if _, err := a.Start(context.Background(), "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	sent := roles(m.Requests()[1].Messages)
	equal(t, "results", sent[2:], []string{
		`tool[c1]:error: there is no tool named "serach". The tools are: search.`,
		"tool[c2]:error: the arguments were not a JSON object. Call the tool again with arguments that match its schema.",
		// No argument text at all is a call with no arguments, and runs.
		"tool[c3]:search got {}",
	})
}

func TestToolErrorIsShownToTheModelAndAPanicStopsTheRun(t *testing.T) {
	failing := tool.Tool{Name: "fetch", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(context.Context, tool.Call) (string, error) { return "", errors.New("host unreachable") }}
	m := script(modeltest.Reply{Calls: []model.ToolCall{call("c1", "fetch", `{}`)}}, modeltest.Reply{Text: "sorry"})
	a := build(t, agent.Config{Model: m, Tools: []tool.Tool{failing}})
	var rec recorder
	if _, err := a.Start(context.Background(), "t1", user("go"), rec.sink); err != nil {
		t.Fatal(err)
	}
	if res := rec.of(event.ToolResult)[0]; !res.IsError || res.Text != "error: host unreachable" {
		t.Fatalf("tool result = %+v", res)
	}

	panicking := tool.Tool{Name: "boom", Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: true,
		Run: func(context.Context, tool.Call) (string, error) { panic("nil map") }}
	b := build(t, agent.Config{
		Model: script(modeltest.Reply{Calls: []model.ToolCall{call("c1", "boom", `{}`), call("c2", "boom", `{}`)}}),
		Tools: []tool.Tool{panicking},
	})
	_, err := b.Start(context.Background(), "t2", user("go"), event.Discard)
	var panicked *agent.PanicError
	if !errors.As(err, &panicked) || panicked.Tool != "boom" || len(panicked.Stack) == 0 {
		t.Fatalf("err = %v, want a PanicError naming the tool", err)
	}
}

func TestEmptyAndRepeatedCallIDsAreMadeUnique(t *testing.T) {
	var runs atomic.Int32
	m := script(
		// Two calls with no id, then a step that reuses an id from before.
		modeltest.Reply{Calls: []model.ToolCall{call("", "search", `{"n":1}`), call("", "search", `{"n":2}`)}},
		modeltest.Reply{Calls: []model.ToolCall{call("call_2_0", "search", `{"n":3}`), call("x", "search", `{"n":4}`), call("x", "search", `{"n":5}`)}},
		// An id with a slash, which is the separator of a delegate's path.
		modeltest.Reply{Calls: []model.ToolCall{call("a/b", "search", `{"n":6}`)}},
		modeltest.Reply{Text: "done"},
	)
	a := build(t, agent.Config{Model: m, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}})
	var rec recorder
	if _, err := a.Start(context.Background(), "t1", user("go"), rec.sink); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, ev := range rec.of(event.ToolResult) {
		if ev.CallID == "" || seen[ev.CallID] {
			t.Fatalf("call id %q is empty or repeated in %s", ev.CallID, rec.trace())
		}
		seen[ev.CallID] = true
	}
	// The ids the model is shown are the settled ones, in both the assistant
	// message and the tool messages that answer it.
	equal(t, "conversation", roles(m.Requests()[3].Messages)[1:], []string{
		"assistant:->call_1_0,call_1_1",
		`tool[call_1_0]:search got {"n":1}`,
		`tool[call_1_1]:search got {"n":2}`,
		"assistant:->call_2_0,x,call_3_2",
		`tool[call_2_0]:search got {"n":3}`,
		`tool[x]:search got {"n":4}`,
		`tool[call_3_2]:search got {"n":5}`,
		"assistant:->call_5_0",
		`tool[call_5_0]:search got {"n":6}`,
	})
	// A started event and its deltas carry the same settled id.
	if !strings.Contains(rec.trace(), "tool-call-started(call_3_2) tool-call-delta(call_3_2)") {
		t.Fatalf("stream ids disagree with stored ids: %s", rec.trace())
	}
}

func TestACallTheAdapterDidNotStreamStillGetsStartedReadyResult(t *testing.T) {
	var runs atomic.Int32
	m := script(
		modeltest.Reply{Silent: true, Calls: []model.ToolCall{call("c1", "search", `{}`)}},
		modeltest.Reply{Silent: true, Text: "done"},
	)
	a := build(t, agent.Config{Model: m, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)}})
	var rec recorder
	if _, err := a.Start(context.Background(), "t1", user("go"), rec.sink); err != nil {
		t.Fatal(err)
	}
	equal(t, "events", rec.trace(), "step-started tool-call-started(c1) tool-call-ready(c1) model-finished step-finished "+
		"step-started tool-result(c1) step-finished step-started model-finished step-finished done")
}

func TestNewRefusesAnIncompleteConfig(t *testing.T) {
	ok := func() agent.Config {
		return agent.Config{Model: script(), MaxSteps: 1, MaxParallelTools: 1, Store: checkpoint.NewMemory()}
	}
	run := func(context.Context, tool.Call) (string, error) { return "", nil }
	good := tool.Tool{Name: "a", Parameters: []byte(anyObject), Approval: tool.Auto, Run: run}

	cases := map[string]func(*agent.Config){
		"Model is not set":                       func(c *agent.Config) { c.Model = nil },
		"Store is not set":                       func(c *agent.Config) { c.Store = nil },
		"MaxSteps must be at least 1":            func(c *agent.Config) { c.MaxSteps = 0 },
		"MaxParallelTools must be at least 1":    func(c *agent.Config) { c.MaxParallelTools = 0 },
		"two tools are named":                    func(c *agent.Config) { c.Tools = []tool.Tool{good, good} },
		"does not say whether it needs approval": func(c *agent.Config) { c.Tools = []tool.Tool{{Name: "a", Parameters: []byte(anyObject), Run: run}} },
		"must be 1 to 64 characters": func(c *agent.Config) {
			c.Tools = []tool.Tool{{Name: "has space", Parameters: []byte(anyObject), Approval: tool.Auto, Run: run}}
		},
		"has no Run function": func(c *agent.Config) {
			c.Tools = []tool.Tool{{Name: "a", Parameters: []byte(anyObject), Approval: tool.Auto}}
		},
		"Parameters must be a JSON Schema object": func(c *agent.Config) { c.Tools = []tool.Tool{{Name: "a", Approval: tool.Auto, Run: run}} },
		"delegate helper has no Agent":            func(c *agent.Config) { c.Delegates = []agent.Delegate{{Name: "helper"}} },
		"two tools or delegates are named": func(c *agent.Config) {
			c.Tools = []tool.Tool{good}
			c.Delegates = []agent.Delegate{{Name: "a", Agent: build(t, ok())}}
		},
	}
	for want, breakIt := range cases {
		cfg := ok()
		breakIt(&cfg)
		if _, err := agent.New(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("New error = %v, want one containing %q", err, want)
		}
	}
	if _, err := agent.New(ok()); err != nil {
		t.Fatalf("a complete config was refused: %v", err)
	}
}

func TestRunsNeedAThreadAndASink(t *testing.T) {
	a := build(t, agent.Config{Model: script()})
	if _, err := a.Start(context.Background(), "", user("hi"), event.Discard); err == nil {
		t.Fatal("an empty thread id was accepted")
	}
	if _, err := a.Start(context.Background(), "t1", user("hi"), nil); err == nil || !strings.Contains(err.Error(), "event.Discard") {
		t.Fatalf("a nil sink was accepted or the error does not say what to pass: %v", err)
	}
}
