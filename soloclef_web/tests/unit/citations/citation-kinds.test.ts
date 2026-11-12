import assert from "node:assert/strict";
import test from "node:test";
import { createStore } from "jotai";
import {
	citationPanelAtom,
	closeCitationPanelAtom,
	openCitationPanelAtom,
} from "@/atoms/citation/citation-panel.atom";
import { parseTextWithCitations } from "@/lib/citations/citation-parser";

// Run with: pnpm exec tsx --test tests/unit/citations/citation-kinds.test.ts

// The right panel's collapsed state persists through window.cookieStore.
(globalThis as { window?: unknown }).window ??= {};

test("chunk and web-address markers still become citations", () => {
	assert.deepEqual(parseTextWithCitations("Fever [citation:42] and [citation:doc-7].", new Map()), [
		"Fever ",
		{ kind: "chunk", chunkId: 42, isDocsChunk: false },
		" and ",
		{ kind: "chunk", chunkId: 7, isDocsChunk: true },
		".",
	]);
	assert.deepEqual(parseTextWithCitations("See [citation:https://example.org/a].", new Map()), [
		"See ",
		{ kind: "url", url: "https://example.org/a" },
		".",
	]);
});

test("a scraper-run marker is plain text now that scraper runs are gone", () => {
	// Old answers and notes may still hold one; it is shown as written rather
	// than as a chip that would open a run nobody can fetch.
	const text = "Prices [citation:run_0f8e2c4a-1b2c-4d5e-8f90-123456789abc] rose.";
	assert.deepEqual(parseTextWithCitations(text, new Map()), [text]);
});

test("the citation panel opens on a chunk and closes back to nothing", () => {
	const store = createStore();
	store.set(openCitationPanelAtom, { chunkId: 42 });
	assert.deepEqual(store.get(citationPanelAtom), { isOpen: true, chunkId: 42 });
	store.set(openCitationPanelAtom, { chunkId: 43 });
	assert.deepEqual(store.get(citationPanelAtom), { isOpen: true, chunkId: 43 });
	store.set(closeCitationPanelAtom);
	assert.deepEqual(store.get(citationPanelAtom), { isOpen: false, chunkId: null });
});
