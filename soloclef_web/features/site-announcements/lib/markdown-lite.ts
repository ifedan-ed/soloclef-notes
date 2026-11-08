/**
 * The small Markdown an announcement body may use: paragraphs, "- " bullet
 * lists, **bold**, *italic* or _italic_, `code`, and [text](link). Anything
 * else is plain text. A link that is not a web address or a page in the app
 * stays as text, so a body can never run script.
 */

export type Inline =
	| { kind: "text"; text: string }
	| { kind: "strong"; text: string }
	| { kind: "em"; text: string }
	| { kind: "code"; text: string }
	| { kind: "link"; text: string; href: string }
	| { kind: "break" };

export type Block = { kind: "paragraph"; inlines: Inline[] } | { kind: "list"; items: Inline[][] };

/** A web address or a page in the app; nothing that runs. */
export function isSafeHref(href: string): boolean {
	const value = href.trim();
	if (!value || /\s/.test(value)) return false;
	// "//host" and "/\\host" both leave the site: browsers read either as a
	// protocol-relative address. A page in the app is one slash, then a path.
	if (value.startsWith("/")) return !/^\/[\/\\]/.test(value);
	return /^https?:\/\//i.test(value);
}

const INLINE = /\*\*([^*]+)\*\*|`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\)|\*([^*]+)\*|_([^_]+)_/g;

function inlineTokens(line: string): Inline[] {
	const out: Inline[] = [];
	let last = 0;
	for (const match of line.matchAll(INLINE)) {
		const at = match.index ?? 0;
		if (at > last) out.push({ kind: "text", text: line.slice(last, at) });
		const [whole, strong, code, linkText, href, em1, em2] = match;
		if (strong !== undefined) out.push({ kind: "strong", text: strong });
		else if (code !== undefined) out.push({ kind: "code", text: code });
		else if (linkText !== undefined && href !== undefined)
			out.push(
				isSafeHref(href) ? { kind: "link", text: linkText, href } : { kind: "text", text: whole }
			);
		else out.push({ kind: "em", text: (em1 ?? em2) as string });
		last = at + whole.length;
	}
	if (last < line.length) out.push({ kind: "text", text: line.slice(last) });
	return out;
}

function joinLines(lines: string[]): Inline[] {
	const out: Inline[] = [];
	lines.forEach((line, i) => {
		if (i > 0) out.push({ kind: "break" });
		out.push(...inlineTokens(line));
	});
	return out;
}

const BULLET = /^\s*[-*]\s+/;

export function parseMarkdownLite(source: string): Block[] {
	const blocks: Block[] = [];
	const chunks = source
		.replace(/\r\n?/g, "\n")
		.trim()
		.split(/\n\s*\n/);
	for (const chunk of chunks) {
		const lines = chunk.split("\n").filter((line) => line.trim() !== "");
		if (lines.length === 0) continue;
		if (lines.every((line) => BULLET.test(line))) {
			blocks.push({
				kind: "list",
				items: lines.map((line) => inlineTokens(line.replace(BULLET, ""))),
			});
		} else {
			blocks.push({ kind: "paragraph", inlines: joinLines(lines) });
		}
	}
	return blocks;
}

/** The body as one line of plain text, for a clamped preview. */
export function plainTextOf(source: string): string {
	return parseMarkdownLite(source)
		.flatMap((block) => (block.kind === "list" ? block.items : [block.inlines]))
		.map((inlines) =>
			inlines.map((inline) => (inline.kind === "break" ? " " : inline.text)).join("")
		)
		.join(" ");
}
