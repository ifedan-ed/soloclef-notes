import assert from "node:assert/strict";
import test from "node:test";

import {
	hasModelCatalogue,
	listsFromEndpoint,
	previewFailureOf,
	previewRequestFor,
	providersFor,
	withNamedModel,
} from "@/components/settings/model-connections/model-listing";
import {
	endpointModelPreviewRequest,
	modelPreviewRequest,
	modelProviderRead,
} from "@/contracts/types/model-connections.types";
import { AppError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/settings/model-listing.test.ts

test("only a provider with a list has models to refresh", () => {
	assert.equal(hasModelCatalogue({ discovery: "proxy_catalogue" }), true);
	// An OpenAI-compatible endpoint's own GET /models is a list too.
	assert.equal(hasModelCatalogue({ discovery: "endpoint" }), true);
	assert.equal(listsFromEndpoint({ discovery: "endpoint" }), true);
	assert.equal(listsFromEndpoint({ discovery: "proxy_catalogue" }), false);
	assert.equal(hasModelCatalogue({ discovery: "none" }), false);
	// A word the old server used for asking a provider directly is not a list.
	assert.equal(hasModelCatalogue({ discovery: "openai_models" }), false);
	assert.equal(hasModelCatalogue(undefined), false);
});

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

test("listing a provider's models sends the provider and no credential", () => {
	const sent = modelPreviewRequest.parse({
		provider: "openai",
		api_key: "typed-key",
		base_url: "https://example.test/v1",
	});
	assert.deepEqual(sent, { provider: "openai" });
});

const KEY = "fake-key-not-a-real-credential";

const provider = (name: string, discovery: string, anyAccount: boolean) =>
	modelProviderRead.parse({
		provider: name,
		transport: "OPENAI_COMPATIBLE",
		discovery,
		base_url_required: true,
		auth_style: "bearer",
		any_account: anyAccount,
	});

const openai = provider("openai", "proxy_catalogue", false);
const compatible = provider("openai_compatible", "endpoint", true);

test("an account that is not an administrator is offered only what any account may add", () => {
	assert.deepEqual(
		providersFor([openai, compatible], false).map((item) => item.provider),
		["openai_compatible"]
	);
	assert.deepEqual(
		providersFor([openai, compatible], true).map((item) => item.provider),
		["openai", "openai_compatible"]
	);
	// Which providers any account may add is the server's to say.
	assert.equal(
		modelProviderRead.safeParse({ ...compatible, any_account: undefined }).success,
		false
	);
});

test("a catalogue is asked by provider alone; an endpoint with what was typed", () => {
	assert.deepEqual(previewRequestFor(openai, { base_url: "https://x.test", api_key: KEY }), {
		provider: "openai",
	});
	assert.deepEqual(
		previewRequestFor(compatible, {
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
	const bare = previewRequestFor(compatible, { base_url: "https://a.test/v1", api_key: KEY });
	assert.equal(bare && Object.hasOwn(bare, "headers"), false);
});

test("an endpoint cannot be listed before its address and key are typed", () => {
	assert.equal(previewRequestFor(compatible, { base_url: null, api_key: null }), null);
	assert.equal(
		previewRequestFor(compatible, { base_url: "https://a.test/v1", api_key: " " }),
		null
	);
	assert.equal(previewRequestFor(compatible, { base_url: "", api_key: KEY }), null);
	// A provider with no list at all is never asked.
	assert.equal(
		previewRequestFor(provider("ollama_chat", "none", false), {
			base_url: "http://x",
			api_key: null,
		}),
		null
	);
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
