// A text's citation markers as the numbers its reader sees: "[citation:812]"
// becomes "[1]". Used for every copy and download of rendered Markdown, so a
// pasted answer reads like the one on screen and never shows a passage id.
//
// Pure string work, no React and no DOM, so it is unit-tested directly.

import {
	type CitationNumbering,
	citationKey,
	markerCitations,
} from "@/lib/citations/citation-numbering";
import { CITATION_REGEX } from "@/lib/citations/citation-parser";
import { FENCED_OR_INLINE_CODE } from "@/lib/markdown/code-regions";

/**
 * A marker with the spaces and tabs in front of it, so that a marker with no
 * number (a legacy "doc" pill) can go with its space: "see [citation:doc-4]."
 * becomes "see." and not "see .".
 */
const MARKER_WITH_SPACE = new RegExp(`([ \\t]*)(?:${CITATION_REGEX.source})`, "g");

/**
 * `text` with each marker replaced by its number in `numbering`, written by
 * `write` ("[1]" for a download, escapedNumber for the copy's parser). A comma marker becomes
 * one number per id, as the reader sees one chip per id. Code is left as it
 * is: a marker there was typed as an example.
 */
export function numberMarkers(
	text: string,
	numbering: CitationNumbering,
	write: (number: number) => string = plainNumber
): string {
	if (!text.includes("citation:")) return text;
	return text
		.split(FENCED_OR_INLINE_CODE)
		.map((part, i) =>
			i % 2 === 1
				? part
				: part.replace(MARKER_WITH_SPACE, (_whole, space: string, captured: string) => {
						const numbers = markerCitations(captured)
							.map((citation) => citationKey(citation))
							.map((key) => (key === null ? undefined : numbering.numbers.get(key)))
							.filter((n): n is number => n !== undefined);
						return numbers.length === 0 ? "" : space + numbers.map(write).join("");
					})
		)
		.join("");
}

/** "[1]": the form a reader sees, and a download keeps. */
export function plainNumber(number: number): string {
	return `[${number}]`;
}

/**
 * "[1]" for a Markdown parser, as character references: the parser reads
 * them back as brackets in the text. A backslash escape would not do, since
 * the answer's maths normalisation reads "\[1\]" as display maths, and a
 * bare "[1][2]" is a reference link's shape.
 */
export function escapedNumber(number: number): string {
	return `&#91;${number}&#93;`;
}
