import assert from "node:assert/strict";
import test from "node:test";
import { hasEditableInput, isEditableInput } from "@/features/chat-messages/hitl/approval-cards/editable-inputs";

// Run with: pnpm exec tsx --test tests/unit/hitl/editable-inputs.test.ts

test("text, numbers and truth values can be edited as a line of text", () => {
	for (const value of ["https://example.org", 3, true, null, undefined]) {
		assert.equal(isEditableInput(value), true, String(value));
	}
});

test("a list or an object is shown as it is, not edited into a string", () => {
	assert.equal(isEditableInput(["38123456", "https://example.org/page"]), false);
	assert.equal(isEditableInput({ port: "brest" }), false);
});

test("Edit is offered only when some input can be edited", () => {
	assert.equal(hasEditableInput({ sources: ["https://example.org/page"] }), false);
	assert.equal(hasEditableInput({ sources: ["x"], note: "why" }), true);
	assert.equal(hasEditableInput({}), false);
});
