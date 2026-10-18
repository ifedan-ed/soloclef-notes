import type { Notebook, NotebookSource } from "../api/schemas";

export type HomeTab = "all" | "recent";
export const RECENT_LIMIT = 12;

function matches(notebook: Notebook, query: string): boolean {
	const needle = query.trim().toLocaleLowerCase();
	return !needle || notebook.title.toLocaleLowerCase().includes(needle);
}

function byUpdated(a: Notebook, b: Notebook): number {
	return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime() || b.id - a.id;
}

/**
 * What the home shows, as on the web (features/notebooks/lib/home-filter.ts):
 * "All" is featured notebooks, then every notebook newest first; "Recent" is
 * the most recently used twelve. A search narrows both by title.
 */
export function homeSections(
	notebooks: readonly Notebook[],
	tab: HomeTab,
	query: string
): { featured: Notebook[]; notebooks: Notebook[] } {
	const visible = notebooks.filter((notebook) => matches(notebook, query)).sort(byUpdated);
	if (tab === "recent") return { featured: [], notebooks: visible.slice(0, RECENT_LIMIT) };
	return { featured: visible.filter((notebook) => notebook.featured), notebooks: visible };
}

/** As the web (lib/content-search.ts): search inside notebooks from two characters, 300 ms after typing stops. */
export const CONTENT_SEARCH_MIN_CHARS = 2;
export const CONTENT_SEARCH_DEBOUNCE_MS = 300;

export function contentSearchQuery(raw: string): string {
	const query = raw.trim().replace(/\s+/g, " ");
	return query.length >= CONTENT_SEARCH_MIN_CHARS ? query : "";
}

export type SnippetPart = { text: string; hit: boolean; start: number };

/** ``snippet`` cut into plain and highlighted runs; bad ranges are tolerated. */
export function snippetParts(snippet: string, ranges: readonly (readonly [number, number])[]): SnippetPart[] {
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
	if (cursor < snippet.length) parts.push({ text: snippet.slice(cursor), hit: false, start: cursor });
	return parts;
}

export const NOTEBOOK_ICONS = ["📓", "📚", "🧪", "🧠", "🗂️", "🔬", "💡", "🧭", "🩺", "📊", "🌍", "✏️"];

export function defaultNotebookIcon(seed: number): string {
	return NOTEBOOK_ICONS[Math.abs(seed) % NOTEBOOK_ICONS.length];
}

export function isIndexing(source: Pick<NotebookSource, "status">): boolean {
	return source.status === "pending" || source.status === "processing";
}

/** Optimistic toggle, as the web's ``withToggled`` (lib/source-selection.ts). */
export function withToggled(
	sources: readonly NotebookSource[],
	sourceId: number | "all",
	enabled: boolean
): NotebookSource[] {
	return sources.map((source) => (sourceId === "all" || source.id === sourceId ? { ...source, enabled } : source));
}

const UNITS: [string, number][] = [
	["year", 365 * 24 * 3600],
	["month", 30 * 24 * 3600],
	["week", 7 * 24 * 3600],
	["day", 24 * 3600],
	["hour", 3600],
	["minute", 60],
];

/**
 * "3 days ago", localized when the runtime has Intl.RelativeTimeFormat, and a
 * compact English fallback when it does not (older Hermes builds).
 */
export function relativeTime(iso: string, locale: string, now: Date = new Date()): string {
	const seconds = Math.max(0, (now.getTime() - new Date(iso).getTime()) / 1000);
	const Rtf = (Intl as unknown as { RelativeTimeFormat?: typeof Intl.RelativeTimeFormat }).RelativeTimeFormat;
	for (const [unit, size] of UNITS) {
		if (seconds >= size) {
			const value = Math.floor(seconds / size);
			if (Rtf) {
				try {
					return new Rtf(locale, { numeric: "auto" }).format(-value, unit as Intl.RelativeTimeFormatUnit);
				} catch {
					// fall through to English
				}
			}
			return `${value} ${unit}${value === 1 ? "" : "s"} ago`;
		}
	}
	if (Rtf) {
		try {
			return new Rtf(locale, { numeric: "auto" }).format(0, "second");
		} catch {
			// fall through
		}
	}
	return "just now";
}
