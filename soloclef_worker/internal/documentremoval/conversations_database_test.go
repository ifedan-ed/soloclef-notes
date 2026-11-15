package documentremoval

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// An erased account's saved conversations go with it: its chats (with
// their specialists' threads and kept turn), its Studio runs and its
// automation runs. Another account's, and names that only begin the same
// way, stay.
func TestAnErasedAccountsConversationsGo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := testdatabase.Account(t, f.pool)
	stranger := testdatabase.Account(t, f.pool)
	// The ids are chosen so the stranger's begin with the owner's: "chat-
	// 9300001" must not take "chat-93000010" with it.
	// One statement per call: a statement with parameters is prepared, and
	// Postgres prepares one command at a time.
	testdatabase.Exec(t, f.pool, `INSERT INTO new_chat_threads (id, user_id, title) VALUES (9300001, $1::uuid, 'mine'), (93000010, $2::uuid, 'theirs')`,
		owner, stranger)
	testdatabase.Exec(t, f.pool, `INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, attempt_count)
		VALUES (9300002, $1::uuid, 'artifact', 'mine', 'm', 2), (93000020, $2::uuid, 'artifact', 'theirs', 't', 1)`, owner, stranger)
	testdatabase.Exec(t, f.pool, `INSERT INTO automations (id, user_id, name, definition) VALUES (9300003, $1::uuid, 'mine', '{}'), (93000030, $2::uuid, 'theirs', '{}')`,
		owner, stranger)
	testdatabase.Exec(t, f.pool, `INSERT INTO automation_runs (id, automation_id, definition_snapshot) VALUES (9300004, 9300003, '{}'), (93000040, 93000030, '{}')`)
	mine := []string{
		"chat-9300001", "chat-9300001/call-1", "chat-9300001/call-1/call-2", "chat-9300001.turn",
		"studio-job-9300002-attempt-1", "studio-job-9300002-attempt-2", "studio-job-9300002-attempt-2/call-1",
		"automation-run-9300004-0-1", "automation-run-9300004-1-1/call-1",
	}
	theirs := []string{
		"chat-93000010", "chat-93000010.turn", "studio-job-93000020-attempt-1", "automation-run-93000040-0-1",
	}
	for _, name := range append(append([]string{}, mine...), theirs...) {
		testdatabase.Exec(t, f.pool, `INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state) VALUES ($1, 1, 'done', '', '\x7b7d')`, name)
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM soloclef_agent_checkpoints WHERE thread_id = ANY($1)`, append(mine, theirs...))
	})

	testdatabase.Exec(t, f.pool, `UPDATE "user" SET is_active = false WHERE id = $1::uuid`, owner)
	if err := f.removal.EraseAccount(ctx, owner); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	rows, err := f.pool.Query(ctx, `SELECT thread_id FROM soloclef_agent_checkpoints WHERE thread_id = ANY($1)`, append(mine, theirs...))
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		left[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, name := range mine {
		if left[name] {
			t.Errorf("%s outlived its account", name)
		}
	}
	for _, name := range theirs {
		if !left[name] {
			t.Errorf("%s, another account's, was removed", name)
		}
	}
}
