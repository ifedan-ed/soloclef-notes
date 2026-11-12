"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { preloadPlateEditor } from "@/components/editor/plate-editor-module";
import { notesApi } from "../api/notes-api";
import { notesKeys } from "../api/notes-query-keys";

/**
 * Opening a note from a list, drawn whole the first time: the list (and the
 * note already open beside it) stays as it is until the note's text and the
 * editor's code are both there, and only then does the note take its place.
 * Showing the note at once meant a blank pane, then a placeholder while the
 * editor's code loaded, then the note: the flicker of opening one.
 *
 * The editor's code is asked for as the list mounts, so it is usually there
 * before the first click. A load that fails opens the note anyway, which
 * then says itself that it could not be opened. A second click before the
 * first is ready wins.
 *
 * (This was lost when the notes became one kind: the comment in
 * plate-editor-module.ts still described a preload that nothing made.)
 */
export function useNoteOpening(onReady: (noteId: number) => void) {
	const queryClient = useQueryClient();
	/** The note clicked and not yet shown; the list marks its row. */
	const [opening, setOpening] = useState<number | null>(null);
	const latest = useRef(0);
	const readyRef = useRef(onReady);
	readyRef.current = onReady;

	useEffect(() => {
		preloadPlateEditor().catch(() => undefined);
	}, []);

	const open = useCallback(
		(noteId: number) => {
			latest.current += 1;
			const ticket = latest.current;
			setOpening(noteId);
			// A note already in the cache (opened before, or just typed in)
			// is not read again: the cache is the person's copy of it.
			const text = queryClient.prefetchQuery({
				queryKey: notesKeys.note(noteId),
				queryFn: () => notesApi.get(noteId),
				staleTime: Number.POSITIVE_INFINITY,
			});
			void Promise.allSettled([preloadPlateEditor(), text]).then(() => {
				if (ticket !== latest.current) return;
				setOpening(null);
				readyRef.current(noteId);
			});
		},
		[queryClient]
	);

	return { opening, open };
}
