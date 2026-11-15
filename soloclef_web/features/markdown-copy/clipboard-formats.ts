// Rendered Markdown in the three forms a copy or download needs:
//
//   html      for text/html: what a word processor, mail or a doc editor
//             pastes, with bold, lists, tables, headings and code kept;
//   plain     for text/plain: the same words with no Markdown marks, for
//             a plain field, a terminal or a chat box;
//   markdown  for "Download as Markdown": still Markdown, with numbered
//             citations instead of "[citation:812]".
//
// Each ends with a short "Sources" list naming what the numbers point at.
//
// Maths is written as readable text (r² ≤ α/2, lib/markdown/math-as-text.ts)
// in all three of html and plain: KaTeX's HTML needs its stylesheet and its
// fonts, which the place it is pasted into does not have, and pasted it
// reads as each symbol twice.
//
// Pure: no React and no DOM (one Markdown parse, two serialisers), so it runs
// in the unit tests and costs one parse per copy.

import type { Element, ElementContent, Root as HastRoot, Properties } from "hast";
import { toHtml } from "hast-util-to-html";
import type { Nodes as MdastNodes, Root as MdastRoot, Parent, RootContent } from "mdast";
import { toHast } from "mdast-util-to-hast";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { type CitationNumbering, numberCitations } from "@/lib/citations/citation-numbering";
import { normalizeAnswerMarkdown } from "@/lib/markdown/answer-markdown";
import { texAsText } from "@/lib/markdown/math-as-text";
import type { CitedSource } from "./cited-source";
import { escapedNumber, numberMarkers, plainNumber } from "./numbered-markdown";

/** What a copy puts on the clipboard, and a download saves. */
export interface ClipboardFormats {
	html: string;
	plain: string;
	markdown: string;
}

/** The heading of the list of sources a copy ends with. */
export const SOURCES_HEADING = "Sources";

/**
 * The numbering a copy of these texts uses: the chips' own rule, over the
 * saved text, so "[1]" in a paste is the chip a reader saw as 1.
 */
export function numberingForCopy(texts: readonly string[]): CitationNumbering {
	return numberCitations(texts, { labels: false });
}

// The parser is built once: building it is most of the cost of a small copy.
const parser = unified()
	.use(remarkParse)
	.use(remarkGfm)
	.use(remarkMath, { singleDollarTextMath: false });

/**
 * Render one answer (its text parts, in order) for the clipboard.
 *
 * `sources` lists the numbered sources in number order; it is what the
 * caller could name (a document's title, a page's address). The text is
 * numbered by `numbering`, which must be the numbering of `texts`.
 */
export function clipboardFormats(
	texts: readonly string[],
	numbering: CitationNumbering,
	sources: readonly CitedSource[]
): ClipboardFormats {
	const joined = texts.filter((text) => text.trim() !== "").join("\n\n");
	const markdown = withMarkdownSources(
		numberMarkers(joined, numbering, plainNumber).trim(),
		sources
	);

	const tree = parser.parse(
		normalizeAnswerMarkdown(numberMarkers(joined, numbering, escapedNumber))
	);
	const html =
		toHtml(styledForPaste(toHast(mathAsTextNodes(tree)) as HastRoot)) + htmlSources(sources);
	const plain = (plainBlocks(tree.children, "").trim() + plainSources(sources)).trim();
	return { html, plain, markdown };
}

// ---------------------------------------------------------------------------
// Maths
// ---------------------------------------------------------------------------

/** The tree with each formula replaced by its readable text. */
function mathAsTextNodes(tree: MdastRoot): MdastRoot {
	const visit = (node: MdastNodes): MdastNodes => {
		if (node.type === "inlineMath") return { type: "text", value: texAsText(node.value) };
		if (node.type === "math") {
			return { type: "paragraph", children: [{ type: "text", value: texAsText(node.value) }] };
		}
		if ("children" in node) {
			return { ...node, children: node.children.map(visit) } as MdastNodes;
		}
		return node;
	};
	return visit(tree) as MdastRoot;
}

// ---------------------------------------------------------------------------
// HTML
// ---------------------------------------------------------------------------

/**
 * Inline styles for what pastes badly without them: a table with no borders
 * is a block of words in most editors, and code loses its font. Classes are
 * useless on a clipboard; the receiving app has none of this page's CSS.
 */
const PASTE_STYLES: Record<string, string> = {
	table: "border-collapse:collapse",
	th: "border:1px solid #d0d0d0;padding:4px 8px;text-align:left",
	td: "border:1px solid #d0d0d0;padding:4px 8px",
	pre: "font-family:monospace;white-space:pre-wrap",
	code: "font-family:monospace",
};

/** Addresses a pasted link may keep: anything else could run on a click. */
const SAFE_HREF = /^(?:https?:|mailto:|#)/i;

function styledForPaste(root: HastRoot): HastRoot {
	const visit = (node: ElementContent | HastRoot): void => {
		if (node.type === "element") {
			const style = PASTE_STYLES[node.tagName];
			if (style) node.properties = { ...node.properties, style } as Properties;
			if (node.tagName === "a") {
				const href = String(node.properties?.href ?? "");
				if (!SAFE_HREF.test(href)) delete node.properties.href;
			}
		}
		if ("children" in node) for (const child of node.children) visit(child as ElementContent);
	};
	visit(root);
	return root;
}

function htmlSources(sources: readonly CitedSource[]): string {
	if (sources.length === 0) return "";
	const lines = sources.map((source) => {
		const name: ElementContent = source.href
			? {
					type: "element",
					tagName: "a",
					properties: { href: source.href },
					children: [{ type: "text", value: source.name }],
				}
			: { type: "text", value: source.name };
		return [{ type: "text", value: `${plainNumber(source.number)} ` } as ElementContent, name];
	});
	const list: Element = {
		type: "element",
		tagName: "p",
		properties: {},
		children: lines.flatMap((line, i) =>
			i === 0 ? line : [{ type: "element", tagName: "br", properties: {}, children: [] }, ...line]
		),
	};
	return toHtml([
		{ type: "element", tagName: "hr", properties: {}, children: [] },
		{
			type: "element",
			tagName: "p",
			properties: {},
			children: [{ type: "text", value: SOURCES_HEADING }],
		},
		list,
	]);
}

// ---------------------------------------------------------------------------
// Plain text
// ---------------------------------------------------------------------------

/**
 * Blocks separated by a blank line (or by a line break inside a tight list
 * item), each line of them under `indent`.
 */
function plainBlocks(blocks: readonly RootContent[], indent: string, separator = "\n\n"): string {
	return blocks
		.map((block) => plainBlock(block, indent))
		.filter((text) => text !== "")
		.join(separator);
}

function plainBlock(node: RootContent, indent: string): string {
	switch (node.type) {
		case "heading":
		case "paragraph":
			return indentLines(plainInline(node), indent);
		case "code":
			return indentLines(node.value, indent);
		case "math":
			return indentLines(texAsText(node.value), indent);
		case "thematicBreak":
			return `${indent}---`;
		case "blockquote":
			return plainBlocks(node.children, `${indent}> `);
		case "list": {
			const start = node.start ?? 1;
			return node.children
				.map((item, i) => {
					const check = item.checked === true ? "[x] " : item.checked === false ? "[ ] " : "";
					const marker = `${node.ordered ? `${start + i}.` : "-"} ${check}`;
					// The item's first line follows its marker; the rest line up under it.
					const body = plainBlocks(
						item.children,
						" ".repeat(marker.length),
						node.spread || item.spread ? "\n\n" : "\n"
					);
					return `${indent}${marker}${body.trimStart()}`.replace(/\n(?=.)/g, `\n${indent}`);
				})
				.join(node.spread ? "\n\n" : "\n");
		}
		case "table":
			// " | " between cells reads as a table in a plain editor; the
			// html form is what a spreadsheet or a doc pastes.
			return node.children
				.map((row) => indent + row.children.map((cell) => plainInline(cell)).join(" | "))
				.join("\n");
		case "footnoteDefinition":
			return `${indent}[${node.label ?? node.identifier}] ${plainBlocks(node.children, "").trim()}`;
		case "html":
			// Raw HTML is not drawn in the chat either.
			return "";
		default:
			return "children" in node ? plainBlocks((node as Parent).children, indent) : "";
	}
}

function plainInline(node: MdastNodes): string {
	switch (node.type) {
		case "text":
		case "inlineCode":
			return node.value;
		case "inlineMath":
			return texAsText(node.value);
		case "break":
			return "\n";
		case "image":
			return node.alt ?? "";
		case "footnoteReference":
			return `[${node.label ?? node.identifier}]`;
		case "html":
			return "";
		case "link": {
			const words = node.children.map(plainInline).join("");
			// The address is kept after the words, since a plain paste has
			// nowhere else to put it; not twice when the words are the address.
			return SAFE_HREF.test(node.url) && words !== node.url && !node.url.startsWith("#")
				? `${words} (${node.url})`
				: words;
		}
		default:
			return "children" in node ? (node as Parent).children.map(plainInline).join("") : "";
	}
}

function indentLines(text: string, indent: string): string {
	return indent === "" ? text : text.replace(/^/gm, indent);
}

function plainSources(sources: readonly CitedSource[]): string {
	if (sources.length === 0) return "";
	const lines = sources.map(
		(source) =>
			`${plainNumber(source.number)} ${source.name}${source.href && source.href !== source.name ? ` (${source.href})` : ""}`
	);
	return `\n\n${SOURCES_HEADING}\n${lines.join("\n")}`;
}

// ---------------------------------------------------------------------------
// Markdown
// ---------------------------------------------------------------------------

/** Characters a title must not bring into Markdown as marks. */
const MARKDOWN_MARKS = /[\\`*_[\]<>#|~]/g;

function withMarkdownSources(markdown: string, sources: readonly CitedSource[]): string {
	if (sources.length === 0) return markdown;
	const lines = sources.map((source) => {
		const name = source.name.replace(MARKDOWN_MARKS, (mark) => `\\${mark}`);
		const where = source.href && source.href !== source.name ? ` <${source.href}>` : "";
		const shown = source.href === source.name ? `<${source.href}>` : name;
		// No colon follows the bracket, so no line reads as a link definition.
		return `- ${plainNumber(source.number)} ${shown}${where}`;
	});
	return `${markdown}\n\n---\n\n${SOURCES_HEADING}\n\n${lines.join("\n")}\n`;
}
