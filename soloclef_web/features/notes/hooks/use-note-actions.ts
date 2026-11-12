"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { type NewNote, notesApi } from "../api/notes-api";
import { forgetNote, refreshLists, storeNote } from "../lib/note-cache";
import type { Note } from "../model/note";

/**
 * What can be done to a note besides typing in it. Each answers the note as
 * the server now has it and puts that in the caches; a change that can move
 * a note in or out of a list (filing, pinning, a new note) also reads the
 * lists again.
 */
export function useNoteActions() {
	const queryClient = useQueryClient();
	return useMemo(() => {
		const stored = (note: Note) => {
			storeNote(queryClient, note);
			return note;
		};
		return {
			create: async (body: NewNote) => {
				const note = stored(await notesApi.create(body));
				refreshLists(queryClient);
				return note;
			},
			setPinned: async (noteId: number, pinned: boolean) => {
				const note = stored(await notesApi.setPinned(noteId, pinned));
				refreshLists(queryClient);
				return note;
			},
			file: async (noteId: number, notebookId: number | null) => {
				const note = stored(await notesApi.file(noteId, notebookId));
				refreshLists(queryClient);
				return note;
			},
			/** The notebook's sources list shows the note too, so it is read again. */
			setSource: async (noteId: number, notebookId: number, on: boolean) => {
				const note = stored(await notesApi.setSource(noteId, notebookId, on));
				void queryClient.invalidateQueries({ queryKey: notebookKeys.sources(notebookId) });
				void queryClient.invalidateQueries({ queryKey: notebookKeys.detail(notebookId) });
				return note;
			},
			remove: async (note: Pick<Note, "id" | "sources">) => {
				await notesApi.remove(note.id);
				forgetNote(queryClient, note.id);
				for (const source of note.sources) {
					void queryClient.invalidateQueries({ queryKey: notebookKeys.sources(source.id) });
				}
			},
		};
	}, [queryClient]);
}
