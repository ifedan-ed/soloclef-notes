import assert from "node:assert/strict";
import { test } from "node:test";
import {
	isSafeHref,
	parseMarkdownLite,
	plainTextOf,
} from "../../../features/site-announcements/lib/markdown-lite";

test("bold, italic, code and links become their own pieces", () => {
	const [block] = parseMarkdownLite(
		"Now **30 minutes**, *longer* _talks_, `cedar` and [the docs](/docs)."
	);
	assert.equal(block?.kind, "paragraph");
	assert.deepEqual(block?.kind === "paragraph" ? block.inlines : [], [
		{ kind: "text", text: "Now " },
		{ kind: "strong", text: "30 minutes" },
		{ kind: "text", text: ", " },
		{ kind: "em", text: "longer" },
		{ kind: "text", text: " " },
		{ kind: "em", text: "talks" },
		{ kind: "text", text: ", " },
		{ kind: "code", text: "cedar" },
		{ kind: "text", text: " and " },
		{ kind: "link", text: "the docs", href: "/docs" },
		{ kind: "text", text: "." },
	]);
});

test("blank lines split paragraphs, single newlines break lines, dashes make lists", () => {
	const blocks = parseMarkdownLite("First line\nsecond line\n\n- one\n- **two**\n\nLast");
	assert.deepEqual(
		blocks.map((b) => b.kind),
		["paragraph", "list", "paragraph"]
	);
	const first = blocks[0];
	assert.ok(first?.kind === "paragraph" && first.inlines.some((i) => i.kind === "break"));
	const list = blocks[1];
	assert.ok(list?.kind === "list");
	assert.equal(list.items.length, 2);
	assert.deepEqual(list.items[1], [{ kind: "strong", text: "two" }]);
});

test("a link that could run script stays plain text", () => {
	const [block] = parseMarkdownLite("[click](javascript:alert(1))");
	assert.ok(block?.kind === "paragraph");
	assert.ok(block.inlines.every((inline) => inline.kind === "text"));
	assert.equal(isSafeHref("javascript:alert(1)"), false);
	assert.equal(isSafeHref("//evil.example"), false);
	assert.equal(isSafeHref("/\\evil.example"), false);
	assert.equal(isSafeHref("https://notes.example.com"), true);
	assert.equal(isSafeHref("/dashboard/1"), true);
});

test("the plain-text summary drops the markup", () => {
	assert.equal(plainTextOf("**New:** voices\n\n- Marin\n- Cedar"), "New: voices Marin Cedar");
});
