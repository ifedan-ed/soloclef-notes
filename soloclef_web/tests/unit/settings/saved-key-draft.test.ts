import assert from "node:assert/strict";
import test from "node:test";

import {
	connectionUpdateFor,
	hasConnectionChanges,
	savedKeyIntent,
} from "@/components/settings/model-connections/saved-key-draft";
import {
	connectionRead,
	connectionTestUpdateRequest,
} from "@/contracts/types/model-connections.types";

// Run with: pnpm exec tsx --test tests/unit/settings/saved-key-draft.test.ts
//
// The server never sends a saved API key back. These hold the editor to what
// follows from that: an empty field keeps the saved key by sending nothing,
// and `api_key` is sent only for a key that was typed or a removal that was
// asked for. Every key here is invented.

const TYPED = "fake-key-not-a-real-credential-typed";

test("an empty field keeps the saved key", () => {
	assert.deepEqual(savedKeyIntent("", false), { kind: "keep" });
	assert.deepEqual(savedKeyIntent("   ", false), { kind: "keep" });
});

test("a typed key replaces the saved one, trimmed", () => {
	assert.deepEqual(savedKeyIntent(`  ${TYPED} `, false), { kind: "replace", apiKey: TYPED });
});

test("removal is its own request, and a typed key overrides it", () => {
	assert.deepEqual(savedKeyIntent("", true), { kind: "remove" });
	assert.deepEqual(savedKeyIntent(TYPED, true), { kind: "replace", apiKey: TYPED });
});

test("keeping the key sends no api_key property at all", () => {
	const data = connectionUpdateFor("https://models.example/v1", { kind: "keep" });
	// Absent, not null and not "": null would remove the saved key.
	assert.equal(Object.hasOwn(data, "api_key"), false);
	assert.deepEqual(data, { base_url: "https://models.example/v1" });
});

test("replacing sends the typed key and removing sends null", () => {
	assert.deepEqual(connectionUpdateFor("", { kind: "replace", apiKey: TYPED }), {
		base_url: null,
		api_key: TYPED,
	});
	assert.deepEqual(connectionUpdateFor("", { kind: "remove" }), {
		base_url: null,
		api_key: null,
	});
});

test("opening and closing the editor without typing is not a change", () => {
	// The old editor compared the field with the key it had been sent. There is
	// no key to compare with now, and an untouched field must not look edited.
	assert.equal(
		hasConnectionChanges("https://models.example/v1", "https://models.example/v1", {
			kind: "keep",
		}),
		false
	);
	assert.equal(hasConnectionChanges(null, "", { kind: "keep" }), false);
});

test("a new base URL, a typed key and a removal are each a change", () => {
	assert.equal(hasConnectionChanges(null, "https://models.example/v1", { kind: "keep" }), true);
	assert.equal(hasConnectionChanges(null, "", { kind: "replace", apiKey: TYPED }), true);
	assert.equal(hasConnectionChanges(null, "", { kind: "remove" }), true);
});

test("the test request leaves api_key out when the saved key is to be used", () => {
	const kept = connectionTestUpdateRequest.parse({
		...connectionUpdateFor("https://models.example/v1", { kind: "keep" }),
		model_id: "invented/model-one",
	});
	// The server reads an absent api_key as "test with the saved key".
	assert.equal(Object.hasOwn(kept, "api_key"), false);

	const removed = connectionTestUpdateRequest.parse({
		...connectionUpdateFor("", { kind: "remove" }),
		model_id: "invented/model-one",
	});
	assert.equal(removed.api_key, null);
});

test("a connection read from the server holds no key, even if one were sent", () => {
	const connection = connectionRead.parse({
		id: 1,
		provider: "openai",
		scope: "user",
		enabled: true,
		has_api_key: true,
		api_key: TYPED,
	});
	assert.equal(Object.hasOwn(connection, "api_key"), false);
	assert.equal(connection.has_api_key, true);
});
