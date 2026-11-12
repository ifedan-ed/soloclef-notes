package parts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/uistream"
)

func builder() *Builder {
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	card := func(ev event.Event) any {
		if ev.Tool == "save_artifact" {
			return json.RawMessage(ev.Text)
		}
		return uistream.LengthOnly(ev)
	}
	return NewBuilder(card, nil, func() time.Time { at = at.Add(time.Second); return at }, func(kind string) string { return kind + "_1" })
}

func encoded(t *testing.T, parts []Part) string {
	t.Helper()
	out, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func keep(s string) string { return s }

// A turn that thinks, searches and answers, as the web reader would build it.
func TestATurnWithReasoningAToolAndAnAnswer(t *testing.T) {
	b := builder()
	for _, ev := range []event.Event{
		{Kind: event.ReasoningDelta, Text: "Let me "},
		{Kind: event.ReasoningDelta, Text: "look."},
		{Kind: event.ToolCallStarted, CallID: "c1", Tool: "search_knowledge_base"},
		{Kind: event.ToolCallDelta, CallID: "c1", Text: `{"query":`},
		{Kind: event.ToolCallDelta, CallID: "c1", Text: `"mitosis"}`},
		{Kind: event.ToolCallReady, CallID: "c1", Tool: "search_knowledge_base", Args: `{"query":"mitosis"}`},
		{Kind: event.ToolResult, CallID: "c1", Tool: "search_knowledge_base", Text: "0123456789"},
		{Kind: event.TextDelta, Text: "Four phases "},
		{Kind: event.TextDelta, Text: "[1]."},
		{Kind: event.Done},
	} {
		b.Event(ev)
	}
	rewrite := func(s string) string { return strings.ReplaceAll(s, "[1]", "[citation:481]") }
	want := `[` +
		`{"type":"reasoning","text":"Let me look.","id":"reasoning_1","status":"completed","startedAt":"2026-11-11T09:00:01+00:00","completedAt":"2026-11-11T09:00:02+00:00"},` +
		`{"type":"tool-call","toolCallId":"c1","toolName":"search_knowledge_base","args":{"query":"mitosis"},"argsText":"{\n  \"query\": \"mitosis\"\n}","result":{"status":"completed","result_length":10},"langchainToolCallId":"c1"},` +
		`{"type":"text","text":"Four phases [citation:481]."}]`
	if got := encoded(t, b.Finish(nil, rewrite)); got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

// What a nested agent says is its working. Only its tool calls show.
func TestANestedAgentShowsAsCardsAndNeverAsText(t *testing.T) {
	b := builder()
	for _, ev := range []event.Event{
		{Kind: event.ToolCallStarted, CallID: "d1", Tool: "deliverables"},
		{Kind: event.ToolCallReady, CallID: "d1", Tool: "deliverables", Args: `{"task":"make cards"}`},
		{Kind: event.TextDelta, Parent: "d1", Text: "I will now save."},
		{Kind: event.ReasoningDelta, Parent: "d1", Text: "thinking"},
		{Kind: event.ToolCallStarted, Parent: "d1", CallID: "d1/s1", Tool: "save_artifact"},
		{Kind: event.ToolCallDelta, Parent: "d1", CallID: "d1/s1", Text: `{"ti`},
		{Kind: event.ToolCallReady, Parent: "d1", CallID: "d1/s1", Tool: "save_artifact", Args: `{"title":"T"}`},
		{Kind: event.ToolResult, Parent: "d1", CallID: "d1/s1", Tool: "save_artifact", Text: `{"status":"saved","artifact_id":7}`},
		{Kind: event.Done, Parent: "d1"},
		{Kind: event.ToolResult, CallID: "d1", Tool: "deliverables", Text: "Saved."},
		{Kind: event.TextDelta, Text: "Your deck is ready."},
		{Kind: event.Done},
	} {
		b.Event(ev)
	}
	parts := b.Finish(nil, keep)
	if len(parts) != 3 || parts[0].ToolName != "deliverables" || parts[1].ToolName != "save_artifact" || parts[2].Text != "Your deck is ready." {
		t.Fatalf("parts = %s", encoded(t, parts))
	}
	// The nested call's card is keyed by its full id, and carries the id
	// the specialist's model gave it beside that.
	if parts[1].ToolCallID != "d1/s1" || parts[1].ModelCallID != "s1" || string(parts[1].Result) != `{"status":"saved","artifact_id":7}` {
		t.Fatalf("nested card = %+v", parts[1])
	}
	if strings.Contains(encoded(t, parts), "I will now save") || strings.Contains(encoded(t, parts), "thinking") {
		t.Fatal("the specialist's words were saved into the answer")
	}
}

func TestTextBeforeAndAfterAToolAreTwoParts(t *testing.T) {
	b := builder()
	b.Event(event.Event{Kind: event.TextDelta, Text: "One moment."})
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "c1", Tool: "t", Args: `{}`})
	b.Event(event.Event{Kind: event.ToolResult, CallID: "c1", Tool: "t", Text: "ok"})
	b.Event(event.Event{Kind: event.TextDelta, Text: "Done."})
	parts := b.Finish(nil, keep)
	if len(parts) != 3 || parts[0].Text != "One moment." || parts[1].Type != TypeToolCall || parts[2].Text != "Done." {
		t.Fatalf("parts = %s", encoded(t, parts))
	}
}

// A turn that was stopped: what streamed is kept, the open reasoning is
// marked interrupted, and the call that never finished is marked aborted.
func TestAStoppedTurnKeepsWhatItHadAndMarksWhatWasCutOff(t *testing.T) {
	b := builder()
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "c1", Tool: "slow", Args: `{}`})
	b.Event(event.Event{Kind: event.ReasoningDelta, Text: "Half a thou"})
	parts := b.Finish(nil, keep)
	if parts[0].State != "aborted" || parts[0].Result != nil {
		t.Fatalf("the unfinished call = %+v", parts[0])
	}
	if parts[1].Status != "interrupted" || parts[1].CompletedAt == "" || parts[1].Text != "Half a thou" {
		t.Fatalf("the open reasoning = %+v", parts[1])
	}
}

// A call waiting for approval is not aborted: its card is the approval card.
func TestACallWaitingForApprovalIsNotAborted(t *testing.T) {
	b := builder()
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "w1", Tool: "write_note", Args: `{"a":1}`})
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "x1", Tool: "other", Args: `{}`})
	b.Event(event.Event{Kind: event.Paused})
	parts := b.Finish(map[string]bool{"w1": true}, keep)
	if parts[0].State != "" || parts[1].State != "aborted" {
		t.Fatalf("states = %q, %q", parts[0].State, parts[1].State)
	}
	if strings.Contains(encoded(t, parts[:1]), `"state"`) || strings.Contains(encoded(t, parts[:1]), `"result"`) {
		t.Fatalf("a waiting call was saved with a state or a result: %s", encoded(t, parts[:1]))
	}
}

// A resumed turn's first event is the result of a call made before the
// pause. It is kept for the card that is already saved, and written there.
func TestAResultForACallMadeBeforeAPauseIsKeptForItsSavedCard(t *testing.T) {
	b := builder()
	b.Event(event.Event{Kind: event.ToolResult, CallID: "w1", Tool: "write_note", Text: "written"})
	b.Event(event.Event{Kind: event.TextDelta, Text: "Noted."})
	if parts := b.Finish(nil, keep); len(parts) != 1 || parts[0].Text != "Noted." {
		t.Fatalf("the resumed message = %s", encoded(t, parts))
	}
	late := b.Late()
	if string(late["w1"]) != `{"status":"completed","result_length":7}` {
		t.Fatalf("late = %v", late)
	}
	paused := `[{"type":"text","text":"I will note it."},{"type":"tool-call","toolCallId":"w1","toolName":"write_note","args":{"a":1},"future_field":[1,2]},{"type":"tool-call","toolCallId":"x9","toolName":"other","args":{},"state":"aborted"}]`
	filled, changed, err := Fill([]byte(paused), late)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var back []map[string]any
	if err := json.Unmarshal(filled, &back); err != nil {
		t.Fatal(err)
	}
	card := back[1]
	if card["result"].(map[string]any)["result_length"] != 7.0 || card["future_field"] == nil || card["toolName"] != "write_note" {
		t.Errorf("the filled card = %v", card)
	}
	// A call the results do not name is left as it was.
	if back[2]["state"] != "aborted" || back[2]["result"] != nil {
		t.Errorf("another card was changed: %v", back[2])
	}
	// A result removes "aborted": a call that finished was not cut off.
	again, _, _ := Fill([]byte(paused), map[string]json.RawMessage{"x9": json.RawMessage(`{"ok":true}`)})
	if strings.Contains(string(again), "aborted") {
		t.Errorf("a finished call is still marked aborted: %s", again)
	}
	if _, changed, _ := Fill([]byte(paused), map[string]json.RawMessage{"zz": json.RawMessage(`1`)}); changed {
		t.Error("content was rewritten for a result that matches no card")
	}
	if _, _, err := Fill([]byte(`{"not":"a list"}`), late); err == nil {
		t.Error("content that is not a list of parts was accepted")
	}
}

func TestATurnThatProducedNothingSaysSo(t *testing.T) {
	want := `[{"type":"status","code":"no_response","text":"No response was produced."}]`
	if got := encoded(t, builder().Finish(nil, keep)); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestArgumentsThatAreNotAnObjectAreSavedAsAnEmptyOne(t *testing.T) {
	b := builder()
	b.Event(event.Event{Kind: event.ToolCallStarted, CallID: "c1", Tool: "t"})
	b.Event(event.Event{Kind: event.ToolCallDelta, CallID: "c1", Text: `{"broken`})
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "c1", Tool: "t", Args: `{"broken`})
	parts := b.Finish(nil, keep)
	if string(parts[0].Args) != `{}` || parts[0].ArgsText != `{}` {
		t.Fatalf("part = %+v", parts[0])
	}
}

func TestProgressIsVisibleWhileTheTurnRuns(t *testing.T) {
	b := builder()
	before := b.Version()
	b.Event(event.Event{Kind: event.ReasoningDelta, Text: "Thinking"})
	if b.Version() == before {
		t.Fatal("a change did not move the version")
	}
	snapshot := b.Snapshot()
	if len(snapshot) != 1 || snapshot[0].Status != "running" || snapshot[0].CompletedAt != "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	// A snapshot is a copy: the turn going on does not change it.
	b.Event(event.Event{Kind: event.ReasoningDelta, Text: " more"})
	if snapshot[0].Text != "Thinking" {
		t.Fatal("a snapshot changed after it was taken")
	}
	steady := b.Version()
	b.Event(event.Event{Kind: event.StepStarted})
	if b.Version() != steady {
		t.Fatal("an event that changes nothing moved the version")
	}
}

func TestTextIsNotHTMLEscapedAndAnEmptyTextPartKeepsItsField(t *testing.T) {
	got := encoded(t, []Part{{Type: TypeText, Text: "a < b && c"}, {Type: TypeText}})
	// encoding/json escapes inside a RawMessage-free outer Marshal call too,
	// so compare after one decode: the stored value must read back exactly.
	var back []map[string]any
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatal(err)
	}
	if back[0]["text"] != "a < b && c" {
		t.Fatalf("text read back as %v", back[0]["text"])
	}
	if _, has := back[1]["text"]; !has {
		t.Fatalf("an empty text part lost its text field: %s", got)
	}
}

func BenchmarkTextDelta(b *testing.B) {
	builder := builder()
	ev := event.Event{Kind: event.TextDelta, Text: "word "}
	b.ReportAllocs()
	for b.Loop() {
		builder.Event(ev)
	}
}

// A tool whose arguments are private content is saved as a call with no
// arguments, whether its arguments streamed or arrived whole, and whether
// the turn finished or a snapshot was taken half-way through them.
func TestArgumentsNamedAsUnsavedAreNotKept(t *testing.T) {
	b := builder()
	b.LeaveOutArguments("update_memory")
	b.Event(event.Event{Kind: event.ToolCallStarted, CallID: "m1", Tool: "update_memory"})
	b.Event(event.Event{Kind: event.ToolCallDelta, CallID: "m1", Text: `{"updated_memory":"## Facts\n- SEC`})
	if mid := encoded(t, b.Snapshot()); strings.Contains(mid, "SEC") {
		t.Fatalf("a snapshot taken while the arguments streamed kept them: %s", mid)
	}
	b.Event(event.Event{Kind: event.ToolCallDelta, CallID: "m1", Text: `RET"}`})
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "m1", Tool: "update_memory", Args: `{"updated_memory":"## Facts\n- SECRET"}`})
	b.Event(event.Event{Kind: event.ToolResult, CallID: "m1", Tool: "update_memory", Text: `{"status":"saved"}`})
	// Another tool's arguments in the same turn are kept as ever.
	b.Event(event.Event{Kind: event.ToolCallReady, CallID: "c1", Tool: "search_knowledge_base", Args: `{"query":"mitosis"}`})
	b.Event(event.Event{Kind: event.Done})
	got := encoded(t, b.Finish(nil, keep))
	if strings.Contains(got, "SECRET") || !strings.Contains(got, `"toolName":"update_memory","args":{},"argsText":"{}"`) || !strings.Contains(got, `"args":{"query":"mitosis"}`) {
		t.Fatalf("saved %s", got)
	}
}
