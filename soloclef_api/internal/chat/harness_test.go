package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/memory"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// The accounts of the tests. Ada has threads 1 and 2; Dee has thread 3; Ben
// has none. Each is somebody else to the other two: nothing of one account
// may be read, answered, stopped or replaced by another.
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
	dee = "dddddddd-0000-4000-8000-000000000004"
)

// storedMessage is one row of new_chat_messages.
type storedMessage struct {
	ID        int64
	ThreadID  int64
	Assistant bool
	Author    string
	TurnID    string
	Content   string
}

// rows is the chat's database in memory, behind the interface the routes
// use. It keeps the two rules of the real tables that a route could break:
// one row per (thread, turn, role), a trust row per (account, tool), and a
// thread that is answered only to its owner.
type rows struct {
	mu         sync.Mutex
	threads    map[int64]*Thread
	messages   []storedMessage
	nextID     int64
	responding map[int64]string
	touched    map[int64]int
	titles     map[int64]string
	usage      map[int64]Usage
	trust      map[string]bool
	prompt     string
	// about is the rest of a turn's context: the asker, the memory, the
	// notebook. Its instructions are taken from prompt.
	about TurnContext
	// failReplace makes ReplaceTurn fail.
	failReplace bool
	// progress counts the saves made while a turn ran.
	progress int
	// failClose makes CloseTurn fail, as a database that went away would.
	failClose bool
	// notebook is what PrepareNotebook answers (the zero value: the thread
	// is no notebook's); prepared records what it was asked, queued the
	// tasks published, and failQueue makes publishing fail.
	notebook  NotebookTurn
	prepared  []preparedNotebook
	queued    []produce.Message
	failQueue bool
	// remembered is every memory document a turn's update_memory tool was
	// made for, and saved the documents it was called with.
	remembered []memory.Target
	saved      []string
}

// preparedNotebook is one call of PrepareNotebook.
type preparedNotebook struct {
	turn Turn
}

func (r *rows) PrepareNotebook(_ context.Context, turn Turn) (NotebookTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepared = append(r.prepared, preparedNotebook{turn: turn})
	return r.notebook, nil
}

// Enqueue makes rows the worker tier's queue.
func (r *rows) Enqueue(_ context.Context, message produce.Message) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failQueue {
		return "", errors.New("the broker is down")
	}
	r.queued = append(r.queued, message)
	return "task-id", nil
}

// Protocol and Usage are the real writer's words, so the prompt a test reads
// is the prompt a turn is given.
func (r *rows) Protocol() string { return memory.Protocol }

func (r *rows) Usage(document string) string { return memory.Usage(document) }

// Tool makes rows the memory writer's stand-in: the tool records which
// document it was made for and what it was asked to save, and answers as
// the real one does for a saved document.
func (r *rows) Tool(target memory.Target) tool.Tool {
	r.mu.Lock()
	r.remembered = append(r.remembered, target)
	r.mu.Unlock()
	return tool.Tool{
		Name: memory.ToolName, Description: "Replace the memory document.", Approval: tool.Auto,
		Parameters: json.RawMessage(`{"type":"object","properties":{"updated_memory":{"type":"string"}},"required":["updated_memory"]}`),
		Run: func(_ context.Context, call tool.Call) (string, error) {
			var args struct {
				Updated string `json:"updated_memory"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", err
			}
			r.mu.Lock()
			r.saved = append(r.saved, args.Updated)
			r.mu.Unlock()
			return `{"status":"saved","message":"Memory updated."}`, nil
		},
	}
}

func newRows() *rows {
	return &rows{
		threads: map[int64]*Thread{
			1: {ID: 1, UserID: ada},
			2: {ID: 2, UserID: ada},
			3: {ID: 3, UserID: dee},
		},
		nextID: 100, responding: map[int64]string{}, touched: map[int64]int{}, titles: map[int64]string{},
		usage: map[int64]Usage{}, trust: map[string]bool{},
	}
}

// Thread keeps the statement's rule: the thread by its id and its account,
// so another account's thread is nil like one that does not exist.
func (r *rows) Thread(_ context.Context, userID string, threadID int64) (*Thread, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if thread := r.threads[threadID]; thread != nil && thread.UserID == userID {
		return thread, nil
	}
	return nil, nil
}

func (r *rows) insert(m storedMessage) (int64, error) {
	for _, existing := range r.messages {
		if existing.ThreadID == m.ThreadID && existing.TurnID == m.TurnID && existing.Assistant == m.Assistant {
			return 0, errors.New("duplicate (thread_id, turn_id, role)")
		}
	}
	r.nextID++
	m.ID = r.nextID
	r.messages = append(r.messages, m)
	return m.ID, nil
}

func (r *rows) OpenTurn(_ context.Context, turn NewTurn) (OpenedTurn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var opened OpenedTurn
	var err error
	if turn.UserContent != nil {
		if opened.UserMessageID, err = r.insert(storedMessage{ThreadID: turn.ThreadID, Author: turn.UserID, TurnID: turn.TurnID, Content: string(turn.UserContent)}); err != nil {
			return opened, err
		}
		r.touched[turn.ThreadID]++
	}
	if opened.AssistantMessageID, err = r.insert(storedMessage{ThreadID: turn.ThreadID, Assistant: true, TurnID: turn.TurnID, Content: string(turn.AssistantContent)}); err != nil {
		return opened, err
	}
	r.responding[turn.ThreadID] = turn.UserID
	return opened, nil
}

func (r *rows) store(messageID int64, content []byte) {
	for i := range r.messages {
		if r.messages[i].ID == messageID {
			r.messages[i].Content = string(content)
		}
	}
}

func (r *rows) MessageContent(_ context.Context, threadID, messageID int64) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.messages {
		if m.ID == messageID && m.ThreadID == threadID {
			return []byte(m.Content), nil
		}
	}
	return nil, nil
}

func (r *rows) StoreContent(_ context.Context, messageID int64, content []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store(messageID, content)
	r.progress++
	return nil
}

func (r *rows) CloseTurn(_ context.Context, turn ClosedTurn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failClose {
		return errors.New("connection to 10.0.0.5:5432 refused")
	}
	r.store(turn.MessageID, turn.Content)
	if turn.Usage != nil {
		r.usage[turn.MessageID] = *turn.Usage
	}
	delete(r.responding, turn.ThreadID)
	return nil
}

func (r *rows) History(_ context.Context, threadID int64) ([]HistoryMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []HistoryMessage
	for _, m := range r.messages {
		if m.ThreadID == threadID {
			out = append(out, HistoryMessage{Assistant: m.Assistant, Content: []byte(m.Content)})
		}
	}
	return out, nil
}

func (r *rows) AssistantMessages(_ context.Context, threadID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for _, m := range r.messages {
		if m.ThreadID == threadID && m.Assistant {
			n++
		}
	}
	return n, nil
}

func (r *rows) LatestAssistantMessage(_ context.Context, threadID int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest int64
	for _, m := range r.messages {
		if m.ThreadID == threadID && m.Assistant {
			latest = m.ID
		}
	}
	return latest, nil
}

func (r *rows) SetTitle(_ context.Context, _ string, threadID int64, title string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.titles[threadID] = title
	return nil
}

func (r *rows) Context(context.Context, Turn) (TurnContext, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	about := r.about
	about.Instructions, about.Citations = r.prompt, true
	return about, nil
}

// Question, HistoryBefore and ReplaceTurn keep the real statements' rules:
// a thread's order is the order its rows were written in, an answer stands
// for the question of its turn or else the newest before it, and replacing
// removes the question and everything after it.
func (r *rows) Question(_ context.Context, threadID, fromMessageID int64) (*Question, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at := -1
	if fromMessageID == 0 {
		for i, m := range r.messages {
			if m.ThreadID == threadID && !m.Assistant {
				at = i
			}
		}
		if at < 0 {
			return nil, nil
		}
	} else {
		named := -1
		for i, m := range r.messages {
			if m.ThreadID == threadID && m.ID == fromMessageID {
				named = i
			}
		}
		if named < 0 {
			return nil, errNotInThread
		}
		at = named
		if r.messages[named].Assistant {
			at = -1
			for i, m := range r.messages[:named] {
				if m.ThreadID == threadID && !m.Assistant && m.TurnID == r.messages[named].TurnID {
					at = i
				}
			}
			for i := named - 1; i >= 0 && at < 0; i-- {
				if m := r.messages[i]; m.ThreadID == threadID && !m.Assistant {
					at = i
				}
			}
			if at < 0 {
				return nil, nil
			}
		}
	}
	question := &Question{ID: r.messages[at].ID, TurnID: r.messages[at].TurnID, Content: []byte(r.messages[at].Content)}
	for _, m := range r.messages[at:] {
		if m.ThreadID == threadID {
			question.Replaced = append(question.Replaced, m.ID)
		}
	}
	return question, nil
}

func (r *rows) HistoryBefore(_ context.Context, threadID int64, question *Question) ([]HistoryMessage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []HistoryMessage
	for _, m := range r.messages {
		if m.ID == question.ID {
			break
		}
		if m.ThreadID == threadID {
			out = append(out, HistoryMessage{Assistant: m.Assistant, Content: []byte(m.Content)})
		}
	}
	return out, nil
}

func (r *rows) ReplaceTurn(ctx context.Context, question *Question, turn NewTurn) (OpenedTurn, error) {
	r.mu.Lock()
	if r.failReplace {
		r.mu.Unlock()
		return OpenedTurn{}, errors.New("connection to 10.0.0.5:5432 refused")
	}
	kept := r.messages[:0:0]
	for _, m := range r.messages {
		replaced := false
		for _, id := range question.Replaced {
			replaced = replaced || m.ID == id
		}
		if !replaced {
			kept = append(kept, m)
		}
	}
	r.messages = kept
	r.mu.Unlock()
	return r.OpenTurn(ctx, turn)
}

// thread returns a thread's rows, oldest first.
func (r *rows) thread(threadID int64) []storedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []storedMessage
	for _, m := range r.messages {
		if m.ThreadID == threadID {
			out = append(out, m)
		}
	}
	return out
}

// offeredModels is the proxy's list, fixed; chosenModels keeps accounts'
// choices in memory.
type offeredModels struct {
	names []string
	err   error
}

func (o *offeredModels) Names(context.Context) ([]string, error) { return o.names, o.err }

type chosenModels struct {
	mu     sync.Mutex
	chosen map[string]string
}

func (c *chosenModels) ChatModel(_ context.Context, userID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chosen[userID], nil
}

func (c *chosenModels) ChooseChatModel(_ context.Context, userID, name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chosen == nil {
		c.chosen = map[string]string{}
	}
	if name == "" {
		delete(c.chosen, userID)
	} else {
		c.chosen[userID] = name
	}
	return nil
}

// menu is a model menu whose every model is m: the proxy lists "test-model"
// and "other-model", and the server's default is the first.
func menu(m model.Model) (*Models, *offeredModels, *chosenModels) {
	list, chosen := &offeredModels{names: []string{"other-model", "test-model"}}, &chosenModels{}
	return &Models{Catalogue: list, Choices: chosen, Default: "test-model", Open: func(string) (model.Model, error) { return m, nil },
		Own: ownmodels.NoConnections{}}, list, chosen
}

func trustKey(userID, name string) string { return userID + "|" + name }

func (r *rows) Trusted(_ context.Context, userID, name string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trust[trustKey(userID, name)], nil
}

func (r *rows) Trust(_ context.Context, userID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trust[trustKey(userID, name)] = true
	return nil
}

// assistant returns the thread's assistant rows, oldest first.
func (r *rows) assistant(threadID int64) []storedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []storedMessage
	for _, m := range r.messages {
		if m.ThreadID == threadID && m.Assistant {
			out = append(out, m)
		}
	}
	return out
}

// scripted is an Assembler over a scripted model and hand-made tools: the
// routes' own logic is under test, not the production tool set.
type scripted struct {
	model model.Model
	tools []tool.Tool
	store checkpoint.Store
	rows  *rows
	// delegates are added to every agent, for the nested-run tests.
	delegates func(turn Turn) []agent.Delegate
	maxSteps  int
	// trusted answers "always allow"; nil reads the in-memory rows.
	trusted func(ctx context.Context, userID, name string) (bool, error)
	// models, when set, picks the turn's model as a deployment does.
	models *Models
	// activities are the turn's own tools for the step display.
	activities map[string]Activity
	// builds records the turns agents were built for; closed counts the
	// agents given back.
	mu     sync.Mutex
	builds []Turn
	closed int
}

func (s *scripted) Build(_ context.Context, turn Turn) (Built, error) {
	s.mu.Lock()
	s.builds = append(s.builds, turn)
	s.mu.Unlock()
	trusted := s.trusted
	if trusted == nil {
		trusted = s.rows.Trusted
	}
	chat, name := s.model, "test-model"
	if s.models != nil {
		var err error
		if chat, name, err = s.models.For(context.Background(), turn.UserID); err != nil {
			return Built{}, err
		}
	}
	cfg := agent.Config{
		Model: chat, System: "You are a test.", Tools: s.tools,
		MaxSteps: s.maxSteps, MaxParallelTools: 4, Store: s.store,
		Trusted: func(ctx context.Context, call tool.Call) (bool, error) {
			return trusted(ctx, turn.UserID, call.Name)
		},
	}
	if s.delegates != nil {
		cfg.Delegates = s.delegates(turn)
	}
	built, err := agent.New(cfg)
	return Built{
		Agent: built, Chat: chat, Model: name, Preface: assembly.Preface(turn.PreferredTool, map[string]bool{}), Activities: s.activities,
		Trust: func(ctx context.Context, name string) error {
			return s.rows.Trust(ctx, turn.UserID, name)
		},
		Close: func() {
			s.mu.Lock()
			s.closed++
			s.mu.Unlock()
		},
	}, err
}

// fixedTitle names every thread the same, or fails.
type fixedTitle struct {
	title string
	err   error
}

func (f fixedTitle) Title(context.Context, model.Model, string) (string, error) {
	return f.title, f.err
}

// app is the chat's routes served over a real listener, as cmd/api mounts
// them: the request id and the body reader outside, the caller set where
// the credential gate would set it.
type app struct {
	t        testing.TB
	server   *httptest.Server
	rows     *rows
	service  *Service
	assembly *scripted
	registry *turns.Registry
	shutdown context.CancelFunc
}

type appOption func(*Service, *scripted)

func newApp(t testing.TB, m model.Model, tools []tool.Tool, options ...appOption) *app {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := checkpoint.NewMemory()
	data := newRows()
	var owners atomic.Int64
	registry, err := turns.New(turns.Config{
		Store: turns.NewMemory(time.Now), Lifetime: time.Minute, Heartbeat: 20 * time.Millisecond,
		NewOwner: func() string { return "owner-" + strconv.FormatInt(owners.Add(1), 10) },
	})
	if err != nil {
		t.Fatal(err)
	}
	lifetime, shutdown := context.WithCancel(context.Background())
	listening := make(chan struct{})
	go func() {
		close(listening)
		_ = registry.Listen(lifetime)
	}()
	<-listening
	assembly := &scripted{model: m, tools: tools, store: store, rows: data, maxSteps: 12}
	// A clock that moves a millisecond per reading, so that two turns of
	// one test never share a turn id.
	var ticks atomic.Int64
	start := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	models, _, _ := menu(m)
	service := &Service{
		Rows: data, Notebooks: data, Queue: data, Turns: registry, Assembler: assembly, Store: store, Models: models, Choices: models.Choices.(ModelChoices),
		Titler: fixedTitle{title: "Cell division"},
		// Far above anything a test says, so that only the tests of
		// summarising summarise.
		Summary: SummaryLimits{Above: 1_000_000, Keep: 100_000, ResultChars: 1000},
		Timing:  Timing{MaxTurn: 20 * time.Second, KeepAlive: 10 * time.Second, SaveEvery: 10 * time.Millisecond, Title: time.Second, Stall: 5 * time.Second},
		Logger:  logger, Lifetime: lifetime,
		Now: func() time.Time { return start.Add(time.Duration(ticks.Add(1)) * time.Millisecond) },
	}
	for _, option := range options {
		option(service, assembly)
	}
	if err := service.Check(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	Routes{Service: service, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}}.Register(api)
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if caller := request.Header.Get("X-Test-Caller"); caller != "" {
			method := principal.MethodSession
			request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: caller, Method: method}))
		}
		mux.ServeHTTP(response, request)
	})
	bodies := requestbody.Middleware{
		Covers: func(r *http.Request) bool {
			return (r.Method == http.MethodPost || r.Method == http.MethodPut) && r.ContentLength != 0
		},
	}
	server := httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))
	a := &app{t: t, server: server, rows: data, service: service, assembly: assembly, registry: registry, shutdown: shutdown}
	t.Cleanup(func() {
		shutdown()
		service.Wait()
		server.Close()
	})
	return a
}

// request sends one request as caller and returns the open response.
func (a *app) request(caller, method, path, body string) *http.Response {
	a.t.Helper()
	var sent io.Reader
	if body != "" {
		sent = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, a.server.URL+path, sent)
	if err != nil {
		a.t.Fatal(err)
	}
	request.Header.Set("X-Request-ID", "req_test")
	if caller != "" {
		request.Header.Set("X-Test-Caller", caller)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.server.Client().Do(request)
	if err != nil {
		a.t.Fatal(err)
	}
	return response
}

// answered is a JSON answer.
type answered struct {
	status int
	header http.Header
	raw    string
	body   map[string]any
}

func (a *app) call(caller, method, path, body string) answered {
	a.t.Helper()
	response := a.request(caller, method, path, body)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	got := answered{status: response.StatusCode, header: response.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &got.body)
	return got
}

// code is the refusal code where the apps look for it.
func (a answered) code() string {
	detail, _ := a.body["detail"].(map[string]any)
	code, _ := detail["errorCode"].(string)
	return code
}

// frame is one parsed frame of a stream.
type frame map[string]any

func (f frame) kind() string { s, _ := f["type"].(string); return s }

func (f frame) data() map[string]any { d, _ := f["data"].(map[string]any); return d }

// readFrames reads a stream the way the web app's readSSEStream does: split
// on blank lines, take the "data: " lines, skip "[DONE]" and anything that
// is not JSON. It also reports whether [DONE] was the last thing sent, which
// the app does not need and a test of the protocol does.
func readFrames(t testing.TB, body io.Reader) (frames []frame, done bool) {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	events := strings.Split(string(raw), "\n\n")
	for i, event := range events {
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(line[6:])
			if data == "[DONE]" {
				done = i == len(events)-2
				continue
			}
			var parsed frame
			if json.Unmarshal([]byte(data), &parsed) == nil {
				frames = append(frames, parsed)
			}
		}
	}
	return frames, done
}

func kinds(frames []frame) string {
	names := make([]string, len(frames))
	for i, f := range frames {
		names[i] = f.kind()
	}
	return strings.Join(names, " ")
}

// find returns the frames of one type.
func find(frames []frame, kind string) []frame {
	var out []frame
	for _, f := range frames {
		if f.kind() == kind {
			out = append(out, f)
		}
	}
	return out
}

const turnBody = `{"chat_id":1,"user_query":"What are the phases of mitosis?"}`

// ask posts a question and reads the whole stream.
func (a *app) ask(caller, body string) ([]frame, bool) {
	a.t.Helper()
	response := a.request(caller, http.MethodPost, TurnPath, body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		a.t.Fatalf("the turn was refused: %d %s", response.StatusCode, raw)
	}
	return readFrames(a.t, response.Body)
}

// until reads frames from an open stream until one of the given type has
// arrived, and returns the reader to go on with.
func until(t *testing.T, reader *bufio.Reader, kind string) {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended before a %q frame: %v", kind, err)
		}
		if strings.Contains(line, `"type": "`+kind+`"`) {
			return
		}
	}
}

// eventually waits for a condition another goroutine brings about.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// savedParts decodes a saved message's content: every part, the step
// display at its head included.
func savedParts(t testing.TB, m storedMessage) []map[string]any {
	t.Helper()
	var parts []map[string]any
	if err := json.Unmarshal([]byte(m.Content), &parts); err != nil {
		t.Fatalf("saved content is not a list of parts: %v: %s", err, m.Content)
	}
	return parts
}

// contentOf is a saved message's answer: its parts without the step
// display, which displayOf returns.
func contentOf(t testing.TB, m storedMessage) []map[string]any {
	t.Helper()
	var answer []map[string]any
	for _, part := range savedParts(t, m) {
		if part["type"] != "data-activities" {
			answer = append(answer, part)
		}
	}
	return answer
}

// displayOf is a saved message's step display: its activities and clock.
func displayOf(t testing.TB, m storedMessage) ([]map[string]any, map[string]any) {
	t.Helper()
	parts := savedParts(t, m)
	if len(parts) == 0 || parts[0]["type"] != "data-activities" {
		return nil, nil
	}
	return stepsIn(parts[0])
}
