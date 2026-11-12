import assert from "node:assert/strict";
import test from "node:test";

import { createThread, getThreadFull } from "@/lib/chat/thread-persistence";

// Run with: pnpm exec tsx --test tests/unit/chat/thread-persistence.test.ts

/** A thread as the Go route writes it: no owner, no workspace, no sharing level. */
const THREAD = {
	title: "New Chat",
	archived: false,
	id: 5,
	created_at: "2026-11-11T10:00:00Z",
	updated_at: "2026-11-11T10:00:00Z",
};

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

test("creating a thread sends a title and nothing about who owns it", async () => {
	await withFetch(THREAD, async (calls) => {
		const thread = await createThread("Photosynthesis");
		assert.equal(thread.id, 5);
		assert.equal(calls.length, 1);
		assert.match(calls[0].url, /\/api\/v1\/threads$/);
		assert.equal(calls[0].init.method, "POST");
		// Exactly these two fields: the server keys the thread by the
		// session's account, and sharing a thread no longer exists.
		assert.deepEqual(JSON.parse(String(calls[0].init.body)), {
			title: "Photosynthesis",
			archived: false,
		});
	});
});

test("a thread created without a title is called New Chat", async () => {
	await withFetch(THREAD, async (calls) => {
		await createThread();
		assert.deepEqual(JSON.parse(String(calls[0].init.body)), {
			title: "New Chat",
			archived: false,
		});
	});
});

test("reading a thread's record asks for it by id alone", async () => {
	await withFetch(THREAD, async (calls) => {
		const thread = await getThreadFull(5);
		assert.deepEqual(thread, THREAD);
		assert.match(calls[0].url, /\/api\/v1\/threads\/5\/full$/);
		assert.doesNotMatch(calls[0].url, /workspace/);
	});
});
