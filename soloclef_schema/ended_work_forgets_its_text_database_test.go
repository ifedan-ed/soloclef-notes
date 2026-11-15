package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// endedWorkForgetsItsText is 00028, which removes the copies of a person's
// text left by work that has ended: stored Studio tasks and saved
// conversations whose thread, job or run is gone or over.
const endedWorkForgetsItsText = 28

// The Up clears the task of ended jobs only, and removes only the
// conversations whose owner can no longer read them, matching each family's
// exact name shape; the Down puts nothing back and fails on nothing.
func TestEndedWorkForgetsItsText(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	before := versionBefore(t, endedWorkForgetsItsText)
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO "user" (id, email) VALUES ('11111111-1111-4111-8111-111111111111', 'a@example.test');
INSERT INTO new_chat_threads (id, user_id, title) VALUES (5, '11111111-1111-4111-8111-111111111111', 'kept');
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, attempt_count, request) VALUES
 (1, '11111111-1111-4111-8111-111111111111', 'artifact', 'cancelled', 'a', 'cancelled', 1, '{"kind":"quiz","task":"t"}'),
 (2, '11111111-1111-4111-8111-111111111111', 'artifact', 'failed', 'b', 'failed', 1, '{"task":"t"}'),
 (3, '11111111-1111-4111-8111-111111111111', 'artifact', 'ready', 'c', 'ready', 1, '{"task":"t"}'),
 (4, '11111111-1111-4111-8111-111111111111', 'artifact', 'queued', 'd', 'queued', 2, '{"task":"t"}'),
 (6, '11111111-1111-4111-8111-111111111111', 'artifact', 'running', 'e', 'running', 2, '{"task":"t"}');
INSERT INTO automations (id, user_id, name, definition) VALUES (7, '11111111-1111-4111-8111-111111111111', 'a', '{}');
INSERT INTO automation_runs (id, automation_id, status, definition_snapshot) VALUES (8, 7, 'running', '{}'), (9, 7, 'succeeded', '{}');
INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state)
SELECT name, 1, 'done', '', '\x7b7d'::bytea FROM unnest(ARRAY[
  'chat-5', 'chat-5/call', 'chat-5.turn', 'chat-50', 'chat-50.turn', 'chat-50/call', 'chat-notes',
  'studio-job-6-attempt-2', 'studio-job-6-attempt-2/call', 'studio-job-6-attempt-1', 'studio-job-4-attempt-1',
  'studio-job-1-attempt-1', 'studio-job-60-attempt-2', 'studio-job-6-attempt-x',
  'automation-run-8-0-1', 'automation-run-9-0-1', 'automation-run-90-0-1', 'automation-runner',
  'something-else'
]) AS name;`); err != nil {
		t.Fatal(err)
	}
	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if tasks := column[int64](t, pool, `SELECT id FROM deliverable_jobs WHERE request ? 'task' ORDER BY id`); len(tasks) != 2 || tasks[0] != 4 || tasks[1] != 6 {
		t.Errorf("the jobs still holding a task: %v, want the queued and the running one", tasks)
	}
	if kinds := column[string](t, pool, `SELECT request ->> 'kind' FROM deliverable_jobs WHERE id = 1`); !equal(kinds, []string{"quiz"}) {
		t.Errorf("the rest of a cancelled job's request: %v", kinds)
	}
	want := []string{
		"automation-run-8-0-1", "automation-runner", "chat-5", "chat-5.turn", "chat-5/call", "chat-notes",
		"something-else", "studio-job-6-attempt-2", "studio-job-6-attempt-2/call", "studio-job-6-attempt-x",
	}
	if left := column[string](t, pool, `SELECT thread_id FROM soloclef_agent_checkpoints ORDER BY thread_id COLLATE "C"`); !equal(left, want) {
		t.Errorf("the conversations left:\n %v\nwant\n %v", left, want)
	}
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("the Down: %v", err)
	}
}
