// How a chat answer's Markdown is tidied before it is parsed, for the chat
// (components/assistant-ui/markdown-text.tsx) and for a copy or download of
// the answer (features/markdown-copy): one function, so what is pasted is
// read the way what was on screen was. Citation markers are not touched
// here; each caller handles them its own way.

/**
 * The answer's Markdown with the model's habits normalised: sandbox links
 * reduced to their words, every form of maths written as $$...$$ (the only
 * form the parser is set to read, so that currency stays text), and
 * headings put on lines of their own.
 */
export function normalizeAnswerMarkdown(content: string): string {
	// A model that has just written a file sometimes offers it as
	// [Download ...](sandbox:/workspace/thing.json). That path is inside the
	// throwaway container the deliverable was built in, destroyed when the job
	// ended. Worse, react-markdown strips schemes it does not know BEFORE the
	// link component can see them, so the anchor arrives with href="" — and a
	// click on an empty href reloads the current page, which is exactly what
	// was reported. Take the link apart here, before parsing, and leave the
	// words. The artifact card above the message is the real download.
	content = content.replace(/\[([^\]]*)\]\(\s*sandbox:[^)]*\)/gi, (_, label) => label);

	// All math forms are normalised to $$...$$ so we can disable single-dollar
	// inline math in remark-math (otherwise currency like "$3,120.00 and $0.00"
	// gets parsed as a LaTeX expression).
	// 1. Block math: \[...\] → $$...$$
	content = content.replace(/\\\[([\s\S]*?)\\\]/g, (_, inner) => `$$${inner}$$`);
	// 2. Inline math: \(...\) → $$...$$
	content = content.replace(/\\\(([\s\S]*?)\\\)/g, (_, inner) => `$$${inner}$$`);
	// 3. Block: \begin{equation}...\end{equation} → $$...$$
	content = content.replace(
		/\\begin\{equation\}([\s\S]*?)\\end\{equation\}/g,
		(_, inner) => `$$${inner}$$`
	);
	// 4. Block: \begin{displaymath}...\end{displaymath} → $$...$$
	content = content.replace(
		/\\begin\{displaymath\}([\s\S]*?)\\end\{displaymath\}/g,
		(_, inner) => `$$${inner}$$`
	);
	// 5. Inline: \begin{math}...\end{math} → $$...$$
	content = content.replace(
		/\\begin\{math\}([\s\S]*?)\\end\{math\}/g,
		(_, inner) => `$$${inner}$$`
	);
	// 6. Strip backtick wrapping around math: `$$...$$` → $$...$$ and `$...$` → $...$
	content = content.replace(/`(\${1,2})((?:(?!\1).)+)\1`/g, "$1$2$1");

	// A heading the model glued to the sentence before it ("done.## Next")
	// is put on its own line. The character before must not be a "#": the
	// old pattern matched inside "## Next" itself, its first "#" taken as the
	// text before it, and every level-2+ heading was drawn as an empty
	// heading and a level-1 one. A single "#" only counts after sentence
	// punctuation, so "C# is" and "item # 3" stay text.
	content = content.replace(/([^\n#])(#{2,6}[ \t])/g, "$1\n\n$2");
	content = content.replace(/([.!?:;)\]])(#[ \t])/g, "$1\n\n$2");

	return content;
}
