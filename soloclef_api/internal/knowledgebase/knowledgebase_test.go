package knowledgebase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// indexed records the documents a change queued for indexing.
type indexed struct{ ids []int64 }

func (i *indexed) Enqueue(_ context.Context, documentID int64, _ produce.LogContext) error {
	i.ids = append(i.ids, documentID)
	return nil
}

// forgotten records the points a delete removed.
type forgotten struct{ ids []int64 }

func (f *forgotten) Delete(_ context.Context, chunkIDs []int64) error {
	f.ids = append(f.ids, chunkIDs...)
	return nil
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The card the page draws for a call made inside the specialist is the
// delegating call's id, "/", and the call's own: the path the nested run's
// events carry. The log must name the same card, or its undo button never
// finds its row.
func TestTheLoggedCallIsTheCardThePageDraws(t *testing.T) {
	for _, c := range []struct{ thread, call, want string }{
		{"chat-7", "call_1", "call_1"},
		{"chat-7/call_d", "call_w", "call_d/call_w"},
		{"chat-7/call_d/call_e", "call_w", "call_d/call_e/call_w"},
		{"", "call_1", "call_1"},
	} {
		if got := cardID(tool.Call{Thread: c.thread, ID: c.call}); got != c.want {
			t.Errorf("thread %q, call %q: %q, want %q", c.thread, c.call, got, c.want)
		}
	}
}

func TestAnEditIsOnePassageOrTheWholeText(t *testing.T) {
	text := func(s string) *string { return &s }
	for _, c := range []struct {
		name                      string
		oldText, newText, content *string
		refused                   string
	}{
		{"both kinds at once", text("a"), text("b"), text("c"), "not both"},
		{"a passage without its replacement", text("a"), nil, nil, "together"},
		{"nothing at all", nil, nil, nil, "together"},
		{"an empty passage", text(""), text("b"), nil, "empty"},
		{"a NUL", text("a"), text("b\x00"), nil, "NUL"},
	} {
		if _, err := editOf(c.oldText, c.newText, c.content); err == nil || !strings.Contains(err.Error(), c.refused) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	once, _ := editOf(text("beta"), text("BETA"), nil)
	if got, err := once.apply("alpha beta gamma"); err != nil || got != "alpha BETA gamma" {
		t.Errorf("a passage that appears once: %q %v", got, err)
	}
	if _, err := once.apply("beta and beta"); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Errorf("a passage that appears twice must be refused, saying how often: %v", err)
	}
	if _, err := once.apply("alpha"); err == nil || !strings.Contains(err.Error(), "not in the note") {
		t.Errorf("a passage that is not there: %v", err)
	}
	whole, _ := editOf(nil, nil, text("new"))
	if got, _ := whole.apply("old"); got != "new" {
		t.Errorf("the whole text: %q", got)
	}
}

func TestArgumentsAreReadStrictly(t *testing.T) {
	var args struct {
		FolderID json.RawMessage `json:"folder_id"`
	}
	// A "path" where the tool takes "folder_id" would file the note at the
	// top level in silence.
	if err := decode(json.RawMessage(`{"path":"/Pharmacology"}`), &args); err == nil {
		t.Error("an unknown field was accepted")
	}
	if id, err := optionalID(nil, "folder_id"); id != nil || err != nil {
		t.Errorf("left out: %v %v", id, err)
	}
	if id, err := optionalID(json.RawMessage("null"), "folder_id"); id != nil || err != nil {
		t.Errorf("null: %v %v", id, err)
	}
	if id, err := optionalID(json.RawMessage("12"), "folder_id"); id == nil || *id != 12 || err != nil {
		t.Errorf("an id: %v %v", id, err)
	}
	if _, err := optionalID(json.RawMessage(`"Pharmacology"`), "folder_id"); err == nil {
		t.Error("a name was taken for an id")
	}
	if _, err := requiredID(nil, "folder_id"); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("a move that does not say where: %v", err)
	}
	if id, err := requiredID(json.RawMessage("null"), "folder_id"); id != nil || err != nil {
		t.Errorf("a move to no folder: %v %v", id, err)
	}
	if name, err := cleanFolderName("  Term 2  "); name != "Term 2" || err != nil {
		t.Errorf("a name is trimmed: %q %v", name, err)
	}
	if _, err := cleanFolderName(strings.Repeat("x", 256)); err == nil {
		t.Error("a name past the column's width was accepted")
	}
	if title, err := cleanTitle(" Beta   blockers "); title != "Beta blockers" || err != nil {
		t.Errorf("a title is tidied: %q %v", title, err)
	}
}

func TestAPathIsReadFromTheTreeAndNeverSpins(t *testing.T) {
	one, two, three := int64(1), int64(2), int64(3)
	folders := tree{byID: map[int64]AccountFoldersRow{
		1: {ID: 1, Name: "Pharmacology"},
		2: {ID: 2, Name: "Cardio", ParentID: &one},
		// A parent link that points back at itself.
		3: {ID: 3, Name: "Loop", ParentID: &three},
	}}
	if got := folders.path(&two); got != "/Pharmacology/Cardio" {
		t.Errorf("%q", got)
	}
	if got := folders.path(nil); got != "/" {
		t.Errorf("no folder: %q", got)
	}
	if got := folders.path(&three); !strings.HasPrefix(got, "/Loop") {
		t.Errorf("a circular link: %q", got)
	}
}

// What is asked for is what is changed: writes ask first, deletes ask every
// time, reads never ask; and no change runs beside another.
func TestEveryChangeAsksAndDeletesAlwaysAsk(t *testing.T) {
	want := map[string]tool.Approval{
		listFolderTool: tool.Auto, findDocumentsTool: tool.Auto, readDocumentTool: tool.Auto,
		createNoteTool: tool.Ask, editNoteTool: tool.Ask, renameDocumentTool: tool.Ask, moveDocumentTool: tool.Ask,
		createFolderTool: tool.Ask, renameFolderTool: tool.Ask, moveFolderTool: tool.Ask,
		deleteNoteTool: tool.AlwaysAsk, deleteFolderTool: tool.AlwaysAsk,
	}
	tools := Changes{}.tools(owner{userID: "u", threadID: 1})
	if len(tools) != len(want) {
		t.Fatalf("%d tools, want %d", len(tools), len(want))
	}
	activities := Writer{}.Activities()
	for _, tl := range tools {
		if err := tl.Validate(); err != nil {
			t.Error(err)
		}
		if tl.Approval != want[tl.Name] {
			t.Errorf("%s asks %d, want %d", tl.Name, tl.Approval, want[tl.Name])
		}
		if tl.Parallel && tl.Approval != tool.Auto {
			t.Errorf("%s is a change that may run beside another", tl.Name)
		}
		if activities[tl.Name].Active == "" {
			t.Errorf("%s has no line in the step display", tl.Name)
		}
	}
	if activities[assembly.KnowledgeBaseName].Active == "" {
		t.Error("the specialist has no line in the step display")
	}
}

// A refusal reaches the model as its sentence; any other failure as one
// sentence that names nothing of the server, with the cause in the log.
func TestTheModelIsToldARefusalAndNotTheServersError(t *testing.T) {
	var logged strings.Builder
	c := Changes{Logger: slog.New(slog.NewTextHandler(&logged, nil))}
	refused := c.guard("t", func(context.Context, tool.Call) (string, error) { return "", refuse("There is no folder with id 4.") })
	if _, err := refused(context.Background(), tool.Call{}); err == nil || err.Error() != "There is no folder with id 4." {
		t.Errorf("a refusal: %v", err)
	}
	broken := c.guard("t", func(context.Context, tool.Call) (string, error) {
		return "", errors.New("ERROR: deadlock detected (SQLSTATE 40P01)")
	})
	if _, err := broken(context.Background(), tool.Call{}); err == nil || err.Error() != serverFailure {
		t.Errorf("a server failure: %v", err)
	}
	if !strings.Contains(logged.String(), "deadlock") {
		t.Errorf("the cause is not in the log: %s", logged.String())
	}
}

type unusedModel struct{}

func (unusedModel) Generate(context.Context, model.Request, func(model.Chunk)) (model.Response, error) {
	return model.Response{}, errors.New("not called")
}

func TestASpecialistIsBuiltOnlyForAnAccountAndAThread(t *testing.T) {
	w := Writer{}
	store := checkpoint.NewMemory()
	for _, turn := range []assembly.KnowledgeOwner{
		{ThreadID: 1, Store: store},
		{UserID: "u", Store: store},
		{UserID: "u", ThreadID: 1},
	} {
		if _, err := w.Delegate(unusedModel{}, turn); err == nil {
			t.Errorf("%+v was built", turn)
		}
	}
	delegate, err := w.Delegate(unusedModel{}, assembly.KnowledgeOwner{UserID: "u", ThreadID: 1, Store: store})
	if err != nil || delegate.Name != assembly.KnowledgeBaseName || delegate.Timeout != specialistTimeout {
		t.Errorf("%+v %v", delegate, err)
	}
	if err := w.Check(); err == nil {
		t.Error("a writer with nothing to write with passed its check")
	}
}

// A change whose turn is not known is not made: logged under no turn, it
// would be missing from "undo this turn" with nothing to say so.
func TestAChangeOutsideATurnIsNotRecorded(t *testing.T) {
	if _, err := record(context.Background(), nil, owner{userID: "u", threadID: 1}, change{}); err == nil || !strings.Contains(err.Error(), "turn") {
		t.Errorf("%v", err)
	}
}

func serve(t *testing.T, routes Routes, userID, method, path string) (int, string) {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	routes.Errors = errorenvelope.Writer{}
	if routes.Changes.Logger == nil {
		routes.Changes.Logger = quiet()
	}
	routes.Register(api)
	request := httptest.NewRequest(method, path, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	if userID != "" {
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
	}
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

// What is refused before a row is read: a nil pool would panic if reached.
func TestABadUndoIsRefusedBeforeTheDatabaseIsAsked(t *testing.T) {
	const someone = "11111111-1111-4111-8111-111111111111"
	for _, c := range []struct {
		name, path string
		names      string
	}{
		{"a thread id that is not a number", "/api/v1/threads/abc/revert/1", "thread_id"},
		{"an action id that is not a number", "/api/v1/threads/1/revert/x", "action_id"},
		{"a turn's thread id that is not a number", "/api/v1/threads/abc/revert-turn/t1", "thread_id"},
		{"a turn id wider than the column", "/api/v1/threads/1/revert-turn/" + strings.Repeat("t", 65), "chat_turn_id"},
	} {
		if status, body := serve(t, Routes{}, someone, http.MethodPost, c.path); status != 422 || !strings.Contains(body, c.names) {
			t.Errorf("%s: %d %s", c.name, status, body)
		}
	}
	if status, _ := serve(t, Routes{}, "", http.MethodPost, "/api/v1/threads/1/revert/2"); status != 500 {
		t.Errorf("a request with no caller answered %d", status)
	}
}

// A notebook's chat is confined to the notebook's documents; a plain chat
// to nothing.
func TestTheTurnsNotebookConfinesTheSpecialist(t *testing.T) {
	plain := ownerOf(assembly.KnowledgeOwner{UserID: "u", ThreadID: 1})
	if plain.confined() || plain.readable != nil {
		t.Errorf("a plain chat is confined: %+v", plain)
	}
	inNotebook := ownerOf(assembly.KnowledgeOwner{UserID: "u", ThreadID: 1, Notebook: &assembly.Notebook{ID: 5, DocumentIDs: []int64{7, 8}}})
	if !inNotebook.confined() || *inNotebook.notebookID != 5 || !inNotebook.readable[7] || !inNotebook.readable[8] || inNotebook.readable[9] {
		t.Errorf("a notebook's chat: %+v", inNotebook)
	}
	// A notebook with nothing enabled may read nothing: never "no limit".
	empty := ownerOf(assembly.KnowledgeOwner{UserID: "u", ThreadID: 1, Notebook: &assembly.Notebook{ID: 6}})
	if !empty.confined() || len(empty.readable) != 0 {
		t.Errorf("an empty notebook's chat: %+v", empty)
	}
}
