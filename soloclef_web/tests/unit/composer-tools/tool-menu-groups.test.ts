import assert from "node:assert/strict";
import test from "node:test";
import {
	groupMenuTools,
	type MenuToolEntry,
	menuGroupKey,
} from "../../../features/composer-tools/tool-menu-groups";

const entry = (name: string, group?: string, connector?: string): MenuToolEntry => ({
	name,
	description: `${name} described`,
	enabled_by_default: true,
	...(group ? { group, connector } : {}),
});

// The list as GET /api/v1/agent/tools answers it for an account with a
// mailbox and two MCP servers: the chat's own tools, then each connection's.
const listed = [
	entry("search_knowledge_base"),
	entry("add_sources"),
	entry("pubmed_search"),
	entry("update_memory"),
	entry("a_tool_added_later"),
	entry("search_emails", "Mail", "mail"),
	entry("read_email", "Mail", "mail"),
	entry("lookup_ticket", "Tickets", "mcp"),
	entry("search", "Wiki", "mcp"),
];

test("the chat's own tools are grouped by name, and one the menu does not name goes under Other", () => {
	const menu = groupMenuTools(listed);
	assert.deepEqual(
		menu.own.map((g) => [g.label, g.tools.map((t) => t.name)]),
		[
			["Your sources", ["search_knowledge_base", "add_sources"]],
			["Research", ["pubmed_search"]],
			["Memory", ["update_memory"]],
		]
	);
	assert.deepEqual(
		menu.other?.tools.map((t) => t.name),
		["a_tool_added_later"]
	);
});

test("each connection is one group, in the server's order, with its kind for the icon", () => {
	const menu = groupMenuTools(listed);
	assert.deepEqual(
		menu.connections.map((g) => [g.label, g.connector, g.tools.map((t) => t.name)]),
		[
			["Mail", "mail", ["search_emails", "read_email"]],
			["Tickets", "mcp", ["lookup_ticket"]],
			["Wiki", "mcp", ["search"]],
		]
	);
	// No connection's tool leaks into the chat's own groups.
	const own = [...menu.own, ...(menu.other ? [menu.other] : [])].flatMap((g) => g.tools);
	assert.ok(own.every((t) => !t.group));
});

test("two connections with one name but different kinds stay apart", () => {
	const menu = groupMenuTools([entry("a", "Notes", "mcp"), entry("b", "Notes", "nextcloud")]);
	assert.deepEqual(menu.connections.map(menuGroupKey), ["mcp:Notes", "nextcloud:Notes"]);
});

test("an account with nothing connected has no connection groups", () => {
	const menu = groupMenuTools([entry("update_memory")]);
	assert.equal(menu.connections.length, 0);
	assert.equal(menu.other, undefined);
});
