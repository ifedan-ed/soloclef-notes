import assert from "node:assert/strict";
import test from "node:test";
import { offeredComposerTools } from "@/features/composer-tools/composer-tools";
import { connectedKeyedTools } from "@/features/composer-tools/connected-keyed-tools";
import { preferredToolRequestFields } from "@/features/composer-tools/preferred-tool-request";

test("no status yet offers no keyed tool", () => {
	assert.equal(connectedKeyedTools(undefined).size, 0);
});

test("Exa and Tavily follow the research box's status, Browserbase its page reader", () => {
	const all = connectedKeyedTools({ unavailable: {}, page_reader: "browserbase" });
	assert.deepEqual([...all].sort(), ["browserbase", "exa", "tavily"]);

	const onlyTavily = connectedKeyedTools({
		unavailable: { exa: "not_connected" },
		page_reader: "crawler",
	});
	assert.deepEqual([...onlyTavily], ["tavily"]);
});

test("the platforms are always offered, the keyed tools only when connected", () => {
	const offered = offeredComposerTools(new Set(["exa"]));
	assert.ok(offered.includes("exa"));
	assert.ok(!offered.includes("tavily"));
	assert.ok(!offered.includes("browserbase"));
	assert.equal(offeredComposerTools(new Set()).length, 10);
});

test("a keyed tool is sent like any other pick", () => {
	assert.deepEqual(preferredToolRequestFields("documents", "browserbase"), {
		retrieval_scope: "all",
		preferred_tool: "browserbase",
	});
});
