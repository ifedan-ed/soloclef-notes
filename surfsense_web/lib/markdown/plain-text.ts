// Markdown as the plain words it shows, for a note's title and the opening
// words under it: maths as text (math-as-text.ts), the marks of headings,
// lists, quotes, emphasis and code dropped, a link as its words, an escaped
// character as itself. Line breaks stay, so a caller can take the first line.
// The phone app has a copy held to this file (soloclef_mobile/src/lib/render/
// plainText.ts).
import { mathAsText } from "./math-as-text";

/**
 * A link or image anywhere in a text, `[words](where)` or `![words](where)`.
 * A label stops at the next "[" and a destination at the next "]", so its
 * time grows only with the text's length (it runs over the whole note);
 * not after a backslash, so `\[x\](1)` stays maths. The backend's
 * _LINK_ANYWHERE (app/workspace_notes/readable_text.py) is the same.
 */
const LINK_ANYWHERE = /(?<!\\)!?\[([^[\]\n]*)\]\([^)\]\n]*\)/g;

/**
 * Every link and image of a whole text as its words, before mathAsText cuts
 * it to MATH_TEXT_MAX_CHARS: a note opening with an image whose address is
 * longer than that (a pasted data: image) or a long link was cut inside it,
 * and its title and preview showed the raw markup.
 */
export function linksAsWords(markdown: string): string {
	return markdown.replace(LINK_ANYWHERE, "$1");
}

function withoutMarks(part: string): string {
	return part
		.replace(/^[ \t]*(?:>[ \t]?)+/gm, "")
		.replace(/^[ \t]*#{1,6}[ \t]+/gm, "")
		.replace(/^[ \t]*(?:[-+*]|\d{1,9}[.)])[ \t]+/gm, "")
		.replace(/!?\[([^\]\n]*)\]\([^)\n]*\)/g, "$1")
		.replace(/\\([!-/:-@[-`{-~])|[*_`]/g, (_mark, escaped?: string) => escaped ?? "");
}

export function plainText(markdown: string): string {
	return mathAsText(linksAsWords(markdown), withoutMarks);
}
