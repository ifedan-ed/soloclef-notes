// ---------------------------------------------------------------------------
// Safe markdown deserialization for the Plate editor
// ---------------------------------------------------------------------------
// `remark-mdx` treats any HTML-like tag as JSX, so unbalanced inline HTML
// (very common in GitHub READMEs, web-scraped pages, PDF conversions) makes
// it throw "Expected a closing tag for `<a>`" and crash the editor.
//
// Per the MDX maintainers' guidance (mdx-js/mdx, ipikuka/next-mdx-remote-client
// #14), MDX is the wrong format for untrusted markdown and the recommended
// fix is to fall back to plain markdown parsing. `MarkdownPlugin.deserialize`
// accepts a per-call `remarkPlugins` override, so we can:
//
//   1. Try with `remarkMdx` (rich MDX features, e.g. JSX-style components).
//   2. On failure, retry without `remarkMdx` (lenient HTML, like GitHub).
//   3. As a last resort, render the raw source in a paragraph so the user
//      never sees a crashed editor.
//
// Plate's deserializer catches an MDX error itself and, made for a stream
// that stops mid-tag, keeps only the text before the first unclosed tag and
// reads the rest as one line (`markdownToSlateNodesSafely`). A note with a
// <br> and an autolink showed nothing after the <br>, and was saved so at
// its next edit. Its `onError` tells us the MDX parse failed, so step 2 runs
// instead, on the whole text.
// ---------------------------------------------------------------------------

import { MarkdownPlugin, remarkMdx } from "@platejs/markdown";
import type { Descendant } from "platejs";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import type { PlateEditorInstance } from "@/components/editor/plate-editor";
import type { remarkChatMath } from "@/components/editor/utils/chat-math";
import { remarkQuotedListsAsLines } from "@/components/editor/utils/quoted-lists";
import { remarkReferenceLinks } from "@/components/editor/utils/reference-links";
import { FENCED_OR_INLINE_CODE } from "@/lib/markdown/code-regions";

type RemarkMath = typeof remarkMath | typeof remarkChatMath;

// Markdown Plate would drop, read as what it shows: lists in quotes, reference links and images.
const NOTHING_HIDDEN = [remarkQuotedListsAsLines, remarkReferenceLinks];

// A line break tag in code was typed, and is kept as typed. Plate reads
// every "<br>" as "<br />" in MDX mode (and the plain reader below is given
// "<br />" for it), code included, so a note's "<br>" in code became
// "<br />" at its first edit. Those in code are hidden from it, and put
// back in the editor's tree.
const HIDDEN_BR = "<br\u2060>";

function hideTypedLineBreaks(markdown: string): string {
	return markdown
		.split(FENCED_OR_INLINE_CODE)
		.map((part, i) => (i % 2 === 1 ? part.replaceAll("<br>", HIDDEN_BR) : part))
		.join("");
}

function showTypedLineBreaks(nodes: Descendant[]): Descendant[] {
	return nodes.map((node) => {
		if ("text" in node && typeof node.text === "string") {
			return node.text.includes(HIDDEN_BR)
				? ({ ...node, text: node.text.replaceAll(HIDDEN_BR, "<br>") } as Descendant)
				: node;
		}
		const children = (node as { children?: Descendant[] }).children;
		return children ? ({ ...node, children: showTypedLineBreaks(children) } as Descendant) : node;
	});
}

/** Plate's plain reader takes <br /> for a line break, not "<br>"; code keeps what was typed. */
function lineBreaksForPlainMarkdown(markdown: string): string {
	return markdown
		.split(FENCED_OR_INLINE_CODE)
		.map((part, i) => (i % 2 === 1 ? part : part.replace(/<br\s*\/?>/gi, "<br />")))
		.join("");
}

function plainTextFallback(markdown: string): Descendant[] {
	return [
		{
			type: "p",
			children: [{ text: markdown }],
		} as unknown as Descendant,
	];
}

/**
 * Deserialize markdown into a Plate value, gracefully degrading when the
 * MDX-strict parser rejects raw HTML. Always returns a renderable value;
 * never throws. `math` is how maths is read: remark-math's default, or the
 * chat's (`remarkChatMath`, $$…$$ only).
 */
export function safeDeserializeMarkdown(
	editor: NonNullable<PlateEditorInstance>,
	markdown: string,
	math: RemarkMath = remarkMath
): Descendant[] {
	const api = editor.getApi(MarkdownPlugin).markdown;
	const source = hideTypedLineBreaks(markdown);

	let mdxError: unknown = null;
	try {
		const value = api.deserialize(source, {
			remarkPlugins: [remarkGfm, math, ...NOTHING_HIDDEN, remarkMdx],
			onError: (error) => {
				mdxError = error;
			},
		}) as Descendant[];
		if (mdxError === null) return showTypedLineBreaks(value);
	} catch (error) {
		mdxError = error;
	}
	if (process.env.NODE_ENV !== "production") {
		console.warn("[plate-editor] MDX parse failed, retrying without remark-mdx:", mdxError);
	}
	try {
		return showTypedLineBreaks(
			api.deserialize(lineBreaksForPlainMarkdown(source), {
				remarkPlugins: [remarkGfm, math, ...NOTHING_HIDDEN],
				withoutMdx: true,
			}) as Descendant[]
		);
	} catch (fallbackError) {
		console.error("[plate-editor] markdown deserialize failed:", fallbackError);
		return plainTextFallback(markdown);
	}
}
