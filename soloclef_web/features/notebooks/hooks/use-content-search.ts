"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { notebookKeys } from "../api/notebook-query-keys";
import { notebooksApi } from "../api/notebooks-api";
import { CONTENT_SEARCH_DEBOUNCE_MS, contentSearchQuery } from "../lib/content-search";

/**
 * The home's "Found inside notebooks" search. Titles filter instantly on the
 * client; this looks inside sources and notes once typing pauses. Earlier
 * results stay painted while the next ones load, so the list never blanks.
 */
export function useContentSearch(workspaceId: number, rawQuery: string) {
	const current = contentSearchQuery(rawQuery);
	const sent = useDebouncedValue(current, CONTENT_SEARCH_DEBOUNCE_MS);
	const query = useQuery({
		queryKey: notebookKeys.search(workspaceId, sent),
		queryFn: () => notebooksApi.search(workspaceId, sent),
		enabled: workspaceId > 0 && sent !== "",
		placeholderData: keepPreviousData,
		staleTime: 30_000,
		retry: 1,
	});
	const active = current !== "";
	// Results that answer what is typed now (not a previous query's, kept on screen).
	const settled = active && sent === current && !!query.data && !query.isPlaceholderData;
	return {
		active,
		query: current,
		hits: active ? (query.data?.notebooks ?? []) : [],
		pending: active && (sent !== current || query.isFetching),
		settled,
		failed: active && sent === current && query.isError && !query.data,
	};
}
