package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// citingSearch is a search tool that labels each document it returns with
// the number the thread's registry gives it, as the real search does when
// it renders passages for the model.
func citingSearch() tool.Tool {
	return tool.Tool{Name: "search", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			var args struct {
				Docs []string `json:"docs"`
			}
			if err := json.Unmarshal(c.Args, &args); err != nil {
				return "", err
			}
			var out []string
			for _, doc := range args.Docs {
				out = append(out, fmt.Sprintf("[%d] %s", c.Citations.Number(citation.Source{Kind: "chunk", Ref: doc}), doc))
			}
			return strings.Join(out, "\n"), nil
		}}
}

func refs(sources []citation.Source) string {
	out := make([]string, len(sources))
	for i, s := range sources {
		out[i] = s.Ref
	}
	return strings.Join(out, ",")
}

// A specialist cites two sources, pauses for an approval, and the process
// restarts. After the resume it cites a third: the numbering goes on from
// where it was, in the parent's registry, and the parent's result lists all
// three. A later turn of the same conversation keeps the numbers too,
// because its history still holds the labels.
func TestCitationsFromADelegateReachTheParentAndSurviveAPauseAndARestart(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var writes atomic.Int32
	tree := func(specialistModel, mainModel model.Model) *agent.Agent {
		specialist := build(t, agent.Config{Store: store, Model: specialistModel,
			Tools: []tool.Tool{citingSearch(), countingTool("write", tool.Ask, &writes)}})
		return build(t, agent.Config{Store: store, Model: mainModel, Tools: []tool.Tool{citingSearch()},
			Delegates: []agent.Delegate{{Name: "notes", Description: "Notes.", Agent: specialist}}})
	}

	first := tree(
		script(
			modeltest.Reply{Calls: []model.ToolCall{call("s1", "search", `{"docs":["a","b"]}`)}},
			modeltest.Reply{Calls: []model.ToolCall{call("w1", "write", `{}`)}},
		),
		script(modeltest.Reply{Calls: []model.ToolCall{call("k1", "notes", `{"task":"look"}`)}}),
	)
	res, err := first.Start(ctx, "t1", user("go"), event.Discard)
	if err != nil || res.Outcome != agent.Paused || refs(res.Citations) != "a,b" {
		t.Fatalf("start: %+v, %v", res, err)
	}

	specialistModel := script(
		modeltest.Reply{Calls: []model.ToolCall{call("s2", "search", `{"docs":["b","c"]}`)}},
		modeltest.Reply{Text: "b [2] and c [3]"},
	)
	second := tree(specialistModel, script(modeltest.Reply{Text: "See [2] and [3]."}))
	res, err = second.Resume(ctx, "t1", []agent.Decision{{CallID: "k1/w1", Verdict: agent.Approve}}, event.Discard)
	if err != nil || res.Answer != "See [2] and [3]." {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	// "b" kept its number; "c" took the next one.
	sent := roles(specialistModel.Requests()[1].Messages)
	if got := sent[len(sent)-1]; got != "tool[s2]:[2] b\n[3] c" {
		t.Fatalf("specialist's second search = %q", got)
	}
	if refs(res.Citations) != "a,b,c" {
		t.Fatalf("parent's citations = %s, want a,b,c", refs(res.Citations))
	}

	// A new turn, a new process: the main agent's own search continues the
	// sequence the specialist's numbers are in.
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{call("s3", "search", `{"docs":["d","a"]}`)}},
		modeltest.Reply{Text: "ok"},
	)
	res, err = tree(script(), m).Start(ctx, "t1", user("and more?"), event.Discard)
	if err != nil || refs(res.Citations) != "a,b,c,d" {
		t.Fatalf("next turn: %+v, %v", res, err)
	}
	sent = roles(m.Requests()[1].Messages)
	if got := sent[len(sent)-1]; got != "tool[s3]:[4] d\n[1] a" {
		t.Fatalf("next turn's search = %q", got)
	}
}

// After a crash in the middle of a delegate, the parent's saved state is
// older than the delegate's: the delegate numbered a source that the parent
// never saved. Picking the delegate up must bring that number back, not
// give it out again.
func TestADelegateResumedAfterACrashRestoresTheNumbersItGaveOut(t *testing.T) {
	ctx := context.Background()
	// The process dies, so nothing more of the parent is saved: its last
	// save is from before the delegate ran.
	store := &dyingStore{Store: checkpoint.NewMemory(), thread: "t1"}
	tree := func(specialistModel, mainModel model.Model) *agent.Agent {
		specialist := build(t, agent.Config{Store: store, Model: specialistModel, Tools: []tool.Tool{citingSearch()}})
		return build(t, agent.Config{Store: store, Model: mainModel,
			Delegates: []agent.Delegate{{Name: "notes", Description: "Notes.", Agent: specialist}}})
	}

	runCtx, crash := context.WithCancel(ctx)
	dying := modeltest.Func(func(_ context.Context, req model.Request, _ func(model.Chunk)) (model.Response, error) {
		if req.Messages[len(req.Messages)-1].Role == model.RoleTool {
			store.dead.Store(true)
			crash()
			return model.Response{}, runCtx.Err()
		}
		return model.Response{Message: model.Message{ToolCalls: []model.ToolCall{call("s1", "search", `{"docs":["a"]}`)}}}, nil
	})
	before := tree(dying, script(modeltest.Reply{Calls: []model.ToolCall{call("k1", "notes", `{"task":"look"}`)}}))
	if _, err := before.Start(runCtx, "t1", user("go"), event.Discard); err == nil {
		t.Fatal("the crashed run reported success")
	}
	if st, _, _ := before.Load(ctx, "t1"); len(st.Citations) != 0 {
		t.Fatalf("the parent saved citations %v; the test needs it not to have", st.Citations)
	}
	store.dead.Store(false)

	specialistModel := script(
		modeltest.Reply{Calls: []model.ToolCall{call("s2", "search", `{"docs":["b","a"]}`)}},
		modeltest.Reply{Text: "done"},
	)
	res, err := tree(specialistModel, script(modeltest.Reply{Text: "ok"})).Continue(ctx, "t1", event.Discard)
	if err != nil || refs(res.Citations) != "a,b" {
		t.Fatalf("continue: %+v, %v", res, err)
	}
	sent := roles(specialistModel.Requests()[1].Messages)
	if got := sent[len(sent)-1]; got != "tool[s2]:[2] b\n[1] a" {
		t.Fatalf("search after the crash = %q", got)
	}
}

// Calls that wait are announced in one order, the order Pending lists them
// and a client answers them in, however their tools happen to finish. Here
// two delegates side by side both pause, the first one more slowly, and a
// tool asks its question last of all.
func TestWaitingCallsAreAnnouncedInTheOrderPendingListsThem(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	asker := func(name string, delay time.Duration) tool.Tool {
		return tool.Tool{Name: name, Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: true,
			Run: func(context.Context, tool.Call) (string, error) {
				time.Sleep(delay)
				return "", tool.AskUser(map[string]string{"type": "structured_question", "from": name})
			}}
	}
	specialist := func(name string, delay time.Duration) *agent.Agent {
		return build(t, agent.Config{Store: store, Tools: []tool.Tool{asker(name, delay)},
			Model: modeltest.Func(func(context.Context, model.Request, func(model.Chunk)) (model.Response, error) {
				return model.Response{Message: model.Message{ToolCalls: []model.ToolCall{call("q", name, `{}`)}}}, nil
			})})
	}
	main := build(t, agent.Config{Store: store,
		Tools: []tool.Tool{asker("local", 0)},
		Delegates: []agent.Delegate{
			{Name: "slow", Description: "Slow.", Agent: specialist("slow_ask", 40*time.Millisecond), Parallel: true},
			{Name: "fast", Description: "Fast.", Agent: specialist("fast_ask", 0), Parallel: true},
		},
		Model: script(modeltest.Reply{Calls: []model.ToolCall{
			call("d1", "slow", `{"task":"x"}`), call("d2", "fast", `{"task":"y"}`), call("l1", "local", `{}`),
		}}),
	})
	var rec recorder
	res, err := main.Start(ctx, "t1", user("go"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	var announced []string
	for _, ev := range rec.of(event.ApprovalNeeded) {
		announced = append(announced, ev.Parent+">"+ev.CallID)
	}
	equal(t, "announced", announced, []string{"d1>d1/q", "d2>d2/q", ">l1"})
	pending, err := main.Pending(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, p := range pending {
		listed = append(listed, p.CallID)
	}
	equal(t, "pending", listed, []string{"d1/q", "d2/q", "l1"})

	// Every announcement is in the stream before the pause, and after every
	// other event of the tree.
	trace := rec.trace()
	if !strings.HasSuffix(trace, "approval-needed(d1/q) approval-needed(d2/q) approval-needed(l1) paused") {
		t.Fatalf("the announcements are not the end of the stream: %s", trace)
	}
}

// dyingStore refuses every save of one thread while dead is set: what a
// process that has died saves of that thread, which is nothing.
type dyingStore struct {
	checkpoint.Store
	thread string
	dead   atomic.Bool
}

func (d *dyingStore) Save(ctx context.Context, cp checkpoint.Checkpoint) error {
	if d.dead.Load() && cp.Thread == d.thread {
		return errors.New("the process is gone")
	}
	return d.Store.Save(ctx, cp)
}
