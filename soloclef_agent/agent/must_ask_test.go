package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// pagesAsk is a tool declared to run unasked, whose calls naming a "page"
// must be approved: what it is told about each run is recorded.
func pagesAsk(runs *[]tool.Call) tool.Tool {
	return tool.Tool{
		Name: "add", Parameters: []byte(anyObject), Approval: tool.Auto,
		MustAsk: func(args json.RawMessage) bool { return strings.Contains(string(args), "page") },
		Run: func(_ context.Context, c tool.Call) (string, error) {
			*runs = append(*runs, c)
			return "added", nil
		},
	}
}

// A call MustAsk picks out asks under the person's own policy although
// they marked nothing, "always allow" is not offered for it, and the tool
// is told it was approved. A call it does not pick out runs unasked and is
// told it was not.
func TestMustAskHoldsUnderThePersonsPolicy(t *testing.T) {
	ctx := context.Background()
	var runs []tool.Call
	a := build(t, agent.Config{
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{call("i1", "add", `{"ref":"id"}`), call("p1", "add", `{"ref":"page"}`)}},
			modeltest.Reply{Text: "done"},
		),
		Tools:    []tool.Tool{pagesAsk(&runs)},
		AskFirst: func(context.Context, tool.Call) (bool, error) { return false, nil },
	})
	var rec recorder
	res, err := a.Start(ctx, "t1", user("go"), rec.sink)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}
	asked := rec.of(event.ApprovalNeeded)
	if len(asked) != 1 || asked[0].CallID != "p1" || asked[0].AllowAlways {
		t.Fatalf("approval events = %+v; want p1 alone, without always allow", asked)
	}
	pending, err := a.Pending(ctx, "t1")
	if err != nil || len(pending) != 1 || pending[0].AllowAlways {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "p1", Verdict: agent.ApproveAlways}}, event.Discard); err == nil {
		t.Fatal("always allow was accepted for a call that must ask")
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "p1", Verdict: agent.Approve}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "i1" || runs[0].Approved || runs[1].ID != "p1" || !runs[1].Approved {
		t.Fatalf("runs = %+v; want i1 unapproved, then p1 approved", runs)
	}
}

// Trust cannot waive it either, and neither can an earlier "always allow"
// for the same tool in the same turn.
func TestMustAskIsNotWaivedByTrust(t *testing.T) {
	ctx := context.Background()
	var runs []tool.Call
	a := build(t, agent.Config{
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{call("p1", "add", `{"ref":"page"}`)}},
			modeltest.Reply{Text: "done"},
		),
		Tools:   []tool.Tool{pagesAsk(&runs)},
		Trusted: func(context.Context, tool.Call) (bool, error) { return true, nil },
	})
	res, err := a.Start(ctx, "t1", user("go"), event.Discard)
	if err != nil || res.Outcome != agent.Paused || len(runs) != 0 {
		t.Fatalf("start: %+v, %v, runs %d; want a pause before the call", res, err, len(runs))
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "p1", Verdict: agent.Reject, Reason: "no"}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatal("a rejected call ran")
	}
}

// The person's edit while approving is what runs, and is approved.
func TestMustAskRunsTheEditedArguments(t *testing.T) {
	ctx := context.Background()
	var runs []tool.Call
	var count atomic.Int32
	a := build(t, agent.Config{
		Model: script(
			modeltest.Reply{Calls: []model.ToolCall{call("p1", "add", `{"ref":"page one"}`)}},
			modeltest.Reply{Text: "done"},
		),
		Tools: []tool.Tool{pagesAsk(&runs), countingTool("other", tool.Auto, &count)},
	})
	if _, err := a.Start(ctx, "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Resume(ctx, "t1", []agent.Decision{{CallID: "p1", Verdict: agent.Approve, Args: json.RawMessage(`{"ref":"page two"}`)}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || string(runs[0].Args) != `{"ref":"page two"}` || !runs[0].Approved {
		t.Fatalf("runs = %+v", runs)
	}
}
