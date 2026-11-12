import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import {
	type ChatModel,
	chatModelSchema,
	chooseFailureSentence,
	defaultIsOffered,
	matchingModels,
	unavailableSentence,
	withChoice,
} from "@/features/chat-model/chat-model";
import { chatModelApi } from "@/features/chat-model/chat-model-api";
import { modelFamily } from "@/features/chat-model/model-family";
import { AppError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/chat-model/chat-model.test.ts

/** The answer of GET /api/v1/users/me/chat-model, as the Go route writes it. */
function answer(overrides: Partial<ChatModel> = {}): ChatModel {
	return chatModelSchema.parse({
		selected: null,
		model: "claude-sonnet",
		available: true,
		default: "claude-sonnet",
		models: [
			{ id: "claude-sonnet", label: "claude-sonnet", own: false },
			{ id: "deepseek-chat", label: "deepseek-chat", own: false },
			{ id: "gpt-5-mini", label: "gpt-5-mini", own: false },
			{ id: "own:7", label: "llama-3.3-70b", own: true, connection: "api.example.test" },
		],
		...overrides,
	});
}

test("the server's answer parses, with a null choice and with a named one", () => {
	assert.equal(answer().selected, null);
	assert.equal(answer({ selected: "gpt-5-mini", model: "gpt-5-mini" }).selected, "gpt-5-mini");
});

test("an answer in the old numeric shape is refused, not read as a model", () => {
	assert.equal(chatModelSchema.safeParse({ chat_model_id: 3 }).success, false);
	assert.equal(chatModelSchema.safeParse({ ...answer(), selected: 3 }).success, false);
});

test("a choice is shown at once: the name, or the default for null", () => {
	const picked = withChoice(answer(), "deepseek-chat");
	assert.deepEqual(
		[picked.selected, picked.model, picked.available],
		["deepseek-chat", "deepseek-chat", true]
	);

	const cleared = withChoice(picked, null);
	assert.deepEqual(
		[cleared.selected, cleared.model, cleared.available],
		[null, "claude-sonnet", true]
	);
});

test("going back to a default the proxy does not offer is shown as unavailable", () => {
	const noDefault = answer({
		default: "retired-model",
		selected: "gpt-5-mini",
		model: "gpt-5-mini",
	});
	assert.equal(defaultIsOffered(noDefault), false);
	assert.equal(withChoice(noDefault, null).available, false);
	assert.equal(defaultIsOffered(answer()), true);
});

test("a model that answers has no warning", () => {
	assert.equal(unavailableSentence(answer()), null);
});

test("a removed model is told as a sentence that names it and whose choice it was", () => {
	const chosenGone = answer({ selected: "old-model", model: "old-model", available: false });
	assert.equal(
		unavailableSentence(chosenGone),
		"The model you chose, old-model, is no longer offered. Choose another model."
	);

	const defaultGone = answer({ model: "retired", default: "retired", available: false });
	assert.equal(
		unavailableSentence(defaultGone),
		"You have not chosen a model, and the server's default model, retired, is not offered. Choose a model."
	);
});

test("search matches any part of a name, whatever its case", () => {
	const ids = (search: string) => matchingModels(answer(), search).map((option) => option.id);
	assert.deepEqual(ids("  GPT "), ["gpt-5-mini"]);
	assert.deepEqual(ids(""), ["claude-sonnet", "deepseek-chat", "gpt-5-mini", "own:7"]);
	assert.deepEqual(ids("mistral"), []);
});

test("an own model is found by its label and its endpoint, never only by its number", () => {
	const ids = (search: string) => matchingModels(answer(), search).map((option) => option.id);
	assert.deepEqual(ids("llama"), ["own:7"]);
	assert.deepEqual(ids("example.test"), ["own:7"]);
});

test("an own model that left the menu is named by what it was, not by its number", () => {
	const ownGone = answer({ selected: "own:9", model: "own:9", available: false });
	assert.equal(
		unavailableSentence(ownGone),
		"The model you chose, a model from one of your connections, is no longer offered. Choose another model."
	);
	// Still listed but unavailable, it is named by its label.
	const ownListed = answer({ selected: "own:7", model: "own:7", available: false });
	assert.match(unavailableSentence(ownListed) ?? "", /llama-3\.3-70b/);
});

test("an entry without a label or the own flag is refused, not guessed", () => {
	assert.equal(chatModelSchema.safeParse({ ...answer(), models: [{ id: "x" }] }).success, false);
});

test("a refused choice shows the field's own sentence, not the envelope's summary", () => {
	const refused = new AppError(
		"Validation failed: body.model: The model old-model is not offered. Choose one from the list.",
		422,
		"Unprocessable Entity",
		"VALIDATION_ERROR"
	);
	refused.fields = [
		{
			loc: ["body", "model"],
			msg: "The model old-model is not offered. Choose one from the list.",
		},
	];
	assert.equal(
		chooseFailureSentence(refused),
		"The model old-model is not offered. Choose one from the list."
	);
});

test("any other refusal shows the server's sentence, and nothing shows a blank", () => {
	const down = new AppError(
		"The model service cannot be reached right now. Try again in a moment.",
		503,
		"Service Unavailable",
		"MODEL_PROVIDER_UNAVAILABLE"
	);
	assert.equal(chooseFailureSentence(down), down.message);
	assert.match(chooseFailureSentence(undefined), /could not be changed/);
	assert.match(chooseFailureSentence(new Error("")), /could not be changed/);
});

test("the maker's mark is read from the family word, and unknown names get none", () => {
	assert.equal(modelFamily("claude-sonnet-4-5"), "ANTHROPIC");
	assert.equal(modelFamily("openrouter/anthropic/claude-haiku"), "ANTHROPIC");
	assert.equal(modelFamily("gpt-5-mini"), "OPENAI");
	assert.equal(modelFamily("o3-mini"), "OPENAI");
	assert.equal(modelFamily("DeepSeek-V4-Flash"), "DEEPSEEK");
	assert.equal(modelFamily("gemini-3-pro"), "GOOGLE");
	// "o3" inside another word is not OpenAI's o3.
	assert.equal(modelFamily("turbo3-chat"), null);
	assert.equal(modelFamily("house-model"), null);
});

/** Stand in for the network: answer once, and keep what was asked. */
async function withFetch<T>(
	body: unknown,
	run: (calls: Array<{ url: string; init: RequestInit }>) => Promise<T>
): Promise<T> {
	const calls: Array<{ url: string; init: RequestInit }> = [];
	const real = globalThis.fetch;
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		calls.push({ url: String(input), init: init ?? {} });
		return new Response(JSON.stringify(body), {
			status: 200,
			headers: { "Content-Type": "application/json" },
		});
	}) as typeof fetch;
	try {
		return await run(calls);
	} finally {
		globalThis.fetch = real;
	}
}

test("choosing sends the name, and null (never an absent field) for the default", async () => {
	await withFetch(answer(), async (calls) => {
		await chatModelApi.choose("deepseek-chat");
		await chatModelApi.choose(null);
		assert.equal(calls.length, 2);
		for (const call of calls) {
			// The account comes from the session: the path names no owner.
			assert.match(call.url, /\/api\/v1\/users\/me\/chat-model$/);
			assert.doesNotMatch(call.url, /workspaces/);
			assert.equal(call.init.method, "PUT");
		}
		assert.deepEqual(JSON.parse(String(calls[0].init.body)), { model: "deepseek-chat" });
		// The server refuses a body without "model"; null is how the default is asked for.
		assert.equal(String(calls[1].init.body), '{"model":null}');
	});
});

test("reading asks the chat-model route and returns the parsed answer", async () => {
	await withFetch(answer({ selected: "gpt-5-mini", model: "gpt-5-mini" }), async (calls) => {
		const read = await chatModelApi.read();
		assert.equal(read.model, "gpt-5-mini");
		assert.match(calls[0].url, /\/api\/v1\/users\/me\/chat-model$/);
		assert.equal(calls[0].init.method, "GET");
	});
});

test("nothing in the web app reads or writes the numeric chat model any more", () => {
	// The Go chat reads the name stored by the chat-model route. A page
	// still writing `chat_model_id` through model-roles would look like it
	// changed the model and change nothing.
	const root = process.cwd();
	assert.equal(existsSync(join(root, "components/new-chat/model-selector.tsx")), false);
	for (const file of [
		"contracts/types/model-connections.types.ts",
		"components/settings/model-connections-settings.tsx",
		"components/new-chat/chat-header.tsx",
	]) {
		const source = readFileSync(join(root, file), "utf8");
		assert.doesNotMatch(source, /roles\??\.chat_model_id|chat_model_id:\s*(z\.|Number)/, file);
	}
	// An automation names its model the same way: the hook that listed
	// numeric model ids for it is gone, and nothing it writes is an id.
	assert.equal(existsSync(join(root, "hooks/use-automation-eligible-models.ts")), false);
	for (const file of [
		"lib/automations/builder-schema.ts",
		"contracts/types/automation.types.ts",
		"components/tool-ui/automation/create-automation.tsx",
	]) {
		const source = readFileSync(join(root, file), "utf8");
		assert.doesNotMatch(source, /(chat_model_id|image_gen_model_id|vision_model_id)\s*:/, file);
	}
});
