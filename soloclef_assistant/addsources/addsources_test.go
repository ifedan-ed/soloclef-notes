package addsources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// adder stands in for the research import: it records what it was asked and
// answers what the test says.
type adder struct {
	userID     string
	notebookID int64
	threadID   int64
	refs       []string
	approved   bool
	answer     []Outcome
	err        error
	// wrote is the addresses the person wrote in the conversation;
	// wroteErr makes that lookup fail.
	wrote    map[string]bool
	wroteErr error
}

func (a *adder) UserWrote(_ context.Context, _ string, _ int64, addresses []string) (map[string]bool, error) {
	if a.wroteErr != nil {
		return nil, a.wroteErr
	}
	out := map[string]bool{}
	for _, address := range addresses {
		if a.wrote[address] {
			out[address] = true
		}
	}
	return out, nil
}

func (a *adder) Add(_ context.Context, request Request) ([]Outcome, error) {
	a.userID, a.notebookID, a.threadID, a.refs, a.approved = request.UserID, request.NotebookID, request.ThreadID, request.Refs, request.PagesApproved
	return a.answer, a.err
}

func call(t *testing.T, built tool.Tool, args string) (string, error) {
	t.Helper()
	return built.Run(context.Background(), tool.Call{ID: "c1", Name: ToolName, Args: json.RawMessage(args)})
}

// The account and the notebook are the turn's, never the model's: an
// argument naming another notebook is not read at all.
func TestTheToolAddsForTheTurnsAccountAndNotebookOnly(t *testing.T) {
	stub := &adder{answer: []Outcome{{Ref: "38123456", Status: StatusAdded, DocumentID: 7, Title: "A paper", Reading: true}}}
	joined := &documents.Joined{}
	out, err := call(t, Tool(stub, "ada", 12, 34, joined), `{"sources":[" 38123456 ","38123456",""],"notebook_id":99}`)
	if err != nil {
		t.Fatal(err)
	}
	if stub.userID != "ada" || stub.notebookID != 12 || stub.threadID != 34 || len(stub.refs) != 1 || stub.refs[0] != "38123456" || stub.approved {
		t.Fatalf("asked for %s %d %d %q (approved %v)", stub.userID, stub.notebookID, stub.threadID, stub.refs, stub.approved)
	}
	if ids := joined.IDs(); len(ids) != 1 || ids[0] != 7 {
		t.Errorf("the turn's search was told of %v", ids)
	}
	var result Result
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.NotebookID != 12 || !strings.Contains(result.Note, "Do not say you have read") {
		t.Errorf("answer %s (%v)", out, err)
	}
}

// A list over the bound is refused whole, so the model sends the rest and
// never believes a paper was added that was not; nothing is asked of the
// import.
func TestMoreThanTheBoundIsRefusedWhole(t *testing.T) {
	stub := &adder{}
	refs := make([]string, MaxPerCall+1)
	for i := range refs {
		refs[i] = `"` + strings.Repeat("1", i+1) + `"`
	}
	_, err := call(t, Tool(stub, "ada", 12, 34, &documents.Joined{}), `{"sources":[`+strings.Join(refs, ",")+`]}`)
	if err == nil || !strings.Contains(err.Error(), "several calls") || stub.refs != nil {
		t.Fatalf("err=%v asked=%v", err, stub.refs)
	}
	if _, err := call(t, Tool(stub, "ada", 12, 34, &documents.Joined{}), `{"sources":[]}`); err == nil {
		t.Error("an empty list was accepted")
	}
}

func TestANotebookThatIsGoneIsSaidInWords(t *testing.T) {
	stub := &adder{err: ErrNotebookAbsent}
	if _, err := call(t, Tool(stub, "ada", 12, 34, &documents.Joined{}), `{"sources":["1"]}`); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("err = %v", err)
	}
}

// Nothing not added is joined to the search, and a source not still being
// read needs no note.
func TestOnlySourcesAreJoinedAndTheNoteIsForReadingOnes(t *testing.T) {
	stub := &adder{answer: []Outcome{
		{Ref: "x", Status: StatusNotAdded, Reason: "Not an id."},
		{Ref: "2", Status: StatusAlready, DocumentID: 3, Title: "Held"},
	}}
	joined := &documents.Joined{}
	out, err := call(t, Tool(stub, "ada", 12, 34, joined), `{"sources":["x","2"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if ids := joined.IDs(); len(ids) != 1 || ids[0] != 3 {
		t.Errorf("joined %v", ids)
	}
	if strings.Contains(out, `"note"`) {
		t.Errorf("a note with nothing being read: %s", out)
	}
	if Finished(out) != "Added no sources" {
		t.Errorf("line %q", Finished(out))
	}
}

func TestTheLineCountsWhatWasAdded(t *testing.T) {
	for result, want := range map[string]string{
		`{"notebook_id":1,"items":[{"status":"added"},{"status":"added"},{"status":"already_a_source"}]}`: "Added 2 sources",
		`{"notebook_id":1,"items":[{"status":"added"}]}`:                                                  "Added 1 source",
		`not json`: "Added no sources",
	} {
		if got := Finished(result); got != want {
			t.Errorf("%s: %q, want %q", result, got, want)
		}
	}
}

// The card carries the rows' fields it draws and nothing else; a reason and
// the identity only for a source that was not added.
func TestTheCardIsAnAllowlist(t *testing.T) {
	text := `{"notebook_id":12,"items":[{"ref":"38123456","status":"added","document_id":7,"title":"A paper","url":"https://pubmed.ncbi.nlm.nih.gov/38123456/","reading":true},{"ref":"zz","status":"not_added","reason":"Not an id."}],"note":"n","secret":"s"}`
	payload, ok := Card(event.Event{Tool: ToolName, Text: text})
	if !ok {
		t.Fatal("no card")
	}
	encoded, _ := json.Marshal(payload)
	got := string(encoded)
	want := `{"notebook_id":12,"items":[{"status":"added","document_id":7,"title":"A paper","reading":true},{"status":"not_added","reason":"Not an id.","ref":"zz"}]}`
	if got != want {
		t.Errorf("card %s\nwant %s", got, want)
	}
	for _, other := range []event.Event{{Tool: "other", Text: text}, {Tool: ToolName, Text: text, IsError: true}, {Tool: ToolName, Text: "nope"}} {
		if _, ok := Card(other); ok {
			t.Errorf("a card for %+v", other)
		}
	}
}

// A call naming only papers, by id or by their canonical links, runs
// without asking; a call naming any other address must be approved by the
// person, and the import is told whether it was.
func TestPapersRunAndPagesAsk(t *testing.T) {
	built := Tool(&adder{}, "ada", 12, 34, &documents.Joined{})
	if built.Approval != tool.Auto || built.MustAsk == nil {
		t.Fatalf("approval %v, MustAsk set %v", built.Approval, built.MustAsk != nil)
	}
	for args, asks := range map[string]bool{
		`{"sources":["38123456"]}`:                                                      false,
		`{"sources":["PMID: 38123456","PMC1234567"]}`:                                   false,
		`{"sources":["10.1056/NEJMoa2034577","https://doi.org/10.1056/NEJMoa2034577"]}`: false,
		`{"sources":["arXiv:2401.01234v2","https://arxiv.org/abs/2401.01234"]}`:         false,
		`{"sources":["https://pubmed.ncbi.nlm.nih.gov/38123456/?q=x"]}`:                 false,
		`{"sources":["https://www.biorxiv.org/content/10.1101/2024.01.15.575678v2"]}`:   false,
		`{"sources":["https://example.org/tides"]}`:                                     true,
		`{"sources":["38123456","https://evil.example/?d=secret"]}`:                     true,
		`{"sources":["https://arxiv.org/list/cs.LG"]}`:                                  true,
		`{"sources":["the tides paper"]}`:                                               false,
		`{"sources":"not a list"}`:                                                      false,
	} {
		if got := built.MustAsk(json.RawMessage(args)); got != asks {
			t.Errorf("%s: asks %v, want %v", args, got, asks)
		}
	}
	// A page the person wrote in the conversation is theirs: no question.
	// Every page must be theirs; one the model added beside it still asks,
	// and a lookup that fails asks.
	theirs := Tool(&adder{wrote: map[string]bool{"https://example.org/tides": true}}, "ada", 12, 34, &documents.Joined{})
	if theirs.MustAsk(json.RawMessage(`{"sources":["https://example.org/tides"]}`)) {
		t.Error("a page the person wrote was asked about")
	}
	if !theirs.MustAsk(json.RawMessage(`{"sources":["https://example.org/tides","https://evil.example/?d=secret"]}`)) {
		t.Error("a page the model added beside the person's was not asked about")
	}
	broken := Tool(&adder{wroteErr: errors.New("database away")}, "ada", 12, 34, &documents.Joined{})
	if !broken.MustAsk(json.RawMessage(`{"sources":["https://example.org/tides"]}`)) {
		t.Error("a failed lookup did not ask")
	}

	stub := &adder{}
	page := Tool(stub, "ada", 12, 34, &documents.Joined{})
	if _, err := page.Run(context.Background(), tool.Call{ID: "c1", Name: ToolName, Args: json.RawMessage(`{"sources":["https://example.org/tides"]}`), Approved: true}); err != nil {
		t.Fatal(err)
	}
	if !stub.approved {
		t.Error("the import was not told the person approved the call")
	}
}
