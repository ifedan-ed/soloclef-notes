// The numbers a reader sees on citation chips: 1, 2, 3 in order of first
// appearance within one answer (or one note, or one document).
//
// The rule, which the phone app follows too, so the two show the same
// numbers for the same saved message:
//
//   Read the message's text parts in order, skipping code (fenced and
//   inline); each distinct marker value X in "[citation:X]" (each id of a
//   comma list, left to right) gets the next number from 1 the first time
//   it appears, and keeps it for every later appearance. Legacy "doc-" and
//   negative ids are not numbered: they render as inert "doc" pills. An
//   addressless source ("src_mail_…", lib/citations/addressless-source.ts)
//   is numbered like any other; one that does not decode is not drawn and
//   takes no number.
//
// Nothing is stored for this. The saved text already holds the markers in
// order, and a second copy of the numbers could disagree with the text.
//
// Why not the numbers the model wrote ("[3]")? Those come from the thread's
// citation registry (soloclef_agent/citation), which numbers sources across
// the whole conversation, so a third answer may start at [17]. They are only
// on screen while an answer streams; the saved text has "[citation:<ref>]"
// instead (soloclef_assistant/cite). Labels are numbered here by the same
// first-appearance rule, and since one label always means one source, the
// numbers do not change when the saved text replaces the streamed one.
//
// No React here, so the rule is unit-tested on its own and shared by the
// chips, the copy and the download.

import { decodeSourceMarker, isSourceMarker } from "@/lib/citations/addressless-source";
import { CITATION_REGEX, type CitationToken } from "@/lib/citations/citation-parser";
import { FENCED_OR_INLINE_CODE } from "@/lib/markdown/code-regions";

/**
 * A label the model writes while it streams: "[3]", spaces allowed inside
 * the brackets, up to nine digits. The grammar is exactly the one the Go
 * rewrite turns into markers (soloclef_assistant/cite, `label`), so what is
 * shown as a citation while streaming is what becomes one when saved.
 */
export const LABEL_REGEX = /\[[ \t]*(\d{1,9})[ \t]*\]/g;

/** One streamed label, before the answer is saved. */
export interface CitationLabel {
	kind: "label";
	label: number;
}

/** Anything a chip can be drawn for. */
export type NumberedCitation = CitationToken | CitationLabel;

/**
 * The identity of a cited source within one answer. Two chips with the same
 * key are the same source and show the same number. Returns null for the
 * legacy markers that are never numbered.
 */
export function citationKey(citation: NumberedCitation): string | null {
	switch (citation.kind) {
		case "url":
			return `url:${citation.url}`;
		case "source":
			// The marker's value already says which kind of source it is.
			return `source:${citation.ref}`;
		case "label":
			return `label:${citation.label}`;
		case "chunk":
			if (citation.isDocsChunk || citation.chunkId < 0) return null;
			return `chunk:${citation.chunkId}`;
	}
}

/** The numbers of one answer, and its sources in number order. */
export interface CitationNumbering {
	/** Keys in order of first appearance: the key numbered n is at n-1. */
	readonly order: readonly string[];
	/** The number of each key. */
	readonly numbers: ReadonlyMap<string, number>;
}

export const EMPTY_NUMBERING: CitationNumbering = { order: [], numbers: new Map() };

/**
 * Number the citations of one answer's text parts.
 *
 * `labels` is true only for an answer still streaming: there "[3]" is a
 * citation the model wrote. In a saved answer every label has already been
 * rewritten or removed, and a bracketed number left in old text (a
 * reference list, "see [2] below") is the author's text, not a chip.
 */
export function numberCitations(
	texts: readonly string[],
	options: { labels: boolean } = { labels: false }
): CitationNumbering {
	const order: string[] = [];
	const numbers = new Map<string, number>();
	const add = (key: string | null) => {
		if (key === null || numbers.has(key)) return;
		order.push(key);
		numbers.set(key, order.length);
	};
	for (const text of texts) {
		for (const citation of citationsInOrder(text, options.labels)) add(citationKey(citation));
	}
	return { order, numbers };
}

/**
 * Every citation in a text in reading order, code skipped. Markers and (when
 * asked) labels are read in one pass, so a label written before a marker is
 * numbered before it.
 */
export function citationsInOrder(text: string, labels: boolean): NumberedCitation[] {
	// Fast path: most answers to a casual question cite nothing.
	if (!text.includes("[") && !text.includes("【")) return [];
	const found: NumberedCitation[] = [];
	const parts = text.split(FENCED_OR_INLINE_CODE);
	for (let i = 0; i < parts.length; i += 2) {
		const part = parts[i];
		const hits: { at: number; citations: NumberedCitation[] }[] = [];
		for (const match of part.matchAll(new RegExp(CITATION_REGEX.source, "g"))) {
			hits.push({ at: match.index ?? 0, citations: markerCitations(match[1]) });
		}
		if (labels) {
			for (const match of part.matchAll(new RegExp(LABEL_REGEX.source, "g"))) {
				hits.push({
					at: match.index ?? 0,
					citations: [{ kind: "label", label: Number(match[1]) }],
				});
			}
		}
		hits.sort((a, b) => a.at - b.at);
		for (const hit of hits) found.push(...hit.citations);
	}
	return found;
}

/** The citations one marker's captured value names (a comma list is several). */
export function markerCitations(captured: string): CitationToken[] {
	const value = captured.trim();
	if (value.startsWith("http://") || value.startsWith("https://")) {
		return [{ kind: "url", url: value }];
	}
	if (isSourceMarker(value)) {
		const source = decodeSourceMarker(value);
		return source ? [source] : [];
	}
	const tokens: CitationToken[] = [];
	for (const raw of value.split(",")) {
		const id = raw.trim();
		const isDocsChunk = id.startsWith("doc-");
		const chunkId = Number.parseInt(isDocsChunk ? id.slice(4) : id, 10);
		if (!Number.isNaN(chunkId)) tokens.push({ kind: "chunk", chunkId, isDocsChunk });
	}
	return tokens;
}

/**
 * A text with its streamed labels taken out as citations, for the renderer.
 * Called on the words between markers, which hold no code: the Markdown
 * renderer hands code to its own component.
 */
export function splitLabels(text: string): (string | CitationLabel)[] {
	if (!text.includes("[")) return [text];
	const out: (string | CitationLabel)[] = [];
	let last = 0;
	for (const match of text.matchAll(new RegExp(LABEL_REGEX.source, "g"))) {
		const at = match.index ?? 0;
		if (at > last) out.push(text.slice(last, at));
		out.push({ kind: "label", label: Number(match[1]) });
		last = at + match[0].length;
	}
	if (last < text.length) out.push(text.slice(last));
	return out;
}
