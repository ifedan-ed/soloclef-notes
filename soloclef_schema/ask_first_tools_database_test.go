package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// askFirstVersion is 00015, where "always allow" rules became "ask first"
// marks.
const askFirstVersion = 15

// Migration 15 drops every old permission rule (each meant "always allow",
// which no longer says anything) and every MCP server's trusted list, and
// leaves the table as marks: one per account, connection and tool, gone
// with the connection. Rolled back, the table is the rules again, empty:
// no mark becomes an "always allow".
func TestAlwaysAllowRulesBecomeAskFirstMarks(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	const ada = "11111111-1111-4111-8111-111111111111"
	if _, err := schema.DownTo(ctx, pool, versionBefore(t, askFirstVersion), testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", versionBefore(t, askFirstVersion), err)
	}
	exec(t, pool, `INSERT INTO "user" (id, email) VALUES ($1, 'a@example.test')`, ada)
	exec(t, pool, `INSERT INTO new_chat_threads (id, title, user_id) VALUES (1, 'A chat', $1)`, ada)
	exec(t, pool, `INSERT INTO agent_permission_rules (user_id, thread_id, permission, pattern, action) VALUES
		($1, NULL, 'search_emails', '*', 'allow'), ($1, 1, 'web_crawl', 'https://example.org/*', 'deny')`, ada)
	exec(t, pool, `INSERT INTO search_source_connectors (id, user_id, name, connector_type, config) VALUES
		(7, $1, 'Wiki', 'MCP_CONNECTOR', '{"server_origin": "member", "trusted_tools": ["search"], "cached_tools": {"tools": [{"name": "search"}]}}')`, ada)

	if _, err := schema.UpTo(ctx, pool, askFirstVersion, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if left := column[int64](t, pool, `SELECT count(*) FROM agent_ask_first_tools`); left[0] != 0 {
		t.Errorf("%d old rules survived as marks", left[0])
	}
	if kept := column[string](t, pool, `SELECT config::text FROM search_source_connectors WHERE id = 7`); kept[0] != `{"cached_tools": {"tools": [{"name": "search"}]}, "server_origin": "member"}` {
		t.Errorf("the server's config is now %s", kept[0])
	}
	// A mark per account, connection and tool; a chat tool's (no
	// connection) is one mark too.
	exec(t, pool, `INSERT INTO agent_ask_first_tools (user_id, tool) VALUES ($1, 'search_emails')`, ada)
	refused(t, pool, "uq_agent_ask_first_tools_tool", `INSERT INTO agent_ask_first_tools (user_id, tool) VALUES ($1, 'search_emails')`, ada)
	exec(t, pool, `INSERT INTO agent_ask_first_tools (user_id, connector_id, tool) VALUES ($1, 7, 'search')`, ada)
	exec(t, pool, `INSERT INTO agent_ask_first_tools (user_id, connector_id, tool) VALUES ($1, 7, 'search_emails')`, ada)
	exec(t, pool, `DELETE FROM search_source_connectors WHERE id = 7`)
	if left := column[string](t, pool, `SELECT tool FROM agent_ask_first_tools`); len(left) != 1 || left[0] != "search_emails" {
		t.Errorf("after the app was removed the marks are %v", left)
	}

	if _, err := schema.DownTo(ctx, pool, versionBefore(t, askFirstVersion), testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", askFirstVersion, err)
	}
	if left := column[int64](t, pool, `SELECT count(*) FROM agent_permission_rules`); left[0] != 0 {
		t.Errorf("a mark came back as %d rules", left[0])
	}
	exec(t, pool, `INSERT INTO agent_permission_rules (user_id, permission, action) VALUES ($1, 'edit', 'allow')`, ada)
	refused(t, pool, "uq_agent_permission_rules_scope", `INSERT INTO agent_permission_rules (user_id, permission, action) VALUES ($1, 'edit', 'allow')`, ada)
	refused(t, pool, "ck_agent_permission_rules_action", `INSERT INTO agent_permission_rules (user_id, permission, action) VALUES ($1, 'edit', 'maybe')`, ada)
}
