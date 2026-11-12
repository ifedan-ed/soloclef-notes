import assert from "node:assert/strict";
import test from "node:test";

import { modelTag } from "@/components/settings/model-connections/model-utils";
import { connectionRead, modelRead } from "@/contracts/types/model-connections.types";

// Run with: pnpm exec tsx --test tests/unit/settings/model-tag.test.ts

const shared = { scope: "global", provider: "OPENAI_COMPATIBLE" } as const;
const own = { scope: "user", provider: "OPENAI_COMPATIBLE" } as const;
const providerName = (provider: string) =>
	provider === "OPENAI_COMPATIBLE" ? "OpenAI-Compatible" : provider;

test("a shared model carries no provider label", () => {
	assert.equal(modelTag(shared, providerName), null);
});

test("a person's own connection keeps its provider name", () => {
	assert.equal(modelTag(own, providerName), "OpenAI-Compatible");
});

test("a model has no billing tier, so nothing can be tagged Premium", () => {
	const model = modelRead.parse({
		id: 1,
		connection_id: 2,
		model_id: "invented/model-one",
		source: "DISCOVERED",
		enabled: true,
		billing_tier: "premium",
	});
	assert.equal(Object.hasOwn(model, "billing_tier"), false);
});

test("a connection's scope is user or global, and nothing else", () => {
	const connection = { id: 1, provider: "openai", enabled: true, has_api_key: false };
	assert.equal(connectionRead.parse({ ...connection, scope: "user" }).scope, "user");
	assert.equal(connectionRead.parse({ ...connection, scope: "global" }).scope, "global");
	// The values from when a connection could belong to a workspace are refused
	// rather than read as some other scope.
	for (const gone of ["USER", "GLOBAL", "SEARCH_SPACE"]) {
		assert.equal(connectionRead.safeParse({ ...connection, scope: gone }).success, false);
	}
	// A connection belongs to an account: no workspace id survives a read.
	const read = connectionRead.parse({ ...connection, scope: "user", workspace_id: 7 });
	assert.equal(Object.hasOwn(read, "workspace_id"), false);
});
