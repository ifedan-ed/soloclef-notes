import { type NotesFilter, notesQueryString } from "../lib/notes-filter";

export const notesKeys = {
	/** Every notes query. */
	all: () => ["notes"] as const,
	/** Every list, whatever its filter: what a change to any note makes stale. */
	lists: () => ["notes", "list"] as const,
	list: (filter: NotesFilter) => ["notes", "list", notesQueryString(filter)] as const,
	note: (noteId: number) => ["notes", "note", noteId] as const,
};
