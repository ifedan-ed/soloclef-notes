package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// studioOutputsOnly is 00019, which left Studio outputs as the only thing a
// public link can share.
const studioOutputsOnly = 19

// shareStructure is every column, constraint and index the sharing tables
// and the chat copy's columns have, by name and definition and in name
// order. Column position is left out on purpose: a Down that adds a column
// back appends it, and no query here reads a row by position.
func shareStructure(t *testing.T, sql func(string) []string) []string {
	t.Helper()
	return sql(`
		SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND (table_name IN ('public_chat_snapshots', 'output_public_links', 'notebook_public_links')
		       OR (table_name = 'new_chat_threads' AND column_name LIKE 'cloned%'))
		UNION ALL
		SELECT 'constraint ' || conrelid::regclass::text || '.' || conname || ' ' || pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid::regclass::text IN ('public_chat_snapshots', 'output_public_links', 'notebook_public_links')
		   OR (conrelid::regclass::text = 'new_chat_threads' AND conname LIKE '%cloned%')
		UNION ALL
		SELECT 'index ' || indexdef FROM pg_indexes
		WHERE schemaname = 'public'
		  AND (tablename IN ('public_chat_snapshots', 'output_public_links', 'notebook_public_links')
		       OR indexname LIKE '%cloned%')
		UNION ALL
		SELECT 'sequence ' || sequencename FROM pg_sequences
		WHERE schemaname = 'public' AND sequencename LIKE ANY (ARRAY['public_chat%', 'output_public%', 'notebook_public%'])
		ORDER BY 1`)
}

// The Up revokes every chat link and every notebook link and takes their
// structure away, while an output's link keeps its token, its expiry and its
// frozen copy. The Down brings the structure back exactly as it was and the
// output link with it; the Up after it lands on the same schema again.
func TestOnlyStudioOutputsCanBeShared(t *testing.T) {
	pool := emptyDatabase(t)
	ctx := context.Background()
	sql := func(statement string) []string { return column[string](t, pool, statement) }
	before := versionBefore(t, studioOutputsOnly)

	if _, err := schema.UpTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("migrating to %d: %v", before, err)
	}
	original := shareStructure(t, sql)

	// One of each kind of link, and a chat a reader copied from the chat
	// link: everything the Up has to decide about.
	exec(t, pool, `
INSERT INTO "user" (id, email) VALUES ('11111111-1111-4111-8111-111111111111', 'a@example.test');
INSERT INTO notebooks (id, user_id, title) VALUES (10, '11111111-1111-4111-8111-111111111111', 'Planets');
INSERT INTO notebook_outputs (id, notebook_id, user_id, kind, title) VALUES (20, 10, '11111111-1111-4111-8111-111111111111', 'mindmap', 'Planets map');
INSERT INTO new_chat_threads (id, user_id, title, notebook_id) VALUES (30, '11111111-1111-4111-8111-111111111111', 'Asked about Mars', 10);
INSERT INTO public_chat_snapshots (id, user_id, thread_id, share_token, content_hash, snapshot_data, message_ids, expires_at)
VALUES (1, '11111111-1111-4111-8111-111111111111', 30, 'tok-chat', 'h1', '{"title": "Asked about Mars"}', '{1,2}', NULL);
INSERT INTO public_chat_snapshots (id, user_id, notebook_output_id, share_token, content_hash, snapshot_data, message_ids, expires_at)
VALUES (2, '11111111-1111-4111-8111-111111111111', 20, 'tok-output', 'h2', '{"kind": "output", "title": "Planets map"}', '{}', '2030-01-01T00:00:00Z');
INSERT INTO new_chat_threads (id, user_id, title, cloned_from_thread_id, cloned_from_snapshot_id, cloned_at)
VALUES (31, '11111111-1111-4111-8111-111111111111', 'Copy of Asked about Mars', 30, 1, now());
INSERT INTO notebook_public_links (notebook_id, user_id, share_token) VALUES (10, '11111111-1111-4111-8111-111111111111', 'tok-notebook');
SELECT setval(pg_get_serial_sequence('public_chat_snapshots', 'id'), 2);`)

	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if left := sql(`SELECT table_name::text FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name IN ('public_chat_snapshots', 'notebook_public_links') ORDER BY 1`); len(left) != 0 {
		t.Errorf("the Up left the tables %v", left)
	}
	if left := sql(`SELECT column_name::text FROM information_schema.columns
		WHERE table_schema = 'public' AND ((table_name = 'output_public_links' AND column_name IN ('thread_id', 'message_ids'))
		   OR (table_name = 'new_chat_threads' AND column_name LIKE 'cloned%')) ORDER BY 1`); len(left) != 0 {
		t.Errorf("the Up left the chat-sharing columns %v", left)
	}
	links := sql(`SELECT id || ' ' || share_token || ' ' || notebook_output_id || ' ' || expires_at::text || ' ' || snapshot_data::text
		FROM output_public_links ORDER BY id`)
	if want := []string{`2 tok-output 20 2030-01-01 00:00:00+00 {"kind": "output", "title": "Planets map"}`}; !equal(links, want) {
		t.Errorf("after the Up the links are\n%v\nwant only the output's, untouched:\n%v", links, want)
	}
	if kept := sql(`SELECT title FROM new_chat_threads ORDER BY id`); !equal(kept, []string{"Asked about Mars", "Copy of Asked about Mars"}) {
		t.Errorf("the Up touched the chats themselves: %v", kept)
	}
	refused(t, pool, `column "notebook_output_id" of relation "output_public_links"`, `
INSERT INTO output_public_links (user_id, share_token, content_hash, snapshot_data)
VALUES ('11111111-1111-4111-8111-111111111111', 'tok-nothing', 'h3', '{}')`)
	// The renamed identity is still the table's, and numbers new links
	// after the old ones.
	if owned := sql(`SELECT pg_get_serial_sequence('output_public_links', 'id')`); !equal(owned, []string{"public.output_public_links_id_seq"}) {
		t.Errorf("the links' identity is %v", owned)
	}
	exec(t, pool, `
INSERT INTO output_public_links (user_id, notebook_output_id, share_token, content_hash, snapshot_data)
VALUES ('11111111-1111-4111-8111-111111111111', 20, 'tok-again', 'h4', '{}');
DELETE FROM output_public_links WHERE share_token = 'tok-again'`)

	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	if back := shareStructure(t, sql); !equal(back, original) {
		t.Errorf("the Down brought back\n%v\nwant what %d had\n%v", back, before, original)
	}
	links = sql(`SELECT id || ' ' || share_token || ' ' || notebook_output_id || ' ' || coalesce(thread_id::text, '-') || ' ' || message_ids::text
		FROM public_chat_snapshots ORDER BY id`)
	if want := []string{"2 tok-output 20 - {}"}; !equal(links, want) {
		t.Errorf("after the Down the links are %v, want %v", links, want)
	}
	if empty := sql(`SELECT share_token FROM notebook_public_links`); len(empty) != 0 {
		t.Errorf("the Down brought notebook links back: %v", empty)
	}

	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
	if again := sql(`SELECT share_token FROM output_public_links`); !equal(again, []string{"tok-output"}) {
		t.Errorf("after Up, Down and Up the links are %v", again)
	}
}
