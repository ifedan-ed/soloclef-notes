"use client";

import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { notesApi } from "../api/notes-api";
import { notesKeys } from "../api/notes-query-keys";
import { storeNote } from "../lib/note-cache";
import { createNoteSaves, type NoteSaves, type OpenNoteSaves } from "../lib/note-saves";
import type { NoteChanges, SaveState } from "../lib/save-queue";
import type { Note } from "../model/note";

/** Quiet time after the last keystroke before a note saves. */
export const NOTE_SAVE_DELAY_MS = 1000;

// One set of queues for each query client (there is one in the app; a test
// may make its own), found by the client so that nothing has to be threaded
// through a provider.
const savesByClient = new WeakMap<QueryClient, NoteSaves>();

function savesFor(queryClient: QueryClient): NoteSaves {
	let saves = savesByClient.get(queryClient);
	if (!saves) {
		saves = createNoteSaves<Note>({
			delayMs: NOTE_SAVE_DELAY_MS,
			send: (noteId, changes) => notesApi.update(noteId, changes),
			// The note's own cache is the note as the person has it, typed
			// words included: it is what a note opened again is drawn from.
			onTyped: (noteId, changes) =>
				queryClient.setQueryData<Note>(notesKeys.note(noteId), (note) =>
					note ? { ...note, ...changes } : note
				),
			onSaved: (_noteId, note) => storeNote(queryClient, note),
		});
		savesByClient.set(queryClient, saves);
	}
	return saves;
}

/**
 * Saves a note as it is typed (lib/save-queue.ts says how), and once more
 * when it is left: closed, another note opened, the tab hidden. The queue
 * is the note's, not this mount's (lib/note-saves.ts says why), so a note
 * opened again while a save is still on its way carries on where it was.
 */
export function useNoteAutosave(noteId: number) {
	const queryClient = useQueryClient();
	const [state, setState] = useState<SaveState>("saved");
	const opened = useRef<OpenNoteSaves | null>(null);

	useEffect(() => {
		const saves = savesFor(queryClient).open(noteId, setState);
		opened.current = saves;
		const leaving = () => {
			if (document.visibilityState === "hidden") void saves.flush();
		};
		// A reload or a closed tab with words still waiting is asked about.
		const unloading = (event: BeforeUnloadEvent) => {
			if (!saves.hasUnsaved()) return;
			void saves.flush();
			event.preventDefault();
		};
		document.addEventListener("visibilitychange", leaving);
		window.addEventListener("beforeunload", unloading);
		return () => {
			document.removeEventListener("visibilitychange", leaving);
			window.removeEventListener("beforeunload", unloading);
			opened.current = null;
			// Sends what is waiting; the queue outlives this mount until it lands.
			saves.close();
		};
	}, [noteId, queryClient]);

	// Stable for the editor, which is given them once. Before the effect
	// above has run there is nothing typed to save.
	const queue = useCallback((changes: NoteChanges) => opened.current?.queue(changes), []);
	const flush = useCallback(() => opened.current?.flush() ?? Promise.resolve(), []);
	return { state, queue, flush };
}
