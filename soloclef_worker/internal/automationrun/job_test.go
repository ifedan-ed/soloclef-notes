package automationrun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
)

const ada = "11111111-1111-4111-8111-111111111111"

// shelf is the account's documents: one passage, found by every search.
type shelf struct{}

func (shelf) Embed(context.Context, string) ([]float32, error) { return []float32{1}, nil }

func (shelf) Nearest(context.Context, string, []float32, []int64, int) ([]documents.Neighbor, error) {
	return []documents.Neighbor{{ChunkID: 481, Score: 0.9}}, nil
}

func (shelf) KeywordRanks(context.Context, string, string, []int64, int) ([]documents.Ranked, error) {
	return []documents.Ranked{{ChunkID: 481, Rank: 1}}, nil
}

func (shelf) Chunks(_ context.Context, userID string, _, _ []int64) ([]documents.Chunk, error) {
	if userID != ada {
		return nil, nil
	}
	return []documents.Chunk{{ID: 481, DocumentID: 12, Title: "Cell biology", DocumentType: "FILE",
		Content: "Mitosis has four phases: prophase, metaphase, anaphase and telophase."}}, nil
}

func (shelf) ChunkIDsOfDocuments(context.Context, string, []int64) ([]int64, error) {
	return []int64{481}, nil
}

func (shelf) DocumentsInFolders(context.Context, string, []int64) ([]int64, error) { return nil, nil }

// noKeys is an account with no web search connected.
type noKeys struct{}

func (noKeys) Key(context.Context, string, websearch.Provider) (string, bool, error) {
	return "", false, nil
}

// account is the prompt's rows and the model choice of the one account.
type account struct{}

func (account) Context(_ context.Context, turn assembly.Turn) (assembly.TurnContext, error) {
	out := assembly.TurnContext{UserName: "Ada", UserMemory: "- 2026-11-01: Ada studies biology.", Citations: true}
	// A confined step reads its notebook the way Store.Context does: the
	// one notebook Ada has is 5, with one source.
	if turn.Unattended != nil && turn.Unattended.NotebookID != 0 {
		out.Notebook = &assembly.Notebook{ID: turn.Unattended.NotebookID}
		if turn.Unattended.NotebookID == 5 {
			out.Notebook.Title, out.Notebook.DocumentIDs, out.Notebook.SourceTitles = "Biology", []int64{12}, []string{"Cell biology"}
		}
	}
	return out, nil
}

// The assembly must not ask either of an unattended turn; the test fails
// if it does.
func (account) Trusted(context.Context, string, string) (bool, error) { return false, errAttended }
func (account) Trust(context.Context, string, string) error           { return errAttended }
func (account) ChatModel(context.Context, string) (string, error)     { return "", nil }

type offered []string

func (o offered) Names(context.Context) ([]string, error) { return o, nil }

// app is the account's one connected app, with one tool that asks first.
// It counts how often the tool really ran.
type app struct {
	mu   sync.Mutex
	runs int
	// standing is the account's "always allow" for the tool, given in chat.
	standing bool
}

const appTool = "post_message"

func (a *app) ForTurn(context.Context, string) (assembly.ConnectedTools, error) { return a, nil }

func (a *app) Tools() []tool.Tool {
	return []tool.Tool{{
		Name: appTool, Description: "Post a message to the team channel.", Approval: tool.Ask,
		Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		Run: func(context.Context, tool.Call) (string, error) {
			a.mu.Lock()
			a.runs++
			a.mu.Unlock()
			return "posted", nil
		},
	}}
}

func (a *app) Trusted(_ context.Context, name string) (bool, bool, error) {
	return a.standing, name == appTool, nil
}
func (a *app) Trust(context.Context, string) error {
	return errors.New("an unattended run stores no trust")
}
func (a *app) App(name string) (string, bool) { return "Team chat", name == appTool }
func (a *app) Close()                         {}

// ledger is the run's row, in memory, and the account's notebooks and
// connected accounts.
type ledger struct {
	mu     sync.Mutex
	run    Run
	status string
	ending *Ending
	next   int64
	// notes are the notes steps saved; noteErr makes a save fail.
	notes   []Note
	noteErr error
}

// Ada has one notebook, 5, and two connected accounts, one switched off.
func (l *ledger) Notebook(_ context.Context, userID string, notebookID int64) (string, bool, error) {
	return "Biology", userID == ada && notebookID == 5, nil
}

func (l *ledger) Connections(_ context.Context, userID string, ids []int64) ([]Connection, error) {
	var found []Connection
	for _, held := range []Connection{{ID: 31, Name: "Email (IMAP)", Kind: "EMAIL_IMAP_CONNECTOR", Enabled: true}, {ID: 32, Name: "Old <drive>", Kind: "GOOGLE_DRIVE_CONNECTOR"}} {
		for _, id := range ids {
			if id == held.ID && userID == ada {
				found = append(found, held)
			}
		}
	}
	return found, nil
}

func (l *ledger) SaveNote(_ context.Context, note Note) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.noteErr != nil {
		return 0, l.noteErr
	}
	l.notes = append(l.notes, note)
	return 4400 + int64(len(l.notes)), nil
}

// indexed records the documents queued for indexing.
type indexed struct {
	mu  sync.Mutex
	ids []int64
}

func (i *indexed) Index(_ context.Context, documentID int64) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.ids = append(i.ids, documentID)
	return nil
}

// recording is the assembly, remembering each turn it was asked to build
// and each model it opened.
type recording struct {
	assembly.Assembler
	mu    sync.Mutex
	turns []assembly.Turn
}

func (r *recording) Build(ctx context.Context, turn assembly.Turn) (assembly.Built, error) {
	r.mu.Lock()
	r.turns = append(r.turns, turn)
	r.mu.Unlock()
	return r.Assembler.Build(ctx, turn)
}

func (l *ledger) Claim(context.Context, int64) (Run, Claim, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status != "pending" {
		return Run{}, Taken, nil
	}
	l.status = "running"
	return l.run, Claimed, nil
}

func (l *ledger) Status(context.Context, int64) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status, nil
}

func (l *ledger) Finish(_ context.Context, _ int64, ending Ending) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ending = &ending
	if l.status != "running" {
		return false, nil
	}
	l.status = ending.Status
	return true, nil
}

func (l *ledger) NextWaiting(context.Context, int64) (int64, bool, error) {
	return l.next, l.next != 0, nil
}

type sent struct {
	mu       sync.Mutex
	messages []produce.Message
}

func (s *sent) Send(_ context.Context, message produce.Message) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return message.ID, nil
}

// fixture is a job over the chat's real assembly, a scripted model, the
// real page reader (which must always ask) and one connected app (which
// asks first).
type fixture struct {
	job    *Job
	rows   *ledger
	app    *app
	script *modeltest.Script
	sent   *sent
	// index is what was queued for indexing, built the turns the job
	// asked for, and opened the proxy names of the models that answered.
	index  *indexed
	built  *recording
	opened *[]string
}

func newFixture(t *testing.T, definition string, replies ...modeltest.Reply) *fixture {
	t.Helper()
	script := modeltest.NewScript(replies...)
	searcher, err := documents.NewSearcher(shelf{}, shelf{}, shelf{}, documents.Limits{Candidates: 200, MaxDocuments: 20, ContextChars: 26000, DocumentChars: 16000})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := academic.New(academic.Settings{HTTP: http.DefaultClient, NCBITool: "test"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := pagereader.New(pagereader.Settings{
		MaxPages: 5, MaxBytes: 1 << 20, Timeout: time.Second, MaxChars: 10_000, MaxRedirects: 2,
		UserAgent: "TestPageReader/1.0", Allow: pagereader.PublicAddress,
		MaxPDFBytes: 1 << 20, PDFTimeout: time.Second, PDFs: noPDFs{},
	})
	if err != nil {
		t.Fatal(err)
	}
	connected := &app{}
	store := checkpoint.NewMemory()
	var opened []string
	agents := assembly.Assembly{
		Models: &assembly.Models{Catalogue: offered{"chat-model", "other-model", "third-model"}, Choices: account{}, Default: "chat-model",
			Open: func(name string) (model.Model, error) {
				opened = append(opened, name)
				return script, nil
			},
			Own: ownmodels.NoConnections{}},
		Store: store, Rows: account{},
		Documents: searcher, Web: websearch.Search{Keys: noKeys{}, HTTP: http.DefaultClient}, Academic: sources,
		Pages: reader.Tool(), Connected: connected,
		Limits: assembly.Limits{MaxSteps: 20, MaxParallelTools: 4, Continuations: 2},
		Now:    func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }, Brand: "Soloclef Notes",
	}
	if err := agents.Check(); err != nil {
		t.Fatal(err)
	}
	rows := &ledger{status: "pending", run: Run{ID: 7, AutomationID: 3, UserID: ada, Name: "Digest", ThreadID: 900,
		Snapshot: []byte(definition), Inputs: []byte(`{"fired_at":"2026-11-12T09:00:00Z"}`)}}
	publisher, index, built := &sent{}, &indexed{}, &recording{Assembler: agents}
	job := &Job{
		Rows: rows, Assembler: built, Store: store, Publisher: publisher, Indexer: index,
		Timing: Timing{StatusInterval: 10 * time.Millisecond, RetryStep: time.Millisecond, MaxPauses: 5, ResultChars: 2000},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
	}
	if err := job.Check(); err != nil {
		t.Fatal(err)
	}
	return &fixture{job: job, rows: rows, app: connected, script: script, sent: publisher, index: index, built: built, opened: &opened}
}

func (f *fixture) run(t *testing.T) Ending {
	t.Helper()
	message := &celery.Message{Task: Task, Kwargs: map[string]any{"run_id": float64(7)}}
	if err := f.job.Run(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if f.rows.ending == nil {
		t.Fatal("the run was given no ending")
	}
	return *f.rows.ending
}

func plan(params string) string {
	return `{"name":"Digest","plan":[{"step_id":"digest","action":"agent_task","params":` + params + `}],"execution":{"max_retries":1,"retry_backoff":"none"}}`
}

func calls(id, name, args string) []model.ToolCall {
	return []model.ToolCall{{ID: id, Name: name, Args: args}}
}

func toolNames(request model.Request) string {
	names := make([]string, 0, len(request.Tools))
	for _, t := range request.Tools {
		names = append(names, t.Name)
	}
	return strings.Join(names, ",")
}

// A run is one turn of the chat's agent: it searches the owner's documents
// with the chat's own tool, answers, and the row is given the transcript,
// the result, the model and the tokens.
func TestARunIsAHeadlessTurnThatRecordsItsTranscriptResultAndTokens(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Summarise mitosis from my notes."}`),
		modeltest.Reply{Calls: calls("c1", documents.ToolName, `{"query":"mitosis"}`), Usage: model.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}},
		modeltest.Reply{Text: "Mitosis has four phases [1].", Usage: model.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220}},
	)
	ending := f.run(t)
	if ending.Status != Succeeded || ending.Error != nil || f.rows.status != Succeeded {
		t.Fatalf("ended %s (%v), row %s", ending.Status, ending.Error, f.rows.status)
	}
	// The label the model wrote is the marker the pages draw as a source
	// chip, from the passage the search numbered.
	if got := ending.Output["final_message"]; got != "Mitosis has four phases [citation:481]." {
		t.Errorf("final message %q", got)
	}
	if _, asked := ending.Output["email"]; asked {
		t.Errorf("a run whose automation asked for no email has %v", ending.Output["email"])
	}
	// The turn was built under the run's own thread, for the account's own
	// chat model.
	if turn := f.built.turns[0]; turn.ThreadID != 900 || turn.UserID != ada || turn.Unattended.Model != "" || turn.Unattended.NotebookID != 0 {
		t.Errorf("the turn was built as %+v, %+v", turn, turn.Unattended)
	}
	if usage := ending.Output["usage"].(map[string]int); usage["prompt_tokens"] != 300 || usage["completion_tokens"] != 30 || usage["total_tokens"] != 330 {
		t.Errorf("usage %v, want the two calls' sum", usage)
	}
	if ending.Output["model"] != "chat-model" {
		t.Errorf("model %v", ending.Output["model"])
	}
	step := ending.Steps[0]
	if step.Status != Succeeded || step.Attempts != 1 || step.Result.RetrievalScope != "documents" || len(step.Result.Refused) != 0 {
		t.Errorf("step %+v", step)
	}
	// The transcript: the call, the passage it found, the answer. The task
	// itself is not repeated.
	transcript := step.Result.Transcript
	if len(transcript) != 3 || transcript[0].Calls[0].Name != documents.ToolName || transcript[1].Tool != documents.ToolName ||
		!strings.Contains(transcript[1].Content, "prophase") || transcript[2].Content != "Mitosis has four phases [1]." {
		t.Errorf("transcript %+v", transcript)
	}
	// What the model was sent: the chat's prompt for this account, the
	// task with the note that nobody is present and what fired the run, and
	// only the tools of the documents scope. No memory writer and no
	// artifact specialist are offered, and the prompt does not describe them.
	first := f.script.Requests()[0]
	system, task := first.Messages[0].Content, first.Messages[len(first.Messages)-1].Content
	for _, piece := range []string{"Soloclef Notes's assistant", "<user_name>Ada</user_name>", "Ada studies biology", "search_knowledge_base"} {
		if !strings.Contains(system, piece) {
			t.Errorf("the system prompt lacks %q", piece)
		}
	}
	for _, absent := range []string{"<artifacts>", "<memory_protocol>", "update_memory"} {
		if strings.Contains(system, absent) {
			t.Errorf("the system prompt offers %q, which a run does not have", absent)
		}
	}
	for _, piece := range []string{"nobody present", "Summarise mitosis from my notes.", "<trigger>", "2026-11-12T09:00:00Z"} {
		if !strings.Contains(task, piece) {
			t.Errorf("the task lacks %q: %s", piece, task)
		}
	}
	if names := toolNames(first); names != documents.ToolName+","+appTool {
		t.Errorf("tools %s, want the document search and the connected app", names)
	}
	if store := f.job.Store.(*checkpoint.Memory); len(storedThreads(t, store)) != 0 {
		t.Errorf("the run's conversation was left in the store")
	}
}

// storedThreads is the threads a memory store still holds for the run.
func storedThreads(t *testing.T, store *checkpoint.Memory) []string {
	t.Helper()
	var held []string
	for _, thread := range []string{"automation-run-7-0-1", "automation-run-7-0-2"} {
		if cp, err := store.Load(context.Background(), thread); err == nil && cp.Seq > 0 {
			held = append(held, thread)
		}
	}
	return held
}

// A tool that asks first is refused when nobody is present: it does not
// run, the model is told why and finishes, and the run says what was
// refused and ends as needs_attention. The account's standing "always
// allow" from chat does not carry into an unattended run.
func TestAnAskFirstToolIsRefusedUnattended(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Post the digest to the team."}`),
		modeltest.Reply{Calls: calls("c1", appTool, `{"text":"Digest"}`)},
		modeltest.Reply{Text: "I could not post the digest: posting needs your approval."},
	)
	f.app.standing = true
	ending := f.run(t)
	if ending.Error["type"] != "approval_refused" {
		t.Errorf("the run's error is of type %v", ending.Error["type"])
	}
	if f.app.runs != 0 {
		t.Fatalf("the tool ran %d times with nobody to approve it", f.app.runs)
	}
	if ending.Status != NeedsAttention || f.rows.status != NeedsAttention {
		t.Fatalf("ended %s, row %s; want needs_attention", ending.Status, f.rows.status)
	}
	step := ending.Steps[0]
	if len(step.Result.Refused) != 1 || step.Result.Refused[0].Tool != appTool || !strings.Contains(step.Result.Refused[0].Reason, "asks before it acts") {
		t.Errorf("refused %+v", step.Result.Refused)
	}
	if reason := step.Result.Outcome.Reason; reason == nil || !strings.Contains(*reason, appTool) || !strings.Contains(ending.Error["message"].(string), appTool) {
		t.Errorf("the run does not name the refused tool: %v, %v", reason, ending.Error)
	}
	// The model was told, in the tool's result, and answered from there.
	second := f.script.Requests()[1]
	if told := second.Messages[len(second.Messages)-1].Content; !strings.Contains(told, "unattended run") {
		t.Errorf("the model was told %q", told)
	}
	if step.Result.FinalMessage != "I could not post the digest: posting needs your approval." {
		t.Errorf("final message %q", step.Result.FinalMessage)
	}
}

// The automation's own trust rule lets an ask-first tool run.
func TestTheAutomationsOwnTrustRuleLetsAnAskFirstToolRun(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Post the digest to the team.","auto_approve_all":true}`),
		modeltest.Reply{Calls: calls("c1", appTool, `{"text":"Digest"}`)},
		modeltest.Reply{Text: "Posted."},
	)
	ending := f.run(t)
	if f.app.runs != 1 || ending.Status != Succeeded || len(ending.Steps[0].Result.Refused) != 0 {
		t.Fatalf("ran %d times, ended %s, refused %+v", f.app.runs, ending.Status, ending.Steps[0].Result.Refused)
	}
}

// A tool that must always ask is never run unattended, whatever the
// automation's trust rule says: the page reader fetches an address the
// model chose.
func TestAnAlwaysAskToolIsNeverAllowedByTheTrustRule(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Read the page.","auto_approve_all":true,"retrieval_scope":"all"}`),
		modeltest.Reply{Calls: calls("c1", pagereader.ToolName, `{"urls":["https://example.com/a"]}`)},
		modeltest.Reply{Text: "I could not read the page."},
	)
	ending := f.run(t)
	step := ending.Steps[0]
	if ending.Status != NeedsAttention || len(step.Result.Refused) != 1 || step.Result.Refused[0].Tool != pagereader.ToolName ||
		!strings.Contains(step.Result.Refused[0].Reason, "every time") {
		t.Fatalf("ended %s, refused %+v", ending.Status, step.Result.Refused)
	}
	// Nothing was fetched: the refusal is the tool's whole result.
	for _, entry := range step.Result.Transcript {
		if entry.Tool == pagereader.ToolName && !strings.Contains(entry.Content, "unattended run") {
			t.Errorf("the page reader answered %q", entry.Content)
		}
	}
}

// A step that fails is tried again as the definition allows, and a run
// whose step keeps failing ends failed with the reason.
func TestAFailedStepIsRetriedAndThenTheRunFails(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Summarise."}`),
		modeltest.Reply{Err: errors.New("the provider is down")},
		modeltest.Reply{Text: "Summary."},
	)
	ending := f.run(t)
	if ending.Status != Succeeded || ending.Steps[0].Attempts != 2 {
		t.Fatalf("ended %s after %d attempts, want success on the second", ending.Status, ending.Steps[0].Attempts)
	}

	f = newFixture(t, plan(`{"query":"Summarise."}`),
		modeltest.Reply{Err: errors.New("the provider is down")},
		modeltest.Reply{Err: errors.New("the provider is down")},
	)
	ending = f.run(t)
	if ending.Status != Failed || ending.Steps[0].Attempts != 2 || !strings.Contains(ending.Error["message"].(string), "the provider is down") || ending.Error["step_id"] != "digest" {
		t.Fatalf("ended %s, %+v", ending.Status, ending.Error)
	}
}

// A run cancelled from the page stops, and its row keeps the cancellation.
func TestACancelledRunStopsAndKeepsItsCancellation(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Summarise."}`), modeltest.Reply{Hang: true})
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.rows.mu.Lock()
		f.rows.status = Cancelled
		f.rows.mu.Unlock()
	}()
	ending := f.run(t)
	if ending.Status != Cancelled || f.rows.status != Cancelled {
		t.Fatalf("ended %s, row %s", ending.Status, f.rows.status)
	}
}

// A run past its time limit is stopped and says so.
func TestARunPastItsTimeLimitIsTimedOut(t *testing.T) {
	f := newFixture(t, `{"name":"Digest","plan":[{"step_id":"digest","action":"agent_task","params":{"query":"Summarise."}}],"execution":{"timeout_seconds":1,"max_retries":0}}`,
		modeltest.Reply{Hang: true})
	ending := f.run(t)
	if ending.Status != TimedOut || !strings.Contains(ending.Error["message"].(string), "time limit") {
		t.Fatalf("ended %s, %+v", ending.Status, ending.Error)
	}
}

// A message for a run that is not pending does nothing, and a finished run
// hands over to the one waiting behind it.
func TestADuplicateMessageDoesNothingAndAFinishedRunHandsOver(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Summarise."}`), modeltest.Reply{Text: "Summary."})
	f.rows.next = 8
	f.run(t)
	if len(f.sent.messages) != 1 || f.sent.messages[0].Kwargs["run_id"] != int64(8) {
		t.Fatalf("handed over %+v, want run 8", f.sent.messages)
	}
	// The same message again: the run is no longer pending.
	if err := f.job.Run(context.Background(), &celery.Message{Task: Task, Kwargs: map[string]any{"run_id": float64(7)}}); err != nil {
		t.Fatal(err)
	}
	if len(f.script.Requests()) != 1 {
		t.Errorf("the model was called %d times, want once", len(f.script.Requests()))
	}
	// A message that names no run is an error, not a guess.
	if err := f.job.Run(context.Background(), &celery.Message{Task: Task, Kwargs: map[string]any{}}); err == nil {
		t.Error("a message with no run id was accepted")
	}
}

// A definition the worker cannot run (saved some other way) fails the run
// with the reason and runs nothing.
func TestADefinitionThatCannotBeRunFailsTheRun(t *testing.T) {
	f := newFixture(t, `{"name":"Digest","plan":[{"step_id":"x","action":"send_email","params":{}}]}`)
	ending := f.run(t)
	if ending.Status != Failed || !strings.Contains(ending.Error["message"].(string), "agent_task") || len(f.script.Requests()) != 0 {
		t.Fatalf("ended %s, %+v", ending.Status, ending.Error)
	}
}

// A step runs on its own model, else the automation's, else the account's;
// and a model the proxy does not offer fails the step by name, once.
func TestAStepRunsOnTheModelItNames(t *testing.T) {
	f := newFixture(t, `{"name":"Digest","models":{"chat":"other-model"},"execution":{"max_retries":2,"retry_backoff":"none"},"plan":[
		{"step_id":"a","action":"agent_task","params":{"query":"One."}},
		{"step_id":"b","action":"agent_task","params":{"query":"Two.","model":"third-model"}}]}`,
		modeltest.Reply{Text: "First."}, modeltest.Reply{Text: "Second."})
	ending := f.run(t)
	if ending.Status != Succeeded || strings.Join(*f.opened, ",") != "other-model,third-model" {
		t.Fatalf("ended %s on models %v", ending.Status, *f.opened)
	}
	if ending.Steps[0].Result.Model != "other-model" || ending.Steps[1].Result.Model != "third-model" || ending.Output["model"] != "third-model" {
		t.Errorf("the steps say %q and %q, the run %v", ending.Steps[0].Result.Model, ending.Steps[1].Result.Model, ending.Output["model"])
	}

	f = newFixture(t, `{"name":"Digest","execution":{"max_retries":2,"retry_backoff":"none"},"plan":[
		{"step_id":"a","action":"agent_task","params":{"query":"One.","model":"withdrawn-model"}}]}`)
	ending = f.run(t)
	step := ending.Steps[0]
	if ending.Status != Failed || step.Attempts != 1 || step.Error.Type != "model_unavailable" || !strings.Contains(step.Error.Message, "withdrawn-model") ||
		len(*f.opened) != 0 || len(f.script.Requests()) != 0 {
		t.Fatalf("ended %s after %d attempts: %+v; opened %v", ending.Status, step.Attempts, step.Error, *f.opened)
	}
}

// A step confined to a notebook is built as that notebook's chat is: its
// search is limited to the notebook's sources and it has no connected
// app. A notebook that is gone fails the step and runs nothing.
func TestAStepConfinedToANotebook(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Summarise.","notebook_id":5}`),
		modeltest.Reply{Calls: calls("c1", documents.ToolName, `{"query":"mitosis"}`)}, modeltest.Reply{Text: "Summary."})
	ending := f.run(t)
	if ending.Status != Succeeded || f.built.turns[0].Unattended.NotebookID != 5 {
		t.Fatalf("ended %s, built as %+v", ending.Status, f.built.turns[0].Unattended)
	}
	first := f.script.Requests()[0]
	if names := toolNames(first); names != documents.ToolName {
		t.Errorf("tools %s, want the document search alone", names)
	}
	if system := first.Messages[0].Content; !strings.Contains(system, "Biology") || !strings.Contains(system, "Cell biology") {
		t.Errorf("the prompt does not name the notebook and its source")
	}

	f = newFixture(t, plan(`{"query":"Summarise.","notebook_id":6}`), modeltest.Reply{Text: "Summary."})
	ending = f.run(t)
	step := ending.Steps[0]
	if ending.Status != Failed || step.Attempts != 1 || step.Error.Type != "invalid_step" || !strings.Contains(step.Error.Message, "reads from no longer exists") ||
		len(f.script.Requests()) != 0 || len(f.built.turns) != 0 {
		t.Fatalf("ended %s: %+v, %d model calls", ending.Status, step.Error, len(f.script.Requests()))
	}
}

// A step's answer is filed as a note in the notebook it names, with its
// source markers, and queued for indexing.
func TestAStepsAnswerIsSavedAsANote(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Summarise mitosis.","save_to_notebook_id":5}`),
		modeltest.Reply{Calls: calls("c1", documents.ToolName, `{"query":"mitosis"}`)}, modeltest.Reply{Text: "Four phases [1]."})
	ending := f.run(t)
	step := ending.Steps[0]
	want := Note{UserID: ada, AutomationID: 3, NotebookID: 5, Title: "Digest, 12 November 2026", Markdown: "Four phases [citation:481]."}
	if ending.Status != Succeeded || len(f.rows.notes) != 1 || f.rows.notes[0] != want {
		t.Fatalf("ended %s, saved %+v; want %+v", ending.Status, f.rows.notes, want)
	}
	if saved := step.Result.SavedNote; saved == nil || *saved != (SavedNote{DocumentID: 4401, NotebookID: 5, Title: want.Title}) {
		t.Errorf("the step says it saved %+v", saved)
	}
	if len(f.index.ids) != 1 || f.index.ids[0] != 4401 {
		t.Errorf("queued for indexing: %v", f.index.ids)
	}

	// A notebook gone since the automation was saved fails the step
	// before anything is asked of a model.
	f = newFixture(t, plan(`{"query":"Summarise.","save_to_notebook_id":6}`), modeltest.Reply{Text: "Summary."})
	if ending = f.run(t); ending.Status != Failed || !strings.Contains(ending.Steps[0].Error.Message, "saves its answer to no longer exists") || len(f.script.Requests()) != 0 {
		t.Fatalf("ended %s: %+v", ending.Status, ending.Steps[0].Error)
	}

	// An answer that could not be saved is not asked for again: the step
	// keeps it and says what was left undone.
	f = newFixture(t, plan(`{"query":"Summarise.","save_to_notebook_id":5}`), modeltest.Reply{Text: "Summary."})
	f.rows.noteErr = errors.New("the database is away")
	ending = f.run(t)
	step = ending.Steps[0]
	if ending.Status != NeedsAttention || ending.Error["type"] != "needs_attention" || step.Attempts != 1 || step.Result.SavedNote != nil ||
		step.Result.FinalMessage != "Summary." || !strings.Contains(*step.Result.Outcome.Reason, "could not be saved as a note") || len(f.index.ids) != 0 {
		t.Fatalf("ended %s (%v): %+v", ending.Status, ending.Error, step.Result)
	}

	// No answer, no note.
	f = newFixture(t, plan(`{"query":"Summarise.","save_to_notebook_id":5}`), modeltest.Reply{Text: "  "})
	if ending = f.run(t); len(f.rows.notes) != 0 || ending.Steps[0].Result.SavedNote != nil {
		t.Errorf("an empty answer was saved: %+v", f.rows.notes)
	}
}

// The connected accounts a task names are named to the model as their
// owner named them; one switched off or gone is said, not passed over.
func TestTheConnectedAccountsATaskNames(t *testing.T) {
	f := newFixture(t, plan(`{"query":"Check my mail.","retrieval_scope":"all","mentioned_connector_ids":[31,32,99]}`), modeltest.Reply{Text: "Checked."})
	f.run(t)
	requests := f.script.Requests()
	task := requests[0].Messages[len(requests[0].Messages)-1].Content
	for _, piece := range []string{"<connected_accounts>", "- Email (IMAP)\n", "- Old ‹drive›, which the user has switched off", "- an account that is no longer connected", "</connected_accounts>\n\nCheck my mail."} {
		if !strings.Contains(task, piece) {
			t.Errorf("the task lacks %q:\n%s", piece, task)
		}
	}
	// A task that names none is told nothing about accounts.
	f = newFixture(t, plan(`{"query":"Summarise."}`), modeltest.Reply{Text: "Summary."})
	f.run(t)
	requests = f.script.Requests()
	if task := requests[0].Messages[len(requests[0].Messages)-1].Content; strings.Contains(task, "connected_accounts") {
		t.Errorf("a task naming no account was told of some:\n%s", task)
	}
}

// A run whose automation asks for its result to be emailed ends with the
// email waiting, whatever its ending, except one the owner cancelled.
func TestARunThatAsksToBeEmailedEndsWithTheEmailPending(t *testing.T) {
	emailed := func(params, execution string) string {
		return `{"name":"Digest","plan":[{"step_id":"digest","action":"agent_task","params":` + params + `}],"execution":{"email_result":true,"max_retries":0` + execution + `}}`
	}
	pending := func(ending Ending) bool {
		email, _ := ending.Output["email"].(map[string]any)
		return len(email) == 1 && email["status"] == EmailPending
	}
	f := newFixture(t, emailed(`{"query":"Summarise."}`, ``), modeltest.Reply{Text: "Summary."})
	if ending := f.run(t); ending.Status != Succeeded || !pending(ending) {
		t.Errorf("a run that succeeded: %s, %v", ending.Status, ending.Output["email"])
	}
	f = newFixture(t, emailed(`{"query":"Summarise."}`, ``), modeltest.Reply{Err: errors.New("the provider is down")})
	if ending := f.run(t); ending.Status != Failed || !pending(ending) {
		t.Errorf("a run that failed: %s, %v", ending.Status, ending.Output["email"])
	}
	f = newFixture(t, emailed(`{"query":"Summarise."}`, ``), modeltest.Reply{Hang: true})
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.rows.mu.Lock()
		f.rows.status = Cancelled
		f.rows.mu.Unlock()
	}()
	if ending := f.run(t); ending.Status != Cancelled || ending.Output["email"] != nil {
		t.Errorf("a run its owner cancelled: %s, %v", ending.Status, ending.Output["email"])
	}
}

// noPDFs is the page reader's PDF reader in a test that fetches nothing.
type noPDFs struct{}

func (noPDFs) Read(context.Context, fileread.File, fileread.Choices) (fileread.Text, error) {
	return fileread.Text{}, errors.New("this test reads no PDF")
}
