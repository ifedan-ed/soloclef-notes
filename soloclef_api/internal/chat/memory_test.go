package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/memory"
)

// A turn can write the asker's memory, and only the asker's: the tool is
// made for the account the turn runs for, with the copy of the document the
// prompt showed. The card says it was saved, and the document is in neither
// the saved card nor its arguments. It is the same in every thread of the
// account: there is no second, shared document for any of them to write.
func TestATurnWritesTheMemoryItsPromptShows(t *testing.T) {
	const document = `## Facts\n- 2026-11-11: Ada is PRIVATE-FACT`
	for _, c := range []struct {
		name, body string
		thread     int64
	}{
		{"one thread", `{"chat_id":1,"user_query":"Remember that I sit the bar exam in May"}`, 1},
		{"another thread", `{"chat_id":2,"user_query":"Remember that releases go out on Tuesdays"}`, 2},
	} {
		script := modeltest.NewScript(
			modeltest.Reply{Text: "Noted.", Calls: call("m1", memory.ToolName, `{"updated_memory":"`+document+`"}`)},
			modeltest.Reply{Text: "I will remember that."},
		)
		a := newApp(t, script, nil, func(s *Service, sc *scripted) {
			sc.rows.about = TurnContext{UserName: "Ada Lovelace", UserMemory: "Prefers metric units."}
			assembly, _, _, _ := production(t, script, script, sc.rows, sc.store, connected{})
			s.Assembler = assembly
		})
		frames, done := a.ask(ada, c.body)
		page := readAll(t, frames)
		if !done || page.failure != nil {
			t.Fatalf("%s: done %v, failure %v", c.name, done, page.failure)
		}
		if got := a.rows.remembered; len(got) != 1 || got[0].Shown != "Prefers metric units." || got[0].UserID != ada {
			t.Fatalf("%s: the tool was made for %+v", c.name, got)
		}
		if len(a.rows.saved) != 1 || !strings.Contains(a.rows.saved[0], "PRIVATE-FACT") {
			t.Fatalf("%s: saved %q", c.name, a.rows.saved)
		}
		if steps, _ := page.steps(); len(steps) != 1 || steps[0]["title"] != "Updated your memory" || steps[0]["kind"] != "memory.personal" || steps[0]["iconKey"] != "brain" {
			t.Errorf("%s: steps %v", c.name, steps)
		}

		// What is kept in the message: the card's result, and nothing of
		// the document.
		saved := a.rows.assistant(c.thread)[0]
		var card map[string]any
		for _, part := range contentOf(t, saved) {
			if part["toolName"] == memory.ToolName {
				card = part
			}
		}
		if card == nil {
			t.Fatalf("%s: the memory card was not saved", c.name)
		}
		if result, _ := json.Marshal(card["result"]); string(result) != `{"message":"Memory updated.","status":"saved"}` {
			t.Errorf("%s: the saved card's result = %s", c.name, result)
		}
		if args, _ := json.Marshal(card["args"]); string(args) != `{}` {
			t.Errorf("%s: the saved card's arguments = %s", c.name, args)
		}
		if kept, _ := json.Marshal(contentOf(t, saved)); strings.Contains(string(kept), "PRIVATE-FACT") {
			t.Errorf("%s: the memory document was saved in the message: %s", c.name, kept)
		}
	}
}

// A tool switched off in the composer's list is not offered and not in the
// prompt; the list the composer shows is the list that can be switched.
func TestAToolSwitchedOffIsNotOffered(t *testing.T) {
	names := func(disabled ...string) string {
		t.Helper()
		script := modeltest.NewScript(modeltest.Reply{Text: "ok"})
		a := newApp(t, script, nil, func(s *Service, sc *scripted) {
			assembly, _, _, _ := production(t, script, script, sc.rows, sc.store, connected{})
			s.Assembler = assembly
		})
		off, _ := json.Marshal(disabled)
		a.ask(ada, `{"chat_id":1,"user_query":"hello","retrieval_scope":"all","disabled_tools":`+string(off)+`}`)
		request := script.Requests()[0]
		var offered []string
		for _, spec := range request.Tools {
			offered = append(offered, spec.Name)
		}
		if off := strings.Contains(strings.Join(disabled, " "), "search_knowledge_base"); off == strings.Contains(request.Messages[0].Content, "<grounding>") {
			t.Errorf("with %v switched off, the prompt's grounding block is there: %v", disabled, !off)
		}
		return strings.Join(offered, " ")
	}
	all := names()
	for _, switchableTool := range pickerTools() {
		if !switchableTool.EnabledByDefault || switchableTool.Description == "" {
			t.Errorf("the composer's list has %+v", switchableTool)
		}
	}
	got := names("search_knowledge_base", memory.ToolName, "pubmed_search", "deliverables", "no_such_tool")
	// The specialist is not a switch, and a name that is no tool is ignored.
	if strings.Contains(got, "search_knowledge_base") || strings.Contains(got, memory.ToolName) || strings.Contains(got, "pubmed_search") || !strings.Contains(got, "deliverables") || !strings.Contains(got, "arxiv_search") {
		t.Errorf("offered %s (all: %s)", got, all)
	}
	// More names than a request may carry is refused, not cut.
	many := make([]string, maxDisabledTools+1)
	for i := range many {
		many[i] = "x"
	}
	off, _ := json.Marshal(many)
	a := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "ok"}), nil)
	response := a.request(ada, "POST", TurnPath, `{"chat_id":1,"user_query":"hello","disabled_tools":`+string(off)+`}`)
	defer response.Body.Close()
	if response.StatusCode != 422 {
		t.Errorf("an over-long list answered %d", response.StatusCode)
	}
}
