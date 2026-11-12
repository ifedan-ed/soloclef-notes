package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// The tests in this file are about one shape: a specialist that builds
// something for the reader (a deck, a quiz, a podcast) as a nested run. It
// is the shape with the most to carry across the boundary between two runs,
// so everything such a specialist needs is shown working here in one place:
// its own tools, its questions and approvals reaching the person through the
// parent, its sources numbered with the parent's, its progress reaching the
// stream while it works, its time limit, and being stopped.

// styleQuestion is a tool that cannot do its work without asking: which
// style should the deck have? It asks from its own body and is run again
// with the answer.
func styleQuestion() tool.Tool {
	return tool.Tool{Name: "choose_style", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			if c.Answer == nil {
				return "", tool.AskUser(map[string]any{"question": "Which style?", "options": []string{"plain", "bold"}})
			}
			var style string
			if err := json.Unmarshal(c.Answer, &style); err != nil {
				return "", err
			}
			return "style: " + style, nil
		}}
}

// renderDeck is a slow tool that says how far it has got.
func renderDeck(slides int) tool.Tool {
	return tool.Tool{Name: "render", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			for n := 1; n <= slides; n++ {
				if err := c.Notify("activity", map[string]any{"text": "Rendering slide", "n": n}); err != nil {
					return "", err
				}
			}
			return "deck rendered", nil
		}}
}

func TestASpecialistsQuestionApprovalProgressAndSourcesAllReachTheParent(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var publishes atomic.Int32
	// Every stage below builds the whole tree again from the store alone:
	// each is a new process as far as the library can tell.
	tree := func(specialistModel, mainModel model.Model) *agent.Agent {
		specialist := build(t, agent.Config{Store: store, Model: specialistModel, Tools: []tool.Tool{
			citingSearch(), styleQuestion(), renderDeck(2), countingTool("publish", tool.Ask, &publishes),
		}})
		return build(t, agent.Config{Store: store, Model: mainModel,
			Delegates: []agent.Delegate{{Name: "deliverables", Description: "Builds decks.", Agent: specialist, Timeout: time.Minute}}})
	}

	// It reads a source, then asks its question.
	var first recorder
	res, err := tree(
		script(
			modeltest.Reply{Calls: []model.ToolCall{call("s1", "search", `{"docs":["a"]}`)}},
			modeltest.Reply{Calls: []model.ToolCall{call("q1", "choose_style", `{}`)}},
		),
		script(modeltest.Reply{Calls: []model.ToolCall{call("k1", "deliverables", `{"task":"a deck about a"}`)}}),
	).Start(ctx, "t1", user("make me a deck"), first.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	asked := first.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "k1/q1" || asked[0].Parent != "k1" || !strings.Contains(string(asked[0].Payload), "Which style?") {
		t.Fatalf("the question reached the parent as %+v", asked)
	}

	// The answer goes down to the tool that asked. The specialist then wants
	// to render and to publish, and publishing needs a yes: nothing of that
	// batch runs before it.
	var second recorder
	res, err = tree(
		script(modeltest.Reply{Calls: []model.ToolCall{call("r1", "render", `{}`), call("p1", "publish", `{}`)}}),
		script(),
	).Resume(ctx, "t1", []agent.Decision{{CallID: "k1/q1", Verdict: agent.Answer, Answer: []byte(`"bold"`)}}, second.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("answer: %+v, %v", res, err)
	}
	if got := second.of(event.ToolResult); len(got) != 1 || got[0].CallID != "k1/q1" || got[0].Text != "style: bold" {
		t.Fatalf("the tool that asked answered %+v", got)
	}
	if len(second.of(event.Notice)) != 0 || publishes.Load() != 0 {
		t.Fatal("part of the batch ran before its approval")
	}
	pending, err := tree(script(), script()).Pending(ctx, "t1")
	if err != nil || len(pending) != 1 || pending[0].CallID != "k1/p1" || !pending[0].AllowAlways {
		t.Fatalf("pending = %+v, %v", pending, err)
	}

	// Approved: it renders, reporting as it goes, publishes, and answers.
	var third recorder
	res, err = tree(
		script(modeltest.Reply{Text: "The deck is built from [1]."}),
		script(modeltest.Reply{Text: "Your deck is ready; it draws on [1]."}),
	).Resume(ctx, "t1", []agent.Decision{{CallID: "k1/p1", Verdict: agent.Approve}}, third.sink)
	if err != nil || res.Answer != "Your deck is ready; it draws on [1]." || publishes.Load() != 1 {
		t.Fatalf("approve: %+v, %v, %d publishes", res, err, publishes.Load())
	}
	// Progress reached the outermost stream while the nested call ran,
	// addressed to that call and marked as the specialist's.
	notices := third.of(event.Notice)
	if len(notices) != 2 || notices[1].CallID != "k1/r1" || notices[1].Parent != "k1" || notices[1].Tool != "render" ||
		notices[1].Name != "activity" || string(notices[1].Payload) != `{"n":2,"text":"Rendering slide"}` {
		t.Fatalf("notices = %+v", notices)
	}
	if !strings.Contains(third.trace(), "notice(k1/r1) notice(k1/r1) tool-result(k1/r1) tool-result(k1/p1)") {
		t.Fatalf("order = %s", third.trace())
	}
	// The label in the answer is the parent's to resolve.
	if refs(res.Citations) != "a" {
		t.Fatalf("the parent's citations = %q", refs(res.Citations))
	}
	// And nothing of the specialist is left behind.
	if _, err := store.Load(ctx, "t1/k1"); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("the specialist's thread is still stored: %v", err)
	}
}

func TestStoppingTheRunStopsASpecialistsToolAndANewTurnClearsItAway(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	started := make(chan struct{})
	var sawStop atomic.Bool
	slow := tool.Tool{Name: "render", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(ctx context.Context, _ tool.Call) (string, error) {
			close(started)
			<-ctx.Done()
			sawStop.Store(true)
			return "", ctx.Err()
		}}
	specialist := build(t, agent.Config{Store: store, Tools: []tool.Tool{slow},
		Model: script(modeltest.Reply{Calls: []model.ToolCall{call("r1", "render", `{}`)}})})
	main := build(t, agent.Config{Store: store,
		Model:     script(modeltest.Reply{Calls: []model.ToolCall{call("k1", "deliverables", `{"task":"a podcast"}`)}}),
		Delegates: []agent.Delegate{{Name: "deliverables", Description: "Builds things.", Agent: specialist}}})

	runCtx, stop := context.WithCancel(ctx)
	go func() {
		<-started
		stop()
	}()
	var rec recorder
	_, err := main.Start(runCtx, "t1", user("make a podcast"), rec.sink)
	if !errors.Is(err, context.Canceled) || !sawStop.Load() {
		t.Fatalf("err = %v, tool saw the stop = %v", err, sawStop.Load())
	}
	// One terminal event for the run the caller started, and it is the last.
	events := rec.all()
	if last := events[len(events)-1]; last.Kind != event.Error || last.Parent != "" {
		t.Fatalf("last event = %+v", last)
	}
	// The specialist's thread is kept as it stood, so Continue could go on.
	if _, err := store.Load(ctx, "t1/k1"); err != nil {
		t.Fatalf("the specialist's thread after the stop: %v", err)
	}

	// The person types something else instead. The abandoned call is
	// answered "interrupted" and the specialist's thread is removed.
	m := script(modeltest.Reply{Text: "ok"})
	next := build(t, agent.Config{Store: store, Model: m,
		Delegates: []agent.Delegate{{Name: "deliverables", Description: "Builds things.", Agent: specialist}}})
	if _, err := next.Start(ctx, "t1", user("never mind"), event.Discard); err != nil {
		t.Fatal(err)
	}
	sent := roles(m.Requests()[0].Messages)
	if !strings.HasPrefix(sent[2], "tool[k1]:error: this tool call was interrupted") || sent[3] != "user:never mind" {
		t.Fatalf("conversation = %v", sent)
	}
	if _, err := store.Load(ctx, "t1/k1"); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("the abandoned specialist thread is still stored: %v", err)
	}
}

func TestASpecialistPastItsTimeLimitIsOneFailedResultAndTheTurnGoesOn(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	stuck := tool.Tool{Name: "render", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(ctx context.Context, _ tool.Call) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}
	specialist := build(t, agent.Config{Store: store, Tools: []tool.Tool{stuck},
		Model: script(modeltest.Reply{Calls: []model.ToolCall{call("r1", "render", `{}`)}})})
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{call("k1", "deliverables", `{"task":"a video"}`)}},
		modeltest.Reply{Text: "The video could not be made in time."},
	)
	main := build(t, agent.Config{Store: store, Model: m,
		Delegates: []agent.Delegate{{Name: "deliverables", Description: "Builds things.", Agent: specialist, Timeout: 20 * time.Millisecond}}})
	res, err := main.Start(ctx, "t1", user("make a video"), event.Discard)
	if err != nil || res.Answer != "The video could not be made in time." {
		t.Fatalf("turn: %+v, %v", res, err)
	}
	sent := roles(m.Requests()[1].Messages)
	if got := sent[len(sent)-1]; got != "tool[k1]:error: deliverables did not finish within 20ms" {
		t.Fatalf("the model was told %q", got)
	}
	if _, err := store.Load(ctx, "t1/k1"); !errors.Is(err, checkpoint.ErrNotFound) {
		t.Fatalf("the timed-out specialist thread is still stored: %v", err)
	}

	if _, err := agent.New(agent.Config{Model: m, Store: store, MaxSteps: 1, MaxParallelTools: 1,
		Delegates: []agent.Delegate{{Name: "d", Agent: specialist, Timeout: -time.Second}}}); err == nil {
		t.Fatal("a negative time limit was accepted")
	}
}

// Two specialists were working when the process died. The second had read a
// source and saved; the first had not started, and the parent's own save is
// older than both. Carrying on must not hand the second one's number to the
// first: the second's saved prose already uses it.
func TestCarryingOnAfterACrashKeepsEveryNumberASiblingSaved(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	save := func(thread, node string, st agent.State) {
		t.Helper()
		raw, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(ctx, checkpoint.Checkpoint{Thread: thread, Seq: 1, Status: checkpoint.Running, Node: node, State: raw}); err != nil {
			t.Fatal(err)
		}
	}
	// The stored threads as the dead process left them.
	save("t1", "tools", agent.State{
		Steps: 2,
		Messages: []model.Message{model.User("compare a and b"),
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call("kA", "reader", `{"task":"read a"}`), call("kB", "reader", `{"task":"read b"}`)}}},
		Batch: []agent.Call{
			{ID: "kA", Name: "reader", Args: `{"task":"read a"}`, Status: agent.CallApproved},
			{ID: "kB", Name: "reader", Args: `{"task":"read b"}`, Status: agent.CallApproved},
		},
	})
	save("t1/kB", "model", agent.State{
		Steps: 2,
		Messages: []model.Message{model.User("read b"),
			{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call("s1", "search", `{"docs":["b"]}`)}},
			{Role: model.RoleTool, ToolCallID: "s1", Content: "[1] b"}},
		Citations: []citation.Source{{Kind: "chunk", Ref: "b"}},
	})

	// The delegate is not Parallel, so kA runs first and to its end before
	// kB is opened: the order in which the mistake would be made.
	specialistModel := script(
		modeltest.Reply{Calls: []model.ToolCall{call("s1", "search", `{"docs":["a"]}`)}},
		modeltest.Reply{Text: "a says so [2]."},
		modeltest.Reply{Text: "b says so [1]."},
	)
	specialist := build(t, agent.Config{Store: store, Model: specialistModel, Tools: []tool.Tool{citingSearch()}})
	main := build(t, agent.Config{Store: store, Model: script(modeltest.Reply{Text: "Both agree [1][2]."}),
		Delegates: []agent.Delegate{{Name: "reader", Description: "Reads.", Agent: specialist}}})
	res, err := main.Continue(ctx, "t1", event.Discard)
	if err != nil || refs(res.Citations) != "b,a" {
		t.Fatalf("continue: %+v, %v", res, err)
	}
	sent := roles(specialistModel.Requests()[1].Messages)
	if got := sent[len(sent)-1]; got != "tool[s1]:[2] a" {
		t.Fatalf("the first specialist's search = %q, want it numbered after its sibling's", got)
	}
}

func TestANoticeThatCannotBeEncodedIsTheToolsErrorToHandle(t *testing.T) {
	bad := tool.Tool{Name: "bad", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			return "", c.Notify("activity", func() {})
		}}
	m := script(modeltest.Reply{Calls: []model.ToolCall{call("b1", "bad", `{}`)}}, modeltest.Reply{Text: "ok"})
	var rec recorder
	if _, err := build(t, agent.Config{Model: m, Tools: []tool.Tool{bad}}).Start(context.Background(), "t1", user("go"), rec.sink); err != nil {
		t.Fatal(err)
	}
	if len(rec.of(event.Notice)) != 0 {
		t.Fatal("a notice with no payload was sent")
	}
	if got := rec.of(event.ToolResult); len(got) != 1 || !got[0].IsError || !strings.Contains(got[0].Text, `notice "activity" from bad cannot be encoded`) {
		t.Fatalf("result = %+v", got)
	}
}
