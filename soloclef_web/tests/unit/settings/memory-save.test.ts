import assert from "node:assert/strict";
import test from "node:test";
import { saveMemoryMarkdown } from "@/features/account-settings/lib/memory";

// Run with: pnpm exec tsx --test tests/unit/settings/memory-save.test.ts
//
// The memory accepts any text: the server files what is not under a heading
// under "## Notes". The page must therefore send exactly what was typed and
// show the document the server answers with, never its own guess.

function answering(status: number, body: unknown) {
	const sent: Array<{ url: string; method?: string; body?: string }> = [];
	const real = globalThis.fetch;
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		sent.push({ url: String(input), method: init?.method, body: init?.body as string });
		return new Response(JSON.stringify(body), {
			status,
			headers: { "Content-Type": "application/json" },
		});
	}) as typeof fetch;
	return { sent, restore: () => (globalThis.fetch = real) };
}

test("plain text is sent as typed and the stored document comes back", async () => {
	const stored = "## Notes\nI teach chemistry";
	const server = answering(200, { memory_md: stored, limits: { soft: 18_000, hard: 25_000 } });
	try {
		const saved = await saveMemoryMarkdown({ markdown: "I teach chemistry" });
		assert.equal(saved.markdown, stored);
		assert.deepEqual(saved.limits, { soft: 18_000, hard: 25_000 });
	} finally {
		server.restore();
	}
	assert.equal(server.sent.length, 1);
	assert.equal(server.sent[0].method, "PUT");
	assert.match(server.sent[0].url, /\/api\/v1\/users\/me\/memory$/);
	assert.deepEqual(JSON.parse(server.sent[0].body ?? ""), { memory_md: "I teach chemistry" });
});

test("a save the server refuses says the server's sentence", async () => {
	const sentence =
		"Memory exceeds 25,000 character limit (25,009 chars). Consolidate by merging related items, removing outdated entries, and shortening descriptions.";
	const server = answering(400, { error: { message: sentence }, detail: sentence });
	try {
		await assert.rejects(saveMemoryMarkdown({ markdown: "x" }), { message: sentence });
	} finally {
		server.restore();
	}
});
