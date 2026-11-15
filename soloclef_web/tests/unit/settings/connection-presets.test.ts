import assert from "node:assert/strict";
import test from "node:test";

import {
	CONNECTION_PRESETS,
	CUSTOM_PRESET,
	OWN_PROVIDER,
	presetForConnection,
} from "@/components/settings/model-connections/connection-presets";

// Run with: pnpm exec tsx --test tests/unit/settings/connection-presets.test.ts

test("the grid offers only OpenAI-compatible presets at public https addresses, and the custom form", () => {
	const ids = CONNECTION_PRESETS.map((preset) => preset.id);
	assert.deepEqual(ids, ["openai", "anthropic", "gemini", "openrouter", "requesty", "custom"]);
	// The providers Go cannot call are not offered in any spelling.
	for (const gone of ["bedrock", "azure", "vertex", "ollama", "lm_studio"]) {
		assert.equal(
			ids.some((id) => id.includes(gone)),
			false,
			gone
		);
	}
	for (const preset of CONNECTION_PRESETS.filter((item) => item !== CUSTOM_PRESET)) {
		assert.match(preset.baseUrl, /^https:\/\/[^/]+\.[a-z]+\//, preset.id);
	}
	assert.equal(CUSTOM_PRESET.baseUrl, "");
	assert.equal(OWN_PROVIDER, "openai_compatible");
});

test("a saved connection is shown as the preset its address came from", () => {
	assert.equal(presetForConnection(OWN_PROVIDER, "https://api.anthropic.com/v1")?.id, "anthropic");
	// A trailing slash or a capital in the host is the same address.
	assert.equal(
		presetForConnection(OWN_PROVIDER, "https://OpenRouter.ai/api/v1/")?.id,
		"openrouter"
	);
	assert.equal(presetForConnection(OWN_PROVIDER, "https://llm.example.test/v1")?.id, "custom");
	assert.equal(presetForConnection(OWN_PROVIDER, null)?.id, "custom");
	// A connection of a provider the server no longer supports is none of them.
	assert.equal(presetForConnection("bedrock", null), null);
});
