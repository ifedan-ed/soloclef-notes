import assert from "node:assert/strict";
import { test } from "node:test";
import {
	defaultLabel,
	type ModelChoice,
	modelChoiceSchema,
	modelRolesSchema,
	unavailableSentence,
	withChoice,
} from "@/features/model-choice/model-choice";

const models = [
	{ id: "fast", label: "fast", own: false },
	{ id: "site-chat", label: "site-chat", own: false },
	{ id: "smart", label: "smart", own: false },
	{ id: "own:3", label: "my-gateway-model", own: true, connection: "gw.example.test" },
];

function choice(overrides: Partial<ModelChoice>): ModelChoice {
	return {
		selected: null,
		model: "site-chat",
		available: true,
		default: "site-chat",
		models,
		...overrides,
	};
}

test("the server's answer parses, with a job the server names no model for", () => {
	const none = { selected: null, model: null, available: false, default: null, models: [] };
	assert.deepEqual(modelChoiceSchema.parse(none), none);
	assert.deepEqual(modelRolesSchema.parse({ vision: choice({}), image: none }).image, none);
	// A name is a string: an id-shaped answer from an older server is refused, not shown.
	assert.equal(modelChoiceSchema.safeParse({ ...none, selected: 4 }).success, false);
});

test("a choice is shown at once with the server's own arithmetic", () => {
	assert.deepEqual(withChoice(choice({}), "smart"), choice({ selected: "smart", model: "smart" }));
	// Back to the default.
	assert.deepEqual(
		withChoice(choice({ selected: "smart", model: "smart" }), null),
		choice({ selected: null, model: "site-chat" })
	);
	// A default the proxy does not list is shown as unavailable.
	assert.equal(withChoice(choice({ default: "retired" }), null).available, false);
	// No default at all: the job has no model, which is not "available".
	const none = withChoice(choice({ default: null, selected: "smart", model: "smart" }), null);
	assert.equal(none.model, null);
	assert.equal(none.available, false);
});

test("the default option says what the default is", () => {
	assert.equal(defaultLabel(choice({})), "Server default (site-chat)");
	assert.equal(defaultLabel(choice({ default: null })), "Server default (none set)");
	// Quick tasks and Studio follow the account's chat model, and say so.
	assert.equal(defaultLabel(choice({ default: "smart" }), "chat"), "Same as chat model (smart)");
	// A chat model of the account's own connection is named by its label.
	assert.equal(
		defaultLabel(choice({ default: "own:3" }), "chat"),
		"Same as chat model (my-gateway-model)"
	);
});

test("an own model the job runs on parses, and is named by its label when it fails", () => {
	const own = choice({ selected: "own:3", model: "own:3", available: false });
	assert.deepEqual(modelChoiceSchema.parse(own), own);
	assert.match(
		unavailableSentence(own) ?? "",
		/The model you chose, my-gateway-model, is no longer/
	);
});

test("only a named model that is not offered is a warning", () => {
	assert.equal(unavailableSentence(choice({})), null);
	assert.match(
		unavailableSentence(choice({ selected: "retired", model: "retired", available: false })) ?? "",
		/The model you chose, retired, is no longer offered/
	);
	assert.match(
		unavailableSentence(choice({ model: "retired", default: "retired", available: false })) ?? "",
		/The default model, retired, is not offered/
	);
	// Nothing chosen and nothing named by the server: the job is simply off.
	assert.equal(unavailableSentence(choice({ model: null, default: null, available: false })), null);
});
