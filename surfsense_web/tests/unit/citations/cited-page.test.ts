import assert from "node:assert/strict";
import test from "node:test";
import { createStore } from "jotai";
import {
	closeDocumentViewerAtom,
	documentViewerAtom,
	openDocumentViewerAtom,
} from "@/atoms/documents/document-viewer.atom";
import {
	citedOriginalTarget,
	openOriginalLabel,
} from "@/components/citation-panel/cited-original-target";

// The right panel's collapsed state persists through window.cookieStore.
(globalThis as { window?: unknown }).window ??= {};

const DOCUMENT = {
	id: 7,
	workspace_id: 3,
	title: "Kawasaki review.pdf",
	chunks: [
		{ id: 41, content: "Fever for five days.", page_start: 11 },
		{ id: 42, content: "IVIG within ten days of onset.", page_start: 12 },
		{ id: 43, content: "No page known.", page_start: null },
	],
};

test("Open goes to the cited chunk's page and marks its text", () => {
	assert.deepEqual(citedOriginalTarget(DOCUMENT, 42), {
		documentId: 7,
		workspaceId: 3,
		title: "Kawasaki review.pdf",
		initialPage: 12,
		highlightText: "IVIG within ten days of onset.",
	});
});

test("a chunk without a page opens the document at its start", () => {
	const target = citedOriginalTarget(DOCUMENT, 43);
	assert.equal(target.initialPage, null);
	assert.equal(target.highlightText, null);
	assert.equal(citedOriginalTarget(DOCUMENT, 999).initialPage, null);
});

test("the button names the page when it is known", () => {
	assert.equal(openOriginalLabel(12), "Open p. 12");
	assert.equal(openOriginalLabel(null), "Open");
	assert.equal(openOriginalLabel(undefined), "Open");
});

test("the document viewer keeps the page and text it was opened with", () => {
	const store = createStore();
	store.set(openDocumentViewerAtom, citedOriginalTarget(DOCUMENT, 42));

	const state = store.get(documentViewerAtom);
	assert.equal(state.isOpen, true);
	assert.equal(state.documentId, 7);
	assert.equal(state.initialPage, 12);
	assert.equal(state.highlightText, "IVIG within ten days of onset.");
});

test("opening without a page, as other callers do, resets any earlier page", () => {
	const store = createStore();
	store.set(openDocumentViewerAtom, citedOriginalTarget(DOCUMENT, 42));
	store.set(openDocumentViewerAtom, { documentId: 8, workspaceId: 3, title: "Notes" });

	const state = store.get(documentViewerAtom);
	assert.equal(state.documentId, 8);
	assert.equal(state.initialPage, null);
	assert.equal(state.highlightText, null);
});

test("a nonsense page is dropped, and closing clears the page", () => {
	const store = createStore();
	store.set(openDocumentViewerAtom, { documentId: 7, workspaceId: 3, title: "x", initialPage: 0 });
	assert.equal(store.get(documentViewerAtom).initialPage, null);

	store.set(openDocumentViewerAtom, { documentId: 7, workspaceId: 3, title: "x", initialPage: 4 });
	store.set(closeDocumentViewerAtom);
	assert.equal(store.get(documentViewerAtom).initialPage, null);
	assert.equal(store.get(documentViewerAtom).isOpen, false);
});
