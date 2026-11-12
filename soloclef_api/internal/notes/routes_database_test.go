package notes

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// tests. Every account in it is deleted (internal/testpostgres), so it must
// never name a database anyone uses. Unset, the tests are skipped.
const databaseVariable = "SOLOCLEF_API_TEST_NOTES_DATABASE"

// TestNotesOverARealDatabase is every route against the schema: writing,
// saving with its debounced indexing, the list's order, search and
// filters, filing, the source switch, a kept chat answer, and a notebook's
// deletion leaving its notes.
func TestNotesOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	s := serve(t, pool).as(testpostgres.Alice)
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title, icon) VALUES
 (501, '`+testpostgres.Alice+`', 'Paediatrics', '🩺'),
 (502, '`+testpostgres.Alice+`', 'Pharmacy', NULL);
INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES
 (601, '`+testpostgres.Alice+`', 'Fluids', 501);
INSERT INTO new_chat_messages (id, thread_id, role, content) VALUES
 (701, 601, 'ASSISTANT', '[]'::jsonb);`)

	written := s.send(http.MethodPost, "/api/v1/notes", `{"content":"# Ward plan\n\nGive maintenance fluids at 100 ml/kg."}`).
		expect(t, http.StatusCreated)
	plan := written.id()
	if written.body["title"] != "Ward plan" || written.body["origin"] != "hand" || written.body["notebook"] != nil {
		t.Errorf("a note written without a title: %v", written.body)
	}
	if written.body["index_state"] != "pending" {
		t.Errorf("a note with text is waiting to be indexed: %v", written.body["index_state"])
	}
	if jobs := s.jobs.taken(); !reflect.DeepEqual(jobs, []job{{plan, 3 * time.Second}}) {
		t.Errorf("jobs %v: one delayed job for the new note", jobs)
	}

	empty := s.send(http.MethodPost, "/api/v1/notes", `{}`).expect(t, http.StatusCreated)
	if empty.body["title"] != untitled || empty.body["index_state"] != "ready" || len(s.jobs.taken()) != 0 {
		t.Errorf("an empty note has nothing to index: %v", empty.body)
	}

	t.Run("saves while a job waits queue no second job", func(t *testing.T) {
		saved := s.send(http.MethodPatch, fmt.Sprint("/api/v1/notes/", plan),
			`{"content":"# Ward plan\n\nGive maintenance fluids at 100 ml/kg. Recheck sodium."}`).expect(t, http.StatusOK)
		if !strings.Contains(saved.body["content"].(string), "Recheck sodium") {
			t.Errorf("the text was not saved: %v", saved.body["content"])
		}
		if jobs := s.jobs.taken(); len(jobs) != 0 {
			t.Errorf("a save while the job waited queued %v", jobs)
		}
		// The worker indexes it.
		testpostgres.Exec(t, pool, fmt.Sprintf(`UPDATE documents SET status = '{"state": "ready"}' WHERE id = %d`, plan))
		s.send(http.MethodPatch, fmt.Sprint("/api/v1/notes/", plan),
			`{"content":"# Ward plan\n\nGive maintenance fluids. Recheck sodium at six hours."}`).expect(t, http.StatusOK)
		if jobs := s.jobs.taken(); len(jobs) != 1 {
			t.Errorf("a save after indexing queued %v, want one job", jobs)
		}
		// A title is not what the indexer reads; nor is the same text again.
		testpostgres.Exec(t, pool, fmt.Sprintf(`UPDATE documents SET status = '{"state": "ready"}' WHERE id = %d`, plan))
		renamed := s.send(http.MethodPatch, fmt.Sprint("/api/v1/notes/", plan),
			`{"title":"  Ward plan, day two ","content":"# Ward plan\n\nGive maintenance fluids. Recheck sodium at six hours."}`).
			expect(t, http.StatusOK)
		if renamed.body["title"] != "Ward plan, day two" || renamed.body["index_state"] != "ready" {
			t.Errorf("renamed: %v", renamed.body)
		}
		if jobs := s.jobs.taken(); len(jobs) != 0 {
			t.Errorf("a rename queued %v", jobs)
		}
		// An emptied title names the note from its text again.
		named := s.send(http.MethodPatch, fmt.Sprint("/api/v1/notes/", plan), `{"title":"  "}`).expect(t, http.StatusOK)
		if named.body["title"] != "Ward plan" {
			t.Errorf("an emptied title: %v", named.body["title"])
		}
	})

	t.Run("pinned first, then the most recently edited", func(t *testing.T) {
		s.send(http.MethodPut, fmt.Sprintf("/api/v1/notes/%d/pin", empty.id()), "").expect(t, http.StatusOK)
		list := s.send(http.MethodGet, "/api/v1/notes", "").expect(t, http.StatusOK)
		if got := list.titles(); !reflect.DeepEqual(got, []string{untitled, "Ward plan"}) {
			t.Errorf("order %v", got)
		}
		if list.body["total"] != float64(2) || list.body["has_more"] != false {
			t.Errorf("total %v, has_more %v", list.body["total"], list.body["has_more"])
		}
		s.send(http.MethodDelete, fmt.Sprintf("/api/v1/notes/%d/pin", empty.id()), "").expect(t, http.StatusOK)
		list = s.send(http.MethodGet, "/api/v1/notes?pinned=true", "").expect(t, http.StatusOK)
		if len(list.titles()) != 0 {
			t.Errorf("unpinned, still listed as pinned: %v", list.titles())
		}
	})

	t.Run("search finds a word in the text and shows the passage", func(t *testing.T) {
		list := s.send(http.MethodGet, "/api/v1/notes?q=sodium", "").expect(t, http.StatusOK)
		if got := list.titles(); !reflect.DeepEqual(got, []string{"Ward plan"}) {
			t.Fatalf("found %v", got)
		}
		item := list.body["items"].([]any)[0].(map[string]any)
		if !strings.Contains(item["preview"].(string), "sodium") {
			t.Errorf("preview %q", item["preview"])
		}
		// A title still being typed.
		if got := s.send(http.MethodGet, "/api/v1/notes?q=war", "").titles(); !reflect.DeepEqual(got, []string{"Ward plan"}) {
			t.Errorf("a title prefix found %v", got)
		}
		if got := s.send(http.MethodGet, "/api/v1/notes?q=insulin", "").titles(); len(got) != 0 {
			t.Errorf("a word in no note found %v", got)
		}
	})

	t.Run("filing, and the notebook's own list", func(t *testing.T) {
		filed := s.send(http.MethodPut, fmt.Sprintf("/api/v1/notes/%d/notebook", plan), `{"notebook_id":501}`).
			expect(t, http.StatusOK)
		notebook := filed.body["notebook"].(map[string]any)
		if notebook["id"] != float64(501) || notebook["title"] != "Paediatrics" || notebook["icon"] != "🩺" {
			t.Errorf("filed in %v", notebook)
		}
		if got := s.send(http.MethodGet, "/api/v1/notes?notebook_id=501", "").titles(); !reflect.DeepEqual(got, []string{"Ward plan"}) {
			t.Errorf("the notebook lists %v", got)
		}
		if got := s.send(http.MethodGet, "/api/v1/notes?unfiled=true", "").titles(); !reflect.DeepEqual(got, []string{untitled}) {
			t.Errorf("unfiled lists %v", got)
		}
		s.send(http.MethodPut, fmt.Sprintf("/api/v1/notes/%d/notebook", empty.id()), `{"notebook_id":null}`).expect(t, http.StatusOK)
	})

	t.Run("the source switch is one row naming the note itself", func(t *testing.T) {
		path := fmt.Sprintf("/api/v1/notes/%d/sources/502", plan)
		s.send(http.MethodPut, path, "").expect(t, http.StatusOK)
		on := s.send(http.MethodPut, path, "").expect(t, http.StatusOK)
		sources := on.body["sources"].([]any)
		if len(sources) != 1 || sources[0].(map[string]any)["id"] != float64(502) {
			t.Errorf("sources %v", sources)
		}
		var rows int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM notebook_sources WHERE document_id = $1`, plan).Scan(&rows); err != nil || rows != 1 {
			t.Errorf("source rows %d, %v", rows, err)
		}
		off := s.send(http.MethodDelete, path, "").expect(t, http.StatusOK)
		if len(off.body["sources"].([]any)) != 0 {
			t.Errorf("switched off, still a source: %v", off.body["sources"])
		}
	})

	t.Run("a kept answer links back and is filed where it was asked", func(t *testing.T) {
		kept := s.send(http.MethodPost, "/api/v1/notes",
			`{"content":"**Bolus** 10 ml/kg [citation:4] then reassess.","thread_id":601,"message_id":701}`).
			expect(t, http.StatusCreated)
		if kept.body["origin"] != "chat" || kept.body["thread_id"] != float64(601) || kept.body["message_id"] != float64(701) {
			t.Errorf("kept %v", kept.body)
		}
		if kept.body["chat_notebook_id"] != float64(501) {
			t.Errorf("the chat it came from is notebook %v", kept.body["chat_notebook_id"])
		}
		if kept.body["title"] != "Bolus 10 ml/kg then reassess." {
			t.Errorf("title %q", kept.body["title"])
		}
		if notebook, _ := kept.body["notebook"].(map[string]any); notebook == nil || notebook["id"] != float64(501) {
			t.Errorf("a notebook chat's answer is filed in that notebook: %v", kept.body["notebook"])
		}
		if got := s.send(http.MethodGet, "/api/v1/notes?thread_id=601", "").titles(); len(got) != 1 {
			t.Errorf("the thread's kept answers: %v", got)
		}
		s.jobs.taken()
		s.send(http.MethodPost, "/api/v1/notes", `{"content":"x","thread_id":601,"message_id":999}`).
			expect(t, http.StatusNotFound)
	})

	t.Run("deleting a notebook unfiles its notes", func(t *testing.T) {
		testpostgres.Exec(t, pool, `DELETE FROM notebooks WHERE id = 501`)
		note := s.send(http.MethodGet, fmt.Sprint("/api/v1/notes/", plan), "").expect(t, http.StatusOK)
		if note.body["notebook"] != nil {
			t.Errorf("still filed in %v", note.body["notebook"])
		}
	})

	t.Run("a note is deleted, waiting job or not", func(t *testing.T) {
		testpostgres.Exec(t, pool, fmt.Sprintf(`UPDATE documents SET status = '{"state": "pending"}' WHERE id = %d`, plan))
		s.send(http.MethodDelete, fmt.Sprint("/api/v1/notes/", plan), "").expect(t, http.StatusNoContent)
		s.send(http.MethodGet, fmt.Sprint("/api/v1/notes/", plan), "").expect(t, http.StatusNotFound)
	})
}

// TestAnotherAccountsNoteIsNotThere is the isolation test of every route:
// account B can neither see nor change account A's note, nor file a note in,
// switch one on as a source of, or keep an answer from A's notebook or chat.
// Each refusal is the same 404 a note never written gets.
func TestAnotherAccountsNoteIsNotThere(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	s := serve(t, pool)
	alice, bob := testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO notebooks (id, user_id, title) VALUES (801, '`+alice+`', 'Alice''s'), (802, '`+bob+`', 'Bob''s');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES (901, '`+alice+`', 'Alice''s chat', 801);
INSERT INTO new_chat_messages (id, thread_id, role, content) VALUES (911, 901, 'ASSISTANT', '[]'::jsonb);`)
	hers := s.as(alice).send(http.MethodPost, "/api/v1/notes", `{"title":"Private","content":"Alice's words","notebook_id":801}`).
		expect(t, http.StatusCreated).id()
	s.send(http.MethodPut, fmt.Sprintf("/api/v1/notes/%d/sources/801", hers), "").expect(t, http.StatusOK)
	his := s.as(bob).send(http.MethodPost, "/api/v1/notes", `{"title":"Bob's"}`).expect(t, http.StatusCreated).id()

	note := fmt.Sprint("/api/v1/notes/", hers)
	refused := []struct{ verb, path, body, message string }{
		{http.MethodGet, note, "", noteAbsent},
		{http.MethodPatch, note, `{"content":"Bob was here"}`, noteAbsent},
		{http.MethodPatch, note, `{"title":""}`, noteAbsent},
		{http.MethodPut, note + "/pin", "", noteAbsent},
		{http.MethodDelete, note + "/pin", "", noteAbsent},
		{http.MethodPut, note + "/notebook", `{"notebook_id":null}`, noteAbsent},
		{http.MethodPut, note + "/sources/802", "", noteAbsent},
		{http.MethodDelete, note + "/sources/801", "", noteAbsent},
		{http.MethodDelete, note, "", noteAbsent},
		// His own note, her notebook or chat.
		{http.MethodPut, fmt.Sprintf("/api/v1/notes/%d/notebook", his), `{"notebook_id":801}`, notebookAbsent},
		{http.MethodPut, fmt.Sprintf("/api/v1/notes/%d/sources/801", his), "", notebookAbsent},
		{http.MethodPost, "/api/v1/notes", `{"content":"x","notebook_id":801}`, notebookAbsent},
		{http.MethodPost, "/api/v1/notes", `{"content":"x","thread_id":901,"message_id":911}`, threadAbsent},
	}
	for _, c := range refused {
		answer := s.send(c.verb, c.path, c.body)
		if answer.status != http.StatusNotFound || !strings.Contains(answer.raw, c.message) {
			t.Errorf("%s %s as Bob: %d %s, want 404 %q", c.verb, c.path, answer.status, answer.raw, c.message)
		}
	}
	for _, query := range []string{"", "?q=Alice", "?notebook_id=801", "?thread_id=901"} {
		if got := s.send(http.MethodGet, "/api/v1/notes"+query, "").expect(t, http.StatusOK).titles(); containsTitle(got, "Private") {
			t.Errorf("Bob's list%s shows %v", query, got)
		}
	}

	// Alice's note is as she left it.
	mine := s.as(alice).send(http.MethodGet, note, "").expect(t, http.StatusOK)
	if mine.body["content"] != "Alice's words" || mine.body["pinned"] != false ||
		mine.body["notebook"] == nil || len(mine.body["sources"].([]any)) != 1 {
		t.Errorf("Alice's note after Bob's attempts: %v", mine.body)
	}
	if rows := s.send(http.MethodGet, "/api/v1/notes", "").titles(); !reflect.DeepEqual(rows, []string{"Private"}) {
		t.Errorf("Alice's list %v", rows)
	}
}

func containsTitle(titles []string, wanted string) bool {
	for _, title := range titles {
		if title == wanted {
			return true
		}
	}
	return false
}

// A note whose text is deleted entirely is queued for indexing like any
// other edit: the indexer takes its old passages out of search and marks it
// ready with none. Without the job they would stay searchable under a note
// that no longer says them. A null text empties a note the same way.
func TestAnEmptiedNoteIsIndexedAgain(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	s := serve(t, pool).as(testpostgres.Alice)

	for _, emptied := range []string{`{"content":""}`, `{"content":null}`} {
		written := s.send(http.MethodPost, "/api/v1/notes", `{"content":"The heron waits for the eel."}`).expect(t, http.StatusCreated)
		note := written.id()
		s.jobs.taken()
		// The worker indexed it.
		testpostgres.Exec(t, pool, fmt.Sprintf(`UPDATE documents SET status = '{"state": "ready"}' WHERE id = %d`, note))

		saved := s.send(http.MethodPatch, fmt.Sprint("/api/v1/notes/", note), emptied).expect(t, http.StatusOK)

		if saved.body["content"] != "" || saved.body["index_state"] != "pending" {
			t.Errorf("%s: content %q, index_state %v", emptied, saved.body["content"], saved.body["index_state"])
		}
		if jobs := s.jobs.taken(); !reflect.DeepEqual(jobs, []job{{note, 3 * time.Second}}) {
			t.Errorf("%s: jobs %v, want one delayed job for the emptied note", emptied, jobs)
		}
		// Emptied again, nothing changed: nothing to index.
		testpostgres.Exec(t, pool, fmt.Sprintf(`UPDATE documents SET status = '{"state": "ready"}' WHERE id = %d`, note))
		s.send(http.MethodPatch, fmt.Sprint("/api/v1/notes/", note), emptied).expect(t, http.StatusOK)
		if jobs := s.jobs.taken(); len(jobs) != 0 {
			t.Errorf("%s: an unchanged empty text queued %v", emptied, jobs)
		}
	}
}
