package chat

import (
	"context"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

func row(assistant bool, content string) HistoryMessage {
	return HistoryMessage{Assistant: assistant, Content: []byte(content)}
}

func TestSavedMessagesBecomeAConversationThatAlternates(t *testing.T) {
	got := transcriptFromRows([]HistoryMessage{
		row(false, `[{"type":"text","text":"First question"}]`),
		row(true, `[{"type":"text","text":"Part one."},{"type":"tool-call","toolCallId":"x","toolName":"t","args":{}},{"type":"text","text":"Part two."}]`),
		// An answer that failed before any text, and the question asked again.
		row(false, `[{"type":"text","text":"Second question"}]`),
		row(true, `[{"type":"status","code":"no_response","text":"No response was produced."}]`),
		row(false, `[{"type":"text","text":"Second question, again"},{"type":"image","image":"data:..."}]`),
		row(true, `{"not":"a list of parts"}`),
		row(true, `[{"type":"text","text":"   "}]`),
	})
	want := []model.Message{
		{Role: model.RoleUser, Content: "First question"},
		{Role: model.RoleAssistant, Content: "Part one.\n\nPart two."},
		{Role: model.RoleUser, Content: "Second question\n\nSecond question, again"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("message %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// The new question joins an unanswered one instead of following it.
	with := withQuestion(got, model.User("Third"))
	if len(with) != 3 || with[2].Content != "Second question\n\nSecond question, again\n\nThird" {
		t.Errorf("with the new question: %+v", with)
	}
	if fresh := withQuestion(nil, model.User("Hello")); len(fresh) != 1 || fresh[0].Role != model.RoleUser {
		t.Errorf("an empty thread: %+v", fresh)
	}
	if after := withQuestion(want[:2], model.User("Next")); len(after) != 3 || after[2].Content != "Next" {
		t.Errorf("after an answer: %+v", after)
	}
}

// A paused turn's choices are kept whole and replaced whole by the next.
func TestATurnsChoicesAreKeptAcrossAPause(t *testing.T) {
	kept := savedTurns{checkpoint.NewMemory()}
	ctx := context.Background()
	if _, found, err := kept.load(ctx, 1); found || err != nil {
		t.Fatalf("found=%v err=%v before anything was kept", found, err)
	}
	first := Turn{ThreadID: 1, UserID: ada, Scope: "all", PreferredTool: "pubmed", DocumentIDs: []int64{4}, FolderIDs: []int64{9}}
	if err := kept.keep(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := Turn{ThreadID: 1, UserID: ben, Scope: "documents"}
	if err := kept.keep(ctx, second); err != nil {
		t.Fatalf("the next turn could not replace the last one's choices: %v", err)
	}
	got, found, err := kept.load(ctx, 1)
	if err != nil || !found || got.UserID != ben || got.PreferredTool != "" || got.DocumentIDs != nil {
		t.Fatalf("got %+v found=%v err=%v", got, found, err)
	}
	if _, found, _ := kept.load(ctx, 2); found {
		t.Fatal("another thread's choices were returned")
	}
}

func TestTheTitlerKeepsAShortTitleAndRefusesAnEssay(t *testing.T) {
	for reply, want := range map[string]string{
		`"Cell Division Basics."`:   "Cell Division Basics",
		"  Mitosis\n phases  ":      "Mitosis phases",
		"“Les phases de la mitose”": "Les phases de la mitose",
	} {
		titler, script := ModelTitler{}, modeltest.NewScript(modeltest.Reply{Text: reply})
		if got, err := titler.Title(context.Background(), script, "q"); err != nil || got != want {
			t.Errorf("%q: got %q, %v", reply, got, err)
		}
	}
	for name, reply := range map[string]string{"an essay": strings.Repeat("word ", 40), "nothing": "  "} {
		titler, script := ModelTitler{}, modeltest.NewScript(modeltest.Reply{Text: reply})
		if got, err := titler.Title(context.Background(), script, "q"); err == nil {
			t.Errorf("%s was kept as a title: %q", name, got)
		}
	}
	// Only the start of a long message is sent.
	script := modeltest.NewScript(modeltest.Reply{Text: "T"})
	_, _ = ModelTitler{}.Title(context.Background(), script, strings.Repeat("é", 5000))
	if sent := script.Requests()[0].Messages[0].Content; len([]rune(sent)) > len([]rune(titlePrompt))+500 {
		t.Errorf("%d characters were sent to name a thread", len([]rune(sent)))
	}
}

func TestAUserMessagesSavedContent(t *testing.T) {
	plain, _ := userContent("Hello <b>", nil)
	if string(plain) != `[{"type":"text","text":"Hello <b>"}]` {
		t.Errorf("plain = %s", plain)
	}
	chips, _ := userContent("Q", []Mention{
		{ID: 4, Title: "Doc", DocumentType: "FILE"},
		{ID: 5, Title: "Mail", Kind: "connector", ConnectorType: "IMAP"},
		{ID: 6, Title: "Chat", Kind: "thread"},
		{ID: 7, Title: "Odd", Kind: "galaxy", DocumentType: "NOTE"},
		{ID: 0, Title: "No id", Kind: "folder"},
		{ID: 8, Title: " ", Kind: "folder"},
		{ID: 9, Title: "Connector without a type", Kind: "connector"},
	})
	want := `[{"type":"text","text":"Q"},{"type":"mentioned-documents","documents":[` +
		`{"id":4,"title":"Doc","kind":"doc","document_type":"FILE"},` +
		`{"id":5,"title":"Mail","kind":"connector","connector_type":"IMAP","account_name":"Mail"},` +
		`{"id":6,"title":"Chat","kind":"thread"},` +
		`{"id":7,"title":"Odd","kind":"doc","document_type":"NOTE"}]}]`
	if string(chips) != want {
		t.Errorf("chips\n got %s\nwant %s", chips, want)
	}
}
