import assert from "node:assert/strict";
import test from "node:test";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { suggestionSources } from "@/features/notebooks/lib/suggestion-sources";
import type { NotebookSource } from "@/features/notebooks/model/notebook";

// Run with: pnpm exec tsx --test tests/unit/notebooks/suggestion-sources.test.ts

const source = (over: Partial<NotebookSource>): NotebookSource => ({
	id: 1,
	kind: "document",
	target_id: 10,
	title: "a.pdf",
	document_type: "FILE",
	status: "ready",
	enabled: true,
	added_at: "2026-11-15T00:00:00Z",
	...over,
});

test("ideas wait while a ticked source is still being read", () => {
	assert.equal(suggestionSources([source({ status: "processing" })]).settled, false);
	assert.equal(suggestionSources([source({ status: "pending" })]).settled, false);
	// An unticked source being read does not hold them: they are not about it.
	assert.equal(
		suggestionSources([
			source({ id: 1 }),
			source({ id: 2, target_id: 11, status: "processing", enabled: false }),
		]).settled,
		true
	);
	// A failed source is done being read.
	assert.equal(suggestionSources([source({ status: "failed" })]).settled, true);
});

test("a source finishing, ticked or added asks for ideas again; the order does not", () => {
	const reading = suggestionSources([source({ status: "processing" })]).signature;
	const read = suggestionSources([source({ status: "ready" })]).signature;
	assert.notEqual(reading, read);

	const two = [source({ id: 1, target_id: 10 }), source({ id: 2, target_id: 11 })];
	assert.equal(suggestionSources(two).signature, suggestionSources([...two].reverse()).signature);
	assert.notEqual(
		suggestionSources(two).signature,
		suggestionSources([two[0], { ...two[1], enabled: false }]).signature
	);
});

test("the ideas' query key carries the sources, under the notebook's own keys", () => {
	const key = notebookKeys.suggestions(7, "chat", "", "", "document:10:ready");
	assert.deepEqual(key.slice(0, 3), ["notebooks", 7, "suggestions"]);
	assert.notDeepEqual(key, notebookKeys.suggestions(7, "chat", "", "", "document:10:processing"));
});
