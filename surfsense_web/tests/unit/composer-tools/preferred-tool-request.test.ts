import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { preferredToolRequestFields } from "@/features/composer-tools/preferred-tool-request";
import { scopeForPreferredTool } from "@/features/composer-tools/scope-for-preferred-tool";

test("a forced tool widens a documents-only turn to all", () => {
	assert.equal(scopeForPreferredTool("documents", "reddit"), "all");
});

test("a forced tool keeps a web or all scope as chosen", () => {
	assert.equal(scopeForPreferredTool("web", "youtube"), "web");
	assert.equal(scopeForPreferredTool("all", "amazon"), "all");
});

test("without a forced tool the scope is unchanged", () => {
	assert.equal(scopeForPreferredTool("documents", null), "documents");
});

test("the request names the tool and its widened scope", () => {
	assert.deepEqual(preferredToolRequestFields("documents", "google_maps"), {
		retrieval_scope: "all",
		preferred_tool: "google_maps",
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
	assert.equal(
		source.match(/preferred_tool:/g)?.length,
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
