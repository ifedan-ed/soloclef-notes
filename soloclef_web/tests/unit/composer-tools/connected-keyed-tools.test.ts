import assert from "node:assert/strict";
import test from "node:test";
import {
	COMPOSER_TOOL_ICONS,
	COMPOSER_TOOL_IDS,
	COMPOSER_TOOL_NAME_KEYS,
	offeredComposerTools,
} from "@/features/composer-tools/composer-tools";
import { connectedKeyedTools } from "@/features/composer-tools/connected-keyed-tools";
import { preferredToolRequestFields } from "@/features/composer-tools/preferred-tool-request";

test("no status yet offers no keyed tool", () => {
	assert.equal(connectedKeyedTools(undefined).size, 0);
});

test("Exa and Tavily follow the research box's status", () => {
	const all = connectedKeyedTools({ unavailable: {}, page_reader: "crawler" });
	assert.deepEqual([...all].sort(), ["exa", "tavily"]);

	const onlyTavily = connectedKeyedTools({
		unavailable: { exa: "not_connected" },
		page_reader: "crawler",
	});
	assert.deepEqual([...onlyTavily], ["tavily"]);
});

test("the page reader is not a tool to pick: Browserbase adds nothing to the menu", () => {
	// The chat refuses `preferred_tool: "browserbase"` by name.
	const status = { unavailable: { exa: "not_connected", tavily: "not_connected" } } as const;
	const withBrowserbase = connectedKeyedTools({ ...status, page_reader: "browserbase" });
	assert.equal(withBrowserbase.size, 0);
});

test("the literatures are always offered, the keyed searches only when connected", () => {
	const offered = offeredComposerTools(new Set(["exa"]));
	assert.deepEqual(offered, ["exa", "pubmed", "arxiv", "preprints"]);
	// With no key at all: the three literatures and nothing else.
	assert.deepEqual(offeredComposerTools(new Set()), ["pubmed", "arxiv", "preprints"]);
});

test("the menu can pick exactly what the chat accepts, and no cut platform", () => {
	// soloclef_api/internal/chat `picks`: any other value is refused by name.
	assert.deepEqual([...COMPOSER_TOOL_IDS].sort(), [
		"arxiv",
		"exa",
		"preprints",
		"pubmed",
		"tavily",
	]);
	assert.deepEqual(Object.keys(COMPOSER_TOOL_ICONS).sort(), [...COMPOSER_TOOL_IDS].sort());
	assert.deepEqual(Object.keys(COMPOSER_TOOL_NAME_KEYS).sort(), [...COMPOSER_TOOL_IDS].sort());
});

test("a keyed tool is sent like any other pick", () => {
	assert.deepEqual(preferredToolRequestFields("documents", "tavily"), {
		retrieval_scope: "all",
		preferred_tool: "tavily",
	});
});
