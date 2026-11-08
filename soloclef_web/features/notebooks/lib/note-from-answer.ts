import { CITATION_REGEX } from "@/lib/citations/citation-parser";
import { plainText } from "@/lib/markdown/plain-text";

const MAX_TITLE_CHARS = 80;

/**
 * A note built from an assistant answer: citation tokens removed, a title
 * from its first line of words (maths as text and Markdown marks dropped,
 * lib/markdown/plain-text.ts), cut at MAX_TITLE_CHARS.
 */
export function noteFromAnswer(markdown: string): { title: string; content: string } {
	const content = markdown
		.replace(new RegExp(CITATION_REGEX.source, "g"), "")
		.replace(/[ \t]+([.,;:!?])/g, "$1")
		.replace(/[ \t]{2,}/g, " ")
		.trim();
	const firstLine =
		plainText(content)
			.split("\n")
			.map((line) => line.replace(/[ \t]+/g, " ").trim())
			.find((line) => line.length > 0) ?? "";
	const title =
		firstLine.length > MAX_TITLE_CHARS
			? `${firstLine.slice(0, MAX_TITLE_CHARS - 1).trimEnd()}…`
			: firstLine;
	return { title, content };
}
