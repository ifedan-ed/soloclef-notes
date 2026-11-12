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

func TestApprovalPausesAndResumesAcrossARestart(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var reads, deletes atomic.Int32
	tools := func() []tool.Tool {
		return []tool.Tool{countingTool("read", tool.Auto, &reads), countingTool("delete", tool.Ask, &deletes)}
	}

	// The first process: the model asks to read and to delete in one batch.
	before := build(t, agent.Config{Store: store, Tools: tools(), Model: script(
		modeltest.Reply{Calls: []model.ToolCall{call("r1", "read", `{}`), call("d1", "delete", `{"id":7}`)}},
	)})
	var rec recorder
	res, err := before.Start(ctx, "t1", user("clean up"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	// Nothing in a batch runs while one of its calls is still to be approved.
	if reads.Load() != 0 || deletes.Load() != 0 {
		t.Fatalf("ran before approval: read %d, delete %d", reads.Load(), deletes.Load())
	}
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "d1" || asked[0].Tool != "delete" || asked[0].Args != `{"id":7}` || !asked[0].AllowAlways {
		t.Fatalf("approval events = %+v", asked)
	}
	// The approval is announced once the pause is saved, just before it ends.
	if !strings.HasSuffix(rec.trace(), "step-finished approval-needed(d1) paused") {
		t.Fatalf("stream did not end paused: %s", rec.trace())
	}

	// A new turn must not be started over a waiting one.
	if _, err := before.Start(ctx, "t1", user("never mind"), event.Discard); !errors.Is(err, agent.ErrAwaitingApproval) {
		t.Fatalf("start over a paused thread: %v", err)
	}

	// The second process: a new Agent value, a new model, the same store.
	// It holds nothing of the first but what the store kept.
	m := script(modeltest.Reply{Text: "Deleted."})
	after := build(t, agent.Config{Store: store, Tools: tools(), Model: m})
	pending, err := after.Pending(ctx, "t1")
	if err != nil || len(pending) != 1 || pending[0].CallID != "d1" || pending[0].Args != `{"id":7}` {
		t.Fatalf("pending after restart = %+v, %v", pending, err)
	}

	var rec2 recorder
	res, err = after.Resume(ctx, "t1", []agent.Decision{{CallID: "d1", Verdict: agent.Approve}}, rec2.sink)
	if err != nil || res.Outcome != agent.Finished || res.Answer != "Deleted." {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	if reads.Load() != 1 || deletes.Load() != 1 {
		t.Fatalf("each tool must run exactly once: read %d, delete %d", reads.Load(), deletes.Load())
	}
	equal(t, "conversation after resume", roles(m.Requests()[0].Messages), []string{
		"user:clean up",
		"assistant:->r1,d1",
		"tool[r1]:read got {}",
		`tool[d1]:delete got {"id":7}`,
	})
	if _, err := after.Resume(ctx, "t1", nil, event.Discard); !errors.Is(err, agent.ErrNotPaused) {
		t.Fatalf("resume of a finished thread: %v", err)
	}
}

func TestRejectAndEditDecisions(t *testing.T) {
	ctx := context.Background()
	var deletes atomic.Int32
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{call("d1", "delete", `{"id":1}`), call("d2", "delete", `{"id":2}`)}},
		modeltest.Reply{Text: "ok"},
	)
	a := build(t, agent.Config{Model: m, Tools: []tool.Tool{countingTool("delete", tool.Ask, &deletes)}})
	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	var rec recorder
	_, err := a.Resume(ctx, "t1", []agent.Decision{
		{CallID: "d1", Verdict: agent.Reject, Reason: "that one is mine"},
		{CallID: "d2", Verdict: agent.Approve, Args: []byte(`{"id":22}`)},
	}, rec.sink)
	if err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 {
		t.Fatalf("delete ran %d times, want 1 (one rejected)", deletes.Load())
	}
	equal(t, "what the model was told", roles(m.Requests()[1].Messages)[2:], []string{
		"tool[d1]:The user declined this tool call and said: that one is mine",
		// The edited arguments are what ran.
		`tool[d2]:delete got {"id":22}`,
	})
	// A rejection is announced as that call's result, so its card can close.
	if got := rec.of(event.ToolResult); len(got) != 2 || got[0].CallID != "d1" || got[0].IsError {
		t.Fatalf("tool results = %+v", got)
	}
}

func TestAWrongSetOfDecisionsIsRefusedWholeAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	var asks, always atomic.Int32
	a := build(t, agent.Config{
		Model: script(modeltest.Reply{Calls: []model.ToolCall{call("a1", "ask", `{}`), call("b1", "always", `{}`)}}),
		Tools: []tool.Tool{countingTool("ask", tool.Ask, &asks), countingTool("always", tool.AlwaysAsk, &always)},
	})
	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}

	ok := agent.Decision{CallID: "a1", Verdict: agent.Approve}
	cases := map[string][]agent.Decision{
		`call "b1" (always) is waiting and was given no decision`: {ok},
		`names call "zz", which is not waiting`:                   {ok, {CallID: "b1", Verdict: agent.Approve}, {CallID: "zz", Verdict: agent.Approve}},
		`call "a1" was given two decisions`:                       {ok, ok, {CallID: "b1", Verdict: agent.Approve}},
		`asks every time and cannot be always allowed`:            {ok, {CallID: "b1", Verdict: agent.ApproveAlways}},
		`cannot take the verdict "answer"`:                        {ok, {CallID: "b1", Verdict: agent.Answer, Answer: []byte(`1`)}},
		`edited arguments for call "b1" are not a JSON object`:    {ok, {CallID: "b1", Verdict: agent.Approve, Args: []byte(`[1]`)}},
	}
	for want, decisions := range cases {
		_, err := a.Resume(ctx, "t1", decisions, event.Discard)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("resume error = %v, want one containing %q", err, want)
		}
	}
	// After six refusals the thread is exactly as it was: paused, both
	// calls waiting, nothing run.
	pending, err := a.Pending(ctx, "t1")
	if err != nil || len(pending) != 2 || asks.Load()+always.Load() != 0 {
		t.Fatalf("pending = %+v, %v; runs = %d", pending, err, asks.Load()+always.Load())
	}
	if pending[0].AllowAlways != true || pending[1].AllowAlways != false {
		t.Fatalf("AllowAlways should be offered for ask and not for always-ask: %+v", pending)
	}
}

func TestAlwaysAllowLastsOneTurnAndNeverCoversAnAlwaysAskTool(t *testing.T) {
	ctx := context.Background()
	var sends, fetches atomic.Int32
	trustedAsked := 0
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{call("s1", "send", `{}`), call("f1", "fetch", `{}`)}},
		// Same turn, later batch: send was always-allowed, fetch must ask again.
		modeltest.Reply{Calls: []model.ToolCall{call("s2", "send", `{}`), call("f2", "fetch", `{}`)}},
		modeltest.Reply{Text: "turn one done"},
		// Next turn: the always-allow is gone.
		modeltest.Reply{Calls: []model.ToolCall{call("s3", "send", `{}`)}},
	)
	a := build(t, agent.Config{
		Model: m,
		Tools: []tool.Tool{countingTool("send", tool.Ask, &sends), countingTool("fetch", tool.AlwaysAsk, &fetches)},
		Trusted: func(_ context.Context, c tool.Call) (bool, error) {
			trustedAsked++
			if c.Name == "fetch" {
				t.Error("Trusted was consulted for an always-ask tool")
			}
			return false, nil
		},
	})

	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	var rec recorder
	res, err := a.Resume(ctx, "t1", []agent.Decision{
		{CallID: "s1", Verdict: agent.ApproveAlways},
		{CallID: "f1", Verdict: agent.Approve},
	}, rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "f2" || asked[0].AllowAlways {
		t.Fatalf("second batch should ask only about fetch, without offering always-allow: %+v", asked)
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "f2", Verdict: agent.Approve}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 2 || fetches.Load() != 2 {
		t.Fatalf("sends %d, fetches %d, want 2 and 2", sends.Load(), fetches.Load())
	}

	res, err = a.Start(ctx, "t1", user("again"), event.Discard)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("the next turn should ask about send again: %+v, %v", res, err)
	}
	if trustedAsked != 2 {
		t.Fatalf("Trusted was asked %d times, want 2 (s1 and s3; s2 was covered by the turn's always-allow)", trustedAsked)
	}
}

func TestTrustIsReadAtEachDecisionAndNeverRemembered(t *testing.T) {
	ctx := context.Background()
	var sends atomic.Int32
	trusted := true
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{call("s1", "send", `{}`)}},
		modeltest.Reply{Calls: []model.ToolCall{call("s2", "send", `{}`)}},
	)
	a := build(t, agent.Config{
		Model:   m,
		Tools:   []tool.Tool{countingTool("send", tool.Ask, &sends)},
		Trusted: func(context.Context, tool.Call) (bool, error) { return trusted, nil },
	})
	// The tool is trusted when the first call is decided. Trust is withdrawn
	// as soon as that call's result is in, the way a settings page would
	// withdraw it from another request in the middle of the turn.
	var rec recorder
	_, err := a.Start(ctx, "t1", user("go"), func(ev event.Event) {
		if ev.Kind == event.ToolResult {
			trusted = false
		}
		rec.sink(ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 1 {
		t.Fatalf("send ran %d times, want 1: the second call came after trust was withdrawn", sends.Load())
	}
	if asked := rec.of(event.ApprovalNeeded); len(asked) != 1 || asked[0].CallID != "s2" {
		t.Fatalf("the second call should have asked: %s", rec.trace())
	}

	// A trust source that fails stops the turn: it is neither a yes nor a no.
	b := build(t, agent.Config{
		Model:   script(modeltest.Reply{Calls: []model.ToolCall{call("s1", "send", `{}`)}}),
		Tools:   []tool.Tool{countingTool("send", tool.Ask, &sends)},
		Trusted: func(context.Context, tool.Call) (bool, error) { return false, errors.New("database is down") },
	})
	if _, err := b.Start(ctx, "t2", user("go"), event.Discard); err == nil || !strings.Contains(err.Error(), "database is down") {
		t.Fatalf("err = %v, want the trust source's failure", err)
	}
}

func TestAToolCanAskAQuestionAndIsRunAgainWithTheAnswer(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var pickRuns, otherRuns atomic.Int32
	tools := func() []tool.Tool {
		pick := tool.Tool{Name: "pick", Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: true,
			Run: func(_ context.Context, c tool.Call) (string, error) {
				pickRuns.Add(1)
				if c.Answer == nil {
					return "", tool.AskUser(map[string]any{"type": "structured_question", "options": []string{"a", "b"}})
				}
				return "picked " + string(c.Answer), nil
			}}
		other := countingTool("other", tool.Auto, &otherRuns)
		other.Parallel = true
		return []tool.Tool{pick, other}
	}

	a := build(t, agent.Config{Store: store, Tools: tools(), Model: script(
		modeltest.Reply{Calls: []model.ToolCall{call("p1", "pick", `{}`), call("o1", "other", `{}`)}},
	)})
	var rec recorder
	res, err := a.Start(ctx, "t1", user("go"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "p1" || string(asked[0].Payload) != `{"options":["a","b"],"type":"structured_question"}` {
		t.Fatalf("question event = %+v", asked)
	}

	// After a restart the question is still there to be shown again, and
	// only an answer will do for it.
	m := script(modeltest.Reply{Text: "ok"})
	b := build(t, agent.Config{Store: store, Tools: tools(), Model: m})
	pending, _ := b.Pending(ctx, "t1")
	if len(pending) != 1 || string(pending[0].Question) != string(asked[0].Payload) {
		t.Fatalf("pending = %+v", pending)
	}
	if _, err := b.Resume(ctx, "t1", []agent.Decision{{CallID: "p1", Verdict: agent.Approve}}, event.Discard); err == nil {
		t.Fatal("a question was answered with an approval")
	}
	if _, err := b.Resume(ctx, "t1", []agent.Decision{{CallID: "p1", Verdict: agent.Answer, Answer: []byte(`"b"`)}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	// The asking tool ran twice, by its own design. Its neighbour, which had
	// finished, was not run again to deliver the answer.
	if pickRuns.Load() != 2 || otherRuns.Load() != 1 {
		t.Fatalf("pick ran %d, other ran %d; want 2 and 1", pickRuns.Load(), otherRuns.Load())
	}
	equal(t, "results", roles(m.Requests()[0].Messages)[2:], []string{`tool[p1]:picked "b"`, "tool[o1]:other got {}"})
}
