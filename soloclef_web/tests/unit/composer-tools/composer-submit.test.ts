import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

test("the composer snapshots the tool on submit and clears the chip", () => {
	const source = readFileSync(
		new URL("../../../components/assistant-ui/thread.tsx", import.meta.url),
		"utf8"
	);
	assert.ok(source.includes("setSubmittedPreferredTool(preferredTool)"));
	assert.ok(source.includes("setPreferredTool(null)"));
});
