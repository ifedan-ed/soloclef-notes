package threads

import (
	"net/http"
	"testing"
)

// A notebook's chats are listed, started and cleared by the notebook's
// owner alone. Clearing goes through the one way a thread is deleted, so the
// saved conversation goes with it, and leaves the studio's own threads and
// every other chat alone.
func TestANotebooksChatsAreItsOwnersAlone(t *testing.T) {
	w := newWorld(t)
	w.exec(`
INSERT INTO notebooks (id, user_id, title, updated_at) VALUES
  (7, '` + ada + `', 'Cells', '2026-01-01'), (8, '` + ben + `', 'His', '2026-01-01');
UPDATE new_chat_threads SET notebook_id = 7 WHERE id IN (1, 2, 4, 6);
INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state)
  VALUES ('chat-1', 1, 'done', 'answer', '\x00');`)

	listed := w.call(ada, http.MethodGet, "/api/v1/notebooks/7/threads", "")
	if listed.status != 200 || len(listed.list) != 2 {
		t.Fatalf("her notebook's chats: %d %s", listed.status, listed.raw)
	}
	// Newest activity first, the archived chat and the studio's thread left out.
	if first := listed.list[0].(map[string]any); first["id"] != float64(2) || first["title"] != "Plans 50% done" {
		t.Errorf("first listed %v", first)
	}
	w.refusedWith("her notebook's chats asked by him", w.call(ben, http.MethodGet, "/api/v1/notebooks/7/threads", ""), 404, notebookAbsent)
	w.refusedWith("his start in her notebook", w.call(ben, http.MethodPost, "/api/v1/notebooks/7/threads", ""), 404, notebookAbsent)

	started := w.call(ada, http.MethodPost, "/api/v1/notebooks/7/threads", "")
	if started.status != 201 || started.body["title"] != "New Chat" {
		t.Fatalf("her start: %d %s", started.status, started.raw)
	}
	if w.count(`SELECT count(*) FROM new_chat_threads WHERE notebook_id = 7 AND user_id = '`+ada+`' AND title = 'New Chat'`) != 1 ||
		w.count(`SELECT count(*) FROM notebooks WHERE id = 7 AND updated_at = '2026-11-11 09:00:00+00'`) != 1 {
		t.Error("the chat was not made in her notebook, or the notebook was not marked used")
	}

	w.refusedWith("his clear of her notebook", w.call(ben, http.MethodDelete, "/api/v1/notebooks/7/threads", ""), 404, notebookAbsent)
	if w.count(`SELECT count(*) FROM new_chat_threads WHERE notebook_id = 7`) != 5 {
		t.Fatal("his clear deleted something")
	}
	if got := w.call(ada, http.MethodDelete, "/api/v1/notebooks/7/threads", ""); got.status != 204 {
		t.Fatalf("her clear: %d %s", got.status, got.raw)
	}
	if left := w.count(`SELECT count(*) FROM new_chat_threads WHERE notebook_id = 7`); left != 1 ||
		w.count(`SELECT count(*) FROM new_chat_threads WHERE id = 6`) != 1 {
		t.Errorf("%d chats are left in her notebook; only the studio's thread should be", left)
	}
	if w.count(`SELECT count(*) FROM new_chat_threads WHERE id IN (5, 10, 3)`) != 3 {
		t.Error("clearing the notebook took chats that were not in it")
	}
	if w.count(`SELECT count(*) FROM soloclef_agent_checkpoints WHERE thread_id = 'chat-1'`) != 0 {
		t.Error("a cleared chat's saved conversation was left behind")
	}
}
