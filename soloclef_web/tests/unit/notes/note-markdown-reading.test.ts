import assert from "node:assert/strict";
import test from "node:test";
import { MarkdownPlugin, remarkMdx } from "@platejs/markdown";
import { createSlateEditor } from "platejs";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import { escapeMdxExpressions } from "@/components/editor/utils/escape-mdx";
import { safeDeserializeMarkdown } from "@/components/editor/utils/safe-deserialize";

/** A note read into the editor as its words: every block's text, in order. */
function read(markdown: string): string[] {
	const editor = createSlateEditor({
		plugins: [
			MarkdownPlugin.configure({ options: { remarkPlugins: [remarkGfm, remarkMath, remarkMdx] } }),
		],
	});
	const text = (node: { text?: string; children?: unknown[] }): string =>
		node.text ?? (node.children ?? []).map((child) => text(child as never)).join("");
	return safeDeserializeMarkdown(editor as never, escapeMdxExpressions(markdown)).map((block) =>
		text(block as never)
	);
}

test("a note with a line break tag and an autolink shows everything after them", () => {
	// MDX cannot read <https://…>; Plate's own fallback kept only the text before the <br>.
	const blocks = read(
		"One<br>two\n\nThe next paragraph.\n\nSee <https://example.org/a>.\n\nThe last one."
	);
	assert.deepEqual(blocks, [
		"One\ntwo",
		"The next paragraph.",
		"See https://example.org/a.",
		"The last one.",
	]);
});

test("a list in a quote shows its items", () => {
	const [quote, after] = read(
		"> Key points:\n>\n> - Single dose works\n> - Prednisolone is an alternative\n\nAfter."
	);
	assert.match(quote, /Single dose works/);
	assert.match(quote, /Prednisolone is an alternative/);
	assert.equal(after, "After.");
});

test("a reference link shows its words", () => {
	const [line] = read("See [the review][1] for more.\n\n[1]: https://example.org/review");
	assert.equal(line, "See the review for more.");
});

test("a line break tag in code is kept as typed", () => {
	const code = "```html\n<p>a<br>b</p>\n```\n\nInline `<br>` here.";
	// Read by Plate's MDX reader, and by the plain one (an autolink makes MDX give up).
	for (const before of ["One<br>two", "One<br>two <https://example.org/a>"]) {
		const [line, block, inline] = read(`${before}\n\n${code}`);
		assert.match(line, /^One\ntwo/);
		assert.equal(block, "<p>a<br>b</p>");
		assert.equal(inline, "Inline <br> here.");
	}
});
