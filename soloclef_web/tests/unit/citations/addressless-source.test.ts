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
	numberCitationNodes,
} from "@/components/editor/utils/citation-nodes";
import { mathReading, readMarkdown, writeMarkdown } from "@/components/editor/utils/read-markdown";
import { numberMarkers } from "@/features/markdown-copy/numbered-markdown";
import { addresslessSourceName, namedSources } from "@/features/markdown-copy/source-namer";
import { noteFromAnswer } from "@/features/notebooks/lib/note-from-answer";
import { decodeSourceMarker } from "@/lib/citations/addressless-source";
import { numberCitations } from "@/lib/citations/citation-numbering";
import {
	parseTextWithCitations,
	preprocessCitationMarkdown,
} from "@/lib/citations/citation-parser";

// Run with: pnpm exec tsx --test tests/unit/citations/addressless-source.test.ts
//
// The markers soloclef_assistant/cite writes for an email and a connected
// app's result: base64url (no padding) of the chip's label.

/** The server's encoding (cite.MailSource / cite.AppSource). */
function marker(kind: "mail" | "app", label: string): string {
	return `src_${kind}_${Buffer.from(label, "utf8").toString("base64url")}`;
}

const MAIL = marker("mail", "Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03");
const APP = marker("app", "GitHub · search_issues");

test("an email and an app result parse to labelled chips", () => {
	assert.deepEqual(
		parseTextWithCitations(`Due Friday [citation:${MAIL}] per [citation:${APP}].`, new Map()),
		[
			"Due Friday ",
			{
				kind: "source",
				source: "mail",
				label: "Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03",
				ref: MAIL,
			},
			" per ",
			{ kind: "source", source: "app", label: "GitHub · search_issues", ref: APP },
			".",
		]
	);
});

test("the Go side's cut label (200 bytes and an ellipsis) still decodes", () => {
	const cut = `${"é".repeat(100)}…`;
	assert.equal(decodeSourceMarker(marker("mail", cut))?.label, cut);
	assert.equal(decodeSourceMarker(marker("mail", `${"é".repeat(102)}`)), null);
});

test("a marker that does not decode is dropped, never shown raw", () => {
	for (const bad of [
		"src_mail_A",
		`src_app_${Buffer.from([0xff, 0xfe]).toString("base64url")}`,
		`src_mail_${Buffer.from([0xc0, 0x80]).toString("base64url")}`,
	]) {
		assert.deepEqual(parseTextWithCitations(`x [citation:${bad}] y`, new Map()), ["x ", " y"], bad);
		assert.deepEqual(parseTextWithCitations(`[citation:${bad}]`, new Map()), [], bad);
	}
});

test("markdown preprocessing keeps the marker away from emphasis and back", () => {
	// "-_" then "_" inside base64url is the shape Markdown reads as emphasis.
	const ref = marker("mail", "a>?~");
	const { content, urlMap } = preprocessCitationMarkdown(
		`See [citation:${ref}] and \`[citation:${ref}]\`.`
	);
	assert.match(content, /^See \[citation:srccite0\] and `\[citation:src_mail_/);
	assert.deepEqual(parseTextWithCitations("[citation:srccite0]", urlMap), [
		{ kind: "source", source: "mail", label: "a>?~", ref },
	]);
});

test("addressless sources take numbers in the answer's one sequence", () => {
	const text = `A [citation:12] B [citation:${MAIL}] C [citation:https://x.org] D [citation:${MAIL}] E [citation:${APP}]`;
	const numbering = numberCitations([text]);
	assert.deepEqual(numbering.order, [
		"chunk:12",
		`source:${MAIL}`,
		"url:https://x.org",
		`source:${APP}`,
	]);
	assert.equal(numberMarkers(text, numbering), "A [1] B [2] C [3] D [2] E [4]");
});

test("a copy's Sources list names an email and an app by their labels", async () => {
	assert.deepEqual(addresslessSourceName(`source:${MAIL}`), {
		name: "Email: Ann Smith <ann@example.org> · Invoice 4411 · 2026-11-03",
	});
	assert.deepEqual(addresslessSourceName(`source:${APP}`), {
		name: "Connected app: GitHub · search_issues",
	});
	assert.equal(addresslessSourceName("url:https://x.org"), null);
	const sources = await namedSources(numberCitations([`x [citation:${APP}]`]), async (keys) => {
		const names = new Map();
		for (const key of keys) {
			const name = addresslessSourceName(key);
			if (name) names.set(key, name);
		}
		return names;
	});
	assert.deepEqual(sources, [{ number: 1, name: "Connected app: GitHub · search_issues" }]);
});

test("a note made from an answer drops the markers like any other", () => {
	assert.equal(noteFromAnswer(`It is due Friday [citation:${MAIL}].`).content, "It is due Friday.");
});

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

type Node = { type?: string; children?: Node[] } & Record<string, unknown>;
function chips(nodes: Node[]): CitationElementNode[] {
	return nodes.flatMap((node) =>
		node.type === CITATION_TYPE
			? [node as unknown as CitationElementNode]
			: chips(node.children ?? [])
	);
}

test("a saved note draws the chips and writes the markers back exactly", () => {
	const note = `The invoice is due Friday [citation:${MAIL}], and the issue is open [citation:${APP}].\n`;
	const editor = noteEditor();
	const value = readMarkdown(editor as never, note, { chatMath: true, citations: true }) as Value;
	const found = chips(value as unknown as Node[]);
	assert.deepEqual(
		found.map((chip) => [chip.kind, chip.sourceRef]),
		[
			["source", MAIL],
			["source", APP],
		]
	);
	assert.deepEqual(numberCitationNodes(value).order, [`source:${MAIL}`, `source:${APP}`]);
	assert.equal(writeMarkdown(editor as never, value).trim(), note.trim());
});
