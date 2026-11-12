import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { reindexDocument, reindexFailure, reindexPath } from "@/features/documents/reindex/reindex";
import { AppError, NetworkError } from "@/lib/error";

// Run with: pnpm exec tsx --test tests/unit/documents/reindex.test.ts

test("a re-index names a parser only when one was picked", () => {
	assert.equal(reindexPath(42), "/api/v1/documents/42/reindex");
	assert.equal(reindexPath(42, "datalab"), "/api/v1/documents/42/reindex?parser=datalab");
});

test("the request is a POST to the document's reindex route", async () => {
	const calls: Array<{ url: string; method?: string }> = [];
	const real = globalThis.fetch;
	globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
		calls.push({ url: String(input), method: init?.method });
		return new Response(
			JSON.stringify({ message: "Document queued for indexing", document_id: 42 }),
			{ status: 200, headers: { "Content-Type": "application/json" } }
		);
	}) as typeof fetch;
	try {
		const kept = await reindexDocument(42);
		assert.equal(kept.document_id, 42);
		await reindexDocument(42, "llamaparse");
	} finally {
		globalThis.fetch = real;
	}
	assert.match(calls[0].url, /\/api\/v1\/documents\/42\/reindex$/);
	assert.match(calls[1].url, /\/api\/v1\/documents\/42\/reindex\?parser=llamaparse$/);
	assert.deepEqual(
		calls.map((call) => call.method),
		["POST", "POST"]
	);
});

test("409 is a document already on its way, in the server's own words", () => {
	// One status, three reasons: only the server's sentence tells them apart.
	for (const sentence of [
		"This document is already being indexed.",
		"This document is being deleted.",
		"This document has no uploaded file and no text, so there is nothing to index.",
	]) {
		assert.deepEqual(reindexFailure(new AppError(sentence, 409, "Conflict")), {
			kind: "busy",
			sentence,
		});
	}
});

test("503 is the queue being down, 400 a parser that does not apply", () => {
	const down = new AppError(
		"The document could not be queued for indexing right now. Please try again.",
		503,
		"Service Unavailable"
	);
	assert.equal(reindexFailure(down).kind, "unavailable");
	assert.equal(reindexFailure(down).sentence, down.message);

	const noFile = new AppError(
		"This document has no uploaded file, so a parser does not apply to it. Re-index it without naming one.",
		400,
		"Bad Request"
	);
	assert.equal(reindexFailure(noFile).kind, "refused");
});

test("anything else is a plain failure, and a missing sentence stays missing", () => {
	assert.deepEqual(reindexFailure(new AppError("", 500)), { kind: "failed", sentence: null });
	assert.deepEqual(reindexFailure(undefined), { kind: "failed", sentence: null });
	const offline = reindexFailure(new NetworkError("Unable to connect to the server."));
	assert.equal(offline.kind, "failed");
	assert.equal(offline.sentence, "Unable to connect to the server.");
});

test("the action sits in the source's own menu and is off while a job is running", () => {
	const source = readFileSync(
		join(process.cwd(), "features/notebooks/ui/sources/source-row.tsx"),
		"utf8"
	);
	assert.match(
		source,
		/<DropdownMenuItem disabled=\{indexing\} onSelect=\{\(\) => setReindex\("open"\)\}>/
	);
	assert.match(source, /t\("reindex_source"\)/);
});
