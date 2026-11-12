package uistream_test

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// A whole turn, from the loop to the bytes: the main agent thinks, hands a
// task to a specialist whose tool needs approval, the turn pauses, and the
// resumed turn answers. The stream is read the way the app reads it.
func TestAWholeTurnWithASpecialistAndAnApprovalAsTheAppReadsIt(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory()
	write := tool.Tool{Name: "write_file", Parameters: []byte(`{"type":"object"}`), Approval: tool.Ask,
		Run: func(context.Context, tool.Call) (string, error) { return "written", nil }}
	build := func(specialistModel, mainModel model.Model) *agent.Agent {
		specialist, err := agent.New(agent.Config{Model: specialistModel, Tools: []tool.Tool{write}, MaxSteps: 10, MaxParallelTools: 2, Store: store})
		if err != nil {
			t.Fatal(err)
		}
		main, err := agent.New(agent.Config{Model: mainModel, MaxSteps: 10, MaxParallelTools: 2, Store: store,
			Delegates: []agent.Delegate{{Name: "knowledge_base", Description: "Edits notes.", Agent: specialist}}})
		if err != nil {
			t.Fatal(err)
		}
		return main
	}

	first := newStream(t, nil)
	first.Begin("msg_1")
	main := build(
		modeltest.NewScript(modeltest.Reply{Text: "I will write it.", Calls: []model.ToolCall{{ID: "w1", Name: "write_file", Args: `{"path":"a.md"}`}}}),
		modeltest.NewScript(modeltest.Reply{Reasoning: "The notes need editing.", Text: "One moment.",
			Calls: []model.ToolCall{{ID: "k1", Name: "knowledge_base", Args: `{"task":"add a line"}`}}}),
	)
	res, err := main.Start(ctx, "thread-1", []model.Message{model.User("add a line to a.md")}, first.Event)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	first.Data("turn-status", struct {
		Status string `json:"status"`
	}{"idle"})
	first.End()

	frames := read(t, first.String())
	want := "start start-step " +
		"reasoning-start reasoning-delta reasoning-delta reasoning-delta reasoning-delta reasoning-end " +
		"text-start text-delta text-delta text-end " +
		"tool-input-start tool-input-delta tool-input-delta tool-input-available " + // the delegate's own card
		"tool-input-start tool-input-available " + // the specialist's tool, as a card
		"data-interrupt-request " +
		"data-turn-status finish-step finish [DONE]"
	if got := types(frames); got != want {
		t.Fatalf("first stream\n got: %s\nwant: %s", got, want)
	}
	if strings.Contains(first.String(), "I will write it.") {
		t.Fatal("the specialist's words were sent as the answer")
	}
	interrupt := frames[len(frames)-5]["data"].(map[string]any)
	id, _ := interrupt["tool_call_id"].(string)
	if id != "k1/w1" {
		t.Fatalf("the interrupt answers to %q, want k1/w1", id)
	}

	// The app posts its decision with the id it was given; the resumed turn
	// is a second stream on the same message.
	second := newStream(t, nil)
	second.Begin("msg_1")
	main = build(
		modeltest.NewScript(modeltest.Reply{Text: "Line added."}),
		modeltest.NewScript(modeltest.Reply{Text: "Done: the line is in a.md."}),
	)
	res, err = main.Resume(ctx, "thread-1", []agent.Decision{{CallID: id, Verdict: agent.Approve}}, second.Event)
	if err != nil || res.Outcome != agent.Finished {
		t.Fatalf("resume: %+v, %v", res, err)
	}
	second.End()

	frames = read(t, second.String())
	want = "start start-step " +
		"tool-output-available " + // the specialist's write, now approved and run
		"tool-output-available " + // the delegate call itself
		"text-start text-delta text-delta text-delta text-delta text-delta text-delta text-end " +
		"finish-step finish [DONE]"
	if got := types(frames); got != want {
		t.Fatalf("second stream\n got: %s\nwant: %s", got, want)
	}
	if frames[2]["toolCallId"] != "k1/w1" || frames[3]["toolCallId"] != "k1" {
		t.Fatalf("results landed on the wrong cards: %v then %v", frames[2]["toolCallId"], frames[3]["toolCallId"])
	}
}
