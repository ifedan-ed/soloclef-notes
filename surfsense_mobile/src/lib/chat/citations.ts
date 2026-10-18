/**
 * ``[citation:...]`` tokens, parsed exactly as the web does
 * (surfsense_web/lib/citations/citation-parser.ts): numeric chunk ids
 * (comma-separated, ``doc-`` prefixed or negative), URLs from live web
 * search, and scraper-run handles. Chinese brackets and zero-width spaces
 * that models sometimes emit are accepted.
 */
export const CITATION_REGEX =
	/[[【]​?citation:\s*(https?:\/\/[^\]】​]+|urlcite\d+|run_[0-9a-fA-F-]+|(?:doc-)?-?\d+(?:\s*,\s*(?:doc-)?-?\d+)*)\s*​?[\]】]/g;

export type CitationToken =
	| { kind: "url"; url: string }
	| { kind: "run"; runId: string }
	| { kind: "chunk"; chunkId: number; isDocsChunk: boolean };

export type Segment = string | CitationToken;

export function parseCitations(text: string): Segment[] {
	const segments: Segment[] = [];
	let lastIndex = 0;
	const regex = new RegExp(CITATION_REGEX.source, "g");
	let match = regex.exec(text);
	while (match !== null) {
		if (match.index > lastIndex) segments.push(text.substring(lastIndex, match.index));
		const captured = match[1];
		if (captured.startsWith("http://") || captured.startsWith("https://")) {
			segments.push({ kind: "url", url: captured.trim() });
		} else if (captured.startsWith("run_")) {
			segments.push({ kind: "run", runId: captured.trim() });
		} else if (!captured.startsWith("urlcite")) {
			for (const rawId of captured.split(",").map((s) => s.trim())) {
				const isDocsChunk = rawId.startsWith("doc-");
				const chunkId = Number.parseInt(isDocsChunk ? rawId.slice(4) : rawId, 10);
				if (!Number.isNaN(chunkId)) segments.push({ kind: "chunk", chunkId, isDocsChunk });
			}
		}
		lastIndex = match.index + match[0].length;
		match = regex.exec(text);
	}
	if (lastIndex < text.length) segments.push(text.substring(lastIndex));
	return segments.length > 0 ? segments : [text];
}

/**
 * Number citations in order of first appearance, so the answer reads
 * "... [1] ... [2] ... [1]" whatever the chunk ids are. Only live chunk ids
 * (positive, not legacy docs chunks) and URLs get numbers; the rest are inert
 * on the web too.
 */
export class CitationNumbering {
	private numbers = new Map<string, number>();

	keyOf(token: CitationToken): string | null {
		if (token.kind === "url") return `url:${token.url}`;
		if (token.kind === "chunk" && token.chunkId > 0 && !token.isDocsChunk) {
			return `chunk:${token.chunkId}`;
		}
		return null;
	}

	numberOf(token: CitationToken): number | null {
		const key = this.keyOf(token);
		if (!key) return null;
		let n = this.numbers.get(key);
		if (n === undefined) {
			n = this.numbers.size + 1;
			this.numbers.set(key, n);
		}
		return n;
	}
}
