import assert from "node:assert/strict";
import test from "node:test";
import { groupTools, toolKey, withChoice } from "@/features/agent-permissions/lib/tool-groups";
import type { AgentTool } from "@/lib/apis/agent-permissions-api.service";

const tool = (
	name: string,
	group: string,
	connector_id: number | null = null,
	ask_first = false
): AgentTool => ({
	tool: name,
	group,
	connector_id,
	ask_first,
});

test("the page's sections follow the server's order, a heading where its first tool is", () => {
	const groups = groupTools([
		tool("search_emails", "Mail"),
		tool("web_crawl", "The web"),
		tool("read_email", "Mail"),
		tool("search", "Wiki", 3),
	]);
	assert.deepEqual(
		groups.map((group) => [group.heading, group.tools.map((each) => each.tool)]),
		[
			["Mail", ["search_emails", "read_email"]],
			["The web", ["web_crawl"]],
			["Wiki", ["search"]],
		]
	);
});

test("two apps of one name stay two sections", () => {
	const groups = groupTools([tool("search", "Wiki", 3), tool("search", "Wiki", 4)]);
	assert.equal(groups.length, 2);
});

test("an app's tool is told apart from the chat's own of the same name", () => {
	assert.notEqual(toolKey(tool("search", "Wiki", 3)), toolKey(tool("search", "Your documents")));
	const before = [tool("search", "Your documents"), tool("search", "Wiki", 3)];
	const after = withChoice(before, { tool: "search", connector_id: 3, ask_first: true });
	assert.deepEqual(
		after.map((each) => each.ask_first),
		[false, true]
	);
	// The list it was given is not changed: the cache holds it.
	assert.equal(before[1].ask_first, false);
});
