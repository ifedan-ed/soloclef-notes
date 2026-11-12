import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import { notesKeys } from "../api/notes-query-keys";
import type { Note, NoteSummary, NotesPage } from "../model/note";

/** How much of a note's text a list row keeps as its preview (the server sends as much). */
const PREVIEW_CHARS = 400;

/** A note as a list row shows it. */
export function summaryOf(note: Note): NoteSummary {
	const { content, chat_notebook_id: _chat, ...fields } = note;
	return { ...fields, preview: content.slice(0, PREVIEW_CHARS) };
}

/**
 * Puts a note the server just answered into every cache that shows it: its
 * own, and its row in each list already loaded. The lists are marked stale
 * without being read again now, because whether the note still belongs in a
 * filtered list, and where, is the server's to say the next time the list
 * is looked at; reading every list on every autosave would be a request per
 * second of typing for nothing.
 */
export function storeNote(queryClient: QueryClient, note: Note): void {
	queryClient.setQueryData(notesKeys.note(note.id), note);
	const row = summaryOf(note);
	queryClient.setQueriesData<InfiniteData<NotesPage>>({ queryKey: notesKeys.lists() }, (data) =>
		data
			? {
					...data,
					pages: data.pages.map((page) => ({
						...page,
						items: page.items.map((item) =>
							item.id === note.id ? { ...row, preview: keepPreview(item, row) } : item
						),
					})),
				}
			: data
	);
	void queryClient.invalidateQueries({ queryKey: notesKeys.lists(), refetchType: "none" });
}

/**
 * A search's row shows the passage that matched, which the note's opening
 * words would replace with something unrelated to the search; it is kept
 * unless the text it came from is gone.
 */
function keepPreview(listed: NoteSummary, saved: NoteSummary): string {
	return listed.preview && saved.preview.includes(listed.preview.slice(0, 40))
		? listed.preview
		: saved.preview;
}

/** A note that is gone: out of its own cache, and every list read again. */
export function forgetNote(queryClient: QueryClient, noteId: number): void {
	queryClient.removeQueries({ queryKey: notesKeys.note(noteId) });
	queryClient.setQueriesData<InfiniteData<NotesPage>>({ queryKey: notesKeys.lists() }, (data) =>
		data
			? {
					...data,
					pages: data.pages.map((page) => ({
						...page,
						items: page.items.filter((item) => item.id !== noteId),
						total: Math.max(
							0,
							page.total - (page.items.some((item) => item.id === noteId) ? 1 : 0)
						),
					})),
				}
			: data
	);
	void queryClient.invalidateQueries({ queryKey: notesKeys.lists() });
}

/** A note that is new, or has moved in or out of a list's filter: the lists are read again. */
export function refreshLists(queryClient: QueryClient): void {
	void queryClient.invalidateQueries({ queryKey: notesKeys.lists() });
}
