import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { preferredToolRequestFields } from "@/features/composer-tools/preferred-tool-request";
import { scopeForPreferredTool } from "@/features/composer-tools/scope-for-preferred-tool";

test("a forced tool widens a documents-only turn to all", () => {
	assert.equal(scopeForPreferredTool("documents", "pubmed"), "all");
});

test("a forced tool keeps a web or all scope as chosen", () => {
	assert.equal(scopeForPreferredTool("web", "exa"), "web");
	assert.equal(scopeForPreferredTool("all", "arxiv"), "all");
});

test("without a forced tool the scope is unchanged", () => {
	assert.equal(scopeForPreferredTool("documents", null), "documents");
});

test("the request names the tool and its widened scope", () => {
	assert.deepEqual(preferredToolRequestFields("documents", "exa"), {
		retrieval_scope: "all",
		preferred_tool: "exa",
	});
});

test("the request omits preferred_tool when none was picked", () => {
	const fields = preferredToolRequestFields("web", null);
	assert.deepEqual(fields, { retrieval_scope: "web" });
	assert.equal("preferred_tool" in JSON.parse(JSON.stringify(fields)), false);
});

test("only a new message carries the forced tool, and it is consumed once", () => {
	const source = readFileSync(
		new URL("../../../lib/chat/stream-engine/engine.ts", import.meta.url),
		"utf8"
	);
	// The three turn bodies are built apart from the engine; only the new
	// message's names the tool.
	const bodies = readFileSync(
		new URL("../../../lib/chat/stream-engine/turn-request-bodies.ts", import.meta.url),
		"utf8"
	);
	assert.equal(
		bodies.match(/preferred_tool:/g)?.length,
		1,
		"regenerate and resume must not force a tool"
	);
	assert.ok(source.includes("jotaiStore.set(submittedPreferredToolAtom, null)"));
	assert.ok(source.includes("jotaiStore.set(preferredToolAtom, null)"));
});

test("switching chat threads drops a picked tool", () => {
	const source = readFileSync(
		new URL("../../../features/chat-thread/chat-thread-view.tsx", import.meta.url),
		"utf8"
	);
	assert.ok(source.includes("setPreferredTool(null)"));
});

test("each literature is sent under the chat's own name for it", () => {
	// The Go chat accepts exactly these three values (internal/chat, `picks`);
	// a different spelling is refused by name, so they are pinned here.
	for (const tool of ["pubmed", "arxiv", "preprints"] as const) {
		assert.deepEqual(preferredToolRequestFields("documents", tool), {
			retrieval_scope: "all",
			preferred_tool: tool,
		});
	}
});

test("the three literatures are in the menu's list, once each", async () => {
	const { ACADEMIC_TOOL_IDS, COMPOSER_TOOL_IDS, offeredComposerTools } = await import(
		"@/features/composer-tools/composer-tools"
	);
	assert.deepEqual([...ACADEMIC_TOOL_IDS], ["pubmed", "arxiv", "preprints"]);
	for (const tool of ACADEMIC_TOOL_IDS) {
		assert.equal(COMPOSER_TOOL_IDS.filter((id) => id === tool).length, 1, tool);
	}
	// Offered to an account with no keys at all.
	const offered = offeredComposerTools(new Set());
	assert.ok(ACADEMIC_TOOL_IDS.every((tool) => offered.includes(tool)));
});
