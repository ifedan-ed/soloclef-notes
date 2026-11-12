package knowledgebase

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_KNOWLEDGEBASE_DATABASE"

const alice, bob = testpostgres.Alice, testpostgres.Bob

// The seed: a thread each; Alice's folder "Pharmacology" (1) with a note
// (100) that is pinned, filed in her notebook (5) and a source of it, with
// two passages; an uploaded file of hers (200) at the top level; and Bob's
// own folder (9) and note (900).
const seed = `
INSERT INTO new_chat_threads (id, user_id, title) VALUES (10, '` + alice + `', 'Hers'), (20, '` + bob + `', 'His');
INSERT INTO notebooks (id, user_id, title) VALUES (5, '` + alice + `', 'Cardiology');
INSERT INTO folders (id, user_id, parent_id, name, position) VALUES
 (1, '` + alice + `', NULL, 'Pharmacology', 'a0'),
 (9, '` + bob + `', NULL, 'His folder', 'a0');
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown, folder_id, document_metadata) VALUES
 (100, '` + alice + `', 'Beta blockers', 'NOTE', 'Atenolol is cardioselective.', 'h', 'Atenolol is cardioselective.', 1, '{"NOTE": true}'),
 (200, '` + alice + `', 'Lecture 3.pdf', 'FILE', 'parsed text', 'h', 'parsed text', NULL, NULL),
 (900, '` + bob + `', 'His note', 'NOTE', 'his', 'h', 'his', 9, '{"NOTE": true}');
INSERT INTO notes (document_id, user_id, notebook_id, origin, pinned_at) VALUES
 (100, '` + alice + `', 5, 'hand', '2026-11-01T09:00:00Z'),
 (900, '` + bob + `', NULL, 'hand', NULL);
INSERT INTO notebook_sources (notebook_id, user_id, document_id, enabled) VALUES (5, '` + alice + `', 100, false);
INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes) VALUES
 (200, '` + alice + `', 'local', 'k/200', 'Lecture 3.pdf', 10);
INSERT INTO chunks (id, document_id, user_id, content, position) VALUES
 (7001, 100, '` + alice + `', 'Atenolol', 0), (7002, 100, '` + alice + `', 'is cardioselective.', 1);
-- The rows the changes make are numbered after the seed's.
SELECT setval(pg_get_serial_sequence('folders', 'id'), 1000), setval(pg_get_serial_sequence('documents', 'id'), 1000);
`

// harness is one database, the changes over it, and what they queued.
type harness struct {
	t       *testing.T
	pool    *pgxpool.Pool
	changes Changes
	indexed *indexed
	gone    *forgotten
	calls   int
}

func open(t *testing.T) *harness {
	t.Helper()
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, seed)
	h := &harness{t: t, pool: pool, indexed: &indexed{}, gone: &forgotten{}}
	h.changes = Changes{Pool: pool, Indexing: h.indexed, Vectors: h.gone, Logger: quiet()}
	return h
}

// frame is a "data-action-log" notice a call sent.
type frame struct {
	name    string
	payload actionLogFrame
}

// run calls one tool as the specialist's agent would, inside the delegate
// call "call_kb" of the account's thread, in turn "turn".
func (h *harness) run(who owner, turn, name, args string) (string, error, []frame) {
	h.t.Helper()
	h.calls++
	var frames []frame
	call := tool.Call{
		ID: fmt.Sprintf("call_%d", h.calls), Name: name, Args: json.RawMessage(args),
		Thread: fmt.Sprintf("chat-%d/call_kb", who.threadID),
		Notify: func(name string, payload any) error {
			frames = append(frames, frame{name, payload.(actionLogFrame)})
			return nil
		},
	}
	for _, t := range h.changes.tools(who) {
		if t.Name == name {
			answer, err := t.Run(assembly.WithTurnID(context.Background(), turn), call)
			return answer, err, frames
		}
	}
	h.t.Fatalf("no tool %s", name)
	return "", nil, nil
}

// change runs a tool that must succeed and returns its action's id.
func (h *harness) change(who owner, turn, name, args string) int64 {
	h.t.Helper()
	answer, err, frames := h.run(who, turn, name, args)
	if err != nil {
		h.t.Fatalf("%s %s: %v", name, args, err)
	}
	if len(frames) != 1 || frames[0].name != "action-log" || frames[0].payload.ToolName != name || !frames[0].payload.Reversible {
		h.t.Fatalf("%s announced %+v (answer %q)", name, frames, answer)
	}
	return frames[0].payload.ID
}

// refused runs a tool that must be refused with a sentence containing says,
// and checks that it recorded nothing.
func (h *harness) refused(who owner, name, args, says string) {
	h.t.Helper()
	before := h.count(`SELECT count(*) FROM agent_action_log`)
	_, err, frames := h.run(who, "turn-r", name, args)
	if err == nil || !strings.Contains(err.Error(), says) {
		h.t.Errorf("%s %s: %v, want a refusal saying %q", name, args, err, says)
	}
	if len(frames) != 0 || h.count(`SELECT count(*) FROM agent_action_log`) != before {
		h.t.Errorf("%s %s was refused but recorded", name, args)
	}
}

func (h *harness) count(query string, args ...any) int64 {
	h.t.Helper()
	var n int64
	if err := h.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (h *harness) text(query string, args ...any) string {
	h.t.Helper()
	var s *string
	if err := h.pool.QueryRow(context.Background(), query, args...).Scan(&s); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
	if s == nil {
		return "<null>"
	}
	return *s
}

// undo asks the route to undo one action and returns its status and body.
func (h *harness) undo(userID string, threadID, actionID int64) (int, string) {
	h.t.Helper()
	return serve(h.t, Routes{Changes: h.changes}, userID, http.MethodPost, fmt.Sprintf("/api/v1/threads/%d/revert/%d", threadID, actionID))
}

var hers = owner{userID: alice, threadID: 10}
var his = owner{userID: bob, threadID: 20}

// Every change is recorded with the item as it was, in its own
// transaction, names the card the page drew for it, and is queued for
// indexing when its text changed; and undoing the turn gives back exactly
// what there was.
func TestEveryChangeIsRecordedAndATurnIsUndoneWhole(t *testing.T) {
	h := open(t)
	const turn = "10:1000"

	folder := h.change(hers, turn, createFolderTool, `{"name":"Term 2"}`)
	termID := h.count(`SELECT id FROM folders WHERE name = 'Term 2' AND user_id = $1`, alice)
	created := h.change(hers, turn, createNoteTool, fmt.Sprintf(`{"title":"Mitosis","content":"Prophase, metaphase.","folder_id":%d}`, termID))
	noteID := h.count(`SELECT id FROM documents WHERE title = 'Mitosis'`)
	edited := h.change(hers, turn, editNoteTool, fmt.Sprintf(`{"document_id":%d,"old_text":"metaphase.","new_text":"metaphase, anaphase."}`, noteID))
	renamed := h.change(hers, turn, renameDocumentTool, `{"document_id":200,"title":"Lecture 3: beta blockers"}`)
	moved := h.change(hers, turn, moveDocumentTool, fmt.Sprintf(`{"document_id":200,"folder_id":%d}`, termID))
	deleted := h.change(hers, turn, deleteNoteTool, `{"document_id":100}`)

	// The note is a chat note of this thread, in its folder, waiting to be
	// indexed with its edited text.
	if got := h.text(`SELECT d.source_markdown || '|' || (d.status ->> 'state') || '|' || n.origin || '|' || n.thread_id FROM documents d JOIN notes n ON n.document_id = d.id WHERE d.id = $1`, noteID); got != "Prophase, metaphase, anaphase.|pending|chat|10" {
		t.Errorf("the written note: %s", got)
	}
	if fmt.Sprint(h.indexed.ids) != fmt.Sprint([]int64{noteID, noteID}) {
		t.Errorf("indexed %v, want the note on its creation and its edit only (a rename or a move changes nothing indexed)", h.indexed.ids)
	}
	// The deleted note is gone with its passages, and their points.
	if h.count(`SELECT count(*) FROM documents WHERE id = 100`) != 0 || fmt.Sprint(h.gone.ids) != "[7001 7002]" {
		t.Errorf("the delete left the row or its points: %v", h.gone.ids)
	}
	// Each row names the card the page drew: the specialist's call, then
	// the tool's.
	if got := h.text(`SELECT string_agg(tool_name || '@' || tool_call_id || '@' || chat_turn_id || '@' || reversible::text, ' ' ORDER BY id) FROM agent_action_log`); got !=
		"create_folder@call_kb/call_1@10:1000@true create_note@call_kb/call_2@10:1000@true edit_note@call_kb/call_3@10:1000@true "+
			"rename_document@call_kb/call_4@10:1000@true move_document@call_kb/call_5@10:1000@true delete_note@call_kb/call_6@10:1000@true" {
		t.Errorf("the log: %s", got)
	}
	// And keeps what was there before.
	if got := h.text(`SELECT content_before FROM document_revisions WHERE agent_action_id = $1`, edited); got != "Prophase, metaphase." {
		t.Errorf("the edit's revision: %s", got)
	}
	if got := h.text(`SELECT title_before || '|' || COALESCE(folder_id_before::text, '-') || '|' || content_before || '|' || COALESCE(document_id::text, 'gone') FROM document_revisions WHERE agent_action_id = $1`, deleted); got != "Beta blockers|1|Atenolol is cardioselective.|gone" {
		t.Errorf("the delete's revision: %s", got)
	}

	// The whole turn, undone newest first.
	status, body := serve(t, Routes{Changes: h.changes}, alice, http.MethodPost, "/api/v1/threads/10/revert-turn/"+turn)
	if status != 200 || !strings.Contains(body, `"status":"ok","chat_turn_id":"10:1000","total":6,"reverted":6,"already_reverted":0,"not_reversible":0`) {
		t.Fatalf("the turn's undo: %d %s", status, body)
	}
	for _, id := range []int64{folder, created, edited, renamed, moved, deleted} {
		if !strings.Contains(body, fmt.Sprintf(`{"action_id":%d,`, id)) {
			t.Errorf("action %d is not in the answer: %s", id, body)
		}
	}
	if h.count(`SELECT count(*) FROM folders WHERE name = 'Term 2'`) != 0 || h.count(`SELECT count(*) FROM documents WHERE title = 'Mitosis'`) != 0 {
		t.Error("what the turn made is still there")
	}
	if got := h.text(`SELECT title || '|' || COALESCE(folder_id::text, '-') FROM documents WHERE id = 200`); got != "Lecture 3.pdf|-" {
		t.Errorf("the file is not back as it was: %s", got)
	}
	// The deleted note is back, with everything that made it hers: its
	// folder, its text, its notebook, its pin and its switch.
	if got := h.text(`SELECT d.title || '|' || d.folder_id || '|' || d.source_markdown || '|' || n.notebook_id || '|' || n.origin || '|' || to_char(n.pinned_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') || '|' || s.enabled::text
		FROM documents d JOIN notes n ON n.document_id = d.id JOIN notebook_sources s ON s.document_id = d.id WHERE d.title = 'Beta blockers'`); got != "Beta blockers|1|Atenolol is cardioselective.|5|hand|2026-11-01|false" {
		t.Errorf("the note brought back: %s", got)
	}
	restored := h.count(`SELECT id FROM documents WHERE title = 'Beta blockers'`)
	if got := h.count(`SELECT document_id FROM document_revisions WHERE agent_action_id = $1`, deleted); got != restored {
		t.Errorf("the delete's revision points at %d, not the note brought back (%d)", got, restored)
	}
	// Indexed again: the note whose edit was undone (before it too was
	// removed) and the note brought back.
	if last := h.indexed.ids[len(h.indexed.ids)-1]; last != noteID || h.indexed.ids[2] != restored {
		t.Errorf("indexed %v", h.indexed.ids)
	}
	// Each undo is a row of its own pointing at what it undid.
	if got := h.count(`SELECT count(*) FROM agent_action_log WHERE reverse_of IS NOT NULL AND user_id = $1 AND thread_id = 10`, alice); got != 6 {
		t.Errorf("%d undo rows", got)
	}

	// Asked again, each is already undone.
	status, body = serve(t, Routes{Changes: h.changes}, alice, http.MethodPost, "/api/v1/threads/10/revert-turn/"+turn)
	if status != 200 || !strings.Contains(body, `"status":"ok","chat_turn_id":"10:1000","total":6,"reverted":0,"already_reverted":6`) {
		t.Errorf("the turn undone twice: %d %s", status, body)
	}
	if status, body := h.undo(alice, 10, created); status != 409 || !strings.Contains(body, "already been undone") {
		t.Errorf("one action undone twice: %d %s", status, body)
	}
	// An undo row is not itself undone.
	undoRow := h.count(`SELECT id FROM agent_action_log WHERE reverse_of = $1`, created)
	if status, body := h.undo(alice, 10, undoRow); status != 409 || !strings.Contains(body, "itself an undo") {
		t.Errorf("an undo undone: %d %s", status, body)
	}
}

// An undo writes only over the change it undoes. When the item has been
// changed since in a field the undo would write, it says so and changes
// nothing; a change to another field is kept.
func TestAnUndoNeverOverwritesANewerChange(t *testing.T) {
	h := open(t)
	created := h.change(hers, "t1", createNoteTool, `{"title":"Draft","content":"one"}`)
	draft := h.count(`SELECT id FROM documents WHERE title = 'Draft'`)
	// The person edits it in the editor.
	testpostgres.Exec(t, h.pool, fmt.Sprintf(`UPDATE documents SET source_markdown = 'one, and two' WHERE id = %d`, draft))
	if status, body := h.undo(alice, 10, created); status != 409 || !strings.Contains(body, "changed since it was created") {
		t.Errorf("a note edited since its creation: %d %s", status, body)
	}
	if h.count(`SELECT count(*) FROM documents WHERE id = $1`, draft) != 1 {
		t.Fatal("the edited note was deleted")
	}

	first := h.change(hers, "t2", renameDocumentTool, `{"document_id":200,"title":"B"}`)
	second := h.change(hers, "t2", renameDocumentTool, `{"document_id":200,"title":"C"}`)
	if status, body := h.undo(alice, 10, first); status != 409 || !strings.Contains(body, `renamed again since (it is now \"C\")`) {
		t.Errorf("the earlier of two renames: %d %s", status, body)
	}
	if status, body := h.undo(alice, 10, second); status != 200 || !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("the later rename: %d %s", status, body)
	}
	if status, _ := h.undo(alice, 10, first); status != 200 || h.text(`SELECT title FROM documents WHERE id = 200`) != "Lecture 3.pdf" {
		t.Errorf("the earlier rename, once the later is undone: %d %s", status, h.text(`SELECT title FROM documents WHERE id = 200`))
	}

	// A move undone after a rename: the rename did not touch the folder,
	// so the move is undone and the new title kept.
	moved := h.change(hers, "t3", moveDocumentTool, `{"document_id":200,"folder_id":1}`)
	h.change(hers, "t3", renameDocumentTool, `{"document_id":200,"title":"Kept"}`)
	if status, body := h.undo(alice, 10, moved); status != 200 || h.text(`SELECT title || '|' || COALESCE(folder_id::text, '-') FROM documents WHERE id = 200`) != "Kept|-" {
		t.Errorf("a move undone after a rename: %d %s", status, body)
	}

	// A note moved into a folder that has since gone is not put somewhere
	// nobody chose.
	h.change(hers, "t4", createFolderTool, `{"name":"Soon gone"}`)
	soon := h.count(`SELECT id FROM folders WHERE name = 'Soon gone'`)
	h.change(hers, "t4", moveDocumentTool, fmt.Sprintf(`{"document_id":%d,"folder_id":%d}`, draft, soon))
	backIn := h.change(hers, "t4", moveDocumentTool, fmt.Sprintf(`{"document_id":%d,"folder_id":null}`, draft))
	h.change(hers, "t4", deleteFolderTool, fmt.Sprintf(`{"folder_id":%d}`, soon))
	if status, body := h.undo(alice, 10, backIn); status != 409 || !strings.Contains(body, "folder it was in has been deleted") {
		t.Errorf("a move back into a deleted folder: %d %s", status, body)
	}
}

// Nothing of one account is reached from another's chat: not a document,
// a folder, a listing, an action, or a turn.
func TestOneAccountsChangesAreNotAnothersToMakeOrUndo(t *testing.T) {
	h := open(t)
	hersAction := h.change(hers, "10:1", renameDocumentTool, `{"document_id":100,"title":"Renamed by her"}`)

	h.refused(his, renameDocumentTool, `{"document_id":100,"title":"x"}`, "no document with id 100")
	h.refused(his, editNoteTool, `{"document_id":100,"content":"x"}`, "no document with id 100")
	h.refused(his, deleteNoteTool, `{"document_id":100}`, "no document with id 100")
	h.refused(his, moveDocumentTool, `{"document_id":900,"folder_id":1}`, "no folder with id 1")
	h.refused(his, createNoteTool, `{"title":"x","content":"y","folder_id":1}`, "no folder with id 1")
	h.refused(his, renameFolderTool, `{"folder_id":1,"name":"x"}`, "no folder with id 1")
	h.refused(his, deleteFolderTool, `{"folder_id":1}`, "no folder with id 1")
	if _, err, _ := h.run(his, "x", listFolderTool, `{"folder_id":1}`); err == nil {
		t.Error("he listed her folder")
	}
	if _, err, _ := h.run(his, "x", readDocumentTool, `{"document_id":100}`); err == nil {
		t.Error("he read her note")
	}
	if answer, err, _ := h.run(his, "x", findDocumentsTool, `{"query":"Renamed"}`); err != nil || strings.Contains(answer, "Renamed by her") {
		t.Errorf("he found her note: %s %v", answer, err)
	}

	// Her action, asked of her thread by him, and of his thread.
	if status, body := h.undo(bob, 10, hersAction); status != 404 || !strings.Contains(body, threadAbsent) {
		t.Errorf("her thread, asked by him: %d %s", status, body)
	}
	if status, body := h.undo(bob, 20, hersAction); status != 404 || !strings.Contains(body, actionAbsent) {
		t.Errorf("her action, asked in his thread: %d %s", status, body)
	}
	status, body := serve(t, Routes{Changes: h.changes}, bob, http.MethodPost, "/api/v1/threads/20/revert-turn/10:1")
	if status != 200 || !strings.Contains(body, `"total":0`) {
		t.Errorf("her turn, asked in his thread: %d %s", status, body)
	}
	if status, _ := serve(t, Routes{Changes: h.changes}, bob, http.MethodPost, "/api/v1/threads/10/revert-turn/10:1"); status != 404 {
		t.Errorf("her turn, asked in her thread by him: %d", status)
	}
	if h.text(`SELECT title FROM documents WHERE id = 100`) != "Renamed by her" {
		t.Error("her rename was undone by him")
	}
	// And she can.
	if status, body := h.undo(alice, 10, hersAction); status != 200 || h.text(`SELECT title FROM documents WHERE id = 100`) != "Beta blockers" {
		t.Errorf("her own undo: %d %s", status, body)
	}
}

// What is not a note's to lose, or not undoable exactly, is refused with a
// sentence and recorded nowhere.
func TestWhatCannotBeGivenBackIsNotTaken(t *testing.T) {
	h := open(t)
	h.refused(hers, editNoteTool, `{"document_id":200,"content":"x"}`, "is an uploaded file, not a note")
	h.refused(hers, deleteNoteTool, `{"document_id":200}`, "is an uploaded file, not a note")
	h.refused(hers, deleteFolderTool, `{"folder_id":1}`, "not empty")
	h.refused(hers, editNoteTool, `{"document_id":100,"old_text":"nowhere","new_text":"x"}`, "not in the note")
	h.refused(hers, editNoteTool, `{"document_id":100,"content":"Atenolol is cardioselective."}`, "exactly as it is")
	h.refused(hers, createFolderTool, `{"name":"Pharmacology"}`, `already a folder called "Pharmacology"`)
	h.refused(hers, moveDocumentTool, `{"document_id":100,"folder_id":1}`, "already in /Pharmacology")
	h.refused(hers, moveDocumentTool, `{"document_id":100}`, "folder_id is required")
	h.refused(hers, createNoteTool, `{"title":"x","content":"y","path":"/Pharmacology"}`, "not what this tool takes")
	// A note with a stored file is not a plain note.
	testpostgres.Exec(t, h.pool, `INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes) VALUES (100, '`+alice+`', 'local', 'k/100', 'scan.png', 3)`)
	h.refused(hers, deleteNoteTool, `{"document_id":100}`, "stored files")
}

// Folders keep their rules: never inside themselves, never deeper than the
// limit, never two of one name side by side; and every one of those
// changes is undone back to where it was.
func TestFoldersMoveWithinTheirRulesAndBack(t *testing.T) {
	h := open(t)
	h.change(hers, "f", createFolderTool, `{"name":"A","parent_folder_id":1}`)
	a := h.count(`SELECT id FROM folders WHERE name = 'A'`)
	h.change(hers, "f", createFolderTool, fmt.Sprintf(`{"name":"B","parent_folder_id":%d}`, a))
	b := h.count(`SELECT id FROM folders WHERE name = 'B'`)

	h.refused(hers, moveFolderTool, fmt.Sprintf(`{"folder_id":%d,"parent_folder_id":%d}`, a, b), "into itself or into a folder inside it")
	h.refused(hers, moveFolderTool, fmt.Sprintf(`{"folder_id":%d,"parent_folder_id":%d}`, a, a), "into itself")
	h.refused(hers, renameFolderTool, fmt.Sprintf(`{"folder_id":%d,"name":"A"}`, a), "already called")

	moved := h.change(hers, "f", moveFolderTool, fmt.Sprintf(`{"folder_id":%d,"parent_folder_id":null}`, b))
	if got := h.text(`SELECT COALESCE(parent_id::text, 'top') FROM folders WHERE id = $1`, b); got != "top" {
		t.Errorf("B is under %s", got)
	}
	// A folder called B is made where B was, so moving B back would put
	// two folders of one name side by side.
	clash := h.change(hers, "f", createFolderTool, fmt.Sprintf(`{"name":"B","parent_folder_id":%d}`, a))
	if status, body := h.undo(alice, 10, moved); status != 409 || !strings.Contains(body, `already a folder called \"B\" there`) {
		t.Errorf("a move back onto a name taken since: %d %s", status, body)
	}
	if status, _ := h.undo(alice, 10, clash); status != 200 {
		t.Fatal("the clashing folder was not removed")
	}
	if status, body := h.undo(alice, 10, moved); status != 200 || h.count(`SELECT parent_id FROM folders WHERE id = $1`, b) != a {
		t.Errorf("B moved back: %d %s", status, body)
	}

	renamed := h.change(hers, "f", renameFolderTool, fmt.Sprintf(`{"folder_id":%d,"name":"Bee"}`, b))
	if status, _ := h.undo(alice, 10, renamed); status != 200 || h.text(`SELECT name FROM folders WHERE id = $1`, b) != "B" {
		t.Errorf("the rename undone: %d", status)
	}

	// An empty folder deleted, with the notebook it was a source of, and
	// brought back under its parent with its switch.
	testpostgres.Exec(t, h.pool, fmt.Sprintf(`INSERT INTO notebook_sources (notebook_id, user_id, folder_id) VALUES (5, '%s', %d)`, alice, b))
	deleted := h.change(hers, "f", deleteFolderTool, fmt.Sprintf(`{"folder_id":%d}`, b))
	if h.count(`SELECT count(*) FROM folders WHERE name = 'B'`) != 0 {
		t.Fatal("B was not deleted")
	}
	if status, body := h.undo(alice, 10, deleted); status != 200 {
		t.Fatalf("B brought back: %d %s", status, body)
	}
	back := h.count(`SELECT id FROM folders WHERE name = 'B'`)
	if h.count(`SELECT parent_id FROM folders WHERE id = $1`, back) != a || h.count(`SELECT count(*) FROM notebook_sources WHERE folder_id = $1 AND notebook_id = 5`, back) != 1 {
		t.Error("B is not back where it was, with its notebook")
	}

	// Nine deep is past the limit.
	parent := back
	for depth := 4; depth <= 8; depth++ {
		h.change(hers, "deep", createFolderTool, fmt.Sprintf(`{"name":"L%d","parent_folder_id":%d}`, depth, parent))
		parent = h.count(`SELECT id FROM folders WHERE name = $1`, fmt.Sprintf("L%d", depth))
	}
	h.refused(hers, createFolderTool, fmt.Sprintf(`{"name":"L9","parent_folder_id":%d}`, parent), "nested 8 deep")
}

// The reading tools show what a change needs: ids, paths, and a long text
// in parts.
func TestTheTreeIsReadWithIDsAndPaths(t *testing.T) {
	h := open(t)
	answer, err, _ := h.run(hers, "r", listFolderTool, `{}`)
	if err != nil || !strings.Contains(answer, "- [folder 1] Pharmacology") || !strings.Contains(answer, "- [document 200] Lecture 3.pdf (uploaded file)") {
		t.Errorf("the top level: %s %v", answer, err)
	}
	answer, _, _ = h.run(hers, "r", listFolderTool, `{"folder_id":1}`)
	if !strings.Contains(answer, "Folder /Pharmacology (folder 1).") || !strings.Contains(answer, "- [document 100] Beta blockers (note)") {
		t.Errorf("a folder: %s", answer)
	}
	answer, _, _ = h.run(hers, "r", findDocumentsTool, `{"query":"cardioselective"}`)
	if !strings.Contains(answer, "[document 100] Beta blockers (note) in /Pharmacology") {
		t.Errorf("found by its text: %s", answer)
	}
	answer, _, _ = h.run(hers, "r", findDocumentsTool, `{"query":"100%"}`)
	if !strings.Contains(answer, "No document") {
		t.Errorf("a percent sign is a percent sign: %s", answer)
	}
	long := strings.Repeat("é", readChars+5)
	testpostgres.Exec(t, h.pool, fmt.Sprintf(`UPDATE documents SET source_markdown = '%s' WHERE id = 100`, long))
	answer, _, _ = h.run(hers, "r", readDocumentTool, `{"document_id":100}`)
	if !strings.Contains(answer, fmt.Sprintf("Characters 0 to %d of %d", readChars, readChars+5)) || !strings.Contains(answer, fmt.Sprintf("offset %d", readChars)) {
		t.Errorf("a long text's first part: %.200s", answer)
	}
	answer, _, _ = h.run(hers, "r", readDocumentTool, fmt.Sprintf(`{"document_id":100,"offset":%d}`, readChars))
	if !strings.Contains(answer, "<text>\nééééé\n</text>") || strings.Contains(answer, "More follows") {
		t.Errorf("a long text's last part: %s", answer)
	}
}

// In a notebook's chat the specialist files what it writes in the notebook
// and reads only what that chat may read: the notebook's sources and the
// notes filed in it. Every title is still listed, and anything may still
// be renamed or moved.
func TestANotebooksChatReadsOnlyWhatTheNotebookMay(t *testing.T) {
	h := open(t)
	testpostgres.Exec(t, h.pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown) VALUES
 (300, '`+alice+`', 'Private diary', 'NOTE', 'secret words', 'h', 'secret words');
INSERT INTO notes (document_id, user_id, origin) VALUES (300, '`+alice+`', 'hand');`)
	notebook := int64(5)
	// The file is the notebook's one source; note 100 is filed in it.
	inNotebook := owner{userID: alice, threadID: 10, notebookID: &notebook, readable: map[int64]bool{200: true}}

	h.change(inNotebook, "n", createNoteTool, `{"title":"From the notebook chat","content":"x"}`)
	if got := h.text(`SELECT n.notebook_id::text FROM notes n JOIN documents d ON d.id = n.document_id WHERE d.title = 'From the notebook chat'`); got != "5" {
		t.Errorf("the note is filed in notebook %s, want the chat's", got)
	}
	for _, readable := range []string{`{"document_id":200}`, `{"document_id":100}`} {
		if _, err, _ := h.run(inNotebook, "n", readDocumentTool, readable); err != nil {
			t.Errorf("%s may be read in the notebook's chat: %v", readable, err)
		}
	}
	h.refused(inNotebook, readDocumentTool, `{"document_id":300}`, "neither one of its sources nor a note filed in it")
	h.refused(inNotebook, editNoteTool, `{"document_id":300,"content":"overwritten"}`, "neither one of its sources")
	if answer, _, _ := h.run(inNotebook, "n", findDocumentsTool, `{"query":"secret"}`); strings.Contains(answer, "Private diary") {
		t.Errorf("its text was matched: %s", answer)
	}
	if answer, _, _ := h.run(inNotebook, "n", findDocumentsTool, `{"query":"diary"}`); !strings.Contains(answer, "[document 300] Private diary") {
		t.Errorf("its title is not listed: %s", answer)
	}
	h.change(inNotebook, "n", renameDocumentTool, `{"document_id":300,"title":"Diary"}`)
	// The same account's plain chat reads it.
	if answer, err, _ := h.run(hers, "p", readDocumentTool, `{"document_id":300}`); err != nil || !strings.Contains(answer, "secret words") {
		t.Errorf("a plain chat: %s %v", answer, err)
	}
}
