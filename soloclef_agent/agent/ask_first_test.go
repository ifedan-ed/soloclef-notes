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

// With the person's own policy, a tool runs without asking whatever its
// Approval says, and only the tools the person marked ask first wait.
func TestAskFirstIsTheWholePolicy(t *testing.T) {
	ctx := context.Background()
	var reads, sends, fetches atomic.Int32
	marked := map[string]bool{"fetch": true}
	var consulted []string
	a := build(t, agent.Config{
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{call("r1", "read", `{}`), call("s1", "send", `{}`), call("f1", "fetch", `{}`)}},
			modeltest.Reply{Text: "done"},
		),
		Tools: []tool.Tool{
			countingTool("read", tool.Auto, &reads),
			countingTool("send", tool.Ask, &sends),
			countingTool("fetch", tool.AlwaysAsk, &fetches),
		},
		AskFirst: func(_ context.Context, c tool.Call) (bool, error) {
			consulted = append(consulted, c.Name)
			return marked[c.Name], nil
		},
	})
	var rec recorder
	res, err := a.Start(ctx, "t1", user("go"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	// Every call is put to the person's policy, the Auto one too: the
	// person may mark any tool.
	if strings.Join(consulted, " ") != "read send fetch" {
		t.Errorf("AskFirst was consulted for %v", consulted)
	}
	// The marked tool asks, and "always allow" is offered for it although
	// the tool itself says it always asks: under the person's policy that
	// answer means "stop asking", which the person may always choose.
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "f1" || !asked[0].AllowAlways {
		t.Fatalf("approval events = %+v", asked)
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "f1", Verdict: agent.ApproveAlways}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || sends.Load() != 1 || fetches.Load() != 1 {
		t.Fatalf("runs: read %d, send %d, fetch %d, want one each", reads.Load(), sends.Load(), fetches.Load())
	}
}

// The policy is read at each decision, and "always allow" covers the rest
// of the turn without a second question.
func TestAskFirstIsReadLiveAndAlwaysAllowLastsTheTurn(t *testing.T) {
	ctx := context.Background()
	var sends atomic.Int32
	asks := false
	consulted := 0
	a := build(t, agent.Config{
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{call("s1", "send", `{}`)}},
			modeltest.Reply{Calls: []model.ToolCall{call("s2", "send", `{}`)}},
			modeltest.Reply{Calls: []model.ToolCall{call("s3", "send", `{}`)}},
			modeltest.Reply{Text: "done"},
		),
		Tools: []tool.Tool{countingTool("send", tool.Ask, &sends)},
		AskFirst: func(context.Context, tool.Call) (bool, error) {
			consulted++
			return asks, nil
		},
	})
	// The first call runs unasked; the person marks the tool while its
	// result comes in, as the settings page would from another request.
	var rec recorder
	res, err := a.Start(ctx, "t1", user("go"), func(ev event.Event) {
		if ev.Kind == event.ToolResult {
			asks = true
		}
		rec.sink(ev)
	})
	if err != nil || res.Outcome != agent.Paused || sends.Load() != 1 {
		t.Fatalf("start: %+v, %v; sends %d", res, err, sends.Load())
	}
	if asked := rec.of(event.ApprovalNeeded); len(asked) != 1 || asked[0].CallID != "s2" {
		t.Fatalf("the second call should have asked: %s", rec.trace())
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "s2", Verdict: agent.ApproveAlways}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	// s3 is covered by the turn's "always allow" and is not put to the
	// policy at all.
	if sends.Load() != 3 || consulted != 2 {
		t.Fatalf("sends %d, AskFirst consulted %d times; want 3 and 2", sends.Load(), consulted)
	}
}

// A policy that cannot answer stops the turn: it is neither a yes nor a no.
func TestAskFirstThatFailsStopsTheTurn(t *testing.T) {
	var sends atomic.Int32
	a := build(t, agent.Config{
		Model:    script(modeltest.Reply{Calls: []model.ToolCall{call("s1", "send", `{}`)}}),
		Tools:    []tool.Tool{countingTool("send", tool.Auto, &sends)},
		AskFirst: func(context.Context, tool.Call) (bool, error) { return false, errors.New("database is down") },
	})
	if _, err := a.Start(context.Background(), "t1", user("go"), event.Discard); err == nil || !strings.Contains(err.Error(), "database is down") {
		t.Fatalf("err = %v, want the policy's failure", err)
	}
	if sends.Load() != 0 {
		t.Fatalf("send ran %d times on a failed policy", sends.Load())
	}
}

// Two policies for one agent is a mistake made at build time.
func TestAskFirstAndTrustedAreNotBothSet(t *testing.T) {
	var runs atomic.Int32
	_, err := agent.New(agent.Config{
		Model: script(), Store: checkpoint.NewMemory(), MaxSteps: 1, MaxParallelTools: 1,
		Tools:    []tool.Tool{countingTool("send", tool.Ask, &runs)},
		Trusted:  func(context.Context, tool.Call) (bool, error) { return true, nil },
		AskFirst: func(context.Context, tool.Call) (bool, error) { return false, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("err = %v, want a refusal of two policies", err)
	}
}
