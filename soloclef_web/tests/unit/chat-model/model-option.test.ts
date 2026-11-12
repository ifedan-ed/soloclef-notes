import assert from "node:assert/strict";
import test from "node:test";
import {
	isOwnModelId,
	matchingOptions,
	modelOptionSchema,
	optionLabel,
	ownMark,
} from "@/features/chat-model/model-option";

// Run with: pnpm exec tsx --test tests/unit/chat-model/model-option.test.ts

const proxy = modelOptionSchema.parse({ id: "gpt-5", label: "gpt-5", own: false });
const own = modelOptionSchema.parse({
	id: "own:4",
	label: "Llama 3.3",
	own: true,
	connection: "api.example.test",
});
const models = [proxy, own];

test("an own model is marked as the account's, with its endpoint when known", () => {
	assert.equal(ownMark(proxy), null);
	assert.equal(ownMark(own), "Your connection · api.example.test");
	const hostless = modelOptionSchema.parse({ id: "own:5", label: "m", own: true });
	assert.equal(ownMark(hostless), "Your connection");
});

test("a listed id shows its label; an unlisted one is never shown as a bare number", () => {
	assert.equal(optionLabel(models, "own:4"), "Llama 3.3");
	assert.equal(optionLabel(models, "gpt-5"), "gpt-5");
	// A withdrawn proxy model is still its own name; a withdrawn own model is described.
	assert.equal(optionLabel(models, "gpt-4"), "gpt-4");
	assert.equal(optionLabel(models, "own:99"), "a model from one of your connections");
});

test("only the server's own-model shape counts as an own id", () => {
	assert.equal(isOwnModelId("own:12"), true);
	assert.equal(isOwnModelId("own:"), false);
	assert.equal(isOwnModelId("owner/model"), false);
	assert.equal(isOwnModelId("gpt-5"), false);
});

test("search reads the id, the label and the endpoint", () => {
	assert.deepEqual(
		matchingOptions(models, "LLAMA").map((option) => option.id),
		["own:4"]
	);
	assert.deepEqual(
		matchingOptions(models, "example.test").map((option) => option.id),
		["own:4"]
	);
	assert.deepEqual(
		matchingOptions(models, " ").map((option) => option.id),
		["gpt-5", "own:4"]
	);
});
