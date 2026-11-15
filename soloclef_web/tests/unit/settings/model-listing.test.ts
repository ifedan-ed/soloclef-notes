import assert from "node:assert/strict";
import test from "node:test";

import {
	previewFailureOf,
	previewRequestFor,
	withNamedModel,
} from "@/components/settings/model-connections/model-listing";
import { endpointModelPreviewRequest } from "@/contracts/types/model-connections.types";
import { AppError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/settings/model-listing.test.ts

const KEY = "fake-key-not-a-real-credential";

test("a model typed by name is added once, chosen, and marked as typed", () => {
	const one = withNamedModel([], "  llama-test:8b ");
	assert.deepEqual(one, [
		{
			model_id: "llama-test:8b",
			display_name: "llama-test:8b",
			source: "MANUAL",
			enabled: true,
			metadata: {},
		},
	]);
	// The same name again, and a blank one, leave the very same list.
	assert.equal(withNamedModel(one, "llama-test:8b"), one);
	assert.equal(withNamedModel(one, "   "), one);
	assert.equal(withNamedModel(one, "other-test").length, 2);
});

test("an endpoint is listed with what was typed, as the one provider there is", () => {
	assert.deepEqual(
		previewRequestFor({
			base_url: " https://api.example.test/v1 ",
			api_key: ` ${KEY} `,
			headers: { "X-Org": "fake-org" },
		}),
		{
			provider: "openai_compatible",
			base_url: "https://api.example.test/v1",
			api_key: KEY,
			headers: { "X-Org": "fake-org" },
		}
	);
	// No headers typed: none are sent, not an empty object.
	const bare = previewRequestFor({ base_url: "https://a.test/v1", api_key: KEY });
	assert.equal(bare && Object.hasOwn(bare, "headers"), false);
});

test("an endpoint cannot be listed before its address and key are typed", () => {
	assert.equal(previewRequestFor({ base_url: null, api_key: null }), null);
	assert.equal(previewRequestFor({ base_url: "https://a.test/v1", api_key: " " }), null);
	assert.equal(previewRequestFor({ base_url: "", api_key: KEY }), null);
});

test("the endpoint's listing request keeps the address, key and headers", () => {
	const sent = endpointModelPreviewRequest.parse({
		provider: "openai_compatible",
		base_url: "https://api.example.test/v1",
		api_key: KEY,
		headers: { "X-Org": "fake-org" },
	});
	assert.equal(sent.api_key, KEY);
	assert.deepEqual(sent.headers, { "X-Org": "fake-org" });
	assert.equal(
		endpointModelPreviewRequest.safeParse({ provider: "openai_compatible" }).success,
		false
	);
});

test("an endpoint with no list says why and offers typing names; other failures do not", () => {
	const unsupported = new AppError(
		"This endpoint does not list its models (GET /models answered 404). Type the model name; connecting tests it.",
		409,
		"Conflict",
		"MODEL_LIST_UNSUPPORTED"
	);
	assert.deepEqual(previewFailureOf(unsupported), {
		message: unsupported.message,
		typeNames: true,
	});
	for (const [status, code] of [
		[400, "ADDRESS_REFUSED"],
		[400, "KEY_REFUSED"],
		[502, "ENDPOINT_UNREACHABLE"],
	] as const) {
		const failure = previewFailureOf(new AppError(`said ${code}`, status, "", code));
		assert.deepEqual(failure, { message: `said ${code}`, typeNames: false });
	}
	assert.deepEqual(previewFailureOf("not an error"), {
		message: "The models could not be listed.",
		typeNames: false,
	});
});
