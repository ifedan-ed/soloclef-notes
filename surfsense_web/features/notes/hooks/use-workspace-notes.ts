"use client";

import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { noteSavesSettled } from "@/features/notebooks/lib/note-saves";
import { notesApi } from "../api/notes-api";
import { notesKeys } from "../api/notes-query-keys";
import { NOTES_SEARCH_DEBOUNCE_MS, notesQuery } from "../lib/note-places";

/** Every note the person can see, filtered by ``raw`` once typing pauses. */
export function useWorkspaceNotes(workspaceId: number, raw: string) {
	const [query, setQuery] = useState(() => notesQuery(raw));
	useEffect(() => {
		const timer = window.setTimeout(() => setQuery(notesQuery(raw)), NOTES_SEARCH_DEBOUNCE_MS);
		return () => window.clearTimeout(timer);
	}, [raw]);
	return useQuery({
		queryKey: notesKeys.list(workspaceId, query),
		// A note's title and first words, read after its save on the way lands.
		queryFn: async () => {
			await noteSavesSettled(workspaceId);
			return notesApi.list(workspaceId, query);
		},
		enabled: workspaceId > 0,
		// The last results stay painted while a refined search loads.
		placeholderData: keepPreviousData,
	});
}

/** After a note changed here: the list, and the notebook whose Studio shows it. */
export function useRefreshNotes(workspaceId: number) {
	const queryClient = useQueryClient();
	return useCallback(
		(...notebookIds: (number | null | undefined)[]) => {
			void queryClient.invalidateQueries({ queryKey: notesKeys.all(workspaceId) });
			for (const id of notebookIds) {
				if (!id) continue;
				void queryClient.invalidateQueries({ queryKey: notebookKeys.notes(workspaceId, id) });
				void queryClient.invalidateQueries({ queryKey: notebookKeys.sources(workspaceId, id) });
				void queryClient.invalidateQueries({ queryKey: notebookKeys.detail(workspaceId, id) });
			}
		},
		[queryClient, workspaceId]
	);
}
