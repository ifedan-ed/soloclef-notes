package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// trail is a log the hooks of one test write to from any goroutine.
type trail struct {
	mu    sync.Mutex
	lines []string
}

func (l *trail) add(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *trail) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

func TestHooksStackInTheOrderGivenAroundModelAndToolCalls(t *testing.T) {
	ctx := context.Background()
	var log trail
	modelHook := func(name string) agent.ModelHook {
		return func(ctx context.Context, call agent.ModelCall, next agent.ModelFunc) (model.Response, error) {
			log.add("%s before model %s#%d", name, call.Thread, call.Step)
			resp, err := next(ctx, call)
			log.add("%s after model", name)
			return resp, err
		}
	}
	toolHook := func(name string) agent.ToolHook {
		return func(ctx context.Context, call tool.Call, next tool.Func) (string, error) {
			log.add("%s before %s", name, call.Name)
			out, err := next(ctx, call)
			log.add("%s after %s", name, call.Name)
			return out, err
		}
	}
	var runs atomic.Int32
	a := build(t, agent.Config{
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{call("c1", "look", `{}`)}},
			modeltest.Reply{Text: "done"},
		),
		Tools:      []tool.Tool{countingTool("look", tool.Auto, &runs)},
		ModelHooks: []agent.ModelHook{modelHook("outer"), modelHook("inner")},
		ToolHooks:  []agent.ToolHook{toolHook("outer"), toolHook("inner")},
	})
	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	equal(t, "order", log.get(), []string{
		"outer before model t1#1", "inner before model t1#1", "inner after model", "outer after model",
		"outer before look", "inner before look", "inner after look", "outer after look",
		"outer before model t1#3", "inner before model t1#3", "inner after model", "outer after model",
	})
}

func TestAToolHookCanChangeACallRefuseItOrChangeItsResult(t *testing.T) {
	ctx := context.Background()
	var runs atomic.Int32
	// One hook that does all three, by tool name: the shape of a permission
	// rule, an argument fix-up and an output filter.
	hook := func(ctx context.Context, call tool.Call, next tool.Func) (string, error) {
		switch call.Name {
		case "blocked":
			return "", errors.New("this account does not allow that tool")
		case "fixed":
			call.Args = json.RawMessage(`{"limit":10}`)
			return next(ctx, call)
		default:
			out, err := next(ctx, call)
			return strings.ToUpper(out), err
		}
	}
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{call("b", "blocked", `{}`), call("f", "fixed", `{"limit":9999}`), call("l", "loud", `{}`)}},
		modeltest.Reply{Text: "ok"},
	)
	a := build(t, agent.Config{Model: m, ToolHooks: []agent.ToolHook{hook}, Tools: []tool.Tool{
		countingTool("blocked", tool.Auto, &runs), countingTool("fixed", tool.Auto, &runs), countingTool("loud", tool.Auto, &runs),
	}})
	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	equal(t, "results", roles(m.Requests()[1].Messages)[2:], []string{
		"tool[b]:error: this account does not allow that tool",
		`tool[f]:fixed got {"limit":10}`,
		"tool[l]:LOUD GOT {}",
	})
	if runs.Load() != 2 {
		t.Fatalf("%d tools ran, want 2: the refused one must not run", runs.Load())
	}
}

// The doom-loop check, written as a hook: the third identical call in a turn
// asks the person whether to go on. Nothing in the loop knows about it; it
// pauses through tool.AskUser like a tool would, survives a restart like a
// tool's question does, and runs again with the answer.
func TestAToolHookCanPauseTheTurnToAskAndSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var runs atomic.Int32
	repeatCheck := func() agent.ToolHook {
		var mu sync.Mutex
		seen := map[string]int{}
		return func(ctx context.Context, call tool.Call, next tool.Func) (string, error) {
			if call.Answer == nil {
				mu.Lock()
				key := call.Name + " " + string(call.Args)
				seen[key]++
				repeats := seen[key]
				mu.Unlock()
				if repeats >= 3 {
					return "", tool.AskUser(map[string]any{"type": "doom_loop", "tool": call.Name, "repeats": repeats})
				}
				return next(ctx, call)
			}
			var goOn bool
			if err := json.Unmarshal(call.Answer, &goOn); err != nil || !goOn {
				return "", errors.New("the user stopped this repeated call; try something else")
			}
			return next(ctx, call)
		}
	}
	same := call("", "search", `{"q":"x"}`)
	first := build(t, agent.Config{Store: store, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)},
		ToolHooks: []agent.ToolHook{repeatCheck()},
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{same}},
			modeltest.Reply{Calls: []model.ToolCall{same}},
			modeltest.Reply{Calls: []model.ToolCall{same}},
		)})
	var rec recorder
	res, err := first.Start(ctx, "t1", user("go"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || !strings.Contains(string(asked[0].Payload), `"doom_loop"`) || runs.Load() != 2 {
		t.Fatalf("asked %+v after %d runs", asked, runs.Load())
	}

	// A new process. The answer is "stop", and the model is told so.
	m := script(modeltest.Reply{Text: "I will stop searching."})
	second := build(t, agent.Config{Store: store, Tools: []tool.Tool{countingTool("search", tool.Auto, &runs)},
		ToolHooks: []agent.ToolHook{repeatCheck()}, Model: m})
	res, err = second.Resume(ctx, "t1", []agent.Decision{{CallID: asked[0].CallID, Verdict: agent.Answer, Answer: []byte(`false`)}}, event.Discard)
	if err != nil || res.Answer != "I will stop searching." {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	if runs.Load() != 2 {
		t.Fatalf("the stopped call ran: %d runs", runs.Load())
	}
	sent := roles(m.Requests()[0].Messages)
	if last := sent[len(sent)-1]; !strings.HasSuffix(last, "error: the user stopped this repeated call; try something else") {
		t.Fatalf("the model was told %q", last)
	}
}

// Hooks are put around a delegate call too, and the delegate's pause passes
// through a hook that wraps errors, so an audit hook written in the ordinary
// way cannot lose an approval.
func TestToolHooksWrapDelegateCallsAndPassTheirPauseThrough(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var log trail
	audit := func(ctx context.Context, call tool.Call, next tool.Func) (string, error) {
		out, err := next(ctx, call)
		log.add("%s pause=%v", call.Name, agent.IsPause(err))
		if err != nil {
			return "", fmt.Errorf("audited: %w", err)
		}
		return out, nil
	}
	var writes atomic.Int32
	tree := func(specialistModel, mainModel model.Model) *agent.Agent {
		specialist := build(t, agent.Config{Store: store, Model: specialistModel, Tools: []tool.Tool{countingTool("write", tool.Ask, &writes)}})
		return build(t, agent.Config{Store: store, Model: mainModel, ToolHooks: []agent.ToolHook{audit},
			Delegates: []agent.Delegate{{Name: "notes", Description: "Edits notes.", Agent: specialist}}})
	}
	first := tree(script(modeltest.Reply{Calls: []model.ToolCall{call("w1", "write", `{}`)}}),
		script(modeltest.Reply{Calls: []model.ToolCall{call("k1", "notes", `{"task":"write"}`)}}))
	res, err := first.Start(ctx, "t1", user("go"), event.Discard)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	second := tree(script(modeltest.Reply{Text: "written"}), script(modeltest.Reply{Text: "all done"}))
	res, err = second.Resume(ctx, "t1", []agent.Decision{{CallID: "k1/w1", Verdict: agent.Approve}}, event.Discard)
	if err != nil || res.Answer != "all done" || writes.Load() != 1 {
		t.Fatalf("resume: %+v, %v, %d writes", res, err, writes.Load())
	}
	equal(t, "audit", log.get(), []string{"notes pause=true", "notes pause=false"})
}

func TestContinueCutOffStitchesAnAnswerTheOutputCapStopped(t *testing.T) {
	ctx := context.Background()
	hook, err := agent.ContinueCutOff(2)
	if err != nil {
		t.Fatal(err)
	}
	m := script(
		modeltest.Reply{Text: "The answer is ", FinishReason: "length", Usage: model.Usage{CompletionTokens: 3, TotalTokens: 10}},
		modeltest.Reply{Text: "forty-two.", Usage: model.Usage{CompletionTokens: 2, TotalTokens: 12}},
	)
	a := build(t, agent.Config{Model: m, ModelHooks: []agent.ModelHook{hook}})
	var rec recorder
	res, err := a.Start(ctx, "t1", user("what is it?"), rec.sink)
	if err != nil || res.Answer != "The answer is forty-two." || res.FinishReason != "stop" {
		t.Fatalf("turn: %+v, %v", res, err)
	}
	// The reader saw one answer stream without a break; the model was asked
	// to go on from its own text.
	equal(t, "streamed", rec.text(), "The answer is forty-two.")
	sent := roles(m.Requests()[1].Messages)
	if sent[1] != "assistant:The answer is " || !strings.HasPrefix(sent[2], "user:Your previous response was cut off") {
		t.Fatalf("continuation request = %v", sent)
	}
	// One step, one stored answer, the usage of both calls.
	st, _, _ := a.Load(ctx, "t1")
	equal(t, "stored", roles(st.Messages), []string{"user:what is it?", "assistant:The answer is forty-two."})
	if used := rec.of(event.ModelFinished)[0].Usage; used.CompletionTokens != 5 || used.TotalTokens != 22 {
		t.Fatalf("usage = %+v", used)
	}

	// Past its limit it stops and says so with the finish reason.
	once, _ := agent.ContinueCutOff(1)
	cut := modeltest.Reply{Text: "more ", FinishReason: "length"}
	a = build(t, agent.Config{Model: script(cut, cut), ModelHooks: []agent.ModelHook{once}})
	res, err = a.Start(ctx, "t1", user("go"), event.Discard)
	if err != nil || res.Answer != "more more " || res.FinishReason != "length" {
		t.Fatalf("over the limit: %+v, %v", res, err)
	}
	if _, err := agent.ContinueCutOff(0); err == nil {
		t.Fatal("zero continuations was accepted")
	}
}

func TestNewRefusesANilHook(t *testing.T) {
	m := script()
	if _, err := agent.New(agent.Config{Model: m, MaxSteps: 1, MaxParallelTools: 1, Store: checkpoint.NewMemory(), ModelHooks: []agent.ModelHook{nil}}); err == nil {
		t.Fatal("a nil model hook was accepted")
	}
	if _, err := agent.New(agent.Config{Model: m, MaxSteps: 1, MaxParallelTools: 1, Store: checkpoint.NewMemory(), ToolHooks: []agent.ToolHook{nil}}); err == nil {
		t.Fatal("a nil tool hook was accepted")
	}
}
