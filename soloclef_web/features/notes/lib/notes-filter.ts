import type { NoteOrigin } from "../model/note";

/** How a list is ordered after its pinned notes. */
export type NotesSort = "edited" | "created" | "title";

/** Which of the account's notes a list shows. */
export interface NotesFilter {
	/** Search text as typed; tidied before it is sent. */
	query?: string;
	/** One notebook's notes, or "none" for the notes in no notebook. */
	notebook?: number | "none";
	pinned?: boolean;
	origin?: NoteOrigin;
	/** The notes kept from one conversation (how an answer knows it was saved). */
	threadId?: number;
	sort?: NotesSort;
}

/** Quiet time after the last keystroke before a search is sent. */
export const NOTES_SEARCH_DEBOUNCE_MS = 250;

/** How many notes a page of the list holds. */
export const NOTES_PAGE_SIZE = 50;

/** The search worth sending: trimmed, inner spaces collapsed. */
export function searchText(raw: string | undefined): string {
	return (raw ?? "").trim().replace(/\s+/g, " ");
}

/**
 * The filter as the list route reads it. Only what differs from the
 * server's defaults is written, so one filter is one cache key however it
 * was put together.
 */
export function notesQueryString(filter: NotesFilter, offset = 0): string {
	const params = new URLSearchParams();
	const query = searchText(filter.query);
	if (query) params.set("q", query);
	if (filter.notebook === "none") params.set("unfiled", "true");
	else if (filter.notebook !== undefined) params.set("notebook_id", String(filter.notebook));
	if (filter.pinned) params.set("pinned", "true");
	if (filter.origin) params.set("origin", filter.origin);
	if (filter.threadId !== undefined) params.set("thread_id", String(filter.threadId));
	if (filter.sort && filter.sort !== "edited") params.set("sort", filter.sort);
	params.set("limit", String(NOTES_PAGE_SIZE));
	if (offset > 0) params.set("offset", String(offset));
	return params.toString();
}

/** Whether a filter narrows the list (so an empty list says "nothing matches", not "no notes"). */
export function isFiltered(filter: NotesFilter): boolean {
	return Boolean(
		searchText(filter.query) ||
			filter.notebook !== undefined ||
			filter.pinned ||
			filter.origin ||
			filter.threadId !== undefined
	);
}
