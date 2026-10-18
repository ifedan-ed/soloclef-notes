import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { draftToRestore } from "@/features/composer-tools/failed-send-draft";

test("a failed send puts its text and tool back in an empty composer", () => {
	assert.deepEqual(
		draftToRestore({ isEmpty: true, tool: null }, { text: "summarise HFNC", tool: "youtube" }),
		{ text: "summarise HFNC", tool: "youtube" }
	);
});

test("text typed since the failure is never overwritten; a newer tool pick wins", () => {
	assert.deepEqual(
		draftToRestore({ isEmpty: false, tool: "reddit" }, { text: "old", tool: "youtube" }),
		{ text: null, tool: "reddit" }
	);
	assert.deepEqual(
		draftToRestore({ isEmpty: false, tool: null }, { text: "old", tool: "youtube" }),
		{ text: null, tool: "youtube" }
	);
});

test("the engine hands the draft back on every send that never reached the server", () => {
	const engine = readFileSync(
		new URL("../../../lib/chat/stream-engine/engine.ts", import.meta.url),
		"utf8"
	);
	const start = engine.indexOf("export async function startNewChat");
	const end = engine.indexOf("export async function resumeChat");
	const body = engine.slice(start, end);
	// Filesystem selection, local-folder guard, thread creation, pre-accept failure.
	assert.equal((body.match(/restoreDraft\(\);/g) ?? []).length, 4);
	const thread = readFileSync(
		new URL("../../../components/assistant-ui/thread.tsx", import.meta.url),
		"utf8"
	);
	assert.ok(thread.includes("draftToRestore("));
});
