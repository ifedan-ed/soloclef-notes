"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { publicLinksApi } from "../api/public-links-api";
import type { LiveLink } from "../model/live-link";

/**
 * The notebook's live link (or null), with a way to put a fresh answer from
 * the server in its place.
 */
export function useLiveNotebookLink(notebookId: number) {
	const queryClient = useQueryClient();
	const key = cacheKeys.liveNotebookLinks.byNotebook(notebookId);
	const query = useQuery({
		queryKey: key,
		queryFn: () => publicLinksApi.notebookLink(notebookId),
		enabled: notebookId > 0,
		staleTime: 60_000,
		meta: { suppressGlobalErrorToast: true },
	});
	const set = useCallback(
		(link: LiveLink | null) => {
			queryClient.setQueryData(key, link);
			void queryClient.invalidateQueries({ queryKey: cacheKeys.liveNotebookLinks.all });
		},
		[queryClient, key]
	);
	return { link: query.data ?? null, query, set };
}
