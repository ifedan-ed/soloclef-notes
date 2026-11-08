import assert from "node:assert/strict";
import test from "node:test";
import { MarkdownPlugin, remarkMdx } from "@platejs/markdown";
import { createSlateEditor, createSlatePlugin, type Value } from "platejs";
import remarkGfm from "remark-gfm";
import {
	CITATION_NODE,
	CITATION_TYPE,
	type CitationElementNode,
	citationMarkdownRules,
} from "@/components/editor/utils/citation-nodes";
import { editedMarkdownReporter } from "@/components/editor/utils/edited-markdown";
import { mathReading, readMarkdown, writeMarkdown } from "@/components/editor/utils/read-markdown";

// The notes' editor: maths and citations read as the chat reads them.
function noteEditor() {
	return createSlateEditor({
		plugins: [
			createSlatePlugin({ key: CITATION_TYPE, node: CITATION_NODE }),
			MarkdownPlugin.configure({
				options: {
					remarkPlugins: [remarkGfm, mathReading(true), remarkMdx],
					rules: citationMarkdownRules,
				},
			}),
		],
	});
}

const READING = { chatMath: true, citations: true };

type Node = { type?: string; text?: string; children?: Node[] } & Record<string, unknown>;

function chips(nodes: Node[]): CitationElementNode[] {
	return nodes.flatMap((node) =>
		node.type === CITATION_TYPE
			? [node as unknown as CitationElementNode]
			: chips((node.children ?? []) as Node[])
	);
}

function words(nodes: Node[]): string {
	return nodes.map((node) => node.text ?? words((node.children ?? []) as Node[])).join("");
}

// As the chat writes them: a chunk, several ids in one marker, a web page, a document.
const NOTE = [
	"Bronchiolitis is managed supportively [citation:1204].",
	"",
	"Oxygen below 90% [citation:1204, doc-88] and see [citation: https://www.nejm.org/doi/full/10.1056/x ].",
	"",
	"In code a marker is typed text: `[citation:5]`.",
	"",
	"| Finding | Source |",
	"|---|---|",
	"| Wheeze | [citation:7] |",
	"",
	"Ends with one [citation:9]",
	"",
].join("\n");

test("a note's citation markers are read as the chat's chips, not as text", () => {
	const editor = noteEditor();
	const value = readMarkdown(editor as never, NOTE, READING) as Node[];
	assert.deepEqual(
		chips(value).map(({ kind, chunkId, url }) => ({ kind, chunkId, url })),
		[
			{ kind: "chunk", chunkId: 1204, url: undefined },
			{ kind: "chunk", chunkId: 1204, url: undefined },
			{ kind: "doc", chunkId: 88, url: undefined },
			{ kind: "url", chunkId: undefined, url: "https://www.nejm.org/doi/full/10.1056/x" },
			{ kind: "chunk", chunkId: 7, url: undefined },
			{ kind: "chunk", chunkId: 9, url: undefined },
		]
	);
	const text = words(value);
	assert.doesNotMatch(text.replace("`[citation:5]`", "").replace("[citation:5]", ""), /citation/);
	// Code keeps the marker as typed.
	assert.match(text, /\[citation:5\]/);
});

test("written back, each chip is its marker as the chat writes it", () => {
	const editor = noteEditor();
	const value = readMarkdown(editor as never, NOTE, READING) as Value;
	const written = writeMarkdown(editor as never, value);
	assert.match(written, /supportively \[citation:1204\]\./);
	assert.match(written, /below 90% \[citation:1204\]\[citation:doc-88\] and/);
	assert.match(written, /see \[citation:https:\/\/www\.nejm\.org\/doi\/full\/10\.1056\/x\]\./);
	assert.match(written, /`\[citation:5\]`/);
	assert.match(written, /\| Wheeze +\| \[citation:7\] +\|/);
	assert.match(written, /Ends with one \[citation:9\]\n/);
	assert.doesNotMatch(written, /\\\[citation/);
	// The empty text the editor keeps beside a chip is not written as a zero-width space.
	assert.doesNotMatch(written, /\u200b/);
	// Read again, the same chips.
	const again = readMarkdown(editor as never, written, READING) as Node[];
	assert.equal(chips(again).length, 6);
});

test("a note with chips, only opened, reports nothing", () => {
	const editor = noteEditor();
	const value = readMarkdown(editor as never, NOTE, READING) as Value;
	const report = editedMarkdownReporter(NOTE, writeMarkdown(editor as never, value));
	assert.equal(report(writeMarkdown(editor as never, value)), null);
});
