// ---------------------------------------------------------------------------
// The Plate editor's Markdown in and out
// ---------------------------------------------------------------------------
// How components/editor/plate-editor.tsx reads a Markdown text into its tree
// and writes the tree back, in one place, without React, so tests can read
// and write a note the way the editor does.
// ---------------------------------------------------------------------------

import { MarkdownPlugin } from "@platejs/markdown";
import type { Descendant, Value } from "platejs";
import remarkMath from "remark-math";
import type { PlateEditorInstance } from "@/components/editor/plate-editor";
import { chatMathAsDollars, remarkChatMath } from "@/components/editor/utils/chat-math";
import { citationMarkersAsWritten } from "@/components/editor/utils/citation-markers";
import { injectCitationNodes } from "@/components/editor/utils/citation-nodes";
import { escapeMdxExpressions } from "@/components/editor/utils/escape-mdx";
import { safeDeserializeMarkdown } from "@/components/editor/utils/safe-deserialize";
import { preprocessCitationMarkdown } from "@/lib/citations/citation-parser";

export interface MarkdownReading {
	/** Maths read as the chat reads it (utils/chat-math.ts). */
	chatMath: boolean;
	/** `[citation:…]` markers read as citation chips (utils/citation-nodes.ts). */
	citations: boolean;
}

/** How the editor's Markdown reads maths: remark-math's default, or the chat's. */
export function mathReading(chatMath: boolean) {
	return chatMath ? remarkChatMath : remarkMath;
}

/** Markdown made safe for the MDX parser; with `chatMath`, its maths as $$…$$ first. */
function prepareMarkdown(source: string, chatMath: boolean): string {
	return escapeMdxExpressions(chatMath ? chatMathAsDollars(source) : source);
}

/** `markdown` as the editor's tree. Never throws (utils/safe-deserialize.ts). */
export function readMarkdown(
	editor: NonNullable<PlateEditorInstance>,
	markdown: string,
	{ chatMath, citations }: MarkdownReading
): Descendant[] {
	if (!citations) {
		return safeDeserializeMarkdown(
			editor,
			prepareMarkdown(markdown, chatMath),
			mathReading(chatMath)
		);
	}
	// A web citation's address is put aside first, or it is read as a link.
	const { content, urlMap } = preprocessCitationMarkdown(markdown);
	const value = safeDeserializeMarkdown(
		editor,
		prepareMarkdown(content, chatMath),
		mathReading(chatMath)
	);
	return injectCitationNodes(value, urlMap);
}

/** The editor's tree as Markdown, citation markers as the chat writes them. */
export function writeMarkdown(editor: NonNullable<PlateEditorInstance>, value: Value): string {
	return citationMarkersAsWritten(editor.getApi(MarkdownPlugin).markdown.serialize({ value }));
}
