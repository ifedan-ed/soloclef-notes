"use client";

import { useQuery } from "@tanstack/react-query";
import { notesApi } from "../api/notes-api";
import { notesKeys } from "../api/notes-query-keys";

/** One note with its text; null id is no note. */
export function useNote(noteId: number | null) {
	return useQuery({
		queryKey: notesKeys.note(noteId ?? 0),
		queryFn: () => notesApi.get(noteId ?? 0),
		enabled: noteId !== null,
		// Open in the editor, the note is the person's copy: a read again on
		// focus would bring back the server's while they type.
		refetchOnWindowFocus: false,
		staleTime: Number.POSITIVE_INFINITY,
	});
}
