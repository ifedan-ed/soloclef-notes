import assert from "node:assert/strict";
import test from "node:test";
import { pageFrame } from "@/components/layout/providers/page-frame";
import { noteIdFromPath, notePath } from "@/features/notes/lib/note-paths";
import { previewLine } from "@/features/notes/lib/note-text";
import { isFiltered, notesQueryString } from "@/features/notes/lib/notes-filter";

test("a list asks the server for what differs from its defaults", () => {
	assert.equal(notesQueryString({}), "limit=50");
	assert.equal(
		notesQueryString({ query: "  dose   of  ", notebook: 4, pinned: true, sort: "title" }, 50),
		"q=dose+of&notebook_id=4&pinned=true&sort=title&limit=50&offset=50"
	);
	assert.equal(
		notesQueryString({ notebook: "none", origin: "chat", threadId: 9 }),
		"unfiled=true&origin=chat&thread_id=9&limit=50"
	);
	// The default sort is not written, so it is the same cache key as no sort.
	assert.equal(notesQueryString({ sort: "edited" }), notesQueryString({}));
});

test("a blank search is not a filter", () => {
	assert.equal(isFiltered({ query: "   " }), false);
	assert.equal(isFiltered({ sort: "title" }), false);
	assert.equal(isFiltered({ notebook: "none" }), true);
});

test("each note has an address, and only a note's address names one", () => {
	assert.equal(notePath(12), "/dashboard/notes/12");
	assert.equal(noteIdFromPath("/dashboard/notes/12"), 12);
	assert.equal(noteIdFromPath("/dashboard/notes"), null);
	assert.equal(noteIdFromPath("/dashboard/notes/abc"), null);
	assert.equal(noteIdFromPath("/dashboard/notes/0"), null);
	assert.equal(noteIdFromPath("/dashboard/notebooks/12"), null);
});

test("the Notes place fills the page panel and its text can be selected", () => {
	for (const path of ["/dashboard/notes", "/dashboard/notes/", "/dashboard/notes/12"]) {
		const frame = pageFrame(path);
		assert.equal(frame.focusMode, false, path);
		assert.match(frame.contentClassName ?? "", /max-w-none/, path);
		assert.doesNotMatch(frame.contentClassName ?? "", /select-none/, path);
	}
});

test("a preview keeps a note's lines apart", () => {
	assert.equal(
		previewLine("## Plan\n\nOral fluids **first**.\n\n- Recheck at 4 hours\n- Sodium"),
		"Plan · Oral fluids first. · Recheck at 4 hours · Sodium"
	);
	assert.equal(previewLine(""), "");
});
