import assert from "node:assert/strict";
import test from "node:test";

import {
	instructionsApplied,
	isOverLimit,
	type LintCode,
	lintMessage,
	MAX_INSTRUCTIONS_CHARS,
	savedInstructions,
} from "@/features/chat-instructions/lib/instructions";

// Run with: pnpm exec tsx --test tests/unit/chat-instructions/instructions.test.ts

test("the cap matches the backend's and counts what is saved", () => {
	assert.equal(MAX_INSTRUCTIONS_CHARS, 4000);
	assert.equal(isOverLimit("a".repeat(4000)), false);
	assert.equal(isOverLimit(`  ${"a".repeat(4000)}  `), false);
	assert.equal(isOverLimit("a".repeat(4001)), true);
	assert.equal(savedInstructions("  Be brief.  \n"), "Be brief.");
	assert.equal(savedInstructions("   "), "");
});

test("the chat says answers are shaped only when there are instructions", () => {
	assert.equal(instructionsApplied("Use British English."), true);
	for (const none of [null, undefined, "", "   \n"]) {
		assert.equal(instructionsApplied(none), false);
	}
});

test("every warning says the rule still applies and the part is ignored", () => {
	const codes: LintCode[] = ["citations", "outside_knowledge", "safety", "prompt_injection"];
	for (const code of codes) {
		const message = lintMessage(code);
		assert.ok(message.length > 20, code);
		assert.match(message, /ignored/, code);
	}
});
