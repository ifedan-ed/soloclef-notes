// The editor writes a citation marker's "[" escaped ("\[citation:12]"),
// and a web citation's address as a Markdown link (it was read as one when
// the note was opened), neither of which the chat's citation
// reader (lib/citations/citation-parser.ts) or the phone app's takes for a
// marker: a note saved from an answer lost its citations at its first edit.
// They are written back as the chat writes them. A citation chip
// (utils/citation-nodes.ts) has an empty text on each side, which the editor
// writes as a zero-width space: it is left out. Code is left as it is: an
// escape there was typed.
import { FENCED_OR_INLINE_CODE } from "@/lib/markdown/code-regions";

const ESCAPED_MARKER = /\\([[【])(\u200b?citation:)/g;
const LINKED_ADDRESS = /([[【]\u200b?citation:\s*)\[(https?:\/\/[^\]\s]+)\]\(\2\)/g;
const SPACE_BEFORE_MARKER = /\u200b+(?=[[【]\u200b?citation:)/g;
const SPACE_AFTER_MARKER = /([[【]\u200b?citation:[^\]】\n]*[\]】])\u200b+/g;

export function citationMarkersAsWritten(markdown: string): string {
	return markdown
		.split(FENCED_OR_INLINE_CODE)
		.map((part, i) =>
			i % 2 === 1
				? part
				: part
						.replace(ESCAPED_MARKER, "$1$2")
						.replace(LINKED_ADDRESS, "$1$2")
						.replace(SPACE_BEFORE_MARKER, "")
						.replace(SPACE_AFTER_MARKER, "$1")
		)
		.join("");
}
