"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { preloadPlateEditor } from "@/components/editor/plate-editor-module";
import { prefetchNoteText } from "../api/note-queries";
import type { WorkspaceNote } from "../model/workspace-note";

/**
 * Opening a note from the Notes page, drawn whole the first time: the list
 * stays, its row marked busy, until the note's text and the editor's code
 * are both there, and only then does the note take its place. Showing the
 * note at once meant a placeholder, a second placeholder while the editor's
 * code loaded, then the note: the flicker of opening one. The editor's code
 * is asked for as the page mounts, so it is usually there already. A
 * failed load opens the note anyway, which then says it could not be
 * opened. A second click before the first is ready wins.
 */
export function useNoteOpening(workspaceId: number, onReady: (note: WorkspaceNote) => void) {
	const queryClient = useQueryClient();
	const [opening, setOpening] = useState<WorkspaceNote | null>(null);
	const latest = useRef(0);
	const readyRef = useRef(onReady);
	readyRef.current = onReady;

	useEffect(() => {
		preloadPlateEditor().catch(() => undefined);
	}, []);

	const open = useCallback(
		(note: WorkspaceNote) => {
			latest.current += 1;
			const ticket = latest.current;
			setOpening(note);
			void Promise.allSettled([
				preloadPlateEditor(),
				prefetchNoteText(queryClient, workspaceId, note),
			]).then(() => {
				if (ticket !== latest.current) return;
				setOpening(null);
				readyRef.current(note);
			});
		},
		[queryClient, workspaceId]
	);

	return { opening, open };
}
