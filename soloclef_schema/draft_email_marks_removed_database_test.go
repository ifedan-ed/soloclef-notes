package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// draftEmailNotMarkable is 00024, which removed the "ask first" marks on
// the chat's draft_email tool.
const draftEmailNotMarkable = 24

// Migration 24 removes the chat's own draft_email marks, and only those: a
// mark on another tool, and a connected app's tool that happens to be named
// draft_email, are kept. Rolled back, nothing comes back.
func TestDraftEmailMarksAreRemoved(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	const ada = "11111111-1111-4111-8111-111111111111"
	before := versionBefore(t, draftEmailNotMarkable)
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	exec(t, pool, `INSERT INTO "user" (id, email) VALUES ($1, 'a@example.test')`, ada)
	exec(t, pool, `INSERT INTO search_source_connectors (id, user_id, name, connector_type, config) VALUES
		(7, $1, 'Mail app', 'MCP_CONNECTOR', '{"cached_tools": {"tools": [{"name": "draft_email"}]}}')`, ada)
	exec(t, pool, `INSERT INTO agent_ask_first_tools (user_id, connector_id, tool) VALUES
		($1, NULL, 'draft_email'), ($1, NULL, 'read_email'), ($1, 7, 'draft_email')`, ada)

	if _, err := schema.UpTo(ctx, pool, draftEmailNotMarkable, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	left := column[string](t, pool, `SELECT coalesce(connector_id::text, 'chat') || '/' || tool FROM agent_ask_first_tools ORDER BY 1`)
	if len(left) != 2 || left[0] != "7/draft_email" || left[1] != "chat/read_email" {
		t.Errorf("the marks left are %v", left)
	}

	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", draftEmailNotMarkable, err)
	}
	if kept := column[int64](t, pool, `SELECT count(*) FROM agent_ask_first_tools`); kept[0] != 2 {
		t.Errorf("rolled back, %d marks", kept[0])
	}
}
