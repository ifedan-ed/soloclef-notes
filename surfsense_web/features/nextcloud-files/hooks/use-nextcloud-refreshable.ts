"use client";

import { useQuery } from "@tanstack/react-query";
import { nextcloudFilesApi } from "../api/nextcloud-files-api";

export const nextcloudRefreshableKey = (workspaceId: number) =>
	["nextcloud-refreshable", workspaceId] as const;

/**
 * Document id -> the connection that can refresh it, for this user.
 *
 * Only files picked from Nextcloud through one of the user's own connections
 * are in it. Mount it where the document list is, so the menu item is already
 * decided when a menu opens instead of appearing a moment later.
 */
export function useNextcloudRefreshable(workspaceId: number | null | undefined) {
	const query = useQuery({
		queryKey: nextcloudRefreshableKey(workspaceId ?? 0),
		queryFn: async () => {
			const reply = await nextcloudFilesApi.refreshableDocuments(workspaceId as number);
			return new Map(reply.documents.map((d) => [d.document_id, d.connector_id]));
		},
		enabled: typeof workspaceId === "number" && workspaceId > 0,
		staleTime: 60 * 1000,
	});
	return query.data;
}
