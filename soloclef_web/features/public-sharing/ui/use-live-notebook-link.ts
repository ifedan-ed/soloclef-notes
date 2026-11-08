"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useAtomValue } from "jotai";
import { useCallback } from "react";
import { myAccessAtom } from "@/atoms/members/members-query.atoms";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { publicLinksApi } from "../api/public-links-api";
import { canViewPublicLinks } from "../lib/public-link-access";
import type { LiveLink } from "../model/live-link";

/**
 * The notebook's live link (or null), for members who may see public links,
 * with a way to put a fresh answer from the server in its place.
 */
export function useLiveNotebookLink(workspaceId: number, notebookId: number) {
	const { data: access } = useAtomValue(myAccessAtom);
	const queryClient = useQueryClient();
	const key = cacheKeys.liveNotebookLinks.byNotebook(workspaceId, notebookId);
	const query = useQuery({
		queryKey: key,
		queryFn: () => publicLinksApi.notebookLink(workspaceId, notebookId),
		enabled: canViewPublicLinks(access) && workspaceId > 0 && notebookId > 0,
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
