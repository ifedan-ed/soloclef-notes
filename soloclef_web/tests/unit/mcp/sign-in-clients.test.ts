import assert from "node:assert/strict";
import test from "node:test";

import { SIGN_IN_CLIENTS } from "@/lib/mcp/sign-in-clients";

// Run with: pnpm exec tsx --test tests/unit/mcp/sign-in-clients.test.ts

const SERVER = "https://notes.example.com/mcp";

test("Claude and ChatGPT both connect by signing in", () => {
	assert.deepEqual(
		SIGN_IN_CLIENTS.map((c) => c.id),
		["claude", "chatgpt"]
	);
});

test("each one is told this server's address and to paste the client ID and secret", () => {
	for (const client of SIGN_IN_CLIENTS) {
		const steps = client.steps(SERVER).join("\n");
		assert.ok(steps.includes(SERVER), client.id);
		assert.ok(steps.includes("client ID and secret"), client.id);
		assert.ok(/sign in/i.test(steps), client.id);
	}
});

test("Claude is pointed at its own-client option and ChatGPT at developer mode", () => {
	const [claude, chatgpt] = SIGN_IN_CLIENTS;
	assert.ok(claude.steps(SERVER).some((s) => s.includes("Use your own OAuth client")));
	assert.ok(chatgpt.steps(SERVER).some((s) => s.includes("Developer mode")));
	assert.ok(chatgpt.steps(SERVER).some((s) => s.includes("client_secret_post")));
});
