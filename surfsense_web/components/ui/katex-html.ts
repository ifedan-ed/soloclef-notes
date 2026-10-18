import katex from "katex";

/**
 * A formula as KaTeX's HTML, for an element to draw as it renders. Plate's
 * useEquationElement draws it into the element after mounting, so a note's
 * maths came in a frame after its text. A formula KaTeX cannot read shows
 * its TeX in red (throwOnError: false), as in the chat.
 */
export function katexHtml(tex: string, displayMode: boolean): string {
	return katex.renderToString(tex, { displayMode, throwOnError: false });
}
