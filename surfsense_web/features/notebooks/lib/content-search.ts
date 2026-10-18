import type { SearchPassage } from "../model/notebook";

/** Characters typed before the home search looks inside notebooks. */
export const CONTENT_SEARCH_MIN_CHARS = 2;
/** Quiet time after the last keystroke before that search is sent. */
export const CONTENT_SEARCH_DEBOUNCE_MS = 300;

/** The query worth sending, or "" while it is too short to search. */
export function contentSearchQuery(raw: string): string {
	const query = raw.trim().replace(/\s+/g, " ");
	return query.length >= CONTENT_SEARCH_MIN_CHARS ? query : "";
}

/** A run of the snippet; ``start`` is its offset (a stable React key). */
export type SnippetPart = { text: string; hit: boolean; start: number };

/**
 * ``snippet`` cut into plain and highlighted runs. Ranges come from the
 * server; overlapping, unordered or out-of-bounds ones are tolerated.
 */
export function snippetParts(
	snippet: string,
	ranges: readonly (readonly [number, number])[]
): SnippetPart[] {
	const clean = ranges
		.map(([start, end]) => [Math.max(0, start), Math.min(snippet.length, end)] as const)
		.filter(([start, end]) => end > start)
		.sort((a, b) => a[0] - b[0]);
	const parts: SnippetPart[] = [];
	let cursor = 0;
	for (const [start, end] of clean) {
		if (end <= cursor) continue;
		const from = Math.max(start, cursor);
		if (from > cursor) parts.push({ text: snippet.slice(cursor, from), hit: false, start: cursor });
		parts.push({ text: snippet.slice(from, end), hit: true, start: from });
		cursor = end;
	}
	if (cursor < snippet.length)
		parts.push({ text: snippet.slice(cursor), hit: false, start: cursor });
	return parts;
}

/** Query parameters the notebook page reads to open a source guide or a note. */
export const FOCUS_SOURCE_PARAM = "source";
export const FOCUS_NOTE_PARAM = "note";

/**
 * Where a passage opens: its notebook, with the source's guide (or the note)
 * already open. A document found through a source folder opens the sources
 * list, since a folder has no guide of its own.
 */
export function passageHref(notebookHref: string, passage: SearchPassage): string {
	if (passage.kind === "note" && passage.note_id) {
		return `${notebookHref}?${FOCUS_NOTE_PARAM}=${passage.note_id}`;
	}
	if (passage.kind === "source" && passage.source_id) {
		return `${notebookHref}?${FOCUS_SOURCE_PARAM}=${passage.source_id}`;
	}
	return notebookHref;
}

export type NotebookFocus = { kind: "source" | "note"; id: number } | null;

/** The source or note a link asked the notebook page to open, if any. */
export function readNotebookFocus(search: string): NotebookFocus {
	const params = new URLSearchParams(search);
	for (const [kind, key] of [
		["source", FOCUS_SOURCE_PARAM],
		["note", FOCUS_NOTE_PARAM],
	] as const) {
		const id = Number(params.get(key));
		if (Number.isSafeInteger(id) && id > 0) return { kind, id };
	}
	return null;
}

/** ``url`` without the focus parameters, once the page has acted on them. */
export function withoutFocusParams(url: string): string {
	const next = new URL(url);
	next.searchParams.delete(FOCUS_SOURCE_PARAM);
	next.searchParams.delete(FOCUS_NOTE_PARAM);
	return next.toString();
}
