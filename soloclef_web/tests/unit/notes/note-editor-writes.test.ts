import assert from "node:assert/strict";
import test from "node:test";
import { citationMarkersAsWritten } from "@/components/editor/utils/citation-markers";
import { editedMarkdownReporter } from "@/components/editor/utils/edited-markdown";

// A note as Save to note keeps it, and the editor's own writing of it.
const LOADED = "Pearson's \\(r = 0.463\\) [citation:12].\n\n- one\n- two\n";
const AS_WRITTEN = "Pearson's $$r = 0.463$$ [citation:12].\n\n* one\n* two\n";

test("opening or clicking into a note reports nothing", () => {
	const report = editedMarkdownReporter(LOADED, AS_WRITTEN);
	// A click, a selection: the same text again.
	assert.equal(report(AS_WRITTEN), null);
	// The editor's empty last paragraph, added on the first change.
	assert.equal(report(`${AS_WRITTEN}\n\u200b\n`), null);
	assert.equal(report(AS_WRITTEN), null);
});

test("an edit is reported once, and undoing it gives the note back as it was written", () => {
	const report = editedMarkdownReporter(LOADED, AS_WRITTEN);
	const edited = AS_WRITTEN.replace("two", "three");
	assert.equal(report(edited), edited);
	assert.equal(report(edited), null);
	assert.equal(report(AS_WRITTEN), LOADED);
	assert.equal(report(AS_WRITTEN), null);
});

test("citation markers are written as the chat writes them", () => {
	assert.equal(
		citationMarkersAsWritten("As shown \\[citation:12] and \\[citation:3, 4]."),
		"As shown [citation:12] and [citation:3, 4]."
	);
	assert.equal(
		citationMarkersAsWritten(
			"See \\[citation:[https://example.org/a](https://example.org/a)] here."
		),
		"See [citation:https://example.org/a] here."
	);
	// An ordinary link and typed code stay as they are.
	assert.equal(
		citationMarkersAsWritten("[a](https://x.org) `\\[citation:1]`"),
		"[a](https://x.org) `\\[citation:1]`"
	);
});
