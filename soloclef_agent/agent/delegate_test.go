package agent_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

func TestADelegateRunsTheSameLoopAndItsAnswerIsTheToolResult(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var searches atomic.Int32

	specialistModel := script(
		modeltest.Reply{Text: "Searching.", Calls: []model.ToolCall{call("s1", "search", `{"q":"notes"}`)}},
		modeltest.Reply{Text: "Three notes mention it."},
	)
	specialist := build(t, agent.Config{Store: store, Model: specialistModel, System: "You search.",
		Tools: []tool.Tool{countingTool("search", tool.Auto, &searches)}})

	mainModel := script(
		modeltest.Reply{Calls: []model.ToolCall{call("k1", "knowledge_base", `{"task":"find mentions of X"}`)}},
		modeltest.Reply{Text: "It is in three notes."},
	)
	main := build(t, agent.Config{Store: store, Model: mainModel,
		Delegates: []agent.Delegate{{Name: "knowledge_base", Description: "Searches the notes.", Agent: specialist}}})

	var rec recorder
	res, err := main.Start(ctx, "t1", user("where is X?"), rec.sink)
	if err != nil || res.Answer != "It is in three notes." {
		t.Fatalf("turn: %+v, %v", res, err)
	}

	// The specialist saw only its own prompt and the task, not the parent's
	// conversation.
	equal(t, "specialist's first request", roles(specialistModel.Requests()[0].Messages), []string{"system:You search.", "user:find mentions of X"})
	// Its last message came back as the delegate call's result.
	equal(t, "main's second request", roles(mainModel.Requests()[1].Messages), []string{
		"user:where is X?", "assistant:->k1", "tool[k1]:Three notes mention it.",
	})
	// The model was offered the delegate as a tool that takes a task.
	if spec := mainModel.Requests()[0].Tools[0]; spec.Name != "knowledge_base" || !strings.Contains(string(spec.Parameters), `"task"`) {
		t.Fatalf("delegate spec = %+v", spec)
	}

	// Everything the specialist did is marked with the call that started it
	// and carries an id unique in the tree; only the main agent's text is
	// the answer.
	for _, ev := range rec.all() {
		nested := ev.Kind == event.ToolCallReady && ev.Tool == "search"
		if nested && (ev.Parent != "k1" || ev.CallID != "k1/s1") {
			t.Fatalf("nested call event = %+v", ev)
		}
	}
	equal(t, "answer text", rec.text(), "It is in three notes.")
	if terminal := len(rec.of(event.Done)); terminal != 2 {
		t.Fatalf("%d done events, want 2 (the specialist's, marked nested, and the run's)", terminal)
	}
	if last := rec.all()[len(rec.all())-1]; last.Kind != event.Done || last.Parent != "" {
		t.Fatalf("the stream must end with the top-level run's own done: %+v", last)
	}

	// A finished specialist leaves no thread behind.
	if store.Len() != 1 {
		t.Fatalf("%d threads stored, want 1 (the main thread)", store.Len())
	}
}

func TestAnApprovalInsideADelegateReachesTheParentAndResumesAcrossARestart(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var reads, writes atomic.Int32

	// Both agents are rebuilt for the "second process", as a server would
	// build them again for the resume request.
	buildTree := func(specialistModel, mainModel model.Model) *agent.Agent {
		specialist := build(t, agent.Config{Store: store, Model: specialistModel,
			Tools: []tool.Tool{countingTool("read_file", tool.Auto, &reads), countingTool("write_file", tool.Ask, &writes)}})
		return build(t, agent.Config{Store: store, Model: mainModel,
			Delegates: []agent.Delegate{{Name: "editor", Description: "Edits notes.", Agent: specialist}}})
	}

	before := buildTree(
		script(
			modeltest.Reply{Calls: []model.ToolCall{call("r1", "read_file", `{"path":"a.md"}`)}},
			modeltest.Reply{Calls: []model.ToolCall{call("w1", "write_file", `{"path":"a.md"}`)}},
		),
		script(modeltest.Reply{Calls: []model.ToolCall{call("e1", "editor", `{"task":"fix the typo in a.md"}`)}}),
	)
	var rec recorder
	res, err := before.Start(ctx, "t1", user("fix the typo"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	// The specialist's approval surfaced through the parent's stream, under
	// an id that says where it is.
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "e1/w1" || asked[0].Parent != "e1" || asked[0].Tool != "write_file" {
		t.Fatalf("approval events = %+v", asked)
	}
	if last := rec.all()[len(rec.all())-1]; last.Kind != event.Paused || last.Parent != "" {
		t.Fatalf("the stream must end with the top-level pause: %+v", last)
	}

	specialistModel := script(modeltest.Reply{Text: "Typo fixed."})
	mainModel := script(modeltest.Reply{Text: "Done, the typo is fixed."})
	after := buildTree(specialistModel, mainModel)

	pending, err := after.Pending(ctx, "t1")
	if err != nil || len(pending) != 1 || pending[0].CallID != "e1/w1" || !pending[0].AllowAlways {
		t.Fatalf("pending after restart = %+v, %v", pending, err)
	}
	// A decision that does not name the nested call is refused before
	// anything runs, at the top, not as a failure inside the specialist.
	if _, err := after.Resume(ctx, "t1", []agent.Decision{{CallID: "w1", Verdict: agent.Approve}}, event.Discard); err == nil {
		t.Fatal("a decision with the wrong id was accepted")
	}

	res, err = after.Resume(ctx, "t1", []agent.Decision{{CallID: "e1/w1", Verdict: agent.Approve}}, event.Discard)
	if err != nil || res.Answer != "Done, the typo is fixed." {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	// The specialist went on from where it paused: its read was not redone.
	if reads.Load() != 1 || writes.Load() != 1 {
		t.Fatalf("read ran %d, write ran %d; want 1 and 1", reads.Load(), writes.Load())
	}
	equal(t, "specialist after resume", roles(specialistModel.Requests()[0].Messages), []string{
		"user:fix the typo in a.md",
		"assistant:->r1", `tool[r1]:read_file got {"path":"a.md"}`,
		"assistant:->w1", `tool[w1]:write_file got {"path":"a.md"}`,
	})
	equal(t, "main after resume", roles(mainModel.Requests()[0].Messages)[2], "tool[e1]:Typo fixed.")
	if store.Len() != 1 {
		t.Fatalf("%d threads stored, want 1", store.Len())
	}
}

func TestADelegateThatFailsIsAFailedToolResultAndLeavesNoThread(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	specialist := build(t, agent.Config{Store: store, Model: script(modeltest.Reply{Err: &model.Error{Status: 500, Message: "upstream broke"}})})
	mainModel := script(
		modeltest.Reply{Calls: []model.ToolCall{call("k1", "helper", `{"task":"x"}`), call("k2", "helper", `{}`)}},
		modeltest.Reply{Text: "The helper is unavailable."},
	)
	main := build(t, agent.Config{Store: store, Model: mainModel,
		Delegates: []agent.Delegate{{Name: "helper", Description: "Helps.", Agent: specialist}}})

	res, err := main.Start(ctx, "t1", user("go"), event.Discard)
	if err != nil || res.Answer != "The helper is unavailable." {
		t.Fatalf("turn: %+v, %v", res, err)
	}
	got := roles(mainModel.Requests()[1].Messages)[2:]
	if !strings.Contains(got[0], "tool[k1]:error:") || !strings.Contains(got[0], "upstream broke") {
		t.Fatalf("failed delegate result = %q", got[0])
	}
	equal(t, "delegate without a task", got[1], `tool[k2]:error: the arguments need a "task" string saying what to do.`)
	if store.Len() != 1 {
		t.Fatalf("%d threads stored, want 1", store.Len())
	}
}

func TestParallelDelegatesNumberTheirCitationsFromOneSequence(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()

	// One registry for the turn. Both specialists' tools are built holding
	// it, so there is no second registry and nothing to merge or renumber
	// when they return.
	registry := citation.NewRegistry()
	var both sync.WaitGroup
	both.Add(2)
	search := func(sources ...string) tool.Tool {
		return tool.Tool{Name: "search", Parameters: []byte(anyObject), Approval: tool.Auto,
			Run: func(context.Context, tool.Call) (string, error) {
				// Hold both specialists inside their search at once, so the
				// numbering really is contended.
				both.Done()
				both.Wait()
				var out []string
				for _, ref := range sources {
					out = append(out, fmt.Sprintf("[%d] %s", registry.Number(citation.Source{Kind: "chunk", Ref: ref}), ref))
				}
				return strings.Join(out, "\n"), nil
			}}
	}
	// Each specialist answers by repeating what its search returned, labels
	// and all, as a model citing its sources would.
	echo := func() model.Model {
		return modeltest.Func(func(_ context.Context, req model.Request, _ func(model.Chunk)) (model.Response, error) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role == model.RoleTool {
				return model.Response{Message: model.Message{Content: last.Content}}, nil
			}
			return model.Response{Message: model.Message{ToolCalls: []model.ToolCall{call("s", "search", `{}`)}}}, nil
		})
	}
	a := build(t, agent.Config{Store: store, Model: echo(), Tools: []tool.Tool{search("doc-a", "doc-shared")}})
	b := build(t, agent.Config{Store: store, Model: echo(), Tools: []tool.Tool{search("doc-shared", "doc-b")}})
	mainModel := script(
		modeltest.Reply{Calls: []model.ToolCall{call("k1", "notes", `{"task":"a"}`), call("k2", "papers", `{"task":"b"}`)}},
		modeltest.Reply{Text: "merged"},
	)
	main := build(t, agent.Config{Store: store, Model: mainModel, Delegates: []agent.Delegate{
		{Name: "notes", Description: "Notes.", Agent: a, Parallel: true},
		{Name: "papers", Description: "Papers.", Agent: b, Parallel: true},
	}})
	if _, err := main.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}

	// Every label in either specialist's prose still names the source it was
	// written about, and the source both found has one number, not two.
	seen := map[string]int{}
	for _, msg := range mainModel.Requests()[1].Messages {
		if msg.Role != model.RoleTool {
			continue
		}
		for _, line := range strings.Split(msg.Content, "\n") {
			var n int
			var ref string
			if _, err := fmt.Sscanf(line, "[%d] %s", &n, &ref); err != nil {
				t.Fatalf("unreadable citation line %q", line)
			}
			if src, ok := registry.Lookup(n); !ok || src.Ref != ref {
				t.Fatalf("label [%d] was written for %s but now names %+v", n, ref, src)
			}
			if prev, dup := seen[ref]; dup && prev != n {
				t.Fatalf("%s was numbered both %d and %d", ref, prev, n)
			}
			seen[ref] = n
		}
	}
	if len(seen) != 3 {
		t.Fatalf("%d distinct sources numbered, want 3: %v", len(seen), seen)
	}
}

func TestADelegateInterruptedByACrashIsPickedUpWhereItActuallyStands(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	var searches atomic.Int32
	specialist := func(m model.Model) *agent.Agent {
		return build(t, agent.Config{Store: store, Model: m, Tools: []tool.Tool{countingTool("search", tool.Auto, &searches)}})
	}
	tree := func(specialistModel, mainModel model.Model) *agent.Agent {
		return build(t, agent.Config{Store: store, Model: mainModel,
			Delegates: []agent.Delegate{{Name: "helper", Description: "Helps.", Agent: specialist(specialistModel)}}})
	}

	// The specialist searches, then its model fails; the failure is passed
	// off as the process dying by cancelling the run at that moment, so the
	// parent never records an outcome for the call.
	runCtx, crash := context.WithCancel(ctx)
	dying := modeltest.Func(func(_ context.Context, req model.Request, _ func(model.Chunk)) (model.Response, error) {
		if req.Messages[len(req.Messages)-1].Role == model.RoleTool {
			crash()
			return model.Response{}, runCtx.Err()
		}
		return model.Response{Message: model.Message{ToolCalls: []model.ToolCall{call("s1", "search", `{}`)}}}, nil
	})
	before := tree(dying, script(modeltest.Reply{Calls: []model.ToolCall{call("h1", "helper", `{"task":"look"}`)}}))
	if _, err := before.Start(runCtx, "t1", user("go"), event.Discard); err == nil {
		t.Fatal("the crashed run reported success")
	}
	if store.Len() != 2 {
		t.Fatalf("%d threads stored, want the parent's and the specialist's", store.Len())
	}

	// Continue: the parent runs its delegate call again, finds the
	// specialist's own thread mid-turn, and carries that on rather than
	// starting the task from the top.
	specialistModel := script(modeltest.Reply{Text: "found it"})
	mainModel := script(modeltest.Reply{Text: "The helper found it."})
	res, err := tree(specialistModel, mainModel).Continue(ctx, "t1", event.Discard)
	if err != nil || res.Answer != "The helper found it." {
		t.Fatalf("continue: %+v, %v", res, err)
	}
	if searches.Load() != 1 {
		t.Fatalf("the specialist's search ran %d times, want 1", searches.Load())
	}
	equal(t, "specialist carried on", roles(specialistModel.Requests()[0].Messages), []string{"user:look", "assistant:->s1", "tool[s1]:search got {}"})
	if store.Len() != 1 {
		t.Fatalf("%d threads stored after finishing, want 1", store.Len())
	}

	// The other choice after a crash is a new turn. That abandons the
	// delegate: its call is answered "interrupted" and its thread removed.
	runCtx2, crash2 := context.WithCancel(ctx)
	dying2 := modeltest.Func(func(context.Context, model.Request, func(model.Chunk)) (model.Response, error) {
		crash2()
		return model.Response{}, runCtx2.Err()
	})
	again := tree(dying2, script(modeltest.Reply{Calls: []model.ToolCall{call("h2", "helper", `{"task":"look again"}`)}}))
	if _, err := again.Start(runCtx2, "t2", user("go"), event.Discard); err == nil {
		t.Fatal("the second crashed run reported success")
	}
	fresh := script(modeltest.Reply{Text: "ok"})
	if _, err := tree(script(), fresh).Start(ctx, "t2", user("never mind"), event.Discard); err != nil {
		t.Fatal(err)
	}
	sent := roles(fresh.Requests()[0].Messages)
	if !strings.HasPrefix(sent[2], "tool[h2]:error: this tool call was interrupted") {
		t.Fatalf("abandoned delegate call = %q", sent[2])
	}
	if store.Len() != 2 {
		t.Fatalf("%d threads stored, want 2 (t1 and t2, no orphaned specialist)", store.Len())
	}
}
