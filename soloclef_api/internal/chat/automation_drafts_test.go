package chat

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/automationdrafts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/figuretool"
)

const draftID = "0b5c2f7e-1d2a-4c3b-9e8f-7a6b5c4d3e2f"

// shelvedDrafts keeps drafts the way automationdrafts.Store does, in memory.
type shelvedDrafts struct{ saved int }

func (d *shelvedDrafts) Save(context.Context, string, []byte, string) (string, error) {
	d.saved++
	return draftID, nil
}
func (*shelvedDrafts) Notebooks(context.Context, string) ([]automationdrafts.Choice, error) {
	return nil, nil
}
func (*shelvedDrafts) Folders(context.Context, string) ([]automationdrafts.Choice, error) {
	return nil, nil
}

// passingCheck is the create route's check finding nothing wrong.
type passingCheck struct{}

func (passingCheck) Check(context.Context, string, []byte) ([]string, error) { return nil, nil }

func drafting(shelf *shelvedDrafts) *automationdrafts.Tools {
	return &automationdrafts.Tools{Drafts: shelf, Checker: passingCheck{}, SendsMail: true,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}
}

const mondayDigest = `{"name":"Asthma papers","task":"Summarise new papers on asthma.","schedule":{"cron":"0 8 * * 1","timezone":"Africa/Lagos"},"email_me":true}`

// The draft's card is the person's question: a mark "ask first" on the tool
// (which the settings page does not offer, but a row could hold) adds no
// second one. The stream carries the card's payload, the draft's id, and
// the prompt says the device's time zone.
func TestAnAutomationDraftIsItsOwnQuestion(t *testing.T) {
	shelf := &shelvedDrafts{}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: call("d1", automationdrafts.ToolName, mondayDigest)},
		modeltest.Reply{Text: "It is ready for you to check."},
	)
	a := newApp(t, script, nil, func(s *Service, sc *scripted) {
		made, _, _, _ := production(t, script, script, sc.rows, sc.store, connected{})
		made.Automations = drafting(shelf)
		s.Assembler = made
	})
	a.rows.askFirst[markKey(ada, MarkedTool{Name: automationdrafts.ToolName})] = true
	frames, _ := a.ask(ada, `{"chat_id":1,"user_query":"Every Monday at 8, summarise new asthma papers and email me.","time_zone":"Africa/Lagos"}`)
	if interrupts := find(frames, "data-interrupt-request"); len(interrupts) != 0 || shelf.saved != 1 {
		t.Fatalf("interrupts %v, drafts saved %d: the card is the only question", interrupts, shelf.saved)
	}
	var card map[string]any
	for _, f := range find(frames, "tool-output-available") {
		if f["toolCallId"] == "d1" {
			card, _ = f["output"].(map[string]any)
		}
	}
	if card["status"] != "drafted" || card["draft_id"] != draftID || len(card) != 2 {
		t.Errorf("the card's payload = %v", card)
	}
	system := script.Requests()[0].Messages[0].Content
	for _, says := range []string{"<automations>", "time zone Africa/Lagos", "Ask a short clarifying question"} {
		if !strings.Contains(system, says) {
			t.Errorf("the system prompt lacks %q", says)
		}
	}
}

// A run nobody watches drafts no automation: nobody is there to press
// Create, and an automation must not make automations.
func TestAnUnattendedTurnIsNotOfferedTheDrafter(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"})
	made, _, _, _ := production(t, script, script, newRows(), checkpoint.NewMemory(), connected{})
	made.Automations = drafting(&shelvedDrafts{})
	chat, system, _ := shown(t, made, Turn{ThreadID: 1, UserID: ada, Scope: "documents"}, script)
	if has(chat, automationdrafts.ToolName) != 1 || !strings.Contains(system, "<automations>") {
		t.Fatalf("a chat turn was offered %v", chat)
	}
	run, system, _ := shown(t, made, Turn{ThreadID: 1, UserID: ada, Scope: "documents", Unattended: &assembly.Unattended{TrustAskFirst: true}}, script)
	if has(run, automationdrafts.ToolName) != 0 || strings.Contains(system, "<automations>") {
		t.Fatalf("an unattended turn was offered %v", run)
	}
}

// A zone the server does not know is refused before the turn starts, so a
// schedule is never drafted in it.
func TestAnUnknownTimeZoneIsRefused(t *testing.T) {
	a := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "ok"}), nil)
	response := a.request(ada, http.MethodPost, TurnPath, `{"chat_id":1,"user_query":"hello","time_zone":"Mars/Olympus"}`)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(raw), "time_zone") {
		t.Fatalf("%d %s", response.StatusCode, raw)
	}
}

// Figures are drawn in a chat a person reads, whatever was picked, and the
// prompt says when to draw; a run nobody reads is not offered them.
func TestAFigureIsOfferedToAChatAndNotToAnUnattendedRun(t *testing.T) {
	script := modeltest.NewScript(modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"}, modeltest.Reply{Text: "ok"})
	made, _, _, _ := production(t, script, script, newRows(), checkpoint.NewMemory(), connected{})
	chat, system, _ := shown(t, made, Turn{ThreadID: 1, UserID: ada, Scope: "documents"}, script)
	if has(chat, figuretool.ToolName) != 1 || !strings.Contains(system, "<figures>\n"+figuretool.Guidance+"\n</figures>") {
		t.Fatalf("a chat turn was offered %v", chat)
	}
	if strings.Contains(strings.ToLower(system), "mermaid") {
		t.Error("the prompt offers a figure that is not drawn")
	}
	run, system, _ := shown(t, made, Turn{ThreadID: 1, UserID: ada, Scope: "documents", Unattended: &assembly.Unattended{TrustAskFirst: true}}, script)
	if has(run, figuretool.ToolName) != 0 || strings.Contains(system, "<figures>") {
		t.Fatalf("an unattended turn was offered %v", run)
	}
	// Switched off in the composer, it is neither offered nor described.
	off, system, _ := shown(t, made, Turn{ThreadID: 1, UserID: ada, Scope: "documents", DisabledTools: []string{figuretool.ToolName}}, script)
	if has(off, figuretool.ToolName) != 0 || strings.Contains(system, "<figures>") {
		t.Fatalf("a switched-off figure tool was offered %v", off)
	}
}
