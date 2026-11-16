import { plainText } from "@/lib/markdown/plain-text";

/** Zero-width space, joiner, non-joiner and byte-order mark. */
const ZERO_WIDTH = /[\u200b-\u200d\ufeff]/g;

/**
 * A list row's preview: the note's opening words, or a search's passage, on
 * one line. Its lines (a heading, a list's items) are kept apart with a dot,
 * so "## Plan" and the sentence under it do not run together as one.
 */
export function previewLine(preview: string): string {
	return plainText(preview)
		.split("\n")
		// The editor writes an empty paragraph as a zero-width space, which
		// \s does not match: without dropping it, the empty line between two
		// paragraphs showed as a second dot ("Plan · · Give fluids").
		.map((line) => line.replace(ZERO_WIDTH, "").replace(/\s+/g, " ").trim())
		.filter(Boolean)
		.join(" · ");
}
