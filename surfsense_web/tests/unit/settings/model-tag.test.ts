import assert from "node:assert/strict";
import test from "node:test";

import { isPremiumModel, modelTag } from "@/components/settings/model-connections/model-utils";

// Run with: pnpm exec tsx --test tests/unit/settings/model-tag.test.ts

const shared = { scope: "GLOBAL", provider: "OPENAI_COMPATIBLE" };
const own = { scope: "USER", provider: "OPENAI_COMPATIBLE" };
const providerName = (provider: string) =>
	provider === "OPENAI_COMPATIBLE" ? "OpenAI-Compatible" : provider;

test("a shared standard model carries no provider label", () => {
	assert.equal(modelTag({ billing_tier: "standard" }, shared, providerName), null);
	assert.equal(modelTag({ billing_tier: null }, shared, providerName), null);
	assert.equal(modelTag({}, shared, providerName), null);
});

test("a premium model reads Premium, shared or not", () => {
	assert.equal(modelTag({ billing_tier: "premium" }, shared, providerName), "Premium");
	assert.equal(modelTag({ billing_tier: "PREMIUM" }, own, providerName), "Premium");
});

test("a person's own connection keeps its provider name", () => {
	assert.equal(modelTag({ billing_tier: "standard" }, own, providerName), "OpenAI-Compatible");
});

test("only the premium tier is premium", () => {
	assert.equal(isPremiumModel({ billing_tier: "premium" }), true);
	assert.equal(isPremiumModel({ billing_tier: "standard" }), false);
	assert.equal(isPremiumModel({ billing_tier: "free" }), false);
	assert.equal(isPremiumModel({}), false);
});
