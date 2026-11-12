import { plainText } from "@/lib/markdown/plain-text";

/**
 * A list row's preview: the note's opening words, or a search's passage, on
 * one line. Its lines (a heading, a list's items) are kept apart with a dot,
 * so "## Plan" and the sentence under it do not run together as one.
 */
export function previewLine(preview: string): string {
	return plainText(preview)
		.split("\n")
		.map((line) => line.replace(/\s+/g, " ").trim())
		.filter(Boolean)
		.join(" · ");
}
