import assert from "node:assert/strict";
import test from "node:test";

import { mcpAddressRule, mcpServerOrigin } from "@/lib/mcp/server-origin";

// Run with: pnpm exec tsx --test tests/unit/mcp/server-origin.test.ts

test("a server a member added is theirs; anything else is a site server", () => {
	assert.equal(mcpServerOrigin({ server_origin: "member", server_config: {} }), "member");
	assert.equal(mcpServerOrigin({ server_origin: "site" }), "site");
	// Exa, Tavily and Browserbase predate the marker: held to the allowed list.
	assert.equal(mcpServerOrigin({ server_config: { url: "https://mcp.exa.ai/mcp" } }), "site");
	assert.equal(mcpServerOrigin({ server_origin: "MEMBER" }), "site");
	assert.equal(mcpServerOrigin(null), "site");
	assert.equal(mcpServerOrigin(undefined), "site");
});

test("the hint says the allowed list only where it applies", () => {
	assert.equal(
		mcpAddressRule(mcpServerOrigin({ server_origin: "member" }) === "site"),
		"a public address"
	);
	assert.equal(mcpAddressRule(mcpServerOrigin({}) === "site"), "on the allowed list");
});
