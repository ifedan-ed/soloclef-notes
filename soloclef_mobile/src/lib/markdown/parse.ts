/**
 * A small Markdown reader for answers, source guides and reports: headings,
 * paragraphs, nested lists, quotes, fenced code, rules and GFM tables, with
 * bold / italic / code / link inlines. Citation tokens are left in the text
 * for the renderer (see ../chat/citations.ts). Anything unusual degrades to
 * plain text rather than failing: an answer must always be readable.
 */

export type Inline =
	| { type: "text"; text: string }
	| { type: "strong"; children: Inline[] }
	| { type: "em"; children: Inline[] }
	| { type: "code"; text: string }
	| { type: "link"; href: string; children: Inline[] };

export interface ListItem {
	level: number;
	ordered: boolean;
	marker: string;
	inline: Inline[];
	checked?: boolean;
}

export type Block =
	| { type: "heading"; level: number; inline: Inline[] }
	| { type: "paragraph"; inline: Inline[] }
	| { type: "list"; items: ListItem[] }
	| { type: "code"; lang: string; text: string }
	| { type: "quote"; blocks: Block[] }
	| { type: "hr" }
	| { type: "table"; header: Inline[][]; rows: Inline[][][] };

const HEADING = /^(#{1,6})\s+(.*?)\s*#*\s*$/;
const FENCE = /^\s*(```|~~~)\s*([\w+-]*)\s*$/;
const HR = /^\s*([-*_])(\s*\1){2,}\s*$/;
const LIST_ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const QUOTE = /^\s*>\s?(.*)$/;
const TABLE_DIVIDER = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

function splitRow(line: string): string[] {
	let row = line.trim();
	if (row.startsWith("|")) row = row.slice(1);
	if (row.endsWith("|") && !row.endsWith("\\|")) row = row.slice(0, -1);
	const cells: string[] = [];
	let cell = "";
	for (let i = 0; i < row.length; i += 1) {
		if (row[i] === "\\" && row[i + 1] === "|") {
			cell += "|";
			i += 1;
		} else if (row[i] === "|") {
			cells.push(cell.trim());
			cell = "";
		} else {
			cell += row[i];
		}
	}
	cells.push(cell.trim());
	return cells;
}

export function parseMarkdown(source: string): Block[] {
	const lines = source.replace(/\r\n?/g, "\n").split("\n");
	const blocks: Block[] = [];
	let paragraph: string[] = [];

	const flushParagraph = () => {
		if (paragraph.length === 0) return;
		blocks.push({ type: "paragraph", inline: parseInline(paragraph.join("\n")) });
		paragraph = [];
	};

	let i = 0;
	while (i < lines.length) {
		const line = lines[i];

		const fence = FENCE.exec(line);
		if (fence) {
			flushParagraph();
			const body: string[] = [];
			i += 1;
			while (i < lines.length && !lines[i].trim().startsWith(fence[1])) {
				body.push(lines[i]);
				i += 1;
			}
			blocks.push({ type: "code", lang: fence[2] ?? "", text: body.join("\n") });
			i += 1;
			continue;
		}

		if (!line.trim()) {
			flushParagraph();
			i += 1;
			continue;
		}

		const heading = HEADING.exec(line);
		if (heading) {
			flushParagraph();
			blocks.push({
				type: "heading",
				level: heading[1].length,
				inline: parseInline(heading[2]),
			});
			i += 1;
			continue;
		}

		if (HR.test(line) && paragraph.length === 0) {
			blocks.push({ type: "hr" });
			i += 1;
			continue;
		}

		if (line.includes("|") && i + 1 < lines.length && TABLE_DIVIDER.test(lines[i + 1])) {
			flushParagraph();
			const header = splitRow(line).map(parseInline);
			const rows: Inline[][][] = [];
			i += 2;
			while (i < lines.length && lines[i].includes("|") && lines[i].trim()) {
				rows.push(splitRow(lines[i]).map(parseInline));
				i += 1;
			}
			blocks.push({ type: "table", header, rows });
			continue;
		}

		if (QUOTE.test(line)) {
			flushParagraph();
			const body: string[] = [];
			while (i < lines.length && QUOTE.test(lines[i])) {
				body.push((QUOTE.exec(lines[i]) as RegExpExecArray)[1]);
				i += 1;
			}
			blocks.push({ type: "quote", blocks: parseMarkdown(body.join("\n")) });
			continue;
		}

		if (LIST_ITEM.test(line)) {
			flushParagraph();
			const items: ListItem[] = [];
			const indents: number[] = [];
			while (i < lines.length) {
				const match = LIST_ITEM.exec(lines[i]);
				if (!match) {
					// A lazy continuation line joins the previous item.
					if (lines[i].trim() && items.length > 0 && /^\s+/.test(lines[i])) {
						const last = items[items.length - 1];
						last.inline = [...last.inline, { type: "text", text: " " }, ...parseInline(lines[i].trim())];
						i += 1;
						continue;
					}
					break;
				}
				const indent = match[1].replace(/\t/g, "    ").length;
				while (indents.length > 0 && indent < indents[indents.length - 1]) indents.pop();
				if (indents.length === 0 || indent > indents[indents.length - 1]) indents.push(indent);
				let text = match[3];
				let checked: boolean | undefined;
				const task = /^\[([ xX])\]\s+(.*)$/.exec(text);
				if (task) {
					checked = task[1] !== " ";
					text = task[2];
				}
				const ordered = /\d/.test(match[2]);
				items.push({
					level: indents.length - 1,
					ordered,
					marker: ordered ? match[2].replace(")", ".") : "•",
					inline: parseInline(text),
					checked,
				});
				i += 1;
			}
			blocks.push({ type: "list", items });
			continue;
		}

		paragraph.push(line.trim());
		i += 1;
	}
	flushParagraph();
	return blocks;
}

/** Inline spans. Unclosed markers stay literal text. */
export function parseInline(text: string): Inline[] {
	const out: Inline[] = [];
	let buffer = "";
	const pushText = () => {
		if (buffer) out.push({ type: "text", text: buffer });
		buffer = "";
	};

	let i = 0;
	while (i < text.length) {
		const ch = text[i];

		if (ch === "\\" && i + 1 < text.length && /[\\`*_[\]()#+\-.!|]/.test(text[i + 1])) {
			buffer += text[i + 1];
			i += 2;
			continue;
		}

		if (ch === "`") {
			const close = text.indexOf("`", i + 1);
			if (close > i) {
				pushText();
				out.push({ type: "code", text: text.slice(i + 1, close) });
				i = close + 1;
				continue;
			}
		}

		if ((ch === "*" || ch === "_") && text[i + 1] === ch) {
			const marker = ch + ch;
			const close = text.indexOf(marker, i + 2);
			if (close > i + 2) {
				pushText();
				out.push({ type: "strong", children: parseInline(text.slice(i + 2, close)) });
				i = close + 2;
				continue;
			}
		}

		if (ch === "*" || (ch === "_" && (i === 0 || /[\s(]/.test(text[i - 1])))) {
			const close = findEmphasisClose(text, i + 1, ch);
			if (close > i + 1) {
				pushText();
				out.push({ type: "em", children: parseInline(text.slice(i + 1, close)) });
				i = close + 1;
				continue;
			}
		}

		// Links; a "[citation:…]" is not a link and stays text for the renderer.
		if (ch === "[" && !text.startsWith("[citation:", i)) {
			const closeBracket = findClosingBracket(text, i);
			if (closeBracket > i && text[closeBracket + 1] === "(") {
				const closeParen = text.indexOf(")", closeBracket + 2);
				if (closeParen > closeBracket) {
					const href = text
						.slice(closeBracket + 2, closeParen)
						.trim()
						.split(/\s+/)[0];
					pushText();
					out.push({
						type: "link",
						href,
						children: parseInline(text.slice(i + 1, closeBracket)),
					});
					i = closeParen + 1;
					continue;
				}
			}
		}

		// Bare URLs become links (answers often cite them plainly).
		if ((ch === "h" || ch === "H") && /^https?:\/\//i.test(text.slice(i, i + 8))) {
			const prev = i > 0 ? text[i - 1] : " ";
			if (/[\s(]/.test(prev)) {
				const match = /^https?:\/\/[^\s<>)\]]+/i.exec(text.slice(i));
				if (match) {
					const url = match[0].replace(/[.,;:!?]+$/, "");
					pushText();
					out.push({ type: "link", href: url, children: [{ type: "text", text: url }] });
					i += url.length;
					continue;
				}
			}
		}

		buffer += ch;
		i += 1;
	}
	pushText();
	return out;
}

function findEmphasisClose(text: string, from: number, marker: string): number {
	if (from >= text.length || /\s/.test(text[from])) return -1;
	for (let j = from; j < text.length; j += 1) {
		if (text[j] === marker && text[j + 1] !== marker && !/\s/.test(text[j - 1])) {
			if (marker === "_" && j + 1 < text.length && /\w/.test(text[j + 1])) continue;
			return j;
		}
		if (text[j] === "\n" && text[j + 1] === "\n") return -1;
	}
	return -1;
}

function findClosingBracket(text: string, open: number): number {
	let depth = 0;
	for (let j = open; j < text.length; j += 1) {
		if (text[j] === "[") depth += 1;
		else if (text[j] === "]") {
			depth -= 1;
			if (depth === 0) return j;
		}
	}
	return -1;
}

/** Plain text of inlines (for accessibility labels and copying). */
export function inlineText(inline: readonly Inline[]): string {
	return inline
		.map((node) => (node.type === "text" || node.type === "code" ? node.text : inlineText(node.children)))
		.join("");
}
