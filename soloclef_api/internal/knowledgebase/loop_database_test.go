package knowledgebase

import (
	"context"
	"sync"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// events collects a run's events from any goroutine.
type events struct {
	mu   sync.Mutex
	seen []event.Event
}

func (e *events) sink(ev event.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, ev)
}

func (e *events) notices(name string) []event.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var found []event.Event
	for _, ev := range e.seen {
		if ev.Kind == event.Notice && ev.Name == name {
			found = append(found, ev)
		}
	}
	return found
}

// The specialist through the real loop: the main agent hands it a task, its
// note waits for the person's approval, the approval arrives in a later
// request (a turn of its own), and the change is logged under the card the
// page drew for it and under the turn that made it, and announced on that
// card.
func TestAChangeWaitsForApprovalAndIsLoggedUnderItsCard(t *testing.T) {
	h := open(t)
	store := checkpoint.NewMemory()
	run := func(trusted bool) (*agent.Agent, *modeltest.Script, *modeltest.Script) {
		main := modeltest.NewScript(
			modeltest.Reply{Calls: []model.ToolCall{{ID: "call_kb", Name: assembly.KnowledgeBaseName, Args: `{"task":"Save a note called Loop note saying x."}`}}},
			modeltest.Reply{Text: "Saved."},
		)
		specialist := modeltest.NewScript(
			modeltest.Reply{Calls: []model.ToolCall{{ID: "call_n", Name: createNoteTool, Args: `{"title":"Loop note","content":"x"}`}}},
			modeltest.Reply{Text: "Created the note."},
		)
		delegate, err := Writer{Changes: h.changes}.Delegate(specialist, assembly.KnowledgeOwner{
			UserID: alice, ThreadID: 10, Store: store,
			Trusted: func(context.Context, tool.Call) (bool, error) { return trusted, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		parent, err := agent.New(agent.Config{Model: main, Delegates: []agent.Delegate{delegate}, MaxSteps: 10, MaxParallelTools: 1, Store: store})
		if err != nil {
			t.Fatal(err)
		}
		return parent, main, specialist
	}

	parent, _, _ := run(false)
	asked := &events{}
	result, err := parent.Start(assembly.WithTurnID(context.Background(), "10:1"), "chat-10", []model.Message{model.User("save it")}, asked.sink)
	if err != nil || result.Outcome != agent.Paused {
		t.Fatalf("the note did not wait for approval: %+v %v", result, err)
	}
	waiting, err := parent.Pending(context.Background(), "chat-10")
	if err != nil || len(waiting) != 1 || waiting[0].CallID != "call_kb/call_n" || waiting[0].Tool != createNoteTool || !waiting[0].AllowAlways {
		t.Fatalf("waiting: %+v %v", waiting, err)
	}
	if h.count(`SELECT count(*) FROM documents WHERE title = 'Loop note'`) != 0 {
		t.Fatal("the note was written before it was approved")
	}

	answered := &events{}
	result, err = parent.Resume(assembly.WithTurnID(context.Background(), "10:2"), "chat-10",
		[]agent.Decision{{CallID: "call_kb/call_n", Verdict: agent.Approve}}, answered.sink)
	if err != nil || result.Outcome != agent.Finished || result.Answer != "Saved." {
		t.Fatalf("the approved turn: %+v %v", result, err)
	}
	if got := h.text(`SELECT tool_call_id || '|' || chat_turn_id FROM agent_action_log WHERE tool_name = 'create_note'`); got != "call_kb/call_n|10:2" {
		t.Errorf("the log names %s, want the card call_kb/call_n in the turn that made it", got)
	}
	notices := answered.notices("action-log")
	if len(notices) != 1 || notices[0].CallID != "call_kb/call_n" {
		t.Errorf("the card was not told: %+v", notices)
	}

	// With "always allow" given for the tool, the next note is written
	// without asking; a delete would still ask (tool.AlwaysAsk, which the
	// loop never asks Trusted about).
	parent, _, _ = run(true)
	result, err = parent.Start(assembly.WithTurnID(context.Background(), "10:3"), "chat-11", []model.Message{model.User("save it")}, event.Discard)
	if err != nil || result.Outcome != agent.Finished {
		t.Fatalf("a trusted note: %+v %v", result, err)
	}
	if h.count(`SELECT count(*) FROM documents WHERE title = 'Loop note'`) != 2 {
		t.Error("the trusted note was not written")
	}
}
