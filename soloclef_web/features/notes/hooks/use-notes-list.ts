"use client";

import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { notesApi } from "../api/notes-api";
import { notesKeys } from "../api/notes-query-keys";
import { NOTES_SEARCH_DEBOUNCE_MS, type NotesFilter, searchText } from "../lib/notes-filter";

/**
 * A list of notes, a page at a time. The search text is sent once typing
 * pauses, and the last results stay painted while the next ones load, so
 * the list never blinks empty between keystrokes.
 */
export function useNotesList(filter: NotesFilter, enabled = true) {
	const [query, setQuery] = useState(() => searchText(filter.query));
	useEffect(() => {
		const next = searchText(filter.query);
		const timer = window.setTimeout(() => setQuery(next), next ? NOTES_SEARCH_DEBOUNCE_MS : 0);
		return () => window.clearTimeout(timer);
	}, [filter.query]);
	const sent: NotesFilter = { ...filter, query };
	return useInfiniteQuery({
		queryKey: notesKeys.list(sent),
		queryFn: ({ pageParam }) => notesApi.list(sent, pageParam),
		initialPageParam: 0,
		getNextPageParam: (last) => (last.has_more ? last.offset + last.items.length : undefined),
		placeholderData: keepPreviousData,
		enabled,
	});
}
