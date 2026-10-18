// ---------------------------------------------------------------------------
// Maths in a note, read and written the way chat answers are
// ---------------------------------------------------------------------------
// A note saved from an answer keeps the answer's TeX: \( \) and \[ \] (and
// $$ $$). Plain Markdown reads "\(" as an escaped "(", so the note editor
// showed "Pearson's (r = 0.463)" and, once edited, saved it that way. The
// chat draws these (components/assistant-ui/markdown-text.tsx): every form
// becomes $$…$$ and a single dollar stays money ("$3 and $5"). A note does
// the same, before it is read into the editor, and the editor writes its
// formulas back as $$…$$, which the chat's renderer and the phone app read
// too. Code is left as it is.
// ---------------------------------------------------------------------------

import remarkMath from "remark-math";
import { FENCED_OR_INLINE_CODE } from "@/lib/markdown/code-regions";

/** remark-math as the chat runs it: $$…$$ only, inline or on its own lines; a single dollar is text. */
export function remarkChatMath(this: ThisParameterType<typeof remarkMath>) {
	return remarkMath.call(this, { singleDollarTextMath: false });
}

function asDollars(part: string): string {
	return part
		.replace(/\\\[([\s\S]*?)\\\]/g, (_, inner: string) => `$$${inner}$$`)
		.replace(/\\\(([\s\S]*?)\\\)/g, (_, inner: string) => `$$${inner}$$`)
		.replace(
			/\\begin\{equation\}([\s\S]*?)\\end\{equation\}/g,
			(_, inner: string) => `$$${inner}$$`
		)
		.replace(
			/\\begin\{displaymath\}([\s\S]*?)\\end\{displaymath\}/g,
			(_, inner: string) => `$$${inner}$$`
		)
		.replace(/\\begin\{math\}([\s\S]*?)\\end\{math\}/g, (_, inner: string) => `$$${inner}$$`);
}

/** The note's maths as $$…$$ (the chat's preprocessMarkdown steps 1–5); code is left as written. */
export function chatMathAsDollars(markdown: string): string {
	return markdown
		.split(FENCED_OR_INLINE_CODE)
		.map((part, i) => (i % 2 === 0 ? asDollars(part) : part))
		.join("");
}
