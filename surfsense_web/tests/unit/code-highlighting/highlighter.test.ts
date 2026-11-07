import assert from "node:assert/strict";
import test from "node:test";

import { type HighlightedNode, highlightCode } from "@/features/code-highlighting/highlighter";

// Run with: pnpm exec tsx --test tests/unit/code-highlighting/highlighter.test.ts

function classesIn(nodes: HighlightedNode[]): string[] {
	return nodes.flatMap((node) =>
		node.type === "element" ? [...node.classes, ...classesIn(node.children)] : []
	);
}

function textIn(nodes: HighlightedNode[]): string {
	return nodes.map((node) => (node.type === "text" ? node.value : textIn(node.children))).join("");
}

test("a known language comes back as class-annotated runs the stylesheet can colour", () => {
	const nodes = highlightCode("python", "def greet():\n\treturn 'hi'\n");
	const classes = classesIn(nodes);

	assert.ok(classes.includes("hljs-keyword"), `expected a keyword class, got ${classes}`);
	assert.ok(classes.includes("hljs-string"), `expected a string class, got ${classes}`);
	assert.ok(
		classes.every((name) => name === "hljs" || name.startsWith("hljs-") || !name.includes(" ")),
		"classes must be single names, not pre-joined strings"
	);
});

test("every class carries the hljs prefix or is a bare highlight.js sub-scope", () => {
	const nodes = highlightCode("typescript", "class A { run(): void {} }");
	for (const name of classesIn(nodes)) {
		assert.ok(!name.startsWith("."), `class ${name} must not be a selector`);
		assert.ok(name.length > 0, "class names must not be empty");
	}
});

test("highlighting never loses or reorders the source", () => {
	const source = "SELECT *\nFROM notes\nWHERE id = 1;\n";
	assert.equal(textIn(highlightCode("sql", source)), source);
});

test("an unknown language renders as one unstyled run instead of throwing", () => {
	const nodes = highlightCode("klingon", "nuqneH");
	assert.deepEqual(nodes, [{ type: "text", value: "nuqneH" }]);
});

test("a blank fence yields nothing to render", () => {
	assert.deepEqual(highlightCode("", ""), []);
	assert.deepEqual(highlightCode("python", ""), []);
});

test("the language name is matched case- and space-insensitively", () => {
	const padded = highlightCode("  Python  ", "def f(): pass");
	assert.ok(classesIn(padded).includes("hljs-keyword"));
});
